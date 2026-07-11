import assert from "node:assert/strict";
import { test } from "node:test";
import { sha256Hex } from "./integrity.mjs";
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

test("v2 sessions transfer multiple files, NACK a corrupt chunk, retry, and complete", async () => {
  let corruptNextBinary = true;
  const [senderChannel, receiverChannel] = channelPair((value) => {
    if (!(value instanceof ArrayBuffer) || !corruptNextBinary) return value;
    corruptNextBinary = false;
    const copy = value.slice(0);
    new Uint8Array(copy)[0] ^= 0xff;
    return copy;
  });
  const outputs = [];
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 1024 }),
    onFile: (file, value) => outputs.push({ file, value }),
  });
  const sender = createSenderSession(senderChannel, [
    namedBlob("a.txt", "abcde", "text/plain"),
    namedBlob("b.txt", "xyz", "text/plain"),
  ], { chunkSize: 4, transferId: "transfer-1", negotiationTimeoutMs: 20, ackTimeoutMs: 100 });

  receiverChannel.open();
  senderChannel.open();
  const sent = await sender.start();
  const received = await receiver.done;

  assert.equal(sent.version, 2);
  assert.equal(sent.files, 2);
  assert.equal(received.version, 2);
  assert.equal(outputs.length, 2);
  assert.equal(await outputs[0].value.text(), "abcde");
  assert.equal(await outputs[1].value.text(), "xyz");
  const headers = senderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => { try { return JSON.parse(value); } catch { return null; } })
    .filter((value) => value?.type === "chunk");
  assert.equal(headers.length, 4, "three chunks plus one retry");
});

test("sender retries after a lost ACK without duplicating the received bytes", async () => {
  let dropAck = true;
  const [senderChannel, receiverChannel] = channelPair(undefined, (value) => {
    if (typeof value !== "string" || !dropAck) return value;
    try {
      if (JSON.parse(value).type === "ack") { dropAck = false; return undefined; }
    } catch {}
    return value;
  });
  const outputs = [];
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 16 }),
    onFile: (file, value) => outputs.push({ file, value }),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a.txt", "data")], {
    chunkSize: 4,
    transferId: "lost-ack",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 2,
  });
  receiverChannel.open();
  senderChannel.open();
  assert.equal((await sender.start()).version, 2);
  await receiver.done;
  assert.equal(await outputs[0].value.text(), "data");
  const headers = senderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => { try { return JSON.parse(value); } catch { return null; } })
    .filter((value) => value?.type === "chunk");
  assert.equal(headers.length, 2);
});

test("sender allows the initial chunk plus three retransmissions", async () => {
  let corruptions = 0;
  const [senderChannel, receiverChannel] = channelPair((value) => {
    if (!(value instanceof ArrayBuffer) || corruptions === 3) return value;
    corruptions++;
    const copy = value.slice(0);
    new Uint8Array(copy)[0] ^= 0xff;
    return copy;
  });
  const outputs = [];
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 16 }),
    onFile: (file, value) => outputs.push({ file, value }),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    chunkSize: 4,
    transferId: "three-retransmissions",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 100,
  });
  receiverChannel.open();
  senderChannel.open();

  assert.equal((await sender.start()).version, 2);
  await receiver.done;
  assert.equal(await outputs[0].value.text(), "data");
  const headers = senderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "chunk");
  assert.equal(headers.length, 4);
});

test("sender stops after three retransmissions when every ACK times out", async () => {
  const [senderChannel, receiverChannel] = channelPair(undefined, (value) => {
    if (typeof value !== "string") return value;
    const message = JSON.parse(value);
    return message.type === "ack" ? undefined : value;
  });
  const receiver = createReceiverSession(receiverChannel, {
    sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 16 }),
  });
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    chunkSize: 4,
    transferId: "ack-timeout-limit",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 2,
  });
  receiverChannel.open();
  senderChannel.open();

  await assert.rejects(sender.start(), /timed out/);
  const headers = senderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "chunk");
  await receiver.abort();
  await assert.rejects(receiver.done, (error) => error?.name === "AbortError");
  assert.equal(headers.length, 4);
});

