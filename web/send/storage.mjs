import { MAX_CHUNK_BYTES, safeFileName } from "./protocol.mjs";

export const MAX_STORAGE_WORKER_CONTROL_CHARS = 1024 * 1024;
export const MAX_STORAGE_WORKER_CHUNK_BYTES = MAX_CHUNK_BYTES;
export const MAX_STORAGE_WORKER_PENDING_REQUESTS = 32;

export function storageWorkerControlChars(value) {
  try {
    return JSON.stringify(value, (key, item) => (
      key === "data" && item instanceof Uint8Array ? { byteLength: item.byteLength } : item
    )).length;
  } catch {
    return Number.POSITIVE_INFINITY;
  }
}

export class MemoryReceiveSink {
  constructor({ maxBytes = 256 * 1024 * 1024 } = {}) {
    if (!Number.isSafeInteger(maxBytes) || maxBytes < 0) throw new RangeError("invalid memory limit");
    this.kind = "memory";
    this.maxBytes = maxBytes;
    this.bytesHeld = 0;
    this.files = new Map();
    this.aborted = false;
  }

  async prepare(files) {
    const total = files.reduce((sum, file) => sum + file.size, 0);
    if (!Number.isSafeInteger(total) || total > this.maxBytes) throw new Error("memory quota exceeded");
    this.files.clear();
    for (const file of files) this.files.set(file.id, { file, offset: 0, chunks: [] });
  }

  async write(file, offset, bytes) {
    if (this.aborted) throw new Error("receive sink is aborted");
    const state = this.files.get(file.id);
    if (!state || offset !== state.offset) throw new Error("unexpected write offset");
    if (!(bytes instanceof Uint8Array) || state.offset + bytes.byteLength > state.file.size ||
        this.bytesHeld + bytes.byteLength > this.maxBytes) throw new Error("write exceeds declared size");
    const copy = bytes.slice();
    state.chunks.push(copy);
    state.offset += copy.byteLength;
    this.bytesHeld += copy.byteLength;
  }

  async finish(file) {
    const state = this.files.get(file.id);
    if (!state || state.offset !== state.file.size) throw new Error("file is incomplete");
    const blob = new Blob(state.chunks, { type: state.file.type });
    this.bytesHeld -= state.offset;
    state.chunks = [];
    this.files.delete(file.id);
    return blob;
  }

  async abort() {
    this.aborted = true;
    this.files.clear();
    this.bytesHeld = 0;
  }
}

async function fileExists(root, name) {
  try {
    await root.getFileHandle(name, { create: false });
    return true;
  } catch (error) {
    if (error?.name === "NotFoundError") return false;
    throw error;
  }
}

async function chooseOutputName(root, requested, reserved) {
  const tried = new Set(reserved);
  let candidate = safeFileName(requested, tried);
  while (await fileExists(root, candidate)) candidate = safeFileName(requested, tried);
  return candidate;
}

export class FileSystemReceiveSink {
  constructor(root, kind) {
    this.root = root;
    this.kind = kind;
    this.handles = new Map();
    this.names = new Set();
    this.reservedNames = new Set();
    this.outputNames = new Map();
  }

  async prepare(files) {
    for (const file of files) {
      const outputName = await chooseOutputName(this.root, file.name, this.reservedNames);
      this.reservedNames.add(outputName);
      const handle = await this.root.getFileHandle(outputName, { create: true });
      this.names.add(outputName);
      const writable = await handle.createWritable({ keepExistingData: false });
      this.handles.set(file.id, { file, handle, writable, offset: 0, outputName });
      this.outputNames.set(file.id, outputName);
    }
  }

  outputName(file) {
    return this.outputNames.get(file.id) || file.name;
  }

  async write(file, offset, bytes) {
    const state = this.handles.get(file.id);
    if (!state || state.offset !== offset) throw new Error("unexpected write offset");
    if (offset + bytes.byteLength > state.file.size) throw new Error("write exceeds declared size");
    await state.writable.write({ type: "write", position: offset, data: bytes });
    state.offset += bytes.byteLength;
  }

  async finish(file) {
    const state = this.handles.get(file.id);
    if (!state || state.offset !== state.file.size) throw new Error("file is incomplete");
    await state.writable.truncate(state.file.size);
    await state.writable.close();
    this.handles.delete(file.id);
    return state.handle;
  }

  async release(file) {
    const outputName = this.outputNames.get(file.id);
    if (this.kind !== "opfs" || !outputName || !this.names.has(outputName) || typeof this.root.removeEntry !== "function") return;
    await this.root.removeEntry(outputName);
    this.names.delete(outputName);
    this.outputNames.delete(file.id);
  }

  async abort() {
    for (const state of this.handles.values()) {
      try { await state.writable.abort(); } catch {}
    }
    this.handles.clear();
    if (typeof this.root.removeEntry === "function") {
      for (const name of this.names) {
        try { await this.root.removeEntry(name); } catch {}
      }
    }
    this.names.clear();
    this.reservedNames.clear();
    this.outputNames.clear();
  }
}

