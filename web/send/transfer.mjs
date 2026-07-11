import { sha256Hex, transferChecksum } from "./integrity.mjs";
import { MAX_CHUNK_BYTES, MAX_FILES, MAX_TOTAL_BYTES, ReceiverProtocol, validateManifest } from "./protocol.mjs";
import { MemoryReceiveSink } from "./storage.mjs";

const LEGACY_MAX_BYTES = 256 * 1024 * 1024;
const MAX_CONTROL_CHARS = 1024 * 1024;
export const MAX_QUEUED_CONTROL_MESSAGES = 256;
export const MAX_QUEUED_CONTROL_CHARS = 256 * 1024;
const DEFAULT_CHUNK_SIZE = 64 * 1024;
const DEFAULT_NEGOTIATION_TIMEOUT_MS = 500;
const DEFAULT_ACK_TIMEOUT_MS = 10_000;
const DEFAULT_BUFFER_HIGH_WATERMARK = 8 * 1024 * 1024;

function timeoutError(label) {
  const error = new Error(`${label} timed out`);
  error.code = "TIMEOUT";
  return error;
}

function randomTransferId() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function validateSourceFiles(files) {
  if (!Array.isArray(files) || files.length < 1 || files.length > MAX_FILES) throw new Error("invalid file selection");
  let total = 0;
  for (const file of files) {
    if (!file || typeof file.name !== "string" || !Number.isSafeInteger(file.size) || file.size < 0 ||
        typeof file.type !== "string" || typeof file.slice !== "function") throw new Error("invalid source file");
    total += file.size;
    if (!Number.isSafeInteger(total) || total > MAX_TOTAL_BYTES) throw new Error("transfer size exceeds limit");
  }
  return files;
}

export function buildTransferManifest(files, { chunkSize = DEFAULT_CHUNK_SIZE, transferId = randomTransferId() } = {}) {
  validateSourceFiles(files);
  return validateManifest({
    version: 2,
    transferId,
    chunkSize,
    files: files.map((file, index) => ({
      id: `f${index}`,
      name: file.name,
      size: file.size,
      type: file.type || "application/octet-stream",
    })),
  });
}

function parseControl(value) {
  if (typeof value !== "string" || value.length > MAX_CONTROL_CHARS) throw new Error("invalid control message");
  let message;
  try { message = JSON.parse(value); } catch { throw new Error("invalid control message"); }
  if (!message || typeof message !== "object" || Array.isArray(message)) throw new Error("invalid control message");
  return message;
}

function sendControl(channel, message) {
  channel.send(JSON.stringify(message));
}

async function asBytes(value) {
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  if (typeof Blob !== "undefined" && value instanceof Blob) return new Uint8Array(await value.arrayBuffer());
  throw new Error("invalid binary message");
}

function waitForOpen(channel) {
  if (channel.readyState === "open") return Promise.resolve();
  if (channel.readyState === "closed" || channel.readyState === "closing") return Promise.reject(new Error("data channel is closed"));
  return new Promise((resolve, reject) => {
    const opened = () => { cleanup(); resolve(); };
    const closed = () => { cleanup(); reject(new Error("data channel closed before opening")); };
    const cleanup = () => {
      channel.removeEventListener("open", opened);
      channel.removeEventListener("close", closed);
    };
    channel.addEventListener("open", opened, { once: true });
    channel.addEventListener("close", closed, { once: true });
  });
}

async function waitForBuffer(channel, highWatermark) {
  if (channel.readyState !== "open") throw new Error("data channel is not open");
  if (channel.bufferedAmount <= highWatermark) return;
  await new Promise((resolve, reject) => {
    const low = () => { cleanup(); resolve(); };
    const closed = () => { cleanup(); reject(new Error("data channel closed while sending")); };
    const cleanup = () => {
      channel.removeEventListener("bufferedamountlow", low);
      channel.removeEventListener("close", closed);
    };
    channel.addEventListener("bufferedamountlow", low, { once: true });
    channel.addEventListener("close", closed, { once: true });
  });
}

