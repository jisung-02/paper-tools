import assert from "node:assert/strict";
import { test } from "node:test";
import { ReceiverProtocol, validateManifest, safeFileName } from "./protocol.mjs";
import { sha256Hex, transferChecksum } from "./integrity.mjs";

test("manifest accepts multiple files and rejects unsafe metadata", () => {
  const manifest = validateManifest({
    version: 2,
    transferId: "transfer-1",
    chunkSize: 4,
    files: [
      { id: "a", name: "a.txt", size: 4, type: "text/plain" },
      { id: "b", name: "b.txt", size: 0, type: "text/plain" },
    ],
  });
  assert.equal(manifest.totalSize, 4);
  assert.throws(() => validateManifest({ ...manifest, files: [{ id: "x", name: "../secret", size: 1, type: "x" }] }), /file name/);
  assert.throws(() => validateManifest({ ...manifest, files: [{ id: "x", name: "x", size: -1, type: "x" }] }), /size/);
  assert.throws(() => validateManifest({
    version: 2,
    transferId: "too-many-chunks",
    chunkSize: 1,
    files: [{ id: "x", name: "x", size: 1_000_001, type: "x" }],
  }), /chunks/);
});

test("safeFileName makes duplicate output names collision-free", () => {
  const used = new Set();
  assert.equal(safeFileName("report.pdf", used), "report.pdf");
  assert.equal(safeFileName("report.pdf", used), "report (2).pdf");
});

test("safeFileName keeps collision suffixes inside the UTF-8 name limit", () => {
  const original = "가".repeat(83) + ".pdf";
  const used = new Set([original]);
  const duplicate = safeFileName(original, used);
  assert.ok(new TextEncoder().encode(duplicate).byteLength <= 255);
  assert.match(duplicate, / \(2\)\.pdf$/);
});

test("receiver validates ordered chunks and resumes at verified offsets", async () => {
  const bytes = new TextEncoder().encode("data");
  const digest = await sha256Hex(bytes);
  const writes = [];
  const receiver = new ReceiverProtocol({ write: async (file, offset, value) => writes.push([file.id, offset, [...value]]) });
  receiver.start({ version: 2, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  assert.deepEqual(receiver.resumeOffsets(), { a: 0 });
  assert.deepEqual(await receiver.chunk({ fileId: "a", seq: 0, offset: 0, length: 4, sha256: digest }, bytes), { type: "ack", fileId: "a", seq: 0, offset: 4 });
  assert.deepEqual(receiver.resumeOffsets(), { a: 4 });
  assert.deepEqual(writes, [["a", 0, [100, 97, 116, 97]]]);
});

test("receiver idempotently ACKs the last verified chunk", async () => {
  const bytes = new TextEncoder().encode("data");
  const digest = await sha256Hex(bytes);
  let writes = 0;
  const receiver = new ReceiverProtocol({ write: async () => { writes++; } });
  receiver.start({ version: 2, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  const header = { fileId: "a", seq: 0, offset: 0, length: 4, sha256: digest };
  const first = await receiver.chunk(header, bytes);
  const duplicate = await receiver.chunk(header, bytes);
  assert.deepEqual(duplicate, first);
  assert.equal(writes, 1);
});

test("receiver emits three NACKs and aborts on the next hash mismatch", async () => {
  const receiver = new ReceiverProtocol({ write: async () => {} });
  receiver.start({ version: 2, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  const header = { fileId: "a", seq: 0, offset: 0, length: 4, sha256: "00".repeat(32) };
  const bytes = new TextEncoder().encode("data");
  assert.equal((await receiver.chunk(header, bytes)).type, "nack");
  assert.equal((await receiver.chunk(header, bytes)).type, "nack");
  assert.equal((await receiver.chunk(header, bytes)).type, "nack");
  await assert.rejects(receiver.chunk(header, bytes), /retry limit/);
});

test("receiver rejects duplicate, skipped and oversized chunks", async () => {
  const receiver = new ReceiverProtocol({ write: async () => {} });
  receiver.start({ version: 2, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  const bytes = new Uint8Array([1]);
  const digest = await sha256Hex(bytes);
  await assert.rejects(receiver.chunk({ fileId: "a", seq: 1, offset: 0, length: 1, sha256: digest }, bytes), /sequence/);
  await assert.rejects(receiver.chunk({ fileId: "a", seq: 0, offset: 1, length: 1, sha256: digest }, bytes), /offset/);
  await assert.rejects(receiver.chunk({ fileId: "a", seq: 0, offset: 0, length: 2, sha256: digest }, bytes), /length/);
  const emptyDigest = await sha256Hex(new Uint8Array());
  await assert.rejects(receiver.chunk({ fileId: "a", seq: 0, offset: 0, length: 0, sha256: emptyDigest }, new Uint8Array()), /length/);
});

test("receiver verifies per-file checksum before finishing the sink", async () => {
  const bytes = new TextEncoder().encode("data");
  const digest = await sha256Hex(bytes);
  const checksum = await transferChecksum([digest]);
  const finished = [];
  const receiver = new ReceiverProtocol({
    write: async () => {},
    finish: async (file) => { finished.push(file.id); return new Blob([bytes]); },
  });
  receiver.start({ version: 2, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  await receiver.chunk({ fileId: "a", seq: 0, offset: 0, length: 4, sha256: digest }, bytes);

  await assert.rejects(receiver.finish("a", "PT-SHA256-v1:" + "00".repeat(32)), /checksum/);
  const result = await receiver.finish("a", checksum);
  assert.equal(result.file.id, "a");
  assert.equal(result.value.size, 4);
  await assert.rejects(receiver.finish("a", "PT-SHA256-v1:" + "00".repeat(32)), /checksum/);
  assert.equal((await receiver.finish("a", checksum)).replayed, true);
  assert.deepEqual(finished, ["a"]);
  assert.deepEqual(receiver.complete(), { transferId: "t", files: 1, bytes: 4 });
});

test("integrity functions are deterministic", async () => {
  assert.equal(await sha256Hex(new TextEncoder().encode("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  assert.equal(await transferChecksum(["00".repeat(32), "ff".repeat(32)]), await transferChecksum(["00".repeat(32), "ff".repeat(32)]));
});
