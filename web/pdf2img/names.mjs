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