async function waitForDrain(channel, pollMs) {
  while (channel.bufferedAmount > 0) {
    if (channel.readyState !== "open") throw new Error("data channel closed while draining");
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { cleanup(); resolve(); }, pollMs);
      const closed = () => { cleanup(); reject(new Error("data channel closed while draining")); };
      const cleanup = () => {
        clearTimeout(timer);
        channel.removeEventListener("close", closed);
      };
      channel.addEventListener("close", closed, { once: true });
    });
  }
}

class ControlInbox {
  constructor(channel) {
    this.channel = channel;
    this.messages = [];
    this.queuedChars = 0;
    this.waiters = [];
    this.closed = false;
    this.error = null;
    this.onMessage = ({ data }) => {
      if (this.error) return;
      if (typeof data !== "string") return;
      let message;
      try { message = parseControl(data); } catch { return; }
      const index = this.waiters.findIndex((waiter) => waiter.predicate(message));
      if (index === -1) {
        if (this.messages.length >= MAX_QUEUED_CONTROL_MESSAGES ||
            this.queuedChars + data.length > MAX_QUEUED_CONTROL_CHARS) {
          this.fail(new Error("control message queue limit exceeded"));
          return;
        }
        this.messages.push({ message, chars: data.length });
        this.queuedChars += data.length;
      }
      else {
        const [waiter] = this.waiters.splice(index, 1);
        clearTimeout(waiter.timer);
        waiter.resolve(message);
      }
    };
    this.onClose = () => {
      this.closed = true;
      this.fail(new Error("data channel closed"));
    };
    channel.addEventListener("message", this.onMessage);
    channel.addEventListener("close", this.onClose);
  }

  fail(error) {
    if (this.error) return;
    this.error = error;
    this.messages = [];
    this.queuedChars = 0;
    for (const waiter of this.waiters.splice(0)) {
      clearTimeout(waiter.timer);
      waiter.reject(error);
    }
  }

  wait(predicate, timeoutMs, label) {
    const index = this.messages.findIndex((entry) => predicate(entry.message));
    if (index !== -1) {
      const [entry] = this.messages.splice(index, 1);
      this.queuedChars -= entry.chars;
      return Promise.resolve(entry.message);
    }
    if (this.error) return Promise.reject(this.error);
    return new Promise((resolve, reject) => {
      const waiter = { predicate, reject, resolve, timer: null };
      waiter.timer = setTimeout(() => {
        const index2 = this.waiters.indexOf(waiter);
        if (index2 !== -1) this.waiters.splice(index2, 1);
        reject(timeoutError(label));
      }, timeoutMs);
      this.waiters.push(waiter);
    });
  }

  dispose() {
    this.channel.removeEventListener("message", this.onMessage);
    this.channel.removeEventListener("close", this.onClose);
    this.onClose();
  }
}

async function readChunk(file, offset, length) {
  return new Uint8Array(await file.slice(offset, offset + length).arrayBuffer());
}

function validateResumeOffsets(manifest, value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid resume offsets");
  const offsets = {};
  for (const file of manifest.files) {
    const offset = value[file.id];
    if (!Number.isSafeInteger(offset) || offset < 0 || offset > file.size ||
        (offset !== file.size && offset % manifest.chunkSize !== 0)) throw new Error("invalid resume offset");
    offsets[file.id] = offset;
  }
  return offsets;
}

function sameManifest(left, right) {
  if (left.transferId !== right.transferId || left.chunkSize !== right.chunkSize ||
      left.files.length !== right.files.length) return false;
  return left.files.every((file, index) => {
    const other = right.files[index];
    return file.id === other.id && file.name === other.name && file.size === other.size && file.type === other.type;
  });
}

