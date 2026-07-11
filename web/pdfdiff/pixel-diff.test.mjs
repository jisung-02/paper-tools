import assert from "node:assert/strict";
import { test } from "node:test";
import { diffPixels } from "./pixel-diff.mjs";

const rgba = (...pixels) => new Uint8ClampedArray(pixels.flat());

test("identical pixels have no changes", () => {
  const a = rgba([255, 255, 255, 255], [0, 0, 0, 255]);
  const result = diffPixels(a, 2, 1, a, 2, 1);
  assert.equal(result.changedPixels, 0);
  assert.equal(result.ratio, 0);
  assert.equal(result.bounds, null);
});

test("one changed pixel reports exact bounds and heatmap", () => {
  const a = rgba([255, 255, 255, 255], [0, 0, 0, 255]);
  const b = rgba([255, 255, 255, 255], [255, 0, 0, 255]);
  const result = diffPixels(a, 2, 1, b, 2, 1);
  assert.equal(result.changedPixels, 1);
  assert.deepEqual(result.bounds, { left: 1, top: 0, right: 2, bottom: 1 });
  assert.deepEqual([...result.heatmap.slice(4, 8)], [255, 0, 0, 255]);
});

test("threshold tolerates small channel and alpha changes", () => {
  const a = rgba([100, 100, 100, 250]);
  const b = rgba([104, 97, 101, 255]);
  assert.equal(diffPixels(a, 1, 1, b, 1, 1, { threshold: 5 }).changedPixels, 0);
  assert.equal(diffPixels(a, 1, 1, b, 1, 1, { threshold: 3 }).changedPixels, 1);
});

test("different canvas sizes count missing area as changed", () => {
  const white = rgba([255, 255, 255, 255]);
  const twoWhite = rgba([255, 255, 255, 255], [255, 255, 255, 255]);
  const result = diffPixels(white, 1, 1, twoWhite, 2, 1);
  assert.equal(result.totalPixels, 2);
  assert.equal(result.changedPixels, 1);
  assert.deepEqual(result.bounds, { left: 1, top: 0, right: 2, bottom: 1 });
});

test("a wholly missing page counts even when the present page is white", () => {
  const missing = new Uint8ClampedArray(0);
  const white = rgba(
    [255, 255, 255, 255], [255, 255, 255, 255],
    [255, 255, 255, 255], [255, 255, 255, 255],
  );
  const result = diffPixels(missing, 0, 0, white, 2, 2);
  assert.equal(result.changedPixels, 4);
  assert.deepEqual(result.bounds, { left: 0, top: 0, right: 2, bottom: 2 });
});

test("anti-alias tolerance ignores a brightness slope but not an isolated edit", () => {
  const a = rgba(
    [0, 0, 0, 255], [80, 80, 80, 255], [255, 255, 255, 255],
    [0, 0, 0, 255], [80, 80, 80, 255], [255, 255, 255, 255],
    [0, 0, 0, 255], [80, 80, 80, 255], [255, 255, 255, 255],
  );
  const antialiased = a.slice();
  for (const index of [1, 4, 7]) {
    const offset = index * 4;
    antialiased[offset] = antialiased[offset + 1] = antialiased[offset + 2] = 100;
  }
  assert.equal(diffPixels(a, 3, 3, antialiased, 3, 3, {
    threshold: 5,
    antialiasTolerance: 24,
  }).changedPixels, 0);

  const edited = antialiased.slice();
  edited.set([255, 0, 0, 255], 4 * 4);
  assert.equal(diffPixels(a, 3, 3, edited, 3, 3, {
    threshold: 5,
    antialiasTolerance: 24,
  }).changedPixels, 1);
});

test("invalid RGBA dimensions are rejected", () => {
  assert.throws(() => diffPixels(new Uint8ClampedArray(3), 1, 1, new Uint8ClampedArray(4), 1, 1), /RGBA/);
});

test("pixel comparison budget failures carry a stable runtime error code", () => {
  const image = new Uint8ClampedArray(16);
  assert.throws(
    () => diffPixels(image, 2, 2, image.slice(), 2, 2, { maxPixels: 3 }),
    (error) => error?.code === "live-pixel-limit" && /budget/.test(error.message),
  );
});

test("physical page extents mark a changed border even when raster dimensions round equally", () => {
  const white = rgba([255, 255, 255, 255]);
  const result = diffPixels(white, 1, 1, white.slice(), 1, 1, {
    extentA: { width: 0.6, height: 1 },
    extentB: { width: 0.9, height: 1 },
  });
  assert.equal(result.changedPixels, 1);
  assert.deepEqual(result.bounds, { left: 0, top: 0, right: 1, bottom: 1 });
  assert.deepEqual([...result.heatmap], [255, 0, 0, 255]);
});
