import assert from "node:assert/strict";
import { test } from "node:test";
import { createReceiveSink, MAX_STORAGE_WORKER_CONTROL_CHARS, MemoryReceiveSink } from "./storage.mjs";
import { createStorageWorkerHandler } from "./storage-worker.mjs";

function fakeDirectory(initialNames = []) {
  const entries = new Map();
  const created = [];
  const removed = [];
  for (const name of initialNames) entries.set(name, { name, existing: true });
  const root = {
    async getFileHandle(name, { create = false } = {}) {
      if (entries.has(name)) return entries.get(name);
      if (!create) throw new DOMException("missing", "NotFoundError");
      const state = { name, existing: false, writes: [], closed: false };
      const handle = {
        name,
        async createWritable() {
          return {
            async write(value) { state.writes.push(value); },
            async truncate(size) { state.size = size; },
            async close() { state.closed = true; },
            async abort() { state.aborted = true; },
          };
        },
        state,
      };
      entries.set(name, handle);
      created.push(name);
      return handle;
    },
    async removeEntry(name) {
      if (!entries.has(name)) throw new DOMException("missing", "NotFoundError");
      entries.delete(name);
      removed.push(name);
    },
  };
  return { root, entries, created, removed };
}

function loopbackWorker(handler) {
  const listeners = new Map();
  return {
    terminated: false,
    addEventListener(type, listener) {
      const entries = listeners.get(type) || [];
      entries.push(listener);
      listeners.set(type, entries);
    },
    removeEventListener(type, listener) {
      listeners.set(type, (listeners.get(type) || []).filter((entry) => entry !== listener));
    },
    postMessage(message) {
      queueMicrotask(async () => {
        try {
          const response = await handler(message);
          for (const listener of listeners.get("message") || []) listener({ data: response });
        } catch (error) {
          for (const listener of listeners.get("error") || []) listener({ error, message: error.message });
        }
      });
    },
    terminate() { this.terminated = true; },
  };
}

test("sink selection prefers a chosen directory", async () => {
  const directory = { getFileHandle() {} };
  const sink = await createReceiveSink({ directory, storage: { getDirectory: async () => { throw new Error("must not run"); } } });
  assert.equal(sink.kind, "directory");
});

test("sink selection falls back to bounded memory when OPFS is unavailable", async () => {
  const sink = await createReceiveSink({
    maxMemoryBytes: 123,
    storage: { getDirectory: async () => { throw new Error("private mode"); } },
  });
  assert.ok(sink instanceof MemoryReceiveSink);
  assert.equal(sink.maxBytes, 123);
});

test("sink selection uses the OPFS worker path when the worker can initialize", async () => {
  const directory = fakeDirectory();
  let mainThreadStorageCalls = 0;
  const worker = loopbackWorker(createStorageWorkerHandler({
    getDirectory: async () => directory.root,
  }));
  const sink = await createReceiveSink({
    workerFactory: () => worker,
    storage: { async getDirectory() { mainThreadStorageCalls++; throw new Error("must not use main thread OPFS"); } },
  });
  const file = { id: "a", name: "worker.bin", size: 1, type: "application/octet-stream" };

  await sink.prepare([file]);
  await sink.write(file, 0, new Uint8Array([7]));
  const handle = await sink.finish(file);

  assert.equal(sink.kind, "opfs");
  assert.equal(sink.workerBacked, true);
  assert.equal(sink.outputName(file), "worker.bin");
  assert.equal(handle.name, "worker.bin");
  assert.equal(mainThreadStorageCalls, 0);
  await sink.release(file);
  assert.deepEqual(directory.removed, ["worker.bin"]);
  assert.equal(worker.terminated, true);
});

test("oversized worker responses are rejected before falling back to main-thread OPFS", async () => {
  const directory = fakeDirectory();
  const worker = loopbackWorker(async (message) => ({
    id: message.id,
    ok: true,
    result: { padding: "x".repeat(MAX_STORAGE_WORKER_CONTROL_CHARS + 1) },
  }));
  const sink = await createReceiveSink({
    workerFactory: () => worker,
    storage: { async getDirectory() { return directory.root; } },
  });

  assert.equal(sink.kind, "opfs");
  assert.equal(sink.workerBacked, undefined);
  assert.equal(worker.terminated, true);
});

