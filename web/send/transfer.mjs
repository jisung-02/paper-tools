import { sha256Hex, transferChecksum } from "./integrity.mjs";
import { MAX_CHUNK_BYTES, MAX_FILES, MAX_TOTAL_BYTES, ReceiverProtocol, validateManifest } from "./protocol.mjs";
import { MemoryReceiveSink } from "./storage.mjs";

const MAX_CONTROL_CHARS = 1024 * 1024;
export const MAX_QUEUED_CONTROL_MESSAGES = 256;
export const MAX_QUEUED_CONTROL_CHARS = 256 * 1024;
const DEFAULT_CHUNK_SIZE = 64 * 1024;
const DEFAULT_NEGOTIATION_TIMEOUT_MS = 500;
const DEFAULT_BUFFER_HIGH_WATERMARK = 8 * 1024 * 1024;
const OUTDATED_PEER_MESSAGE = "peer is running an outdated page — reload on both devices";

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
    version: 3,
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

// waitForBuffer(channel, highWatermark) -> resolves once bufferedAmount is at
// or below highWatermark, using the "bufferedamountlow" event rather than
// polling. Used both for per-chunk backpressure (highWatermark = the send
// buffer ceiling) and, with highWatermark 0 and the threshold pinned to 0,
// for a full drain (see waitForFullDrain below).
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

// waitForFullDrain(channel) -> resolves once bufferedAmount reaches exactly
// zero. "bufferedamountlow" only fires on a transition down through
// bufferedAmountLowThreshold, so the threshold is pinned to 0 for the
// duration of the wait — otherwise, if bufferedAmount is already below the
// configured threshold, no further transition (and so no event) would ever
// occur even though bytes are still in flight.
async function waitForFullDrain(channel) {
  if (channel.readyState !== "open") throw new Error("data channel is not open");
  if (channel.bufferedAmount === 0) return;
  const previousThreshold = channel.bufferedAmountLowThreshold;
  channel.bufferedAmountLowThreshold = 0;
  try {
    await waitForBuffer(channel, 0);
  } finally {
    channel.bufferedAmountLowThreshold = previousThreshold;
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

  // wait(predicate, timeoutMs, label) -> the next queued or future message
  // matching predicate. timeoutMs is optional: omit it to wait indefinitely
  // (still rejected promptly by a "close" event via fail()) — used for every
  // wait past initial negotiation, since the data channel's own reliable,
  // ordered delivery is what bounds how long a well-behaved peer takes to
  // reply, not an arbitrary clock.
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
      if (Number.isFinite(timeoutMs)) {
        waiter.timer = setTimeout(() => {
          const index2 = this.waiters.indexOf(waiter);
          if (index2 !== -1) this.waiters.splice(index2, 1);
          reject(timeoutError(label));
        }, timeoutMs);
      }
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
    let hello;
    try {
      hello = await this.inbox.wait(
        (message) => message.type === "hello",
        negotiationTimeoutMs,
        "protocol negotiation",
      );
    } catch (error) {
      if (error.code === "TIMEOUT") throw new Error(OUTDATED_PEER_MESSAGE);
      throw error;
    }
    if (!Array.isArray(hello.versions) || !hello.versions.includes(3)) throw new Error(OUTDATED_PEER_MESSAGE);
    return this.sendV3();
  }

  async waitControl(predicate, label) {
    const message = await this.inbox.wait(
      (value) => value.type === "error" || predicate(value),
      undefined,
      label,
    );
    if (message.type === "error") throw new Error(message.message || "receiver rejected the transfer");
    return message;
  }

  async sendV3() {
    const manifest = buildTransferManifest(this.files, this.options);
    sendControl(this.channel, {
      type: "manifest",
      transferId: manifest.transferId,
      chunkSize: manifest.chunkSize,
      files: manifest.files.map((file) => ({ id: file.id, name: file.name, size: file.size, type: file.type })),
    });
    const resume = await this.waitControl((message) => message.type === "resume", "resume handshake");
    const offsets = validateResumeOffsets(manifest, resume.offsets);
    let sent = manifest.files.reduce((sum, file) => sum + offsets[file.id], 0);

    for (let fileIndex = 0; fileIndex < manifest.files.length; fileIndex++) {
      const descriptor = manifest.files[fileIndex];
      const source = this.files[fileIndex];
      const offset = offsets[descriptor.id];
      const digests = [];
      for (let prior = 0; prior < offset; prior += manifest.chunkSize) {
        const bytes = await readChunk(source, prior, Math.min(manifest.chunkSize, offset - prior));
        digests.push(await sha256Hex(bytes));
      }

      sendControl(this.channel, { type: "file-start", fileId: descriptor.id, offset });

      let position = offset;
      while (position < descriptor.size) {
        const length = Math.min(manifest.chunkSize, descriptor.size - position);
        const bytes = await readChunk(source, position, length);
        await waitForBuffer(this.channel, this.options.bufferedHighWatermark ?? DEFAULT_BUFFER_HIGH_WATERMARK);
        this.channel.send(bytes.buffer);
        digests.push(await sha256Hex(bytes));
        position += bytes.byteLength;
        sent += bytes.byteLength;
        this.options.onProgress?.({ file: descriptor, sent, total: manifest.totalSize });
      }

      const checksum = await transferChecksum(digests);
      const fileAck = this.waitControl(
        (message) => message.type === "file-ack" && message.fileId === descriptor.id,
        "file acknowledgement",
      );
      sendControl(this.channel, { type: "file-done", fileId: descriptor.id, checksum });
      await fileAck;
    }

    const completeAck = this.waitControl((message) => message.type === "complete-ack", "transfer completion");
    sendControl(this.channel, { type: "complete", transferId: manifest.transferId });
    await completeAck;
    await waitForFullDrain(this.channel);
    return { transferId: manifest.transferId, files: manifest.files.length, bytes: manifest.totalSize };
  }

  dispose() {
    this.inbox.dispose();
  }
}