test("sender rejects an inbox that queues too many unmatched control messages", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    transferId: "bounded-inbox-count",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 5,
  });
  receiverChannel.open();
  senderChannel.open();
  for (let index = 0; index <= MAX_QUEUED_CONTROL_MESSAGES; index++) {
    receiverChannel.send(JSON.stringify({ type: "unmatched", index }));
  }
  receiverChannel.send(JSON.stringify({ type: "hello", versions: [2, 1] }));
  await new Promise((resolve) => setTimeout(resolve, 0));

  await assert.rejects(sender.start(), /control message queue limit/);
  assert.equal(senderChannel.sent.length, 0);
  assert.ok(MAX_QUEUED_CONTROL_CHARS > 0);
});

test("sender rejects an inbox whose queued control text exceeds the char budget", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a.bin", "data")], {
    transferId: "bounded-inbox-chars",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 5,
  });
  receiverChannel.open();
  senderChannel.open();
  const payload = "x".repeat(Math.floor(MAX_QUEUED_CONTROL_CHARS / 2));
  receiverChannel.send(JSON.stringify({ type: "unmatched", payload, index: 0 }));
  receiverChannel.send(JSON.stringify({ type: "unmatched", payload, index: 1 }));
  receiverChannel.send(JSON.stringify({ type: "hello", versions: [2, 1] }));
  await new Promise((resolve) => setTimeout(resolve, 0));

  await assert.rejects(sender.start(), /control message queue limit/);
  assert.equal(senderChannel.sent.length, 0);
});

test("sender honors a verified resume offset", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  let header = null;
  const received = [];
  receiverChannel.addEventListener("open", () => {
    receiverChannel.send(JSON.stringify({ type: "hello", versions: [2, 1] }));
  });
  receiverChannel.addEventListener("message", ({ data }) => {
    if (typeof data === "string") {
      const message = JSON.parse(data);
      if (message.type === "manifest") {
        receiverChannel.send(JSON.stringify({ type: "resume", transferId: message.manifest.transferId, offsets: { f0: 4 } }));
      } else if (message.type === "chunk") {
        header = message;
      } else if (message.type === "file-done") {
        receiverChannel.send(JSON.stringify({ type: "file-ack", fileId: message.fileId }));
      } else if (message.type === "complete") {
        receiverChannel.send(JSON.stringify({ type: "complete-ack", transferId: message.transferId }));
      }
      return;
    }
    received.push(...new Uint8Array(data));
    receiverChannel.send(JSON.stringify({ type: "ack", fileId: header.fileId, seq: header.seq, offset: header.offset + header.length }));
  });

  const sender = createSenderSession(senderChannel, [namedBlob("resume.bin", "abcdefgh")], {
    chunkSize: 4,
    transferId: "resume-1",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 100,
  });
  receiverChannel.open();
  senderChannel.open();
  const result = await sender.start();

  assert.equal(result.version, 2);
  assert.equal(header.offset, 4);
  assert.equal(header.seq, 1);
  assert.equal(new TextDecoder().decode(new Uint8Array(received)), "efgh");
});

test("fresh sessions resume verified bytes from the retained partial sink", async () => {
  const store = new ReceiveTransferStore({ expiryMs: 1000 });
  const sink = new MemoryReceiveSink({ maxBytes: 16 });
  const source = namedBlob("resume.bin", "abcdefgh");
  let firstReceiverChannel;
  let closedAfterFirstAck = false;
  const [firstSenderChannel, receiverChannel] = channelPair(undefined, (value) => {
    if (typeof value === "string" && !closedAfterFirstAck) {
      const message = JSON.parse(value);
      if (message.type === "ack") {
        closedAfterFirstAck = true;
        queueMicrotask(() => firstReceiverChannel.close());
      }
    }
    return value;
  });
  firstReceiverChannel = receiverChannel;
  const firstReceiver = createReceiverSession(firstReceiverChannel, {
    transferStore: store,
    sinkFactory: async () => sink,
  });
  const firstSender = createSenderSession(firstSenderChannel, [source], {
    chunkSize: 4,
    transferId: "real-resume",
    negotiationTimeoutMs: 20,
    ackTimeoutMs: 100,
  });
  firstReceiverChannel.open();
  firstSenderChannel.open();

  await assert.rejects(firstSender.start(), /closed/);
  await assert.rejects(firstReceiver.done, /closed/);

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
    ackTimeoutMs: 100,
  });
  secondReceiverChannel.open();
  secondSenderChannel.open();

  assert.equal((await secondSender.start()).version, 2);
  assert.equal((await secondReceiver.done).bytes, 8);
  assert.equal(await outputs[0].value.text(), "abcdefgh");
  const resumedHeaders = secondSenderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "chunk");
  assert.deepEqual(resumedHeaders.map((header) => header.offset), [4]);
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
    ackTimeoutMs: 100,
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
    ackTimeoutMs: 100,
  });
  secondReceiverChannel.open();
  secondSenderChannel.open();

  await secondSender.start();
  await secondReceiver.done;
  assert.deepEqual(outputs, [["a.bin", "abcd"], ["b.bin", "efgh"]]);
  const resumedHeaders = secondSenderChannel.sent.filter((value) => typeof value === "string")
    .map((value) => JSON.parse(value))
    .filter((value) => value.type === "chunk");
  assert.deepEqual(resumedHeaders.map((header) => header.fileId), ["f1"]);
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
    version: 2,
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
    version: 2,
    transferId: "abort-disconnected",
    chunkSize: 4,
    files: [{ id: "f0", name: "a.bin", size: 8, type: "application/octet-stream" }],
  };
  const bytes = new TextEncoder().encode("abcd");
  receiverChannel.open();
  senderChannel.open();
  senderChannel.send(JSON.stringify({ type: "manifest", manifest }));
  senderChannel.send(JSON.stringify({
    type: "chunk",
    fileId: "f0",
    seq: 0,
    offset: 0,
    length: bytes.byteLength,
    sha256: await sha256Hex(bytes),
  }));
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

