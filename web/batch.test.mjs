import assert from "node:assert/strict";
import { test } from "node:test";
import { failureManifestBlob, iterateBatch, runBatch, uniqueOutputName } from "./batch.mjs";
import { MemoryBlobSink } from "./output-sinks.mjs";
import { zipStoreStream } from "./pdf2img/zip.mjs";

test("independent batch processes sequentially and isolates failures", async () => {
  let active = 0;
  let peak = 0;
  const outputs = [];
  const result = await runBatch(["a", "bad", "c"], async (item) => {
    active++;
    peak = Math.max(peak, active);
    await Promise.resolve();
    active--;
    if (item === "bad") throw new Error("broken");
    return `${item}-out`;
  }, { onOutput: async ({ value }) => outputs.push(value) });
  assert.equal(peak, 1);
  assert.deepEqual(outputs, ["a-out", "c-out"]);
  assert.equal(result.succeeded, 2);
  assert.equal("outputs" in result, false);
  assert.equal(result.failures[0].index, 1);
  assert.equal("input" in result.failures[0], false);
  assert.equal(result.failures[0].name, "bad");
  assert.match(result.failures[0].error.message, /broken/);
});

test("grouped batch invokes the operation once", async () => {
  let calls = 0;
  const outputs = [];
  const result = await runBatch(["a", "b"], async (items) => { calls++; return items.join("+"); }, {
    mode: "grouped",
    onOutput: async ({ value }) => outputs.push(value),
  });
  assert.equal(calls, 1);
  assert.deepEqual(outputs, ["a+b"]);
  assert.equal(result.succeeded, 1);
});

test("batch retries only failed inputs", async () => {
  const attempts = new Map();
  const result = await runBatch(["a", "b"], async (item) => {
    attempts.set(item, (attempts.get(item) || 0) + 1);
    if (item === "b" && attempts.get(item) === 1) throw new Error("once");
    return item;
  }, { retries: 1 });
  assert.deepEqual([...attempts], [["a", 1], ["b", 2]]);
  assert.equal(result.succeeded, 2);
  assert.equal(result.failures.length, 0);
});

test("batch awaits output handling and does not start the next input after sink failure", async () => {
  const calls = [];
  await assert.rejects(runBatch(["a", "b"], async (item) => {
    calls.push(item);
    return item;
  }, {
    onOutput: async () => { throw new Error("sink full"); },
  }), /sink full/);
  assert.deepEqual(calls, ["a"]);
});

test("output names are stable and collision-free", () => {
  const used = new Set();
  assert.equal(uniqueOutputName("report.pdf", used), "report.pdf");
  assert.equal(uniqueOutputName("report.pdf", used), "report (2).pdf");
  assert.equal(uniqueOutputName("REPORT.PDF", used), "REPORT (3).PDF");
  assert.equal(uniqueOutputName("report", used), "report");
  assert.equal(uniqueOutputName("report", used), "report (2)");
});

test("aborted batch does not start another input", async () => {
  const controller = new AbortController();
  const calls = [];
  await assert.rejects(runBatch(["a", "b"], async (item) => {
    calls.push(item);
    controller.abort();
    return item;
  }, { signal: controller.signal }), /Abort/);
  assert.deepEqual(calls, ["a"]);
});

test("batch rejects more than the 500-file budget", async () => {
  await assert.rejects(runBatch(Array.from({ length: 501 }, (_, index) => index), async (value) => value), /500/);
});

test("batch iterator does not start the next input until the current result is consumed", async () => {
  const calls = [];
  const iterator = iterateBatch(["a", "b"], async (value) => {
    calls.push(value);
    return `${value}-out`;
  });

  const first = await iterator.next();
  assert.equal(first.value.value, "a-out");
  assert.deepEqual(calls, ["a"]);
  await Promise.resolve();
  assert.deepEqual(calls, ["a"]);

  const second = await iterator.next();
  assert.equal(second.value.value, "b-out");
  assert.deepEqual(calls, ["a", "b"]);
  assert.equal((await iterator.next()).done, true);
});

test("failure manifest contains bounded metadata and no successful output values", async () => {
  const failures = [];
  for await (const event of iterateBatch([{ name: "ok.pdf" }, { name: "bad.pdf" }], async (input) => {
    if (input.name === "bad.pdf") throw new Error("broken input");
    return { data: new Uint8Array([1, 2, 3]) };
  }, { retries: 1 })) {
    if (!event.ok) failures.push(event);
  }

  const manifest = JSON.parse(await failureManifestBlob(failures).text());
  assert.deepEqual(manifest, {
    version: 1,
    failed: 1,
    failures: [{ index: 1, name: "bad.pdf", attempts: 2, message: "broken input" }],
  });
  assert.equal(JSON.stringify(manifest).includes("data"), false);
});

test("500 streamed batch entries keep at most one operation payload live", async () => {
  let livePayloads = 0;
  let peakLivePayloads = 0;
  const inputs = Array.from({ length: 500 }, (_, index) => index);
  const sink = new MemoryBlobSink({ maxBytes: 1024 * 1024 });

  async function* entries() {
    for await (const event of iterateBatch(inputs, async (index) => {
      livePayloads++;
      peakLivePayloads = Math.max(peakLivePayloads, livePayloads);
      return {
        name: `file-${index}.bin`,
        data: (async function* () {
          try {
            yield new Uint8Array([index & 0xff]);
          } finally {
            livePayloads--;
          }
        })(),
      };
    })) {
      assert.equal(event.ok, true);
      yield event.value;
    }
  }

  await zipStoreStream(entries(), (part) => sink.write(part));
  const archive = new Uint8Array(await (await sink.close()).arrayBuffer());
  const eocd = findLastSignature(archive, 0x06054b50);

  assert.equal(readU16(archive, eocd + 10), 500);
  assert.equal(peakLivePayloads, 1);
  assert.equal(livePayloads, 0);
});

function readU16(bytes, offset) {
  return bytes[offset] | (bytes[offset + 1] << 8);
}

function readU32(bytes, offset) {
  return (
    bytes[offset]
    | (bytes[offset + 1] << 8)
    | (bytes[offset + 2] << 16)
    | (bytes[offset + 3] << 24)
  ) >>> 0;
}

function findLastSignature(bytes, signature) {
  for (let offset = bytes.length - 4; offset >= 0; offset--) {
    if (readU32(bytes, offset) === signature) return offset;
  }
  return -1;
}
