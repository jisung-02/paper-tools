import assert from "node:assert/strict";
import { test } from "node:test";
import { MemoryReceiveSink } from "./storage.mjs";
import {
  createReceiverSession,
  createSenderSession,
  MAX_QUEUED_CONTROL_CHARS,
  MAX_QUEUED_CONTROL_MESSAGES,
  ReceiveTransferStore,
} from "./transfer.mjs";

class FakeChannel {
  constructor(transform = (value) => value) {
    this.binaryType = "arraybuffer";
    this.bufferedAmount = 0;
    this.bufferedAmountLowThreshold = 0;
    this.readyState = "connecting";
    this.listeners = new Map();
    this.sent = [];
    this.transform = transform;
  }

  addEventListener(type, listener, options = {}) {
    const entries = this.listeners.get(type) || [];
    entries.push({ listener, once: Boolean(options.once) });
    this.listeners.set(type, entries);
  }

  removeEventListener(type, listener) {
    this.listeners.set(type, (this.listeners.get(type) || []).filter((entry) => entry.listener !== listener));
  }

  emit(type, event = {}) {
    const property = this[`on${type}`];
    if (typeof property === "function") property(event);
    const entries = [...(this.listeners.get(type) || [])];
    for (const entry of entries) {
      entry.listener(event);
      if (entry.once) this.removeEventListener(type, entry.listener);
    }
  }

  send(value) {
    this.sent.push(value);
    const delivered = this.transform(value);
    queueMicrotask(() => this.peer.emit("message", { data: delivered }));
  }

  open() {
    this.readyState = "open";
    this.emit("open");
  }

  close() {
    this.readyState = "closed";
    this.emit("close");
    if (this.peer.readyState !== "closed") {
      this.peer.readyState = "closed";
      this.peer.emit("close");
    }
  }
}

function channelPair(leftTransform, rightTransform) {
  const left = new FakeChannel(leftTransform);
  const right = new FakeChannel(rightTransform);
  left.peer = right;
  right.peer = left;
  return [left, right];
}

function namedBlob(name, value, type = "application/octet-stream") {
  const blob = new Blob([value], { type });
  Object.defineProperty(blob, "name", { value: name });
  return blob;
}

function controlTypes(channel) {
  return channel.sent
    .filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value).type);
}

test("v3 sessions stream multiple files back-to-back with no per-chunk headers or acks", async () => {
  const outputs = [];
  const [senderChannel, receiverChannel] = channelPair();
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 1024 }),
    onFile: async (file, value) => outputs.push([file.name, await value.text()]),
  });
  const sender = createSenderSession(senderChannel, [
    namedBlob("a.txt", "abcde", "text/plain"),
    namedBlob("b.txt", "xyz", "text/plain"),
  ], { chunkSize: 4, transferId: "transfer-1", negotiationTimeoutMs: 20 });

  receiverChannel.open();
  senderChannel.open();
  const sent = await sender.start();
  const received = await receiver.done;

  assert.deepEqual(sent, { transferId: "transfer-1", files: 2, bytes: 8 });
  assert.deepEqual(received, sent);
  assert.deepEqual(outputs, [["a.txt", "abcde"], ["b.txt", "xyz"]]);
  assert.deepEqual(controlTypes(senderChannel), ["manifest", "file-start", "file-done", "file-start", "file-done", "complete"]);
  assert.deepEqual(controlTypes(receiverChannel), ["hello", "resume", "file-ack", "file-ack", "complete-ack"]);
  // "abcde" at chunkSize 4 is two chunks (4 + 1), "xyz" is one chunk.
  const binaryCount = senderChannel.sent.filter((value) => value instanceof ArrayBuffer).length;
  assert.equal(binaryCount, 3);
});

test("a zero-byte file sends file-start immediately followed by file-done with no binary chunks", async () => {
  const outputs = [];
  const [senderChannel, receiverChannel] = channelPair();
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 16 }),
    onFile: async (file, value) => outputs.push([file.name, value.size]),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("empty.bin", "")], {
    chunkSize: 4,
    transferId: "zero-byte",
    negotiationTimeoutMs: 20,
  });
  receiverChannel.open();
  senderChannel.open();

  const sent = await sender.start();
  await receiver.done;

  assert.deepEqual(sent, { transferId: "zero-byte", files: 1, bytes: 0 });
  assert.deepEqual(outputs, [["empty.bin", 0]]);
  assert.equal(senderChannel.sent.filter((value) => value instanceof ArrayBuffer).length, 0);
  assert.deepEqual(controlTypes(senderChannel), ["manifest", "file-start", "file-done", "complete"]);
});