export class ReceiveTransferStore {
  constructor({
    expiryMs = 5 * 60 * 1000,
    scheduleExpiry = (callback, delay) => setTimeout(callback, delay),
    cancelExpiry = (handle) => clearTimeout(handle),
  } = {}) {
    if (!Number.isSafeInteger(expiryMs) || expiryMs < 0) throw new RangeError("invalid transfer expiry");
    if (typeof scheduleExpiry !== "function" || typeof cancelExpiry !== "function") {
      throw new TypeError("invalid transfer expiry scheduler");
    }
    this.expiryMs = expiryMs;
    this.scheduleExpiry = scheduleExpiry;
    this.cancelExpiry = cancelExpiry;
    this.entries = new Map();
  }

  async acquire(value, sinkFactory) {
    const manifest = validateManifest(value);
    const existing = this.entries.get(manifest.transferId);
    if (existing) {
      if (!sameManifest(existing.manifest, manifest)) throw new Error("transfer manifest does not match retained state");
      if (existing.attached) throw new Error("transfer is already connected");
      this.cancelExpiry(existing.expiryTimer);
      existing.expiryTimer = null;
      existing.attached = true;
      return existing;
    }

    let sink = null;
    try {
      sink = await sinkFactory(manifest);
      if (!sink || typeof sink.prepare !== "function") throw new Error("invalid receive sink");
      await sink.prepare(manifest.files);
      const protocol = new ReceiverProtocol(sink);
      protocol.start(manifest);
      const entry = { manifest, sink, protocol, attached: true, expiryTimer: null };
      this.entries.set(manifest.transferId, entry);
      return entry;
    } catch (error) {
      try { await sink?.abort?.(); } catch {}
      throw error;
    }
  }

  detach(transferId) {
    const entry = this.entries.get(transferId);
    if (!entry) return;
    entry.attached = false;
    this.cancelExpiry(entry.expiryTimer);
    entry.expiryTimer = this.scheduleExpiry(() => this.abort(transferId).catch(() => {}), this.expiryMs);
    entry.expiryTimer?.unref?.();
  }

  complete(transferId) {
    const entry = this.entries.get(transferId);
    if (!entry) return;
    this.cancelExpiry(entry.expiryTimer);
    this.entries.delete(transferId);
  }

  async abort(transferId) {
    const entry = this.entries.get(transferId);
    if (!entry) return;
    this.cancelExpiry(entry.expiryTimer);
    this.entries.delete(transferId);
    await entry.sink.abort?.();
  }
}

const defaultReceiveTransferStore = new ReceiveTransferStore();

function validateLegacyFile(file) {
  const nameBytes = new TextEncoder().encode(file.name).byteLength;
  if (!file.name || nameBytes > 255 || /[\\/\0]/.test(file.name) || file.size > LEGACY_MAX_BYTES ||
      (file.type || "").length > 128) throw new Error("file is not compatible with a legacy receiver");
}

class SenderSession {
  constructor(channel, files, options = {}) {
    this.channel = channel;
    this.files = validateSourceFiles(Array.from(files));
    this.options = options;
    this.inbox = new ControlInbox(channel);
    this.started = false;
    channel.binaryType = "arraybuffer";
    channel.bufferedAmountLowThreshold = options.bufferedLowThreshold || 1024 * 1024;
  }

  async start() {
    if (this.started) throw new Error("send session already started");
    this.started = true;
    await waitForOpen(this.channel);
    const negotiationTimeoutMs = this.options.negotiationTimeoutMs ?? DEFAULT_NEGOTIATION_TIMEOUT_MS;
    let hello = null;
    try {
      hello = await this.inbox.wait(
        (message) => message.type === "hello" && Array.isArray(message.versions) && message.versions.includes(2),
        negotiationTimeoutMs,
        "protocol negotiation",
      );
    } catch (error) {
      if (error.code !== "TIMEOUT") throw error;
    }
    if (!hello) return this.sendLegacy();
    return this.sendV2();
  }

