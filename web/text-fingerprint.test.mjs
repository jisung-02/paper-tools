import assert from "node:assert/strict";
import { test } from "node:test";

import { createTextFingerprint, fingerprintText, utf8ByteLength } from "./text-fingerprint.mjs";

test("incremental text fingerprinting is identical to one-shot content", () => {
  const incremental = createTextFingerprint();
  incremental.update("Alpha");
  incremental.update(" ");
  incremental.update("Beta");
  assert.equal(incremental.digest(), fingerprintText("Alpha Beta"));
});

test("full-stream fingerprints distinguish equal-length suffixes beyond a shared prefix", () => {
  const prefix = "x".repeat(8_192);
  assert.notEqual(fingerprintText(`${prefix}A`), fingerprintText(`${prefix}B`));
  assert.equal(fingerprintText(`${prefix}A`).length, fingerprintText(`${prefix}B`).length);
});

test("UTF-8 byte accounting handles ASCII, BMP and surrogate pairs without buffers", () => {
  assert.equal(utf8ByteLength("A한😀"), 8);
  assert.equal(utf8ByteLength("\ud800"), 3);
});