class WorkerReceiveSink {
  constructor(worker, { timeoutMs = 10_000 } = {}) {
    if (!worker || typeof worker.postMessage !== "function") throw new TypeError("invalid storage worker");
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new RangeError("invalid storage worker timeout");
    this.kind = "opfs";
    this.workerBacked = true;
    this.worker = worker;
    this.timeoutMs = timeoutMs;
    this.nextRequestId = 1;
    this.pending = new Map();
    this.outputNames = new Map();
    this.closed = false;
    this.onMessage = ({ data }) => {
      if (!data || !Number.isSafeInteger(data.id)) return;
      const request = this.pending.get(data.id);
      if (!request) return;
      this.pending.delete(data.id);
      clearTimeout(request.timer);
      if (storageWorkerControlChars(data) > MAX_STORAGE_WORKER_CONTROL_CHARS) {
        request.reject(new Error("storage worker response message limit exceeded"));
        return;
      }
      if (data.ok) request.resolve(data.result);
      else request.reject(new Error(String(data.error || "storage worker failed")));
    };
    this.onError = (event) => this.failAll(event?.error || new Error(event?.message || "storage worker failed"));
    worker.addEventListener("message", this.onMessage);
    worker.addEventListener("error", this.onError);
  }

  static async create(worker, options) {
    const sink = new WorkerReceiveSink(worker, options);
    try {
      await sink.request("init");
      return sink;
    } catch (error) {
      sink.dispose();
      throw error;
    }
  }

  request(type, fields = {}, transfer = []) {
    if (this.closed) return Promise.reject(new Error("storage worker is closed"));
    if (this.pending.size >= MAX_STORAGE_WORKER_PENDING_REQUESTS) {
      return Promise.reject(new Error("storage worker pending request limit exceeded"));
    }
    const message = { id: this.nextRequestId++, type, ...fields };
    if (storageWorkerControlChars(message) > MAX_STORAGE_WORKER_CONTROL_CHARS) {
      return Promise.reject(new Error("storage worker control message limit exceeded"));
    }
    if (message.data && (!(message.data instanceof Uint8Array) ||
        message.data.byteLength > MAX_STORAGE_WORKER_CHUNK_BYTES)) {
      return Promise.reject(new Error("storage worker chunk message limit exceeded"));
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(message.id);
        reject(new Error("storage worker request timed out"));
      }, this.timeoutMs);
      this.pending.set(message.id, { resolve, reject, timer });
      try {
        this.worker.postMessage(message, transfer);
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(message.id);
        reject(error);
      }
    });
  }

  async prepare(files) {
    const result = await this.request("prepare", { files });
    this.outputNames = new Map(Object.entries(result.outputNames || {}));
  }

  outputName(file) {
    return this.outputNames.get(file.id) || file.name;
  }

  async write(file, offset, bytes) {
    if (!(bytes instanceof Uint8Array)) throw new Error("chunk payload must be bytes");
    const data = bytes.slice();
    await this.request("write", { file, offset, data }, [data.buffer]);
  }

  async finish(file) {
    const result = await this.request("finish", { file });
    return result.value;
  }

  async release(file) {
    if (this.closed) return;
    await this.request("release", { file });
    this.outputNames.delete(file.id);
    if (this.outputNames.size === 0) this.dispose();
  }

  async abort() {
    if (this.closed) return;
    try { await this.request("abort"); }
    finally { this.dispose(); }
  }

  failAll(error) {
    for (const request of this.pending.values()) {
      clearTimeout(request.timer);
      request.reject(error);
    }
    this.pending.clear();
  }

  dispose() {
    if (this.closed) return;
    this.closed = true;
    this.worker.removeEventListener("message", this.onMessage);
    this.worker.removeEventListener("error", this.onError);
    this.failAll(new Error("storage worker is closed"));
    this.worker.terminate();
  }
}

export async function createReceiveSink(options = {}) {
  if (options.directory) return new FileSystemReceiveSink(options.directory, "directory");
  const workerFactory = options.workerFactory || (
    typeof Worker === "function"
      ? () => new Worker(new URL("./storage-worker.mjs", import.meta.url), { type: "module" })
      : null
  );
  if (workerFactory) {
    let worker = null;
    try {
      worker = workerFactory();
      return await WorkerReceiveSink.create(worker, { timeoutMs: options.storageWorkerTimeoutMs });
    } catch {
      try { worker?.terminate?.(); } catch {}
    }
  }
  if (options.storage?.getDirectory) {
    try {
      const root = await options.storage.getDirectory();
      return new FileSystemReceiveSink(root, "opfs");
    } catch {}
  }
  return new MemoryReceiveSink({ maxBytes: options.maxMemoryBytes });
}
