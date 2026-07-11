import assert from "node:assert/strict";
import { test } from "node:test";
import { normalizeSelection, selectionPixels } from "./geometry.mjs";

test("drag selection normalizes direction and clamps to canvas", () => {
  assert.deepEqual(normalizeSelection(80, 90, 10, -10, 100, 100), {
    left: 0.1, top: 0, right: 0.8, bottom: 0.9,
  });
});

test("pixel selection rounds outward and adds safety padding", () => {
  assert.deepEqual(selectionPixels({ left:.101, top:.202, right:.799, bottom:.898 }, 100, 100, 2), {
    x:8, y:18, width:74, height:74,
  });
});

test("zero-area and invalid selections are rejected", () => {
  assert.throws(() => normalizeSelection(1, 1, 1, 2, 100, 100), /area/);
  assert.throws(() => selectionPixels({ left:0, top:0, right:2, bottom:1 }, 100, 100), /bounds/);
});
