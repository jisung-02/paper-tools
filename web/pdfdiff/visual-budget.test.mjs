import assert from "node:assert/strict";
import { test } from "node:test";

test("visual comparison enforces combined input and page budgets", async () => {
  const module = await import("./visual-budget.mjs").catch(() => ({}));
  assert.equal(typeof module.assertCombinedInputBytes, "function");
  assert.equal(typeof module.assertCombinedPageCount, "function");

  assert.equal(module.assertCombinedInputBytes([new Blob(["abc"]), new Uint8Array(3)], 6), 6);
  assert.throws(
    () => module.assertCombinedInputBytes([new Blob(["abc"]), new Uint8Array(3)], 5),
    (error) => error?.code === "input-limit" && /combined input/i.test(error.message),
  );
  assert.equal(module.assertCombinedPageCount([250, 250], 500), 500);
  assert.throws(
    () => module.assertCombinedPageCount([300, 201], 500),
    (error) => error?.code === "page-limit" && /combined page/i.test(error.message),
  );
});

test("comparison scale accounts for all simultaneously live RGBA surfaces", async () => {
  const module = await import("./visual-budget.mjs").catch(() => ({}));
  assert.equal(typeof module.scaleForLivePixelBudget, "function");

  const scale = module.scaleForLivePixelBudget(
    { width: 100, height: 100 },
    { width: 100, height: 100 },
    1,
    { maxLivePixels: 16_000, retainedPixels: 1_000 },
  );
  assert.equal(scale, 0.5);
  assert.throws(
    () => module.scaleForLivePixelBudget(
      { width: 100, height: 100 },
      { width: 100, height: 100 },
      1,
      { maxLivePixels: 1_000, retainedPixels: 1_000 },
    ),
    (error) => error?.code === "live-pixel-limit",
  );
});

test("byte-limited sink applies the export limit before every underlying sink write", async () => {
  const module = await import("./visual-budget.mjs").catch(() => ({}));
  assert.equal(typeof module.createByteLimitedSink, "function");
  const writes = [];
  let aborted = 0;
  const sink = module.createByteLimitedSink({
    kind: "opfs",
    async write(chunk) { writes.push(chunk.byteLength); },
    async close() { return new Blob(); },
    async abort() { aborted++; },
    async cleanup() {},
  }, 5);

  await sink.write(new Uint8Array(3));
  await assert.rejects(
    sink.write(new Uint8Array(3)),
    (error) => error?.code === "export-limit" && /export byte limit/i.test(error.message),
  );
  assert.deepEqual(writes, [3]);
  await sink.abort();
  assert.equal(aborted, 1);
});
