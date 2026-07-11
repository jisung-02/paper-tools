import assert from "node:assert/strict";
import { test } from "node:test";
import { createOutputSink, MemoryBlobSink } from "./output-sinks.mjs";

function fakeDirectory({
  existing = [],
  blockFirstWrite = false,
  blockRemove = false,
  failRemoveCount = 0,
  failWrite = false,
  replaceAfterFirstRemove = false,
} = {}) {
  const entries = new Map(existing.map((name) => [name, { name, existing: true }]));
  const removed = [];
  let removeCalls = 0;
  let releaseFirstWrite;
  let releaseRemove;
  const firstWriteGate = new Promise((resolve) => { releaseFirstWrite = resolve; });
  const removeGate = new Promise((resolve) => { releaseRemove = resolve; });
  const root = {
    async getFileHandle(name, { create = false } = {}) {
      if (entries.has(name)) return entries.get(name);
      if (!create) throw new DOMException("missing", "NotFoundError");
      const state = { writes: [], chunks: [], aborted: false, closed: false };
      const handle = {
        name,
        state,
        async createWritable() {
          return {
            async write(bytes) {
              state.writes.push([...bytes]);
              if (blockFirstWrite && state.writes.length === 1) await firstWriteGate;
              if (failWrite) throw new Error("disk full");
              state.chunks.push(bytes.slice());
            },
            async close() { state.closed = true; },
            async abort() { state.aborted = true; },
          };
        },
        async getFile() { return new Blob(state.chunks, { type: "application/zip" }); },
      };
      entries.set(name, handle);
      return handle;
    },
    async removeEntry(name) {
      removeCalls++;
      const call = removeCalls;
      if (blockRemove) await removeGate;
      if (call <= failRemoveCount) throw new Error("remove failed");
      entries.delete(name);
      removed.push(name);
      if (replaceAfterFirstRemove && call === 1) entries.set(name, { name, replacement: true });
    },
  };
  return {
    root,
    entries,
    removed,
    releaseFirstWrite,
    releaseRemove,
    get removeCalls() { return removeCalls; },
  };
}

test("output sink selection prefers a chosen directory and avoids existing names", async () => {
  const directory = fakeDirectory({ existing: ["paper-tools-batch.zip"] });
  let storageCalls = 0;
  const sink = await createOutputSink({
    directory: directory.root,
    name: "paper-tools-batch.zip",
    storage: { async getDirectory() { storageCalls++; throw new Error("must not run"); } },
  });

  await sink.write(new Uint8Array([1]));
  assert.equal(await sink.close(), null);
  await sink.cleanup();

  assert.equal(sink.kind, "directory");
  assert.equal(sink.name, "paper-tools-batch (2).zip");
  assert.equal(storageCalls, 0);
  assert.equal(directory.entries.get("paper-tools-batch.zip").existing, true);
  assert.equal(directory.entries.get("paper-tools-batch (2).zip").state.closed, true);
  assert.deepEqual(directory.removed, []);
});

test("output sink selection uses OPFS before bounded memory and cleans up its hidden file", async () => {
  const directory = fakeDirectory();
  const sink = await createOutputSink({
    name: "batch.zip",
    storage: { async getDirectory() { return directory.root; } },
    maxMemoryBytes: 1,
  });

  await sink.write(new Uint8Array([1, 2]));
  const blob = await sink.close();
  assert.deepEqual([...new Uint8Array(await blob.arrayBuffer())], [1, 2]);
  await sink.cleanup();

  assert.equal(sink.kind, "opfs");
  assert.deepEqual(directory.removed, ["batch.zip"]);
});

test("output sink falls back to bounded memory and retains archive chunks only in the Blob", async () => {
  const sink = await createOutputSink({
    maxMemoryBytes: 3,
    storage: { async getDirectory() { throw new Error("OPFS disabled"); } },
  });
  assert.ok(sink instanceof MemoryBlobSink);

  await sink.write(new Uint8Array([1]));
  await sink.write(new Uint8Array([2, 3]));
  const blob = await sink.close();

  assert.equal(blob.size, 3);
  assert.deepEqual([...new Uint8Array(await blob.arrayBuffer())], [1, 2, 3]);
  assert.equal(sink.bufferedBytes, 0);
  assert.equal(sink.chunkCount, 0);
});

