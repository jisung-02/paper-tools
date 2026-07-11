import assert from "node:assert/strict";
import { test } from "node:test";

import { normalizeTextBoxes } from "./boxes.mjs";

test("normalizes tesseract word boxes and confidence to the OCR PDF schema", () => {
  assert.deepEqual(normalizeTextBoxes([
    {
      text: "Hello",
      confidence: 0.875,
      flags: 3,
      rect: { left: 20, top: 10, right: 180, bottom: 50 },
    },
  ], 200, 100), [
    {
      text: "Hello",
      confidence: 0.875,
      left: 0.1,
      top: 0.1,
      right: 0.9,
      bottom: 0.5,
    },
  ]);
});

test("rejects invalid image dimensions and non-array box results", () => {
  assert.throws(() => normalizeTextBoxes([], 0, 100), /image width/);
  assert.throws(() => normalizeTextBoxes([], 100, Number.NaN), /image height/);
  assert.throws(() => normalizeTextBoxes({}, 100, 100), /array/);
});

test("rejects malformed text, confidence, and pixel bounds", () => {
  const valid = {
    text: "word",
    confidence: 0.5,
    rect: { left: 1, top: 2, right: 10, bottom: 20 },
  };
  const normalize = (patch) => normalizeTextBoxes([{ ...valid, ...patch }], 100, 100);

  assert.throws(() => normalize({ text: "   " }), /text/);
  assert.throws(() => normalize({ text: "bad\nword" }), /text/);
  assert.throws(() => normalize({ confidence: -0.01 }), /confidence/);
  assert.throws(() => normalize({ confidence: Number.NaN }), /confidence/);
  assert.throws(() => normalize({ rect: { left: -1, top: 2, right: 10, bottom: 20 } }), /bounds/);
  assert.throws(() => normalize({ rect: { left: 10, top: 2, right: 10, bottom: 20 } }), /bounds/);
  assert.throws(() => normalize({ rect: { left: 1, top: 2, right: 101, bottom: 20 } }), /bounds/);
  assert.throws(() => normalize({ rect: { left: 1, top: 2, right: 10, bottom: Number.POSITIVE_INFINITY } }), /bounds/);
});

test("returns detached plain values instead of exposing tesseract objects", () => {
  const source = {
    text: "word",
    confidence: 1,
    rect: { left: 0, top: 0, right: 10, bottom: 10 },
  };
  const [word] = normalizeTextBoxes([source], 10, 10);
  source.text = "changed";
  source.rect.right = 1;
  assert.deepEqual(word, {
    text: "word",
    confidence: 1,
    left: 0,
    top: 0,
    right: 1,
    bottom: 1,
  });
});
