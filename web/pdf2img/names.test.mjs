import assert from "node:assert/strict";
import { test } from "node:test";
import { imageFileName, imageMime, renderScale } from "./names.mjs";

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
