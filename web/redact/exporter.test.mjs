import assert from "node:assert/strict";
import { test } from "node:test";

import { REDACT_LIMITS, streamRedactedPDF, validateRedactSource } from "./exporter.mjs";

function fakeContext() {
  return {
    fillStyle: "",
    clearRect() {},
    fillRect() {},
  };
}

function fakeDocument(pageCount, {
  width = 1,
  height = 1,
  onGetPage = () => {},
  onRender = () => {},
  onCleanup = () => {},
} = {}) {
  return {
    numPages: pageCount,
    async getPage(number) {
      onGetPage(number);
      return {
        getViewport({ scale }) {
          return { width: width * scale, height: height * scale };
        },
        render() {
          onRender(number);
          return { promise: Promise.resolve(), cancel() {} };
        },
        cleanup() {
          onCleanup(number);
        },
      };
    },
  };
}

// fakeOutputSession emulates the real finish/outputRead/outputRelease wasm
// protocol (wasm/redact/main.go): finish reports {outputRevision, size} as a
// JSON string, outputRead drains fixed-size chunks and reports {done} as
// JSON alongside the raw chunk bytes, and outputRelease acks the revision.
function fakeOutputSession(bytes, { chunkBytes = 1 << 20, outputRevision = 7 } = {}) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let offset = 0;
  return function handle(request) {
    if (request.command === "finish") {
      offset = 0;
      return { json: JSON.stringify({ state: "output-ready", outputRevision, size: data.length }) };
    }
    if (request.command === "outputRead") {
      assert.equal(request.outputRevision, outputRevision);
      assert.equal(request.maxBytes, chunkBytes);
      if (offset >= data.length) {
        return { json: JSON.stringify({ outputRevision, done: true, bytes: 0 }) };
      }
      const chunk = data.subarray(offset, Math.min(offset + chunkBytes, data.length));
      offset += chunk.length;
      return { data: chunk, json: JSON.stringify({ outputRevision, done: false, bytes: chunk.length }) };
    }
    if (request.command === "outputRelease") {
      assert.equal(request.outputRevision, outputRevision);
      return { json: JSON.stringify({ state: "released", outputRevision }) };
    }
    return undefined;
  };
}

test("stream exporter keeps one page raster live and releases transferred PNGs", async () => {
  const commands = [];
  const addRequests = [];
  let live = 0;
  let peak = 0;
  const doc = fakeDocument(500, {
    onGetPage() {
      assert.equal(live, 0, "the previous page canvas was still live")
    },
  });
  const output = fakeOutputSession(new Uint8Array([1]));
  const result = await streamRedactedPDF({
    doc,
    selections: new Map(),
    invoke: async (request) => {
      commands.push(request.command);
      if (request.command === "add") {
        assert.equal(live, 1, "page canvas was released before add acknowledgement")
        addRequests.push(request);
      }
      return output(request) ?? { json: "{}" };
    },
    terminateWorker() {},
    createCanvas(width, height) {
      live++;
      peak = Math.max(peak, live);
      const canvas = { width, height };
      return {
        canvas,
        context: fakeContext(),
        dispose() {
          canvas.width = 0;
          canvas.height = 0;
          live--;
        },
      };
    },
    encodePNG: async () => new Blob([new Uint8Array([1])], { type: "image/png" }),
  });
  assert.deepEqual(commands, [
    "start", ...Array(500).fill("add"), "finish", "outputRead", "outputRead", "outputRelease",
  ]);
  assert.equal(result.data.length, 1);
  assert.equal(result.data[0], 1);
  assert.equal(peak, 1);
  assert.equal(live, 0);
  assert.equal(addRequests.length, 500);
  assert.ok(addRequests.every((request) => request.page.pngData === null), "acknowledged PNG references were retained");
});

test("source and page pixel budgets fail before canvas allocation", async () => {
  assert.throws(
    () => validateRedactSource({ size: REDACT_LIMITS.maxInputBytes + 1 }),
    /input/i,
  );
  let allocations = 0;
  await assert.rejects(streamRedactedPDF({
    doc: fakeDocument(1, { width: 5000, height: 5000 }),
    selections: new Map(),
    invoke: async () => ({ json: "{}" }),
    terminateWorker() {},
    createCanvas() {
      allocations++;
      return { canvas: {}, context: fakeContext(), dispose() {} };
    },
    encodePNG: async () => new Blob([new Uint8Array([1])]),
  }), /pixel/i);
  assert.equal(allocations, 0);
});