  async sendLegacy() {
    if (this.files.length !== 1) throw new Error("the receiving browser only supports one file");
    const file = this.files[0];
    validateLegacyFile(file);
    sendControl(this.channel, { name: file.name, size: file.size, type: file.type || "application/octet-stream" });
    let offset = 0;
    while (offset < file.size) {
      await waitForBuffer(this.channel, this.options.bufferedHighWatermark ?? DEFAULT_BUFFER_HIGH_WATERMARK);
      const length = Math.min(DEFAULT_CHUNK_SIZE, file.size - offset);
      const bytes = await readChunk(file, offset, length);
      this.channel.send(bytes.buffer);
      offset += bytes.byteLength;
      this.options.onProgress?.({ version: 1, file, sent: offset, total: file.size });
    }
    this.channel.send("done");
    await waitForDrain(this.channel, this.options.drainPollMs ?? 50);
    return { version: 1, transferId: null, files: 1, bytes: file.size };
  }

  async waitControl(predicate, label) {
    const message = await this.inbox.wait(
      (value) => value.type === "error" || predicate(value),
      this.options.ackTimeoutMs ?? DEFAULT_ACK_TIMEOUT_MS,
      label,
    );
    if (message.type === "error") throw new Error(message.message || "receiver rejected the transfer");
    return message;
  }

  async sendV2() {
    const manifest = buildTransferManifest(this.files, this.options);
    sendControl(this.channel, { type: "hello", version: 2 });
    sendControl(this.channel, { type: "manifest", manifest });
    const resume = await this.waitControl(
      (message) => message.type === "resume" && message.transferId === manifest.transferId,
      "resume handshake",
    );
    const offsets = validateResumeOffsets(manifest, resume.offsets);
    let sent = manifest.files.reduce((sum, file) => sum + offsets[file.id], 0);

    for (let fileIndex = 0; fileIndex < manifest.files.length; fileIndex++) {
      const descriptor = manifest.files[fileIndex];
      const source = this.files[fileIndex];
      let offset = offsets[descriptor.id];
      const digests = [];
      for (let prior = 0; prior < offset; prior += manifest.chunkSize) {
        const bytes = await readChunk(source, prior, Math.min(manifest.chunkSize, offset - prior));
        digests.push(await sha256Hex(bytes));
      }

      while (offset < descriptor.size) {
        const bytes = await readChunk(source, offset, Math.min(manifest.chunkSize, descriptor.size - offset));
        const digest = await sha256Hex(bytes);
        const header = {
          type: "chunk",
          fileId: descriptor.id,
          seq: Math.floor(offset / manifest.chunkSize),
          offset,
          length: bytes.byteLength,
          sha256: digest,
        };
        let acknowledged = false;
        for (let attempt = 0; attempt < 4; attempt++) {
          await waitForBuffer(this.channel, this.options.bufferedHighWatermark ?? DEFAULT_BUFFER_HIGH_WATERMARK);
          const responsePromise = this.waitControl(
            (message) => (message.type === "ack" || message.type === "nack") &&
              message.fileId === descriptor.id && message.seq === header.seq,
            "chunk acknowledgement",
          );
          sendControl(this.channel, header);
          this.channel.send(bytes.buffer);
          let response;
          try {
            response = await responsePromise;
          } catch (error) {
            if (error.code === "TIMEOUT" && attempt < 3) continue;
            throw error;
          }
          if (response.type === "ack") {
            if (response.offset !== offset + bytes.byteLength) throw new Error("invalid acknowledgement offset");
            acknowledged = true;
            break;
          }
          if (response.offset !== offset) throw new Error("invalid retry offset");
        }
        if (!acknowledged) throw new Error("chunk retry limit exceeded");
        digests.push(digest);
        offset += bytes.byteLength;
        sent += bytes.byteLength;
        this.options.onProgress?.({ version: 2, file: descriptor, sent, total: manifest.totalSize });
      }

      const checksum = await transferChecksum(digests);
      const fileAck = this.waitControl(
        (message) => message.type === "file-ack" && message.fileId === descriptor.id,
        "file acknowledgement",
      );
      sendControl(this.channel, { type: "file-done", fileId: descriptor.id, checksum });
      await fileAck;
    }

    const completeAck = this.waitControl(
      (message) => message.type === "complete-ack" && message.transferId === manifest.transferId,
      "transfer completion",
    );
    sendControl(this.channel, { type: "complete", transferId: manifest.transferId });
    await completeAck;
    return { version: 2, transferId: manifest.transferId, files: manifest.files.length, bytes: manifest.totalSize };
  }

