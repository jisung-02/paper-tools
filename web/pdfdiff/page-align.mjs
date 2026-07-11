import { fingerprintText } from "../text-fingerprint.mjs";

export { fingerprintText } from "../text-fingerprint.mjs";

const DEFAULT_MAX_CELLS = 1_000_000;

function indexPairs(aLength, bLength) {
  const pairs = [];
  const count = Math.max(aLength, bLength);
  for (let i = 0; i < count; i++) {
    pairs.push({ a: i < aLength ? i : null, b: i < bLength ? i : null });
  }
  return pairs;
}

function fingerprintDistance(a = [], b = []) {
  const length = Math.max(a.length, b.length);
  if (!length) return 0;
  let sum = 0;
  for (let i = 0; i < length; i++) {
    const av = Number(a[i] ?? 0);
    const bv = Number(b[i] ?? 0);
    sum += Math.min(1, Math.abs(av - bv) / 255);
  }
  return sum / length;
}

function pageDistance(a, b) {
  const fp = fingerprintDistance(a.fingerprint, b.fingerprint);
  const text = a.textFingerprint == null || b.textFingerprint == null ||
    String(a.textFingerprint) === String(b.textFingerprint) ? 0 : 0.5;
  const aw = Number(a.width) || 0;
  const ah = Number(a.height) || 0;
  const bw = Number(b.width) || 0;
  const bh = Number(b.height) || 0;
  const size = aw === bw && ah === bh ? 0 : 0.5;
  return fp + text + size;
}

export function alignPages(a, b, options = {}) {
  if (!Array.isArray(a) || !Array.isArray(b)) throw new TypeError("pages must be arrays");
  const maxCells = options.maxCells ?? DEFAULT_MAX_CELLS;
  if (!Number.isSafeInteger(maxCells) || maxCells < 1) throw new RangeError("invalid alignment budget");
  if ((a.length + 1) * (b.length + 1) > maxCells) {
    return { pairs: indexPairs(a.length, b.length), fallback: true };
  }
  const gap = options.gapCost ?? 0.75;
  if (!Number.isFinite(gap) || gap <= 0) throw new RangeError("alignment gap cost must be positive");
  const cols = b.length + 1;
  const costs = new Float64Array((a.length + 1) * cols);
  const moves = new Uint8Array((a.length + 1) * cols);
  for (let i = 1; i <= a.length; i++) { costs[i * cols] = i * gap; moves[i * cols] = 1; }
  for (let j = 1; j <= b.length; j++) { costs[j] = j * gap; moves[j] = 2; }
  for (let i = 1; i <= a.length; i++) {
    for (let j = 1; j <= b.length; j++) {
      const idx = i * cols + j;
      const match = costs[(i - 1) * cols + j - 1] + pageDistance(a[i - 1], b[j - 1]);
      const remove = costs[(i - 1) * cols + j] + gap;
      const insert = costs[i * cols + j - 1] + gap;
      if (match <= remove && match <= insert) { costs[idx] = match; moves[idx] = 0; }
      else if (remove <= insert) { costs[idx] = remove; moves[idx] = 1; }
      else { costs[idx] = insert; moves[idx] = 2; }
    }
  }
  const pairs = [];
  let i = a.length;
  let j = b.length;
  while (i || j) {
    const move = moves[i * cols + j];
    if (i && j && move === 0) { pairs.push({ a: --i, b: --j }); }
    else if (i && (j === 0 || move === 1)) { pairs.push({ a: --i, b: null }); }
    else { pairs.push({ a: null, b: --j }); }
  }
  pairs.reverse();
  return { pairs, fallback: false, cost: costs[a.length * cols + b.length] };
}
