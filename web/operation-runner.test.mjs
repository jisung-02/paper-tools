import assert from "node:assert/strict";
import { test } from "node:test";
import { OperationRunner } from "./operation-runner.mjs";
import { createWasmClient } from "./wasm-client.mjs";

const catalog = new Map([
  ["merge", { id: "merge", engine: "wasm", entry: "/merge.wasm", input: { kind: "pdf", min: 2, max: 3 } }],
  ["ocr", { id: "ocr", engine: "module", entry: "/ocr.mjs", input: { kind: "file", min: 1, max: 2 } }],
]);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
  return { promise, reject, resolve };
}

async function nextTurn() {
  await new Promise((resolve) => setImmediate(resolve));
}

test("runner preserves positional arguments and reports phases", async () => {
  const seen = [];
  const phases = [];
  const runner = new OperationRunner(catalog, {
    clientFactory: () => ({ run: async (...args) => { seen.push(args); return { data: new Uint8Array([1]) }; }, cancel() {} }),
  });
  const result = await runner.invoke("merge", [new Uint8Array([1]), new Uint8Array([2]), "1-2"], {
    inputCount: 2,
    inputKinds: ["pdf", "pdf"],
    onProgress: (phase) => phases.push(phase),
  });
  assert.equal(result.data[0], 1);
  assert.deepEqual(seen[0].slice(1), [new Uint8Array([2]), "1-2"]);
  assert.deepEqual(phases, ["queued", "loading", "running", "finalizing", "done"]);
});

test("runner validates input cardinality before creating a worker", async () => {
  let created = 0;
  const runner = new OperationRunner(catalog, { clientFactory: () => { created++; return { run() {} }; } });
  await assert.rejects(runner.invoke("merge", [new Uint8Array([1])], { inputCount: 1, inputKinds: ["pdf"] }), /at least 2/);
  assert.equal(created, 0);
});

test("runner abort cancels the active client", async () => {
  let cancelled = false;
  let rejectRun;
  const started = deferred();
  const runner = new OperationRunner(catalog, {
    clientFactory: () => ({
      run: () => new Promise((resolve, reject) => { rejectRun = reject; started.resolve(); }),
      cancel: () => { cancelled = true; rejectRun?.(new DOMException("aborted", "AbortError")); },
    }),
  });
  const controller = new AbortController();
  const pending = runner.invoke("merge", [1, 2], { inputCount: 2, inputKinds: ["pdf", "pdf"], signal: controller.signal });
  await started.promise;
  controller.abort();
  await assert.rejects(pending, /aborted/i);
  assert.equal(cancelled, true);
});

test("runner dispatches module engines without constructing WASM client", async () => {
  let created = 0;
  const runner = new OperationRunner(catalog, {
    clientFactory: () => { created++; },
    moduleHandlers: { ocr: async (...args) => args.join(":") },
  });
  assert.equal(await runner.invoke("ocr", ["a", "b"], { inputCount: 2, inputKinds: ["file", "file"] }), "a:b");
  assert.equal(created, 0);
});

test("runner keeps the current client when replacement construction fails", async () => {
  const first = {
    disposed: false,
    async run() {
      if (this.disposed) throw new Error("disposed client reused");
      return { json: "first" };
    },
    dispose() { this.disposed = true; },
  };
  const runner = new OperationRunner(new Map([
    ["first", { id: "first", engine: "wasm", entry: "/first.wasm", input: { kind: "pdf", min: 1, max: 1 } }],
    ["second", { id: "second", engine: "wasm", entry: "/second.wasm", input: { kind: "pdf", min: 1, max: 1 } }],
  ]), {
    clientFactory(descriptor) {
      if (descriptor.id === "second") throw new Error("factory failed");
      return first;
    },
  });
  assert.equal((await runner.invoke("first", [], { inputCount: 1, inputKinds: ["pdf"] })).json, "first");
  await assert.rejects(runner.invoke("second", [], { inputCount: 1, inputKinds: ["pdf"] }), /factory failed/);
  assert.equal(first.disposed, false);
  assert.equal((await runner.invoke("first", [], { inputCount: 1, inputKinds: ["pdf"] })).json, "first");
});