  dispose() {
    this.inbox.dispose();
  }
}

class LegacyReceiver {
  constructor(meta) {
    if (!meta || typeof meta.name !== "string" || !meta.name || /[\\/\0]/.test(meta.name) ||
        !Number.isSafeInteger(meta.size) || meta.size < 0 || meta.size > LEGACY_MAX_BYTES ||
        typeof meta.type !== "string" || new TextEncoder().encode(meta.name).byteLength > 255 || meta.type.length > 128) {
      throw new Error("invalid legacy metadata");
    }
    this.file = { id: "legacy", name: meta.name, size: meta.size, type: meta.type || "application/octet-stream" };
    this.chunks = [];
    this.received = 0;
  }

  chunk(bytes) {
    if (this.received + bytes.byteLength > this.file.size) throw new Error("legacy chunk exceeds declared size");
    this.chunks.push(bytes.slice());
    this.received += bytes.byteLength;
  }

  finish() {
    if (this.received !== this.file.size) throw new Error("legacy transfer is incomplete");
    const blob = new Blob(this.chunks, { type: this.file.type });
    this.chunks = [];
    return blob;
  }
}

class ReceiverSession {
  constructor(channel, options = {}) {
    this.channel = channel;
    this.options = options;
    this.mode = null;
    this.protocol = null;
    this.sink = null;
    this.legacy = null;
    this.pendingHeader = null;
    this.finished = false;
    this.transferStore = options.transferStore || defaultReceiveTransferStore;
    this.transferId = null;
    this.attached = false;
    this.queue = Promise.resolve();
    channel.binaryType = "arraybuffer";
    this.done = new Promise((resolve, reject) => { this.resolveDone = resolve; this.rejectDone = reject; });
    this.onOpen = () => {
      try { sendControl(channel, { type: "hello", versions: [2, 1] }); } catch (error) { this.fail(error); }
    };
    this.onMessage = ({ data }) => {
      this.queue = this.queue.then(() => this.handle(data)).catch((error) => this.fail(error));
    };
    this.onClose = () => {
      const error = new Error("data channel closed before transfer completed");
      this.queue = this.queue.then(
        () => this.disconnect(error),
        () => this.disconnect(error),
      );
    };
    channel.addEventListener("open", this.onOpen);
    channel.addEventListener("message", this.onMessage);
    channel.addEventListener("close", this.onClose);
    if (channel.readyState === "open") queueMicrotask(this.onOpen);
  }

  async handle(data) {
    if (this.finished) throw new Error("message received after transfer completion");
    if (typeof data !== "string") return this.handleBinary(await asBytes(data));
    if (this.mode === "legacy" && data === "done") return this.finishLegacy();
    const message = parseControl(data);
    if (!this.mode && typeof message.name === "string" && Number.isSafeInteger(message.size)) {
      this.mode = "legacy";
      this.legacy = new LegacyReceiver(message);
      return;
    }
    if (message.type === "hello") return;
    if (message.type === "manifest") return this.startV2(message.manifest);
    if (message.type === "chunk") {
      if (this.mode !== "v2" || !this.protocol || this.pendingHeader) throw new Error("chunk header out of order");
      this.pendingHeader = message;
      return;
    }
    if (message.type === "file-done") return this.finishV2File(message);
    if (message.type === "complete") return this.finishV2(message);
    throw new Error("unsupported control message");
  }

