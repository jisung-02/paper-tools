import assert from "node:assert/strict";
import { test } from "node:test";
import { MemoryBlobSink } from "../output-sinks.mjs";
import { zipStoreStream } from "../pdf2img/zip.mjs";
import { alignPages, fingerprintText } from "./page-align.mjs";
import { createByteLimitedSink } from "./visual-budget.mjs";
import { visualReportEntries } from "./visual-report.mjs";
import {
  commitLatestClosedOutput,
  createLatestActionController,
  inputRevisionsMatch,
  snapshotInputRevisions,
} from "./visual-state.mjs";

function page(fingerprint, text) {
  return { fingerprint, textFingerprint: fingerprintText(text), width: 100, height: 100 };
}

function outputSink(state) {
  const chunks = [];
  return {
    kind: "opfs",
    async write(chunk) { chunks.push(new Uint8Array(chunk)); },
    async close() { return new Blob(chunks, { type: "application/zip" }); },
    async abort() { state.aborted++; },
    async cleanup() { state.cleaned++; },
  };
}

test("aligned page summaries stream through the bounded export and stale-close gate", async () => {
  const left = [page([10], "first"), page([30], "last")];
  const right = [page([10], "first"), page([20], "inserted"), page([30], "last")];
  const alignment = alignPages(left, right);
  assert.deepEqual(alignment.pairs, [
    { a: 0, b: 0 },
    { a: null, b: 1 },
    { a: 1, b: 2 },
  ]);

  const summaries = alignment.pairs.map((pair, index) => ({
    index,
    pair,
    changedPixels: pair.a == null || pair.b == null ? 100 : 0,
    totalPixels: 100,
    ratio: pair.a == null || pair.b == null ? 1 : 0,
    bounds: pair.a == null || pair.b == null ? { left: 0, top: 0, right: 10, bottom: 10 } : null,
    pageSizeChanged: pair.a == null || pair.b == null,
  }));
  const state = { aborted: 0, cleaned: 0 };
  const sink = createByteLimitedSink(outputSink(state), 64 * 1024);
  await zipStoreStream(visualReportEntries({
    fallback: alignment.fallback,
    summaries,
    threshold: 12,
    antialiasTolerance: 8,
    async heatmap(index) { return new Uint8Array([index, 1, 2, 3]); },
  }), (chunk) => sink.write(chunk));
  const output = await sink.close();
  assert.ok(output.size > 0);
  assert.ok(output.size <= 64 * 1024);

  const actions = createLatestActionController();
  const stale = actions.begin();
  const latest = actions.begin();
  let committed = 0;
  assert.equal(await commitLatestClosedOutput({
    token: stale,
    sink,
    output,
    commit() { committed++; },
  }), false);
  assert.equal(state.cleaned, 1);
  assert.equal(committed, 0);

  const latestState = { aborted: 0, cleaned: 0 };
  const latestSink = createByteLimitedSink(outputSink(latestState), 64 * 1024);
  await latestSink.write(new Uint8Array([1]));
  const latestOutput = await latestSink.close();
  assert.equal(await commitLatestClosedOutput({
    token: latest,
    sink: latestSink,
    output: latestOutput,
    commit(value) {
      assert.equal(value, latestOutput);
      committed++;
    },
  }), true);
  assert.equal(latestState.cleaned, 0);
  assert.equal(committed, 1);
});

test("input revision snapshots reject a rendered result after either dropzone changes", () => {
  const dropzones = [{ __paperRevision: 1 }, { __paperRevision: 4 }];
  const snapshot = snapshotInputRevisions(dropzones);
  assert.equal(inputRevisionsMatch(snapshot, dropzones), true);
  dropzones[1].__paperRevision++;
  assert.equal(inputRevisionsMatch(snapshot, dropzones), false);
});

test("cancelling an in-flight memory write stops later ZIP payload and structure writes", async () => {
  let releaseWrite;
  let markWriteStarted;
  const writeStarted = new Promise((resolve) => { markWriteStarted = resolve; });
  const writeGate = new Promise((resolve) => { releaseWrite = resolve; });
  const raw = new MemoryBlobSink({ maxBytes: 64 * 1024 });
  let writes = 0;
  let payloadWrites = 0;
  let aborts = 0;
  let cleanups = 0;
  let closes = 0;
  let commits = 0;
  const tracked = {
    kind: "memory",
    async write(chunk) {
      writes++;
      if (writes === 2) {
        markWriteStarted();
        await writeGate;
      }
      if (chunk.byteLength === 1_024) payloadWrites++;
      await raw.write(chunk);
    },
    async close() {
      closes++;
      return raw.close();
    },
    async abort() {
      aborts++;
      await raw.abort();
    },
    async cleanup() { cleanups++; },
  };
  async function* payload() {
    for (let index = 0; index < 4; index++) yield new Uint8Array(1_024).fill(index);
  }
  async function* entries() {
    yield { name: "heatmap.png", data: payload() };
  }

  const actions = createLatestActionController();
  const token = actions.begin();
  const sink = createByteLimitedSink(tracked, 64 * 1024);
  let nextExportStarted = false;
  let retainedBytesAtNextExport = null;
  const nextExport = token.completion.then(() => {
    retainedBytesAtNextExport = raw.bufferedBytes;
    nextExportStarted = true;
  });
  const exporting = (async () => {
    try {
      await zipStoreStream(entries(), {
        write: (chunk) => sink.write(chunk),
        signal: token.signal,
        isCurrent: token.isCurrent,
      });
      if (!token.isCurrent()) throw new DOMException("Visual comparison aborted", "AbortError");
      const output = await sink.close();
      await commitLatestClosedOutput({ token, sink, output, commit() { commits++; } });
      return null;
    } catch (error) {
      await sink.abort();
      return error;
    } finally {
      actions.finish(token);
    }
  })();

  await writeStarted;
  actions.cancel();
  await Promise.resolve();
  assert.equal(nextExportStarted, false);
  releaseWrite();
  const error = await exporting;
  await nextExport;

  assert.equal(error?.name, "AbortError");
  assert.equal(writes, 2);
  assert.equal(payloadWrites, 1);
  assert.equal(aborts, 1);
  assert.equal(cleanups, 0);
  assert.equal(closes, 0);
  assert.equal(commits, 0);
  assert.equal(nextExportStarted, true);
  assert.equal(retainedBytesAtNextExport, 0);
  assert.equal(raw.bufferedBytes, 0);
  assert.equal(raw.chunkCount, 0);
});
