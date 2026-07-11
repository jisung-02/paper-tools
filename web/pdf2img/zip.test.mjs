import assert from "node:assert/strict";
import { test } from "node:test";
import { zipStore, zipStoreStream } from "./zip.mjs";

test("zipStore writes stored files with valid directory records", () => {
  const out = zipStore([
    { name: "page-1.txt", data: new TextEncoder().encode("hello") },
    { name: "page-2.txt", data: new Uint8Array([0, 1, 2, 3]) },
  ]);

  assert.equal(new TextDecoder().decode(out.subarray(0, 2)), "PK");
  assert.equal(readU32(out, 0), 0x04034b50);

  const eocd = findSignature(out, 0x06054b50);
  assert.notEqual(eocd, -1);
  assert.equal(readU16(out, eocd + 10), 2);
  assert.equal(readU16(out, eocd + 8), 2);

  const centralDirOffset = readU32(out, eocd + 16);
  assert.equal(readU32(out, centralDirOffset), 0x02014b50);
  assert.equal(readU32(out, centralDirOffset + 20), 5);
  assert.equal(readU32(out, centralDirOffset + 24), 5);

  const firstNameLen = readU16(out, centralDirOffset + 28);
  const firstName = new TextDecoder().decode(
    out.subarray(centralDirOffset + 46, centralDirOffset + 46 + firstNameLen),
  );
  assert.equal(firstName, "page-1.txt");
});

test("zipStore throws past the ZIP32 65535-entry limit", () => {
  const files = Array.from({ length: 65536 }, (_, i) => ({
    name: `f${i}`,
    data: new Uint8Array(0),
  }));
  assert.throws(() => zipStore(files));
});

test("zipStoreStream emits a valid archive without collecting payload parts", async () => {
  const parts = [];
  await zipStoreStream([{ name: "a.txt", data: new TextEncoder().encode("hello") }], async (part) => parts.push(part));
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let offset = 0;
  for (const part of parts) { out.set(part, offset); offset += part.length; }
  assert.equal(new TextDecoder().decode(out.slice(0, 4)), "PK\x03\x04");
  assert.equal(new TextDecoder().decode(out.slice(-22, -18)), "PK\x05\x06");
});

test("zipStoreStream streams async entries and payload chunks through data descriptors", async () => {
  async function* entries() {
    yield {
      name: "first.txt",
      data: (async function* () {
        yield new TextEncoder().encode("hel");
        yield new TextEncoder().encode("lo");
      })(),
    };
    yield { name: "second.bin", data: new Uint8Array([0, 1, 2, 3]) };
  }

  const writes = [];
  await zipStoreStream(entries(), async (part) => writes.push(part.slice()));
  const out = concat(writes);
  const firstNameLength = readU16(out, 26);
  const firstPayloadOffset = 30 + firstNameLength;
  const firstDescriptorOffset = firstPayloadOffset + 5;
  const secondLocalOffset = firstDescriptorOffset + 16;
  const secondNameLength = readU16(out, secondLocalOffset + 26);
  const secondDescriptorOffset = secondLocalOffset + 30 + secondNameLength + 4;
  const eocd = findSignature(out, 0x06054b50);
  const centralOffset = readU32(out, eocd + 16);
  const secondCentralOffset = centralOffset + 46 + readU16(out, centralOffset + 28);

  assert.equal(readU16(out, 6), 0x0808);
  assert.equal(readU32(out, 14), 0);
  assert.equal(readU32(out, 18), 0);
  assert.equal(readU32(out, 22), 0);
  assert.equal(readU32(out, firstDescriptorOffset), 0x08074b50);
  assert.equal(readU32(out, firstDescriptorOffset + 4), 0x3610a686);
  assert.equal(readU32(out, firstDescriptorOffset + 8), 5);
  assert.equal(readU32(out, firstDescriptorOffset + 12), 5);
  assert.equal(readU32(out, secondDescriptorOffset), 0x08074b50);
  assert.equal(readU16(out, eocd + 8), 2);
  assert.equal(readU16(out, eocd + 10), 2);
  assert.equal(readU32(out, centralOffset + 42), 0);
  assert.equal(readU32(out, secondCentralOffset + 42), secondLocalOffset);
  assert.equal(readU32(out, centralOffset + 16), 0x3610a686);
  assert.equal(readU32(out, centralOffset + 20), 5);
  assert.equal(readU32(out, centralOffset + 24), 5);
});

test("zipStoreStream awaits sink backpressure before pulling payload bytes", async () => {
  let releaseHeader;
  let payloadPulls = 0;
  const writes = [];
  const headerGate = new Promise((resolve) => { releaseHeader = resolve; });
  const pending = zipStoreStream([
    {
      name: "blocked.bin",
      data: (async function* () {
        payloadPulls++;
        yield new Uint8Array([7]);
      })(),
    },
  ], async (part) => {
    writes.push(part);
    if (writes.length === 1) await headerGate;
  });

  while (writes.length === 0) await Promise.resolve();
  assert.equal(payloadPulls, 0);
  assert.equal(writes.length, 1);
  releaseHeader();
  await pending;
  assert.equal(payloadPulls, 1);
  assert.ok(writes.length > 1);
});

test("zipStoreStream cancels a ReadableStream payload when the sink fails", async () => {
  let cancelled = 0;
  const stream = new ReadableStream({
    start(controller) { controller.enqueue(new Uint8Array([1, 2, 3])); },
    cancel() { cancelled++; },
  });
  let writes = 0;

  await assert.rejects(zipStoreStream([{ name: "partial.bin", data: stream }], async () => {
    writes++;
    if (writes === 2) throw new Error("sink failed");
  }), /sink failed/);

  assert.equal(cancelled, 1);
  assert.equal(stream.locked, false);
});

function concat(parts) {
  const out = new Uint8Array(parts.reduce((size, part) => size + part.byteLength, 0));
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.byteLength;
  }
  return out;
}

function readU16(bytes, offset) {
  return bytes[offset] | (bytes[offset + 1] << 8);
}

function readU32(bytes, offset) {
  return (
    bytes[offset]
    | (bytes[offset + 1] << 8)
    | (bytes[offset + 2] << 16)
    | (bytes[offset + 3] << 24)
  ) >>> 0;
}

function findSignature(bytes, sig) {
  for (let i = 0; i <= bytes.length - 4; i++) {
    if (readU32(bytes, i) === sig) return i;
  }
  return -1;
}
