export const DEFAULT_MAX_MEMORY_BYTES = 256 * 1024 * 1024;

function bytes(value) {
  if (!(value instanceof Uint8Array)) throw new TypeError("output sink writes require Uint8Array chunks");
  return value;
}

function safeName(value) {
  const cleaned = String(value || "paper-tools-batch.zip")
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, "_")
    .trim();
  return cleaned && cleaned !== "." && cleaned !== ".." ? cleaned : "paper-tools-batch.zip";
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

async function uniqueFileName(root, requested) {
  const name = safeName(requested);
  if (!(await fileExists(root, name))) return name;
  const dot = name.lastIndexOf(".");
  const base = dot > 0 ? name.slice(0, dot) : name;
  const extension = dot > 0 ? name.slice(dot) : "";
  for (let index = 2; ; index++) {
    const candidate = `${base} (${index})${extension}`;
    if (!(await fileExists(root, candidate))) return candidate;
  }
}

export class MemoryBlobSink {
  constructor({ maxBytes = DEFAULT_MAX_MEMORY_BYTES, type = "application/zip" } = {}) {
    if (!Number.isSafeInteger(maxBytes) || maxBytes < 0) throw new RangeError("invalid output memory limit");
    this.kind = "memory";
    this.maxBytes = maxBytes;
    this.type = type;
    this.chunks = [];
    this.bufferedBytes = 0;
    this.state = "open";
  }

  get chunkCount() {
    return this.chunks.length;
  }

  async write(value) {
    if (this.state !== "open") throw new Error("output sink is not open");
    const chunk = bytes(value);
    const next = this.bufferedBytes + chunk.byteLength;
    if (!Number.isSafeInteger(next) || next > this.maxBytes) throw new Error("output memory limit exceeded");
    this.chunks.push(chunk);
    this.bufferedBytes = next;
  }

  async close() {
    if (this.state !== "open") throw new Error("output sink is not open");
    this.state = "closed";
    const blob = new Blob(this.chunks, { type: this.type });
    this.chunks = [];
    this.bufferedBytes = 0;
    return blob;
  }

  async abort() {
    if (this.state === "aborted") return;
    this.state = "aborted";
    this.chunks = [];
    this.bufferedBytes = 0;
  }

  async cleanup() {}
}

export class FileSystemBlobSink {
  static async create(root, kind, requestedName) {
    if (!root || typeof root.getFileHandle !== "function") throw new TypeError("invalid output directory");
    const name = await uniqueFileName(root, requestedName);
    const handle = await root.getFileHandle(name, { create: true });
    try {
      const writable = await handle.createWritable({ keepExistingData: false });
      return new FileSystemBlobSink(root, handle, writable, kind, name);
    } catch (error) {
      try { await root.removeEntry?.(name); } catch {}
      throw error;
    }
  }

  constructor(root, handle, writable, kind, name) {
    this.root = root;
    this.handle = handle;
    this.writable = writable;
    this.kind = kind;
    this.name = name;
    this.state = "open";
    this.failure = null;
    this.abortPromise = null;
    this.removePromise = null;
    this.removed = false;
    this.tail = Promise.resolve();
  }

  queue(task) {
    const operation = this.tail.then(async () => {
      if (this.failure) throw this.failure;
      try {
        return await task();
      } catch (error) {
        this.failure = error instanceof Error ? error : new Error(String(error));
        throw this.failure;
      }
    });
    this.tail = operation.catch(() => {});
    return operation;
  }

  write(value) {
    if (this.state !== "open") return Promise.reject(new Error("output sink is not open"));
    const chunk = bytes(value);
    return this.queue(async () => {
      if (this.state === "aborted") throw new Error("output sink is aborted");
      await this.writable.write(chunk);
    });
  }

  async close() {
    if (this.state !== "open") throw new Error("output sink is not open");
    this.state = "closing";
    return this.queue(async () => {
      await this.writable.close();
      this.state = "closed";
      if (this.kind === "opfs") return this.handle.getFile();
      return null;
    });
  }

  removeOwnedFile() {
    if (this.removed || typeof this.root.removeEntry !== "function") return Promise.resolve();
    if (this.removePromise) return this.removePromise;
    const removal = (async () => {
      await this.root.removeEntry(this.name);
      this.removed = true;
    })();
    this.removePromise = removal;
    removal.catch(() => {
      if (this.removePromise === removal) this.removePromise = null;
    });
    return removal;
  }

  abort() {
    this.state = "aborted";
    if (!this.abortPromise) {
      this.abortPromise = (async () => {
        try { await this.writable.abort(); } catch {}
      })();
    }
    return this.abortPromise.then(async () => {
      try { await this.removeOwnedFile(); } catch {}
    });
  }

  async cleanup() {
    if (this.kind !== "opfs" || this.state !== "closed") return;
    try { await this.removeOwnedFile(); } catch {}
  }
}

export async function createOutputSink(options = {}) {
  const name = options.name || "paper-tools-batch.zip";
  if (options.directory) return FileSystemBlobSink.create(options.directory, "directory", name);

  const storage = options.storage === undefined ? globalThis.navigator?.storage : options.storage;
  if (storage && typeof storage.getDirectory === "function") {
    try {
      const root = await storage.getDirectory();
      return await FileSystemBlobSink.create(root, "opfs", name);
    } catch {}
  }

  return new MemoryBlobSink({ maxBytes: options.maxMemoryBytes, type: options.type });
}
