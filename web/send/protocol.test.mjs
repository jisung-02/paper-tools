import assert from "node:assert/strict";
import { test } from "node:test";
import { ReceiverProtocol, validateManifest, safeFileName } from "./protocol.mjs";
import { sha256Hex, transferChecksum } from "./integrity.mjs";

test("manifest accepts multiple files and rejects unsafe metadata", () => {
  const manifest = validateManifest({
    version: 3,
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
    version: 3,
    transferId: "too-many-chunks",
    chunkSize: 1,
    files: [{ id: "x", name: "x", size: 1_000_001, type: "x" }],
  }), /chunks/);
  assert.throws(() => validateManifest({ ...manifest, version: 2 }), /manifest/);
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

test("receiver hashes each chunk, writes it at the running offset, and resumes at the verified offset", async () => {
  const bytes = new TextEncoder().encode("data");
  const writes = [];
  const receiver = new ReceiverProtocol({ write: async (file, offset, value) => writes.push([file.id, offset, [...value]]) });
  receiver.start({ version: 3, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  assert.deepEqual(receiver.resumeOffsets(), { a: 0 });
  assert.deepEqual(await receiver.chunk("a", bytes), { offset: 4 });
  assert.deepEqual(receiver.resumeOffsets(), { a: 4 });
  assert.deepEqual(writes, [["a", 0, [100, 97, 116, 97]]]);
});

test("receiver rejects a chunk for an unknown file id, wrong length, or once the file is full", async () => {
  const receiver = new ReceiverProtocol({ write: async () => {} });
  receiver.start({ version: 3, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  await assert.rejects(receiver.chunk("missing", new Uint8Array(4)), /unknown file id/);
  await assert.rejects(receiver.chunk("a", new Uint8Array(3)), /unexpected chunk length/);
  await receiver.chunk("a", new Uint8Array(4));
  await assert.rejects(receiver.chunk("a", new Uint8Array(4)), /no remaining bytes/);
});

test("resumeOffsets floors a misaligned offset to the chunk boundary and truncates its digest list to match", async () => {
  const receiver = new ReceiverProtocol({ write: async () => {} });
  receiver.start({ version: 3, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 10, type: "x" }] });
  // Every real write path keeps state chunk-aligned; this reaches into the
  // internal state map to exercise the defensive floor/truncate invariant
  // directly, since the wire protocol can't otherwise produce a misaligned
  // offset (one binary message is always one whole chunk).
  const state = receiver.fileState("a");
  state.offset = 6;
  state.digests = ["digest-for-bytes-0-4", "digest-for-bytes-4-8"];
  assert.deepEqual(receiver.resumeOffsets(), { a: 4 });
  assert.deepEqual(state.digests, ["digest-for-bytes-0-4"]);
});

test("receiver verifies the composite per-file checksum before finishing the sink, and is idempotent", async () => {
  const bytes = new TextEncoder().encode("data");
  const digest = await sha256Hex(bytes);
  const checksum = await transferChecksum([digest]);
  const finished = [];
  const receiver = new ReceiverProtocol({
    write: async () => {},
    finish: async (file) => { finished.push(file.id); return new Blob([bytes]); },
  });
  receiver.start({ version: 3, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "a", size: 4, type: "x" }] });
  await receiver.chunk("a", bytes);

  await assert.rejects(receiver.finish("a", "PT-SHA256-v1:" + "00".repeat(32)), /checksum/);
  const result = await receiver.finish("a", checksum);
  assert.equal(result.file.id, "a");
  assert.equal(result.value.size, 4);
  assert.equal((await receiver.finish("a", checksum)).replayed, true);
  assert.deepEqual(finished, ["a"]);
  assert.deepEqual(receiver.complete(), { transferId: "t", files: 1, bytes: 4 });
});

test("a zero-byte file completes with no chunks, using the checksum of an empty digest list", async () => {
  const receiver = new ReceiverProtocol({ write: async () => {}, finish: async () => new Blob([]) });
  receiver.start({ version: 3, transferId: "t", chunkSize: 4, files: [{ id: "a", name: "empty", size: 0, type: "x" }] });
  assert.deepEqual(receiver.resumeOffsets(), { a: 0 });
  const emptyChecksum = await transferChecksum([]);
  const result = await receiver.finish("a", emptyChecksum);
  assert.equal(result.value.size, 0);
  assert.deepEqual(receiver.complete(), { transferId: "t", files: 1, bytes: 0 });
});

test("integrity functions are deterministic", async () => {
  assert.equal(await sha256Hex(new TextEncoder().encode("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  assert.equal(await transferChecksum(["00".repeat(32), "ff".repeat(32)]), await transferChecksum(["00".repeat(32), "ff".repeat(32)]));
});