test("sender falls back to v1 for an old single-file receiver", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const received = [];
  receiverChannel.addEventListener("message", ({ data }) => received.push(data));
  const sender = createSenderSession(senderChannel, [namedBlob("legacy.txt", "old", "text/plain")], {
    negotiationTimeoutMs: 1,
  });
  receiverChannel.open();
  senderChannel.open();

  const result = await sender.start();
  assert.equal(result.version, 1);
  assert.deepEqual(JSON.parse(received[0]), { name: "legacy.txt", size: 3, type: "text/plain" });
  assert.equal(new TextDecoder().decode(received[1]), "old");
  assert.equal(received[2], "done");
});

test("legacy sender does not report completion while bytes remain buffered", async () => {
  let senderChannel;
  let receiverChannel;
  [senderChannel, receiverChannel] = channelPair((value) => {
    if (value === "done") senderChannel.bufferedAmount = 10;
    return value;
  });
  const sender = createSenderSession(senderChannel, [namedBlob("legacy.txt", "old")], {
    negotiationTimeoutMs: 1,
    drainPollMs: 1,
  });
  receiverChannel.open();
  senderChannel.open();
  let settled = false;
  const result = sender.start().then((value) => { settled = true; return value; });
  await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(settled, false);
  senderChannel.bufferedAmount = 0;
  assert.equal((await result).version, 1);
});

test("sender refuses to collapse multiple files into a legacy transfer", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const sender = createSenderSession(senderChannel, [namedBlob("a", "a"), namedBlob("b", "b")], {
    negotiationTimeoutMs: 1,
  });
  receiverChannel.open();
  senderChannel.open();
  await assert.rejects(sender.start(), /only supports one file/);
  assert.equal(senderChannel.sent.length, 0);
});

test("new receiver accepts a legacy v1 sender", async () => {
  const [senderChannel, receiverChannel] = channelPair();
  const outputs = [];
  const receiver = createReceiverSession(receiverChannel, {
    onFile: (file, value) => outputs.push({ file, value }),
  });
  receiverChannel.open();
  senderChannel.open();
  senderChannel.send(JSON.stringify({ name: "legacy.txt", size: 3, type: "text/plain" }));
  senderChannel.send(new TextEncoder().encode("old").buffer);
  senderChannel.send("done");

  const result = await receiver.done;
  assert.equal(result.version, 1);
  assert.equal(outputs[0].file.name, "legacy.txt");
  assert.equal(await outputs[0].value.text(), "old");
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
    ackTimeoutMs: 100,
  });
  receiverChannel.open();
  senderChannel.open();
  await sender.start();
  const result = await Promise.race([
    receiver.done,
    new Promise((_, reject) => setTimeout(() => reject(new Error("done did not settle")), 20)),
  ]);
  assert.equal(result.version, 2);
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
  senderChannel.send(JSON.stringify({ type: "hello", version: 2 }));
  senderChannel.send(JSON.stringify({
    type: "manifest",
    manifest: { version: 2, transferId: "partial", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] },
  }));
  await new Promise((resolve) => setTimeout(resolve, 0));
  await receiver.abort();
  await assert.rejects(receiver.done, (error) => error?.name === "AbortError");
  assert.equal(aborted, true);
});
