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
  if (!value || value.version !== 3 || typeof value.transferId !== "string" ||
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
  return Object.freeze({ version: 3, transferId: value.transferId, chunkSize: value.chunkSize, files, totalSize });
}

export class ReceiverProtocol {
  constructor(sink) {
    if (!sink || typeof sink.write !== "function") throw new TypeError("receive sink is required");
    this.sink = sink;
    this.manifest = null;
    this.states = new Map();
    this.finished = new Map();
  }

  start(manifest) {
    if (this.manifest) throw new Error("transfer already started");
    this.manifest = validateManifest(manifest);
    for (const file of this.manifest.files) this.states.set(file.id, { file, offset: 0, digests: [] });
    return this.manifest;
  }

  // fileState(fileId) -> the per-file resume/digest bookkeeping, or throws
  // "unknown file id" for any id not present in the manifest (including
  // non-string ids, since Map#get on a foreign key just misses).
  fileState(fileId) {
    if (!this.manifest) throw new Error("transfer not started");
    const state = this.states.get(fileId);
    if (!state) throw new Error("unknown file id");
    return state;
  }

  resumeOffsets() {
    if (!this.manifest) throw new Error("transfer not started");
    const { chunkSize } = this.manifest;
    const offsets = {};
    for (const [id, state] of this.states) {
      // Every chunk write below is whole-chunk-atomic, so state.offset should
      // already be chunk-aligned (or equal to the file size) by construction.
      // Floor+truncate anyway as a defensive invariant for whatever produced
      // this state, since a misaligned resume offset would desync the sender's
      // composite checksum from the receiver's stored digest list.
      if (state.offset !== state.file.size && state.offset % chunkSize !== 0) {
        const aligned = Math.floor(state.offset / chunkSize) * chunkSize;
        state.digests = state.digests.slice(0, Math.floor(aligned / chunkSize));
        state.offset = aligned;
      }
      offsets[id] = state.offset;
    }
    return offsets;
  }

  async chunk(fileId, payload) {
    if (!(payload instanceof Uint8Array)) throw new Error("chunk payload must be bytes");
    const state = this.fileState(fileId);
    if (state.offset >= state.file.size) throw new Error("file has no remaining bytes");
    const expectedLength = Math.min(this.manifest.chunkSize, state.file.size - state.offset);
    if (payload.byteLength !== expectedLength) throw new Error("unexpected chunk length");
    const digest = await sha256Hex(payload);
    await this.sink.write(state.file, state.offset, payload);
    state.offset += payload.byteLength;
    state.digests.push(digest);
    return { offset: state.offset };
  }

  async finish(fileId, checksum) {
    const state = this.fileState(fileId);
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