test("a chunk corrupted in transit fails the transfer via the composite checksum, not a length check", async () => {
  let corruptNextBinary = true;
  const [senderChannel, receiverChannel] = channelPair((value) => {
    if (!(value instanceof ArrayBuffer) || !corruptNextBinary) return value;
    corruptNextBinary = false;
    const copy = value.slice(0);
    new Uint8Array(copy)[0] ^= 0xff;
    return copy;
  });
  const errors = [];
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 1024 }),
    onError: (error) => errors.push(error.message),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a.txt", "abcde", "text/plain")], {
    chunkSize: 4,
    transferId: "corrupt-1",
    negotiationTimeoutMs: 20,
  });
  receiverChannel.open();
  senderChannel.open();

  await assert.rejects(sender.start(), /checksum/);
  await assert.rejects(receiver.done, /checksum/);
  assert.deepEqual(errors, ["file checksum mismatch"]);
});

test("sender fails clearly when no hello arrives before the negotiation timeout", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a.txt", "a")], { negotiationTimeoutMs: 5 });
  receiverChannel.open();
  senderChannel.open();
  await assert.rejects(sender.start(), /outdated page/);
  assert.deepEqual(controlTypes(senderChannel), []);
});

test("sender fails clearly when the peer only speaks legacy protocol versions", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  receiverChannel.addEventListener("open", () => {
    receiverChannel.send(JSON.stringify({ type: "hello", versions: [1, 2] }));
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a.txt", "a")], { negotiationTimeoutMs: 200 });
  receiverChannel.open();
  senderChannel.open();
  await assert.rejects(sender.start(), /outdated page/);
  assert.deepEqual(controlTypes(senderChannel), []);
});

test("sender rejects an inbox that queues too many unmatched control messages", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    transferId: "bounded-inbox-count",
    negotiationTimeoutMs: 200,
  });
  receiverChannel.open();
  senderChannel.open();
  for (let index = 0; index <= MAX_QUEUED_CONTROL_MESSAGES; index++) {
    receiverChannel.send(JSON.stringify({ type: "unmatched", index }));
  }
  receiverChannel.send(JSON.stringify({ type: "hello", versions: [3] }));
  await new Promise((resolve) => setTimeout(resolve, 0));

  await assert.rejects(sender.start(), /control message queue limit/);
  assert.equal(senderChannel.sent.length, 0);
  assert.ok(MAX_QUEUED_CONTROL_CHARS > 0);
});

test("sender rejects an inbox whose queued control text exceeds the char budget", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    transferId: "bounded-inbox-chars",
    negotiationTimeoutMs: 200,
  });
  receiverChannel.open();
  senderChannel.open();
  const payload = "x".repeat(Math.floor(MAX_QUEUED_CONTROL_CHARS / 2));
  receiverChannel.send(JSON.stringify({ type: "unmatched", payload, index: 0 }));
  receiverChannel.send(JSON.stringify({ type: "unmatched", payload, index: 1 }));
  receiverChannel.send(JSON.stringify({ type: "hello", versions: [3] }));
  await new Promise((resolve) => setTimeout(resolve, 0));

  await assert.rejects(sender.start(), /control message queue limit/);
  assert.equal(senderChannel.sent.length, 0);
});

test("sender honors a verified resume offset from a hand-rolled v3 receiver", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  let fileStart = null;
  const received = [];
  receiverChannel.addEventListener("open", () => {
    receiverChannel.send(JSON.stringify({ type: "hello", versions: [3] }));
  });
  receiverChannel.addEventListener("message", ({ data }) => {
    if (typeof data === "string") {
      const message = JSON.parse(data);
      if (message.type === "manifest") {
        receiverChannel.send(JSON.stringify({ type: "resume", offsets: { f0: 4 } }));
      } else if (message.type === "file-start") {
        fileStart = message;
      } else if (message.type === "file-done") {
        receiverChannel.send(JSON.stringify({ type: "file-ack", fileId: message.fileId }));
      } else if (message.type === "complete") {
        receiverChannel.send(JSON.stringify({ type: "complete-ack" }));
      }
      return;
    }
    received.push(...new Uint8Array(data));
  });

  const sender = createSenderSession(senderChannel, [namedBlob("resume.bin", "abcdefgh")], {
    chunkSize: 4,
    transferId: "resume-1",
    negotiationTimeoutMs: 20,
  });
  receiverChannel.open();
  senderChannel.open();
  const result = await sender.start();

  assert.equal(result.transferId, "resume-1");
  assert.deepEqual(fileStart, { type: "file-start", fileId: "f0", offset: 4 });
  assert.equal(new TextDecoder().decode(new Uint8Array(received)), "efgh");
});

