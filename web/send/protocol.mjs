import { sha256Hex, transferChecksum } from "./integrity.mjs";

export const MAX_FILES = 1000;
export const MAX_TOTAL_BYTES = 8 * 1024 * 1024 * 1024;
export const MAX_CHUNK_BYTES = 1024 * 1024;
export const MAX_CHUNKS = 1_000_000;

function validFileName(name) {
  return typeof name === "string" && name.length > 0 && name !== "." && name !== ".." &&
    !/[\\/\0]/.test(name) && new TextEncoder().encode(name).byteLength <= 255;
}

function truncateUTF8(value, maxBytes) {
  let out = "";
  let size = 0;
  for (const char of value) {
    const bytes = new TextEncoder().encode(char).byteLength;
    if (size + bytes > maxBytes) break;
    out += char;
    size += bytes;
  }
  return out;
}

export function safeFileName(name, used) {
  if (!validFileName(name)) throw new Error("invalid file name");
  if (!(used instanceof Set)) throw new TypeError("used names must be a Set");
  if (!used.has(name)) { used.add(name); return name; }
  const dot = name.lastIndexOf(".");
  const base = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  for (let n = 2; ; n++) {
    const suffix = ` (${n})`;
    const suffixBytes = new TextEncoder().encode(suffix).byteLength;
    const safeExt = truncateUTF8(ext, Math.min(64, 255 - suffixBytes));
    const extBytes = new TextEncoder().encode(safeExt).byteLength;
    const safeBase = truncateUTF8(base, 255 - suffixBytes - extBytes);
    const candidate = `${safeBase}${suffix}${safeExt}`;
    if (!used.has(candidate)) { used.add(candidate); return candidate; }
  }
}

export function validateManifest(value) {
  if (!value || value.version !== 2 || typeof value.transferId !== "string" ||
      value.transferId.length < 1 || value.transferId.length > 128 ||
      !Number.isSafeInteger(value.chunkSize) || value.chunkSize < 1 || value.chunkSize > MAX_CHUNK_BYTES ||
      !Array.isArray(value.files) || value.files.length < 1 || value.files.length > MAX_FILES) {
    throw new Error("invalid transfer manifest");
  }
  const ids = new Set();
  const usedNames = new Set();
  let totalSize = 0;
  let totalChunks = 0;
  const files = value.files.map((file) => {
    if (!file || typeof file.id !== "string" || !file.id || file.id.length > 128 || ids.has(file.id)) {
      throw new Error("invalid or duplicate file id");
    }
    ids.add(file.id);
    if (!validFileName(file.name)) throw new Error("invalid file name");
    const name = safeFileName(file.name, usedNames);
    if (!Number.isSafeInteger(file.size) || file.size < 0) throw new Error("invalid file size");
    if (typeof file.type !== "string" || file.type.length > 128) throw new Error("invalid file type");
    totalSize += file.size;
    if (!Number.isSafeInteger(totalSize) || totalSize > MAX_TOTAL_BYTES) throw new Error("transfer size exceeds limit");
    totalChunks += Math.ceil(file.size / value.chunkSize);
    if (!Number.isSafeInteger(totalChunks) || totalChunks > MAX_CHUNKS) throw new Error("transfer has too many chunks");
    return Object.freeze({ id: file.id, name, size: file.size, type: file.type || "application/octet-stream" });
  });
  return Object.freeze({ version: 2, transferId: value.transferId, chunkSize: value.chunkSize, files, totalSize });
}

export class ReceiverProtocol {
  constructor(sink) {
    if (!sink || typeof sink.write !== "function") throw new TypeError("receive sink is required");
    this.sink = sink;
    this.manifest = null;
    this.states = new Map();
    this.retries = new Map();
    this.finished = new Map();
  }

  start(manifest) {
    if (this.manifest) throw new Error("transfer already started");
    this.manifest = validateManifest(manifest);
    for (const file of this.manifest.files) this.states.set(file.id, { file, offset: 0, seq: 0, digests: [], last: null });
    return this.manifest;
  }

  resumeOffsets() {
    if (!this.manifest) throw new Error("transfer not started");
    return Object.fromEntries([...this.states].map(([id, state]) => [id, state.offset]));
  }

  async chunk(header, payload) {
    if (!this.manifest) throw new Error("transfer not started");
    if (!(payload instanceof Uint8Array)) throw new Error("chunk payload must be bytes");
    const state = this.states.get(header?.fileId);
    if (!state) throw new Error("unknown file id");
    if (!Number.isSafeInteger(header.seq) || header.seq < 0) throw new Error("unexpected chunk sequence");
    if (!Number.isSafeInteger(header.offset) || header.offset < 0) throw new Error("unexpected chunk offset");
    if (!Number.isSafeInteger(header.length) || header.length !== payload.byteLength || header.length <= 0 ||
        header.length > this.manifest.chunkSize || header.offset + header.length > state.file.size) {
      throw new Error("invalid chunk length");
    }
    if (typeof header.sha256 !== "string" || !/^[0-9a-f]{64}$/i.test(header.sha256)) throw new Error("invalid chunk hash");
    const actual = await sha256Hex(payload);
    const retryKey = `${state.file.id}:${header.seq}`;
    if (state.last && header.seq === state.last.seq && header.offset === state.last.offset &&
        header.length === state.last.length && header.sha256.toLowerCase() === state.last.sha256) {
      if (actual !== state.last.sha256) {
        const attempts = (this.retries.get(retryKey) || 0) + 1;
        this.retries.set(retryKey, attempts);
        if (attempts > 3) throw new Error("chunk retry limit exceeded");
        return { type: "nack", fileId: state.file.id, seq: header.seq, offset: header.offset };
      }
      this.retries.delete(retryKey);
      return state.last.ack;
    }
    if (header.seq !== state.seq) throw new Error("unexpected chunk sequence");
    if (header.offset !== state.offset) throw new Error("unexpected chunk offset");
    if (actual !== header.sha256.toLowerCase()) {
      const attempts = (this.retries.get(retryKey) || 0) + 1;
      this.retries.set(retryKey, attempts);
      if (attempts > 3) throw new Error("chunk retry limit exceeded");
      return { type: "nack", fileId: state.file.id, seq: state.seq, offset: state.offset };
    }
    await this.sink.write(state.file, state.offset, payload);
    this.retries.delete(retryKey);
    state.offset += payload.byteLength;
    state.seq++;
    state.digests.push(actual);
    const ack = { type: "ack", fileId: state.file.id, seq: header.seq, offset: state.offset };
    state.last = { seq: header.seq, offset: header.offset, length: header.length, sha256: actual, ack };
    return ack;
  }

  async finish(fileId, checksum) {
    if (!this.manifest) throw new Error("transfer not started");
    const state = this.states.get(fileId);
    if (!state) throw new Error("unknown file id");
    if (state.offset !== state.file.size) throw new Error("file is incomplete");
    const expected = await transferChecksum(state.digests);
    if (checksum !== expected) throw new Error("file checksum mismatch");
    const previous = this.finished.get(fileId);
    if (previous) return { ...previous, replayed: true };
    if (typeof this.sink.finish !== "function") throw new Error("receive sink cannot finish files");
    const value = await this.sink.finish(state.file);
    const result = { file: state.file, value };
    this.finished.set(fileId, result);
    return result;
  }

  complete() {
    if (!this.manifest) throw new Error("transfer not started");
    if (this.finished.size !== this.manifest.files.length) throw new Error("transfer is incomplete");
    return { transferId: this.manifest.transferId, files: this.manifest.files.length, bytes: this.manifest.totalSize };
  }
}
