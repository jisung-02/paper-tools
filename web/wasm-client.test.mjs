import assert from "node:assert/strict";
import { test } from "node:test";
import { createWasmClient } from "./wasm-client.mjs";

test("client copies typed-array input and returns result", async () => {
  let seen;
  const client = createWasmClient(async (input) => { seen = input; return input[0]; });
  const source = new Uint8Array([7]);
  assert.equal(await client.run(source), 7);
  assert.notEqual(seen, source);
});

test("worker and fallback preserve every positional argument", async (t) => {
  const previousWorker = globalThis.Worker;
  t.after(() => { globalThis.Worker = previousWorker; });

  let posted;
  class FailingWorker {
    postMessage(message, transfer) {
      posted = { message, transfer };
      queueMicrotask(() => this.onmessage({
        data: { type: "error", id: message.id, error: "worker unavailable" },
      }));
    }
    terminate() {}
  }
  globalThis.Worker = FailingWorker;

  let fallbackArgs;
  const client = createWasmClient((...args) => {
    fallbackArgs = args;
    return { json: JSON.stringify(args.slice(1)) };
  }, { worker: { host: "/wasm-worker.js", wasm: "/tool.wasm" } });
  const bytes = new Uint8Array([1, 2, 3]);
  const result = await client.run(bytes, "pages", 90, { grayscale: true });

  assert.deepEqual(posted.message.args.slice(1), ["pages", 90, { grayscale: true }]);
  assert.equal(new Uint8Array(posted.message.args[0])[0], 1);
  assert.deepEqual(fallbackArgs.slice(1), ["pages", 90, { grayscale: true }]);
  assert.equal(fallbackArgs[0], bytes);
  assert.deepEqual([...fallbackArgs[0]], [1, 2, 3]);
  assert.deepEqual([...bytes], [1, 2, 3]);
  assert.equal(result.json, '["pages",90,{"grayscale":true}]');
});

test("worker success returns the result without calling fallback", async (t) => {
  const previousWorker = globalThis.Worker;
  t.after(() => { globalThis.Worker = previousWorker; });

  class SuccessfulWorker {
    postMessage(message) {
      queueMicrotask(() => this.onmessage({
        data: { type: "done", id: message.id, result: { json: JSON.stringify(message.args.slice(1)) } },
      }));
    }
    terminate() {}
  }
  globalThis.Worker = SuccessfulWorker;

  let fallbackCalled = false;
  const client = createWasmClient(() => { fallbackCalled = true; }, {
    worker: { host: "/wasm-worker.js", wasm: "/tool.wasm" },
  });
  const result = await client.run(new Uint8Array([7]), "all", 11);

  assert.equal(result.json, '["all",11]');
  assert.equal(fallbackCalled, false);
});

test("client cancellation rejects the active operation", async () => {
  const client = createWasmClient(() => new Promise(() => {}));
  const first = client.run(new Uint8Array([1]));
  client.cancel();
  await assert.rejects(first, (error) => error?.name === "AbortError");
});

test("client dispose terminates an idle worker", async (t) => {
  const previousWorker = globalThis.Worker;
  t.after(() => { globalThis.Worker = previousWorker; });
  let terminated = 0;
  class WorkerStub {
    postMessage(message) {
      queueMicrotask(() => this.onmessage({ data: { type: "done", id: message.id, result: {} } }));
    }
    terminate() { terminated++; }
  }
  globalThis.Worker = WorkerStub;
  const client = createWasmClient(() => ({}), { worker: { host: "/worker.js", wasm: "/tool.wasm" } });
  await client.run(new Uint8Array([1]));
  client.dispose();
  assert.equal(terminated, 1);
});
