export function imageFileName(pageNumber, format) {
  const n = String(pageNumber).padStart(3, "0");
  return `page-${n}.${format === "jpeg" ? "jpg" : "png"}`;
}

export function imageMime(format) {
  return format === "jpeg" ? "image/jpeg" : "image/png";
}

export function renderScale(value) {
  return value === "2" ? 2 : 1;
}

export function pageNumbers(value, total) {
  const text = String(value || "").trim();
  if (!text) return Array.from({ length: total }, (_, i) => i + 1);
  const out = [];
  for (const part of text.split(",")) {
    const p = part.trim();
    if (!p) continue;
    const match = p.match(/^(\d+)(?:-(\d*))?$/);
    if (!match) throw new Error(`invalid page range ${JSON.stringify(p)}`);
    const lo = Number.parseInt(match[1], 10);
    const hi = match[2] === undefined ? lo : (match[2] ? Number.parseInt(match[2], 10) : total);
    if (!Number.isInteger(lo) || !Number.isInteger(hi) || lo < 1 || hi > total || lo > hi) {
      throw new Error(`page range ${JSON.stringify(p)} out of bounds (1-${total})`);
    }
    for (let n = lo; n <= hi; n++) out.push(n);
  }
  if (out.length === 0) throw new Error("no pages selected");
  return out;
}

export function jpegQuality(value) {
  const n = Number.parseFloat(value);
  if (!Number.isFinite(n)) return 0.9;
  return Math.min(1, Math.max(0.1, n));
}