test("runner rejects unsupported engines, input kinds, and invalid params before client creation", async () => {
  let clients = 0;
  const catalog = new Map([
    ["send", { id: "send", engine: "transport", entry: "/send.mjs", input: { kind: "file", min: 1, max: 1 } }],
    ["rotate", { id: "rotate", engine: "wasm", entry: "/rotate.wasm", input: { kind: "pdf", min: 1, max: 1 } }],
  ]);
  const runner = new OperationRunner(catalog, {
    clientFactory: () => { clients++; return { run: async () => ({}), dispose() {} }; },
    parameterValidators: {
      rotate(params) {
        if (![90, 180, 270].includes(params.degrees)) throw new Error("invalid rotation params");
      },
    },
  });
  await assert.rejects(runner.invoke("send", [], { inputCount: 1 }), /unsupported operation engine/);
  await assert.rejects(runner.invoke("rotate", [], {
    inputCount: 1,
    inputKinds: ["image"],
    params: { degrees: 90 },
  }), /requires pdf input/);
  await assert.rejects(runner.invoke("rotate", [], {
    inputCount: 1,
    inputKinds: ["pdf"],
    params: { degrees: 45 },
  }), /invalid rotation params/);
  assert.equal(clients, 0);
});

test("runner enforces FIFO concurrency one and reuses the current client", async () => {
  const starts = [];
  const releases = [];
  const phases = [[], []];
  let active = 0;
  let peak = 0;
  let clients = 0;
  const runner = new OperationRunner(catalog, {
    clientFactory: () => {
      clients++;
      return {
        async run(value) {
          starts.push(value);
          active++;
          peak = Math.max(peak, active);
          const gate = deferred();
          releases.push(() => { active--; gate.resolve(value); });
          return gate.promise;
        },
        cancel() {},
      };
    },
  });
  const options = (index) => ({
    inputCount: 2,
    inputKinds: ["pdf", "pdf"],
    onProgress: (phase) => phases[index].push(phase),
  });
  const first = runner.invoke("merge", ["first"], options(0));
  const second = runner.invoke("merge", ["second"], options(1));
  await nextTurn();
  const startsBeforeFirstRelease = [...starts];
  releases[0]();
  assert.equal(await first, "first");
  while (releases.length < 2) await nextTurn();
  releases[1]();
  assert.equal(await second, "second");

  assert.deepEqual(startsBeforeFirstRelease, ["first"]);
  assert.deepEqual(starts, ["first", "second"]);
  assert.equal(peak, 1);
  assert.equal(clients, 1);
  assert.deepEqual(phases[0], ["queued", "loading", "running", "finalizing", "done"]);
  assert.deepEqual(phases[1], ["queued", "loading", "running", "finalizing", "done"]);
});

test("aborting queued work rejects only that item without cancelling the active client", async () => {
  const gate = deferred();
  const starts = [];
  const queuedPhases = [];
  let cancels = 0;
  const runner = new OperationRunner(catalog, {
    clientFactory: () => ({
      async run(value) { starts.push(value); await gate.promise; return value; },
      cancel() { cancels++; },
    }),
  });
  const common = { inputCount: 2, inputKinds: ["pdf", "pdf"] };
  const first = runner.invoke("merge", ["active"], common);
  await nextTurn();
  const controller = new AbortController();
  const queued = runner.invoke("merge", ["queued"], {
    ...common,
    signal: controller.signal,
    onProgress: (phase) => queuedPhases.push(phase),
  });
  controller.abort();
  const queuedOutcome = await queued.then((value) => ({ value }), (error) => ({ error }));
  gate.resolve();

  assert.equal(await first, "active");
  assert.equal(queuedOutcome.error?.name, "AbortError");
  assert.deepEqual(starts, ["active"]);
  assert.equal(cancels, 0);
  assert.deepEqual(queuedPhases, ["queued"]);
});

