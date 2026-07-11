import assert from "node:assert/strict";
import { test } from "node:test";
import {
  buildVisualReport,
  visualReportEntries,
} from "./visual-report.mjs";

const summaries = [
  {
    index: 0,
    pair: { a: 0, b: 0 },
    changedPixels: 0,
    totalPixels: 100,
    ratio: 0,
    bounds: null,
    pageSizeChanged: false,
  },
  {
    index: 1,
    pair: { a: null, b: 1 },
    changedPixels: 25,
    totalPixels: 25,
    ratio: 1,
    bounds: { left: 0, top: 0, right: 5, bottom: 5 },
    pageSizeChanged: true,
  },
];

test("visual report records alignment fallback and changed page summaries", () => {
  const report = buildVisualReport({ fallback: true, summaries, threshold: 12, antialiasTolerance: 8 });
  assert.equal(report.schema, "paper-tools-visual-diff-v1");
  assert.equal(report.alignmentFallback, true);
  assert.equal(report.changedPages, 1);
  assert.deepEqual(report.pages[1].bounds, summaries[1].bounds);
});

test("report export streams changed heatmaps and JSON without retaining image blobs", async () => {
  const generated = [];
  const entries = [];
  for await (const entry of visualReportEntries({
    fallback: false,
    summaries,
    threshold: 12,
    antialiasTolerance: 8,
    async heatmap(index) {
      generated.push(index);
      return new Uint8Array([index, 7]);
    },
  })) entries.push({ name: entry.name, data: [...entry.data] });

  assert.deepEqual(generated, [1]);
  assert.deepEqual(entries.map((entry) => entry.name), ["heatmap-page-0002.png", "report.json"]);
  assert.equal(JSON.parse(new TextDecoder().decode(Uint8Array.from(entries[1].data))).changedPages, 1);
});
