import assert from "node:assert/strict";
import { test } from "node:test";
import * as alignment from "./page-align.mjs";

const { alignPages } = alignment;
const page = (fingerprint, text = "", width = 100, height = 100) => ({
  fingerprint,
  textFingerprint: alignment.fingerprintText?.(text),
  width,
  height,
});

test("identical pages align by index", () => {
  assert.deepEqual(alignPages([page([0], "a"), page([1], "b")], [page([0], "a"), page([1], "b")]).pairs,
    [{ a: 0, b: 0 }, { a: 1, b: 1 }]);
});

test("inserted and deleted pages produce gaps", () => {
  const a = [page([0], "a"), page([2], "c")];
  const b = [page([0], "a"), page([1], "b"), page([2], "c")];
  assert.deepEqual(alignPages(a, b).pairs, [{ a: 0, b: 0 }, { a: null, b: 1 }, { a: 1, b: 2 }]);
});

test("empty pages use size and fingerprint instead of text only", () => {
  const a = [page([0, 0], "", 100, 200)];
  const b = [page([0, 0], "", 100, 200)];
  assert.deepEqual(alignPages(a, b).pairs, [{ a: 0, b: 0 }]);
});

test("blank page insertions and deletions produce stable gaps", () => {
  const first = page([20], "first");
  const blank = page([255], "", 100, 100);
  const last = page([80], "last");
  assert.deepEqual(alignPages([first, last], [first, blank, last]).pairs, [
    { a: 0, b: 0 }, { a: null, b: 1 }, { a: 1, b: 2 },
  ]);
  assert.deepEqual(alignPages([first, blank, last], [first, last]).pairs, [
    { a: 0, b: 0 }, { a: 1, b: null }, { a: 2, b: 1 },
  ]);
});

test("an unavailable bounded text signal is neutral instead of a mismatch penalty", () => {
  const left = page([42], "left");
  const right = page([42], "right");
  left.textFingerprint = null;
  const result = alignPages([left], [right]);
  assert.equal(result.cost, 0);
  assert.deepEqual(result.pairs, [{ a: 0, b: 0 }]);
});

test("alignment falls back to page numbers when cell budget is exceeded", () => {
  const result = alignPages([page([0]), page([1])], [page([0]), page([1])], { maxCells: 1 });
  assert.equal(result.fallback, true);
  assert.deepEqual(result.pairs, [{ a: 0, b: 0 }, { a: 1, b: 1 }]);
});

test("page text is reduced to a fixed-length fingerprint before alignment", () => {
  assert.equal(typeof alignment.fingerprintText, "function");
  const short = alignment.fingerprintText("alpha");
  const long = alignment.fingerprintText("alpha".repeat(10_000));
  assert.equal(short.length, long.length);
  assert.notEqual(short, alignment.fingerprintText("bravo"));
});

test("alignment rejects invalid gap costs", () => {
  assert.throws(() => alignPages([page([0])], [page([0])], { gapCost: -1 }), /gap cost/i);
  assert.throws(() => alignPages([page([0])], [page([0])], { gapCost: Number.NaN }), /gap cost/i);
});