  async startV2(value) {
    if (this.mode || this.protocol) throw new Error("manifest out of order");
    const manifest = validateManifest(value);
    const entry = await this.transferStore.acquire(manifest, async (normalizedManifest) => (
      this.options.sinkFactory
        ? this.options.sinkFactory(normalizedManifest)
        : new MemoryReceiveSink()
    ));
    this.sink = entry.sink;
    this.protocol = entry.protocol;
    this.transferId = manifest.transferId;
    this.attached = true;
    this.mode = "v2";
    this.options.onSink?.(this.sink, manifest);
    sendControl(this.channel, { type: "resume", transferId: manifest.transferId, offsets: this.protocol.resumeOffsets() });
  }

  async handleBinary(bytes) {
    if (this.mode === "legacy") {
      this.legacy.chunk(bytes);
      this.options.onProgress?.({ version: 1, received: this.legacy.received, total: this.legacy.file.size, file: this.legacy.file });
      return;
    }
    if (this.mode !== "v2" || !this.protocol || !this.pendingHeader) throw new Error("binary chunk out of order");
    const header = this.pendingHeader;
    this.pendingHeader = null;
    const response = await this.protocol.chunk(header, bytes);
    sendControl(this.channel, response);
    const received = Object.values(this.protocol.resumeOffsets()).reduce((sum, value) => sum + value, 0);
    this.options.onProgress?.({ version: 2, received, total: this.protocol.manifest.totalSize, fileId: header.fileId });
  }

  async finishV2File(message) {
    if (this.mode !== "v2" || !this.protocol || this.pendingHeader) throw new Error("file completion out of order");
    const result = await this.protocol.finish(message.fileId, message.checksum);
    if (!result.replayed) await this.options.onFile?.(result.file, result.value, this.sink);
    sendControl(this.channel, { type: "file-ack", fileId: result.file.id });
  }

  async finishV2(message) {
    if (this.mode !== "v2" || !this.protocol || message.transferId !== this.protocol.manifest.transferId) {
      throw new Error("transfer completion out of order");
    }
    const result = this.protocol.complete();
    sendControl(this.channel, { type: "complete-ack", transferId: result.transferId });
    this.finished = true;
    this.transferStore.complete(result.transferId);
    this.attached = false;
    const value = { version: 2, ...result };
    try { await this.options.onComplete?.(value, this.sink); }
    catch (error) { try { this.options.onError?.(error); } catch {} }
    this.resolveDone(value);
  }

  async finishLegacy() {
    const value = this.legacy.finish();
    await this.options.onFile?.(this.legacy.file, value, null);
    this.finished = true;
    const result = { version: 1, transferId: null, files: 1, bytes: this.legacy.file.size };
    try { await this.options.onComplete?.(result, null); }
    catch (error) { try { this.options.onError?.(error); } catch {} }
    this.resolveDone(result);
  }

  async fail(error) {
    if (this.finished) return;
    this.finished = true;
    try { sendControl(this.channel, { type: "error", message: String(error?.message || error).slice(0, 512) }); } catch {}
    try {
      if (this.attached) await this.transferStore.abort(this.transferId);
      else await this.sink?.abort?.();
    } catch {}
    this.attached = false;
    try { this.options.onError?.(error); } catch {}
    this.rejectDone(error);
  }

  disconnect(error) {
    if (this.finished) return;
    this.finished = true;
    if (this.attached) this.transferStore.detach(this.transferId);
    this.attached = false;
    try { this.options.onError?.(error); } catch {}
    this.rejectDone(error);
  }

  async abort() {
    if (this.finished) {
      if (this.transferId) await this.transferStore.abort(this.transferId);
      return;
    }
    this.finished = true;
    const error = new DOMException("Aborted", "AbortError");
    try {
      if (this.attached) await this.transferStore.abort(this.transferId);
      else await this.sink?.abort?.();
    } catch {}
    this.attached = false;
    this.rejectDone(error);
  }

  dispose() {
    this.channel.removeEventListener("open", this.onOpen);
    this.channel.removeEventListener("message", this.onMessage);
    this.channel.removeEventListener("close", this.onClose);
  }
}

export function createSenderSession(channel, files, options) {
  return new SenderSession(channel, files, options);
}

export function createReceiverSession(channel, options) {
  return new ReceiverSession(channel, options);
}