test("PNG throughput and output budgets are enforced and abort the session", async () => {
  const commands = [];
  let disposed = 0;
  await assert.rejects(streamRedactedPDF({
    doc: fakeDocument(1),
    selections: new Map(),
    invoke: async (request) => {
      commands.push(request.command);
      return { json: "{}" };
    },
    terminateWorker() {},
    createCanvas(width, height) {
      return { canvas: { width, height }, context: fakeContext(), dispose() { disposed++; } };
    },
	encodePNG: async () => new Blob([new Uint8Array([1, 2])]),
    limits: { ...REDACT_LIMITS, maxPagePNGBytes: 1 },
  }), /PNG/i);
  assert.deepEqual(commands, ["start", "abort"]);
  assert.equal(disposed, 1);

  const oversizedOutput = fakeOutputSession(new Uint8Array([1, 2]));
  await assert.rejects(streamRedactedPDF({
    doc: fakeDocument(1),
    selections: new Map(),
    invoke: async (request) => oversizedOutput(request) ?? { json: "{}" },
    terminateWorker() {},
    createCanvas(width, height) {
      return { canvas: { width, height }, context: fakeContext(), dispose() {} };
    },
    encodePNG: async () => new Blob([new Uint8Array([1])]),
    limits: { ...REDACT_LIMITS, maxOutputBytes: 1 },
  }), /output/i);
});

test("render cancellation is idempotent, terminates the worker, and permits a fresh session", async () => {
  const controller = new AbortController();
  let renderCancelled = 0;
  let workerTerminated = 0;
  let signalRenderStarted;
  const renderStarted = new Promise((resolve) => { signalRenderStarted = resolve; });
  let rejectRender;
  const doc = {
    numPages: 1,
    async getPage() {
      return {
        getViewport({ scale }) { return { width: scale, height: scale }; },
        render() {
          signalRenderStarted();
          return {
            promise: new Promise((resolve, reject) => { rejectRender = reject; }),
            cancel() {
              renderCancelled++;
              rejectRender(new DOMException("Aborted", "AbortError"));
            },
          };
        },
        cleanup() {},
      };
    },
  };
  const pending = streamRedactedPDF({
    doc,
    selections: new Map(),
    invoke: async () => ({ json: "{}" }),
    terminateWorker() { workerTerminated++; },
    createCanvas(width, height) {
      return { canvas: { width, height }, context: fakeContext(), dispose() {} };
    },
    encodePNG: async () => new Blob(),
    signal: controller.signal,
  });
  await renderStarted;
  controller.abort();
  controller.abort();
  await assert.rejects(pending, (error) => error?.name === "AbortError");
  assert.equal(renderCancelled, 1);
  assert.equal(workerTerminated, 1);

  const resumedCommands = [];
  const resumedOutput = fakeOutputSession(new Uint8Array([9]));
  const resumed = await streamRedactedPDF({
    doc: fakeDocument(1),
    selections: new Map(),
    invoke: async (request) => {
      resumedCommands.push({ command: request.command, workerGeneration: workerTerminated });
      return resumedOutput(request) ?? { json: "{}" };
    },
    terminateWorker() { workerTerminated++; },
    createCanvas(width, height) {
      return { canvas: { width, height }, context: fakeContext(), dispose() {} };
    },
    encodePNG: async () => new Blob([new Uint8Array([1])]),
  });
  assert.deepEqual(resumedCommands, [
    { command: "start", workerGeneration: 1 },
    { command: "add", workerGeneration: 1 },
    { command: "finish", workerGeneration: 1 },
    { command: "outputRead", workerGeneration: 1 },
    { command: "outputRead", workerGeneration: 1 },
    { command: "outputRelease", workerGeneration: 1 },
  ]);
  assert.equal(resumed.data[0], 9);
});