test("switching operations waits for the previous invocation before disposing its client", async () => {
  const firstGate = deferred();
  const events = [];
  const localCatalog = new Map([
    ["first", { id: "first", engine: "wasm", entry: "/first.wasm", input: { kind: "pdf", min: 1, max: 1 } }],
    ["second", { id: "second", engine: "wasm", entry: "/second.wasm", input: { kind: "pdf", min: 1, max: 1 } }],
  ]);
  const runner = new OperationRunner(localCatalog, {
    clientFactory(descriptor) {
      events.push(`create:${descriptor.id}`);
      return {
        async run() {
          events.push(`run:${descriptor.id}`);
          if (descriptor.id === "first") await firstGate.promise;
          events.push(`settle:${descriptor.id}`);
          return descriptor.id;
        },
        dispose() { events.push(`dispose:${descriptor.id}`); },
      };
    },
  });
  const options = { inputCount: 1, inputKinds: ["pdf"] };
  const first = runner.invoke("first", [], options);
  await nextTurn();
  const second = runner.invoke("second", [], options);
  await nextTurn();
  const beforeRelease = [...events];
  firstGate.resolve();
  assert.equal(await first, "first");
  assert.equal(await second, "second");

  assert.deepEqual(beforeRelease, ["create:first", "run:first"]);
  assert.ok(events.indexOf("dispose:first") > events.indexOf("settle:first"));
  assert.ok(events.indexOf("run:second") > events.indexOf("dispose:first"));
});

test("fallback cancellation keeps the FIFO slot until the underlying operation settles", async () => {
  const gates = new Map();
  const starts = [];
  let active = 0;
  let peak = 0;
  const client = createWasmClient(async (value) => {
    starts.push(value);
    active++;
    peak = Math.max(peak, active);
    const gate = deferred();
    gates.set(value, gate);
    await gate.promise;
    active--;
    return value;
  });
  const runner = new OperationRunner(catalog, { clientFactory: () => client });
  const controller = new AbortController();
  const options = { inputCount: 2, inputKinds: ["pdf", "pdf"] };
  const first = runner.invoke("merge", ["first"], { ...options, signal: controller.signal });
  while (!gates.has("first")) await nextTurn();
  controller.abort();
  const firstOutcome = first.then((value) => ({ value }), (error) => ({ error }));
  const second = runner.invoke("merge", ["second"], options);
  await nextTurn();
  const startsBeforeSettlement = [...starts];
  gates.get("first").resolve();
  while (!gates.has("second")) await nextTurn();
  gates.get("second").resolve();

  assert.equal((await firstOutcome).error?.name, "AbortError");
  assert.equal(await second, "second");
  assert.deepEqual(startsBeforeSettlement, ["first"]);
  assert.equal(peak, 1);
});

test("dispose aborts old generation work, waits for module settlement, and permits fresh reuse", async () => {
  const gates = new Map();
  const starts = [];
  const contexts = [];
  const phases = new Map();
  let cancels = 0;
  const handler = {
    run(value, context) {
      starts.push(value);
      contexts.push(context);
      const gate = deferred();
      gates.set(value, gate);
      return gate.promise.then(() => value);
    },
    cancel() { cancels++; },
  };
  const runner = new OperationRunner(catalog, { moduleHandlers: { ocr: handler } });
  const options = (value) => ({
    inputCount: 1,
    inputKinds: ["file"],
    onProgress: (phase) => {
      const list = phases.get(value) || [];
      list.push(phase);
      phases.set(value, list);
    },
  });
  const first = runner.invoke("ocr", ["first"], options("first"));
  while (!gates.has("first")) await nextTurn();
  const queued = runner.invoke("ocr", ["queued"], options("queued"));
  const firstOutcome = first.then((value) => ({ value }), (error) => ({ error }));
  const queuedOutcome = queued.then((value) => ({ value }), (error) => ({ error }));
  const disposing = runner.dispose();
  const fresh = runner.invoke("ocr", ["fresh"], options("fresh"));
  await nextTurn();
  const beforeRelease = [...starts];
  for (const gate of gates.values()) gate.resolve();
  await disposing;
  while (!gates.has("fresh")) await nextTurn();
  gates.get("fresh").resolve();

  assert.equal((await firstOutcome).error?.name, "AbortError");
  assert.equal((await queuedOutcome).error?.name, "AbortError");
  assert.equal(await fresh, "fresh");
  assert.deepEqual(beforeRelease, ["first"]);
  assert.deepEqual(starts, ["first", "fresh"]);
  assert.equal(contexts.every((context) => context?.signal instanceof AbortSignal), true);
  assert.equal(cancels, 1);
  assert.deepEqual(phases.get("first"), ["queued", "loading", "running"]);
  assert.deepEqual(phases.get("queued"), ["queued"]);
  assert.deepEqual(phases.get("fresh"), ["queued", "loading", "running", "finalizing", "done"]);
});
