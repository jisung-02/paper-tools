import assert from "node:assert/strict";
import { test } from "node:test";
import { DiffWorkerClient } from "./diff-client.mjs";

class FakeWorker {
  postMessage(message, transfers) {
    this.message = message;
    this.transfers = transfers;
  }

  terminate() {
    this.terminated = true;
  }
}

test("diff client transfers both RGBA buffers and restores the heatmap result", async () => {
  const worker = new FakeWorker();
  const client = new DiffWorkerClient({ workerFactory: () => worker });
  const pending = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(8), widthB: 2, heightB: 1,
    options: { threshold: 12 },
  });
  assert.equal(worker.transfers.length, 2);
  assert.equal(worker.message.widthB, 2);
  worker.onmessage({ data: {
    id: worker.message.id,
    ok: true,
    result: { width: 2, height: 1, changedPixels: 1, heatmap: new ArrayBuffer(8) },
  } });
  const result = await pending;
  assert.ok(result.heatmap instanceof Uint8ClampedArray);
  assert.equal(result.heatmap.byteLength, 8);
});

test("terminating the diff client rejects pending work", async () => {
  const worker = new FakeWorker();
  const client = new DiffWorkerClient({ workerFactory: () => worker });
  const pending = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  client.terminate("comparison cancelled");
  await assert.rejects(pending, /comparison cancelled/);
  assert.equal(worker.terminated, true);
});

test("worker failures reject only the matching request", async () => {
  const worker = new FakeWorker();
  const client = new DiffWorkerClient({ workerFactory: () => worker });
  const pending = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  worker.onmessage({ data: { id: worker.message.id, ok: false, error: "pixel budget exceeded" } });
  await assert.rejects(pending, /pixel budget exceeded/);
});

test("worker message-clone failures reject pending work", async () => {
  const worker = new FakeWorker();
  const client = new DiffWorkerClient({ workerFactory: () => worker });
  const pending = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  worker.onmessageerror({ data: null });
  await assert.rejects(pending, /message/i);
  assert.equal(worker.terminated, true);
});

test("aborting one diff terminates its Worker generation, rejects queued work, and restarts cleanly", async () => {
  const workers = [];
  const client = new DiffWorkerClient({ workerFactory: () => {
    const worker = new FakeWorker();
    workers.push(worker);
    return worker;
  } });
  const controller = new AbortController();
  const first = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
    signal: controller.signal,
  });
  const queued = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  const oldWorker = workers[0];
  const oldMessage = oldWorker.message;
  controller.abort();
  await assert.rejects(first, (error) => error?.name === "AbortError");
  const queuedOutcome = await Promise.race([
    queued.then(() => ({ resolved: true }), (error) => ({ error })),
    new Promise((resolve) => setTimeout(() => resolve({ timeout: true }), 20)),
  ]);
  assert.equal(queuedOutcome.error?.name, "AbortError");
  assert.equal(oldWorker.terminated, true);
  assert.equal(workers.length, 2);

  const resumed = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  oldWorker.onmessage({ data: {
    id: oldMessage.id,
    ok: true,
    result: { width: 1, height: 1, changedPixels: 0, heatmap: new ArrayBuffer(4) },
  } });
  const activeWorker = workers[1];
  activeWorker.onmessage({ data: {
    id: activeWorker.message.id,
    ok: true,
    result: { width: 1, height: 1, changedPixels: 0, heatmap: new ArrayBuffer(4) },
  } });
  assert.equal((await resumed).heatmap.byteLength, 4);
});

test("a fatal Worker error rejects the current generation and allows the next diff on a fresh Worker", async () => {
  const workers = [];
  const client = new DiffWorkerClient({ workerFactory: () => {
    const worker = new FakeWorker();
    workers.push(worker);
    return worker;
  } });
  const failed = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  workers[0].onerror({ message: "worker crashed" });
  await assert.rejects(failed, (error) => error?.code === "worker-failed" && /worker crashed/.test(error.message));
  assert.equal(workers[0].terminated, true);
  assert.equal(workers.length, 2);

  const resumed = client.diff({
    a: new Uint8ClampedArray(4), widthA: 1, heightA: 1,
    b: new Uint8ClampedArray(4), widthB: 1, heightB: 1,
  });
  workers[1].onmessage({ data: {
    id: workers[1].message.id,
    ok: true,
    result: { width: 1, height: 1, changedPixels: 0, heatmap: new ArrayBuffer(4) },
  } });
  await resumed;
});
