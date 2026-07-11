export function normalizeSelection(x1, y1, x2, y2, width, height) {
  if (![x1, y1, x2, y2, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    throw new Error("invalid selection geometry");
  }
  const left = Math.max(0, Math.min(width, Math.min(x1, x2)));
  const right = Math.max(0, Math.min(width, Math.max(x1, x2)));
  const top = Math.max(0, Math.min(height, Math.min(y1, y2)));
  const bottom = Math.max(0, Math.min(height, Math.max(y1, y2)));
  if (right <= left || bottom <= top) throw new Error("selection has no area");
  return { left:left / width, top:top / height, right:right / width, bottom:bottom / height };
}

export function selectionPixels(selection, width, height, padding = 0) {
  if (!selection || ![selection.left, selection.top, selection.right, selection.bottom].every(Number.isFinite) ||
      selection.left < 0 || selection.top < 0 || selection.right > 1 || selection.bottom > 1 ||
      selection.right <= selection.left || selection.bottom <= selection.top ||
      !Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 1 || height < 1 ||
      !Number.isSafeInteger(padding) || padding < 0 || padding > 64) {
    throw new Error("invalid selection bounds");
  }
  const x0 = Math.max(0, Math.floor(selection.left * width) - padding);
  const y0 = Math.max(0, Math.floor(selection.top * height) - padding);
  const x1 = Math.min(width, Math.ceil(selection.right * width) + padding);
  const y1 = Math.min(height, Math.ceil(selection.bottom * height) + padding);
  return { x:x0, y:y0, width:x1 - x0, height:y1 - y0 };
}