class ReceiverSession {
  constructor(channel, options = {}) {
    this.channel = channel;
    this.options = options;
    this.protocol = null;
    this.sink = null;
    this.currentFileId = null;
    this.finished = false;
    this.transferStore = options.transferStore || defaultReceiveTransferStore;
    this.transferId = null;
    this.attached = false;
    this.queue = Promise.resolve();
    channel.binaryType = "arraybuffer";
    this.done = new Promise((resolve, reject) => { this.resolveDone = resolve; this.rejectDone = reject; });
    this.onOpen = () => {
      try { sendControl(channel, { type: "hello", versions: [3] }); } catch (error) { this.fail(error); }
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
    const message = parseControl(data);
    if (message.type === "hello") return;
    if (message.type === "manifest") return this.startTransfer(message);
    if (message.type === "file-start") return this.startFile(message);
    if (message.type === "file-done") return this.finishFile(message);
    if (message.type === "complete") return this.finishTransfer(message);
    if (message.type === "error") throw new Error(message.message || "sender reported an error");
    throw new Error("unsupported control message");
  }

  async startTransfer(message) {
    if (this.protocol) throw new Error("manifest out of order");
    const manifest = validateManifest({
      version: 3,
      transferId: message.transferId,
      chunkSize: message.chunkSize,
      files: message.files,
    });
    const entry = await this.transferStore.acquire(manifest, async (normalizedManifest) => (
      this.options.sinkFactory
        ? this.options.sinkFactory(normalizedManifest)
        : new MemoryReceiveSink()
    ));
    this.sink = entry.sink;
    this.protocol = entry.protocol;
    this.transferId = manifest.transferId;
    this.attached = true;
    this.options.onSink?.(this.sink, manifest);
    sendControl(this.channel, { type: "resume", offsets: this.protocol.resumeOffsets() });
  }

  async startFile(message) {
    if (!this.protocol || this.currentFileId) throw new Error("file-start out of order");
    const state = this.protocol.fileState(message.fileId);
    if (!Number.isSafeInteger(message.offset) || message.offset < 0 || message.offset > state.file.size) {
      throw new Error("invalid file-start offset");
    }
    if (message.offset !== state.offset) throw new Error("unexpected file-start offset");
    this.currentFileId = message.fileId;
  }

  async handleBinary(bytes) {
    if (!this.protocol || !this.currentFileId) throw new Error("binary chunk out of order");
    // One binary message is one chunk — SCTP preserves message boundaries, so
    // there is no per-chunk header to parse or reassemble.
    await this.protocol.chunk(this.currentFileId, bytes);
    const received = Object.values(this.protocol.resumeOffsets()).reduce((sum, value) => sum + value, 0);
    this.options.onProgress?.({ received, total: this.protocol.manifest.totalSize, fileId: this.currentFileId });
  }

  async finishFile(message) {
    if (!this.protocol || message.fileId !== this.currentFileId) throw new Error("file-done out of order");
    if (typeof message.checksum !== "string" || !/^PT-SHA256-v1:[0-9a-f]{64}$/i.test(message.checksum)) {
      throw new Error("invalid checksum");
    }
    const result = await this.protocol.finish(message.fileId, message.checksum);
    this.currentFileId = null;
    if (!result.replayed) await this.options.onFile?.(result.file, result.value, this.sink);
    sendControl(this.channel, { type: "file-ack", fileId: result.file.id });
  }

  async finishTransfer(message) {
    if (!this.protocol || message.transferId !== this.protocol.manifest.transferId) {
      throw new Error("transfer completion out of order");
    }
    const result = this.protocol.complete();
    sendControl(this.channel, { type: "complete-ack" });
    this.finished = true;
    this.transferStore.complete(result.transferId);
    this.attached = false;
    try { await this.options.onComplete?.(result, this.sink); }
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
