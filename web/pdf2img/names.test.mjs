import assert from "node:assert/strict";
import { test } from "node:test";
import { imageFileName, imageMime, jpegQuality, pageNumbers, renderScale } from "./names.mjs";

test("image output metadata follows selected format and page number", () => {
  assert.equal(imageFileName(1, "png"), "page-001.png");
  assert.equal(imageFileName(12, "jpeg"), "page-012.jpg");
  assert.equal(imageMime("png"), "image/png");
  assert.equal(imageMime("jpeg"), "image/jpeg");
});

test("renderScale accepts only supported scale values", () => {
  assert.equal(renderScale("1"), 1);
  assert.equal(renderScale("2"), 2);
  assert.equal(renderScale("bad"), 1);
});

test("pageNumbers parses ranges or defaults to all pages", () => {
  assert.deepEqual(pageNumbers("", 4), [1, 2, 3, 4]);
  assert.deepEqual(pageNumbers("1-2,4", 4), [1, 2, 4]);
  assert.deepEqual(pageNumbers("3-", 5), [3, 4, 5]);
  assert.throws(() => pageNumbers("0", 4), /out of bounds/);
  assert.throws(() => pageNumbers("4-2", 4), /out of bounds/);
  assert.throws(() => pageNumbers("1x", 4), /invalid page range/);
});

test("jpegQuality clamps to a browser-safe range", () => {
  assert.equal(jpegQuality("0.8"), 0.8);
  assert.equal(jpegQuality("2"), 1);
  assert.equal(jpegQuality("0"), 0.1);
  assert.equal(jpegQuality("bad"), 0.9);
});
