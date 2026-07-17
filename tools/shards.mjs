// Usage: node tools/shards.mjs <shardIndex> <shardCount>
// Prints the space-separated wasm tool ids assigned to shard <shardIndex>,
// balanced by (approximate) build cost across <shardCount> shards.
//
// The id set is authoritative: it's every operation-catalog.json entry with
// engine==="wasm". Coverage never depends on the weight table below, so a
// tool that's missing from the table still gets built (just with a guessed
// weight) instead of silently dropped.
import { readFileSync } from "node:fs";

const [, , indexArg, countArg] = process.argv;
const shardIndex = Number(indexArg);
const shardCount = Number(countArg);
if (
  !Number.isInteger(shardIndex) ||
  !Number.isInteger(shardCount) ||
  shardCount <= 0 ||
  shardIndex < 0 ||
  shardIndex >= shardCount
) {
  console.error(`usage: node tools/shards.mjs <shardIndex> <shardCount> (0 <= shardIndex < shardCount, integers)`);
  process.exit(1);
}

const catalog = JSON.parse(readFileSync(new URL("./operation-catalog.json", import.meta.url)));
const ids = catalog.filter((t) => t.engine === "wasm").map((t) => t.id);

// Build-cost proxy: last measured wasm output size (bytes), one per tool id.
// Static, not read at build time, so a stale entry can never break the build
// (see WEIGHTS[id] ?? median fallback below).
// Regenerate with (from repo root, after `./build.sh`):
//   for f in web/*/[a-z]*.wasm; do id=$(basename $(dirname "$f")); size=$(stat -f%z "$f" 2>/dev/null || stat -c%s "$f"); echo "  $id: $size,"; done
const WEIGHTS = {
  blank: 719219,
  compress: 765523,
  crop: 725085,
  docx2hwpx: 779935,
  docx2pdf: 832055,
  flatten: 728110,
  hwp2pdf: 742863,
  hwpx2docx: 785770,
  hwpx2pdf: 835854,
  img2pdf: 666444,
  imgconv: 424497,
  imgresize: 426043,
  info: 742695,
  interleave: 716504,
  md2pdf: 747812,
  mdedit: 541202,
  merge: 716744,
  metadata: 720954,
  nup: 736855,
  ocrpdf: 1005163,
  pagenum: 729690,
  pdf2docx: 812527,
  pdf2hwpx: 816607,
  pdfdiff: 650482,
  pdfimages: 783854,
  pdftext: 634297,
  protect: 741849,
  redact: 903442,
  remove: 722811,
  reorder: 722740,
  resize: 730910,
  rotate: 723367,
  split: 722302,
  stamp: 816829,
  txt2pdf: 685284,
  unlock: 737258,
  watermark: 782396,
  xlsx2csv: 547262,
};

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

const fallbackWeight = median(Object.values(WEIGHTS));
const weightOf = (id) => WEIGHTS[id] ?? fallbackWeight;

// Deterministic LPT (longest processing time first) bin-packing.
const sortedIds = [...ids].sort((a, b) => weightOf(b) - weightOf(a) || (a < b ? -1 : a > b ? 1 : 0));
const bins = Array.from({ length: shardCount }, () => []);
const binWeights = new Array(shardCount).fill(0);
for (const id of sortedIds) {
  let lightest = 0;
  for (let i = 1; i < shardCount; i++) {
    if (binWeights[i] < binWeights[lightest]) lightest = i;
  }
  bins[lightest].push(id);
  binWeights[lightest] += weightOf(id);
}

console.log(bins[shardIndex].join(" "));