test("fresh sessions resume verified bytes from the retained partial sink", async () => {
  const store = new ReceiveTransferStore({ expiryMs: 1000 });
  const sink = new MemoryReceiveSink({ maxBytes: 16 });
  const source = namedBlob("resume.bin", "abcdefgh");
  const [firstSenderChannel, receiverChannel] = channelPair();

  // v3 has no per-chunk ack to hang a "disconnect after N bytes" test off of,
  // and the sender otherwise races ahead (no round trip between chunks), so
  // this simulates backpressure to deterministically block it after the
  // first chunk — a real "bufferedamountlow" just never fires here, which is
  // exactly what a stalled/closed connection looks like from the sender's
  // side.
  const originalSend = firstSenderChannel.send.bind(firstSenderChannel);
  let binarySent = 0;
  firstSenderChannel.send = (value) => {
    originalSend(value);
    if (value instanceof ArrayBuffer && ++binarySent === 1) firstSenderChannel.bufferedAmount = 1e9;
  };

  let firstChunkWritten;
  const firstChunkWrittenPromise = new Promise((resolve) => { firstChunkWritten = resolve; });
  const firstReceiver = createReceiverSession(receiverChannel, {
    transferStore: store,
    sinkFactory: async () => sink,
    onProgress: () => firstChunkWritten(),
  });
  const firstSender = createSenderSession(firstSenderChannel, [source], {
    chunkSize: 4,
    transferId: "real-resume",
    negotiationTimeoutMs: 20,
  });
  receiverChannel.open();
  firstSenderChannel.open();

  const firstStart = firstSender.start();
  const firstDone = firstReceiver.done;
  await firstChunkWrittenPromise;
  receiverChannel.close();

  await assert.rejects(firstStart, /closed/);
  await assert.rejects(firstDone, /closed/);

  const outputs = [];
  const [secondSenderChannel, secondReceiverChannel] = channelPair();
  const secondReceiver = createReceiverSession(secondReceiverChannel, {
    transferStore: store,
    sinkFactory: async () => { throw new Error("resume must reuse the existing sink"); },
    onFile: (file, value) => outputs.push({ file, value }),
  });
  const secondSender = createSenderSession(secondSenderChannel, [source], {
    chunkSize: 4,
    transferId: "real-resume",
    negotiationTimeoutMs: 20,
  });
  secondReceiverChannel.open();
  secondSenderChannel.open();

  assert.deepEqual(await secondSender.start(), { transferId: "real-resume", files: 1, bytes: 8 });
  assert.equal((await secondReceiver.done).bytes, 8);
  assert.equal(await outputs[0].value.text(), "abcdefgh");
  const fileStarts = secondSenderChannel.sent
    .filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "file-start");
  assert.deepEqual(fileStarts, [{ type: "file-start", fileId: "f0", offset: 4 }]);
  assert.equal(secondSenderChannel.sent.filter((value) => value instanceof ArrayBuffer).length, 1);
});

test("fresh sessions replay a completed file ACK and continue later files", async () => {
  const store = new ReceiveTransferStore({ expiryMs: 1000 });
  const sink = new MemoryReceiveSink({ maxBytes: 32 });
  const sources = [namedBlob("a.bin", "abcd"), namedBlob("b.bin", "efgh")];
  const outputs = [];
  let firstReceiverChannel;
  let closedAfterFirstFile = false;
  const [firstSenderChannel, receiverChannel] = channelPair(undefined, (value) => {
    if (typeof value === "string" && !closedAfterFirstFile) {
      const message = JSON.parse(value);
      if (message.type === "file-ack" && message.fileId === "f0") {
        closedAfterFirstFile = true;
        queueMicrotask(() => firstReceiverChannel.close());
      }
    }
    return value;
  });
  firstReceiverChannel = receiverChannel;
  const firstReceiver = createReceiverSession(firstReceiverChannel, {
    transferStore: store,
    sinkFactory: async () => sink,
    onFile: async (file, value) => outputs.push([file.name, await value.text()]),
  });
  const firstSender = createSenderSession(firstSenderChannel, sources, {
    chunkSize: 4,
    transferId: "resume-after-file",
    negotiationTimeoutMs: 20,
  });
  firstReceiverChannel.open();
  firstSenderChannel.open();
  await assert.rejects(firstSender.start(), /closed/);
  await assert.rejects(firstReceiver.done, /closed/);

  const [secondSenderChannel, secondReceiverChannel] = channelPair();
  const secondReceiver = createReceiverSession(secondReceiverChannel, {
    transferStore: store,
    sinkFactory: async () => { throw new Error("resume must reuse the existing sink"); },
    onFile: async (file, value) => outputs.push([file.name, await value.text()]),
  });
  const secondSender = createSenderSession(secondSenderChannel, sources, {
    chunkSize: 4,
    transferId: "resume-after-file",
    negotiationTimeoutMs: 20,
  });
  secondReceiverChannel.open();
  secondSenderChannel.open();

  await secondSender.start();
  await secondReceiver.done;
  assert.deepEqual(outputs, [["a.bin", "abcd"], ["b.bin", "efgh"]]);
  // f0 was already fully received and ack'd, so its file-start is resent with
  // offset === size (no chunks follow); only f1 gets streamed bytes.
  const fileStarts = secondSenderChannel.sent
    .filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "file-start");
  assert.deepEqual(fileStarts, [
    { type: "file-start", fileId: "f0", offset: 4 },
    { type: "file-start", fileId: "f1", offset: 0 },
  ]);
  assert.equal(secondSenderChannel.sent.filter((value) => value instanceof ArrayBuffer).length, 1);
});