test("storage worker serializes writes while an earlier OPFS write is pending", async () => {
  let releaseFirstWrite;
  let writeCalls = 0;
  const writable = {
    async write() {
      writeCalls++;
      if (writeCalls === 1) await new Promise((resolve) => { releaseFirstWrite = resolve; });
    },
    async truncate() {},
    async close() {},
    async abort() {},
  };
  const handle = { async createWritable() { return writable; } };
  const root = {
    async getFileHandle(name, { create = false } = {}) {
      if (!create) throw new DOMException("missing", "NotFoundError");
      return handle;
    },
    async removeEntry() {},
  };
  const handler = createStorageWorkerHandler({ getDirectory: async () => root });
  const file = { id: "a", name: "ordered.bin", size: 2, type: "application/octet-stream" };
  assert.equal((await handler({ id: 1, type: "init" })).ok, true);
  assert.equal((await handler({ id: 2, type: "prepare", files: [file] })).ok, true);

  const first = handler({ id: 3, type: "write", file, offset: 0, data: new Uint8Array([1]) });
  while (!releaseFirstWrite) await new Promise((resolve) => setTimeout(resolve, 0));
  const second = handler({ id: 4, type: "write", file, offset: 1, data: new Uint8Array([2]) });
  await new Promise((resolve) => setTimeout(resolve, 0));
  releaseFirstWrite();

  assert.deepEqual((await Promise.all([first, second])).map((response) => response.ok), [true, true]);
});

test("directory sink chooses a collision-free name without opening an existing file for writing", async () => {
  const directory = fakeDirectory(["report.pdf"]);
  const sink = await createReceiveSink({ directory: directory.root });
  const file = { id: "a", name: "report.pdf", size: 1, type: "application/pdf" };

  await sink.prepare([file]);
  await sink.write(file, 0, new Uint8Array([1]));
  await sink.finish(file);

  assert.equal(sink.outputName(file), "report (2).pdf");
  assert.deepEqual(directory.created, ["report (2).pdf"]);
  assert.equal(directory.entries.get("report.pdf").existing, true);
});

test("failed directory transfer removes only incomplete outputs and preserves finished files", async () => {
  const directory = fakeDirectory(["keep.txt"]);
  const sink = await createReceiveSink({ directory: directory.root });
  const completed = { id: "a", name: "keep.txt", size: 1, type: "text/plain" };
  const partial = { id: "b", name: "partial.txt", size: 2, type: "text/plain" };

  await sink.prepare([completed, partial]);
  await sink.write(completed, 0, new Uint8Array([1]));
  await sink.finish(completed);
  await sink.write(partial, 0, new Uint8Array([2]));
  await sink.abort();

  assert.deepEqual([...directory.entries.keys()].sort(), ["keep (2).txt", "keep.txt"]);
  assert.deepEqual(directory.removed, ["partial.txt"]);
  assert.equal(directory.entries.get("keep.txt").existing, true);
  assert.equal(directory.entries.get("keep (2).txt").state.closed, true);
});

test("OPFS sink releases a completed hidden file", async () => {
  const removed = [];
  const writable = {
    async write() {},
    async truncate() {},
    async close() {},
    async abort() {},
  };
  const handle = { async createWritable() { return writable; } };
  const root = {
    async getFileHandle(name, { create = false } = {}) {
      if (!create) throw new DOMException("missing", "NotFoundError");
      return handle;
    },
    async removeEntry(name) { removed.push(name); },
  };
  const sink = await createReceiveSink({ storage: { async getDirectory() { return root; } } });
  const file = { id: "a", name: "a.bin", size: 1, type: "x" };
  await sink.prepare([file]);
  await sink.write(file, 0, new Uint8Array([1]));
  assert.equal(await sink.finish(file), handle);
  await sink.release(file);
  assert.deepEqual(removed, ["a.bin"]);
});
