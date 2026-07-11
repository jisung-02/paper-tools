// web/thumbs.test.mjs — unit tests for the range-parsing/formatting helpers
// shared by the Reorder/Remove/Split page-thumbnail grid (web/thumbs.js).
// Run with: node --test web/thumbs.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { createPageState, formatOrder, formatRanges, pageStateMove, pageStateSelect, pageStateValue, parseRanges } from "./thumbs.js";

test("parseRanges: single pages", () => {
  assert.deepEqual(parseRanges("1,3,5", 10), [1, 3, 5]);
});

test("parseRanges: dashed ranges", () => {
  assert.deepEqual(parseRanges("1-3,5", 10), [1, 2, 3, 5]);
});

test("parseRanges: open-ended range to total", () => {
  assert.deepEqual(parseRanges("5-", 8), [5, 6, 7, 8]);
});

test("parseRanges: whitespace around tokens is tolerated", () => {
  assert.deepEqual(parseRanges(" 1 , 3 - 4 ", 10), [1, 3, 4]);
});

test("parseRanges: empty/blank input is unparseable", () => {
  assert.equal(parseRanges("", 10), null);
  assert.equal(parseRanges("   ", 10), null);
  assert.equal(parseRanges(null, 10), null);
});

test("parseRanges: non-numeric tokens are unparseable", () => {
  assert.equal(parseRanges("1,a,3", 10), null);
  assert.equal(parseRanges("1-x", 10), null);
});

test("parseRanges: out-of-bounds ranges are rejected", () => {
  assert.equal(parseRanges("11", 10), null); // hi > total
  assert.equal(parseRanges("0", 10), null); // lo < 1
  assert.equal(parseRanges("5-3", 10), null); // lo > hi
});

test("parseRanges: zero total is unparseable", () => {
  assert.equal(parseRanges("1", 0), null);
});

test("formatRanges: compacts consecutive runs", () => {
  assert.equal(formatRanges([2, 4, 5]), "2,4-5");
});

test("formatRanges: dedupes and sorts unordered input", () => {
  assert.equal(formatRanges([5, 1, 3, 2, 5]), "1-3,5");
});

test("formatRanges: single page has no dash", () => {
  assert.equal(formatRanges([7]), "7");
});

test("formatRanges: fully consecutive run collapses to one range", () => {
  assert.equal(formatRanges([1, 2, 3, 4]), "1-4");
});

test("formatOrder: joins the ordered permutation with commas", () => {
  assert.equal(formatOrder([3, 1, 2]), "3,1,2");
});

// Round-trip: format -> parse must reproduce the same page set (order
// aside) for the Remove/Split (set-based) tools.
test("round-trip: formatRanges output re-parses to the same page set", () => {
  const pages = [2, 4, 5, 9, 10, 11];
  const total = 12;
  const text = formatRanges(pages);
  assert.equal(text, "2,4-5,9-11");
  const parsed = parseRanges(text, total);
  assert.deepEqual(parsed, [...pages].sort((a, b) => a - b));
});

// Round-trip: formatOrder output re-parses to the same permutation, in the
// same order, for the Reorder tool.
test("round-trip: formatOrder output re-parses to the same order", () => {
  const order = [4, 2, 1, 3];
  const total = 4;
  const text = formatOrder(order);
  assert.equal(text, "4,2,1,3");
  const parsed = parseRanges(text, total);
  assert.deepEqual(parsed, order);
});

test("page state preserves order and selections beyond the thumbnail limit", () => {
  const order = [500, ...Array.from({ length: 499 }, (_, i) => i + 1)];
  const state = createPageState(500, "reorder", order.join(","));
  assert.equal(state.totalPageCount, 500);
  assert.equal(state.order.length, 500);
  assert.equal(state.order[0], 500);
  pageStateMove(state, 201, 2);
  assert.equal(state.order[2], 201);
  assert.equal(pageStateValue(state, "reorder").split(",").length, 500);
});

test("page state keeps selected pages 199/200/201/500", () => {
  const state = createPageState(500, "split", "199,200,201,500");
  assert.deepEqual([...state.selected], [199, 200, 201, 500]);
  pageStateSelect(state, 200, false);
  assert.equal(pageStateValue(state, "split"), "199,201,500");
});