test("retained transfer expiry aborts its partial sink and frees the transfer id", async () => {
  let expire;
  let aborted = 0;
  const store = new ReceiveTransferStore({
    expiryMs: 25,
    scheduleExpiry(callback, delay) {
      assert.equal(delay, 25);
      expire = callback;
      return 1;
    },
    cancelExpiry() {},
  });
  const manifest = {
    version: 3,
    transferId: "expires",
    chunkSize: 4,
    files: [{ id: "f0", name: "a.bin", size: 4, type: "application/octet-stream" }],
  };
  const partialSink = {
    async prepare() {},
    async write() {},
    async finish() {},
    async abort() { aborted++; },
  };
  await store.acquire(manifest, async () => partialSink);
  store.detach("expires");

  await expire();
  assert.equal(aborted, 1);

  let replacementCreated = false;
  const replacementSink = { ...partialSink, async abort() {} };
  await store.acquire(manifest, async () => {
    replacementCreated = true;
    return replacementSink;
  });
  assert.equal(replacementCreated, true);
  await store.abort("expires");
});

test("explicit abort after disconnect removes retained bytes immediately", async () => {
  const store = new ReceiveTransferStore({ expiryMs: 1000 });
  const [senderChannel, receiverChannel] = channelPair();
  const receiver = createReceiverSession(receiverChannel, {
    transferStore: store,
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 16 }),
  });
  const manifest = {
    version: 3,
    transferId: "abort-disconnected",
    chunkSize: 4,
    files: [{ id: "f0", name: "a.bin", size: 8, type: "application/octet-stream" }],
  };
  const bytes = new TextEncoder().encode("abcd");
  receiverChannel.open();
  senderChannel.open();
  senderChannel.send(JSON.stringify({ type: "manifest", transferId: manifest.transferId, chunkSize: manifest.chunkSize, files: manifest.files }));
  senderChannel.send(JSON.stringify({ type: "file-start", fileId: "f0", offset: 0 }));
  senderChannel.send(bytes.buffer);
  await new Promise((resolve) => setTimeout(resolve, 0));
  senderChannel.close();
  await assert.rejects(receiver.done, /closed/);

  await receiver.abort();
  let replacementCreated = false;
  const replacement = await store.acquire(manifest, async () => {
    replacementCreated = true;
    return new MemoryReceiveSink({ maxBytes: 16 });
  });
  assert.equal(replacementCreated, true);
  assert.deepEqual(replacement.protocol.resumeOffsets(), { f0: 0 });
  await store.abort("abort-disconnected");
});

test("completed transfer settles even when presentation callback fails", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const errors = [];
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 8 }),
    onComplete: async () => { throw new Error("render failed"); },
    onError: (error) => errors.push(error.message),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a", "a")], {
    transferId: "presentation-error",
    negotiationTimeoutMs: 20,
  });
  receiverChannel.open();
  senderChannel.open();
  await sender.start();
  const result = await Promise.race([
    receiver.done,
    new Promise((_, reject) => setTimeout(() => reject(new Error("done did not settle")), 20)),
  ]);
  assert.deepEqual(result, { transferId: "presentation-error", files: 1, bytes: 1 });
  assert.deepEqual(errors, ["render failed"]);
});

test("receiver abort cleans a partial disk sink", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  let aborted = false;
  const sink = {
    async prepare() {},
    async write() {},
    async finish() {},
    async abort() { aborted = true; },
  };
  const receiver = createReceiverSession(receiverChannel, { sinkFactory: async () => sink });
  receiverChannel.open();
  senderChannel.open();
  senderChannel.send(JSON.stringify({ type: "hello", versions: [3] }));
  senderChannel.send(JSON.stringify({
    type: "manifest",
    transferId: "partial",
    chunkSize: 4,
    files: [{ id: "a", name: "a", size: 4, type: "x" }],
  }));
  await new Promise((resolve) => setTimeout(resolve, 0));
  await receiver.abort();
  await assert.rejects(receiver.done, (error) => error?.name === "AbortError");
  assert.equal(aborted, true);
});
