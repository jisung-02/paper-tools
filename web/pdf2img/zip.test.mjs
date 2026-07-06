import assert from "node:assert/strict";
import { test } from "node:test";
import { zipStore } from "./zip.mjs";

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