test("bounded memory sink rejects overflow and abort releases retained chunks", async () => {
  const sink = new MemoryBlobSink({ maxBytes: 2 });
  await sink.write(new Uint8Array([1, 2]));
  await assert.rejects(sink.write(new Uint8Array([3])), /memory limit/);
  await sink.abort();
  assert.equal(sink.bufferedBytes, 0);
  assert.equal(sink.chunkCount, 0);
});

test("filesystem sink serializes concurrent writes behind sink backpressure", async () => {
  const directory = fakeDirectory({ blockFirstWrite: true });
  const sink = await createOutputSink({ directory: directory.root, name: "ordered.zip" });
  const state = directory.entries.get("ordered.zip").state;

  const first = sink.write(new Uint8Array([1]));
  const second = sink.write(new Uint8Array([2]));
  while (state.writes.length === 0) await Promise.resolve();
  assert.deepEqual(state.writes, [[1]]);
  directory.releaseFirstWrite();
  await Promise.all([first, second]);

  assert.deepEqual(state.writes, [[1], [2]]);
});

test("filesystem sink close waits for writes that were already accepted", async () => {
  const directory = fakeDirectory({ blockFirstWrite: true });
  const sink = await createOutputSink({ directory: directory.root, name: "closing.zip" });
  const state = directory.entries.get("closing.zip").state;

  const first = sink.write(new Uint8Array([1]));
  const second = sink.write(new Uint8Array([2]));
  const closing = sink.close();
  await Promise.resolve();
  await Promise.resolve();
  directory.releaseFirstWrite();
  await Promise.all([first, second, closing]);

  assert.deepEqual(state.writes, [[1], [2]]);
  assert.equal(state.closed, true);
});

test("failed filesystem sink aborts its writable and removes only its partial archive", async () => {
  const directory = fakeDirectory({ existing: ["keep.zip"], failWrite: true });
  const sink = await createOutputSink({ directory: directory.root, name: "partial.zip" });
  const state = directory.entries.get("partial.zip").state;

  await assert.rejects(sink.write(new Uint8Array([1])), /disk full/);
  await sink.abort();

  assert.equal(state.aborted, true);
  assert.equal(directory.entries.has("partial.zip"), false);
  assert.equal(directory.entries.get("keep.zip").existing, true);
  assert.deepEqual(directory.removed, ["partial.zip"]);
});

test("concurrent abort callers wait for the same partial-file cleanup", async () => {
  const directory = fakeDirectory({ blockRemove: true });
  const sink = await createOutputSink({ directory: directory.root, name: "partial.zip" });
  const first = sink.abort();
  let secondSettled = false;
  const second = sink.abort().then(() => { secondSettled = true; });

  await Promise.resolve();
  await Promise.resolve();
  assert.equal(secondSettled, false);
  directory.releaseRemove();
  await Promise.all([first, second]);
  assert.deepEqual(directory.removed, ["partial.zip"]);
});

test("concurrent cleanup calls remove once and preserve a same-name replacement", async () => {
  const directory = fakeDirectory({ blockRemove: true, replaceAfterFirstRemove: true });
  const sink = await createOutputSink({
    name: "batch.zip",
    storage: { async getDirectory() { return directory.root; } },
  });
  await sink.close();

  const first = sink.cleanup();
  const second = sink.cleanup();
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(directory.removeCalls, 1);
  directory.releaseRemove();
  await Promise.all([first, second]);

  assert.equal(directory.entries.get("batch.zip").replacement, true);
  assert.deepEqual(directory.removed, ["batch.zip"]);
});

test("cleanup and abort share one pending removal", async () => {
  const directory = fakeDirectory({ blockRemove: true, replaceAfterFirstRemove: true });
  const sink = await createOutputSink({
    name: "batch.zip",
    storage: { async getDirectory() { return directory.root; } },
  });
  await sink.close();

  const cleanup = sink.cleanup();
  const abort = sink.abort();
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(directory.removeCalls, 1);
  directory.releaseRemove();
  await Promise.all([cleanup, abort]);

  assert.equal(directory.entries.get("batch.zip").replacement, true);
});

test("failed owned-file removal can be retried", async () => {
  const directory = fakeDirectory({ failRemoveCount: 1 });
  const sink = await createOutputSink({
    name: "batch.zip",
    storage: { async getDirectory() { return directory.root; } },
  });
  await sink.close();

  await sink.cleanup();
  assert.equal(directory.entries.has("batch.zip"), true);
  await sink.cleanup();
  assert.equal(directory.entries.has("batch.zip"), false);
  assert.equal(directory.removeCalls, 2);
});
