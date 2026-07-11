import { visualError } from "./visual-errors.mjs";

const DEFAULT_MAX_PIXELS = 64 * 1024 * 1024;

function validateImage(data, width, height) {
  if (!(data instanceof Uint8Array || data instanceof Uint8ClampedArray) ||
      !Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 0 || height < 0 ||
      ((width === 0 || height === 0) && (width !== 0 || height !== 0)) ||
      data.byteLength !== width * height * 4) {
    throw visualError("invalid-input", "invalid RGBA image dimensions", TypeError);
  }
}

function pixelDifference(a, ai, b, bi) {
  let difference = 0;
  for (let channel = 0; channel < 4; channel++) {
    difference = Math.max(difference, Math.abs(a[ai + channel] - b[bi + channel]));
  }
  return difference;
}

function brightness(data, index) {
  return data[index] * 0.299 + data[index + 1] * 0.587 + data[index + 2] * 0.114;
}

function hasBrightnessSlope(data, width, height, x, y, minimumContrast) {
  const center = brightness(data, (y * width + x) * 4);
  let darker = false;
  let lighter = false;
  for (let dy = -1; dy <= 1; dy++) {
    for (let dx = -1; dx <= 1; dx++) {
      if ((!dx && !dy) || x + dx < 0 || x + dx >= width || y + dy < 0 || y + dy >= height) continue;
      const neighbor = brightness(data, ((y + dy) * width + x + dx) * 4);
      darker ||= neighbor < center - minimumContrast;
      lighter ||= neighbor > center + minimumContrast;
    }
  }
  return darker && lighter;
}

function isAntialiasChange(a, widthA, heightA, b, widthB, heightB, x, y, difference, tolerance, threshold) {
  if (!tolerance || difference > tolerance || x >= widthA || y >= heightA || x >= widthB || y >= heightB) return false;
  const minimumContrast = Math.max(1, threshold);
  return hasBrightnessSlope(a, widthA, heightA, x, y, minimumContrast) ||
    hasBrightnessSlope(b, widthB, heightB, x, y, minimumContrast);
}

function extent(value, width, height, name) {
  if (value == null) return { width, height };
  const extentWidth = Number(value.width);
  const extentHeight = Number(value.height);
  if (!Number.isFinite(extentWidth) || !Number.isFinite(extentHeight) ||
      extentWidth < 0 || extentHeight < 0 || extentWidth > width || extentHeight > height) {
    throw visualError("invalid-options", `${name} must fit its RGBA raster`, RangeError);
  }
  return { width: extentWidth, height: extentHeight };
}

function pixelCoverage(value, x, y) {
  const horizontal = Math.max(0, Math.min(1, value.width - x));
  const vertical = Math.max(0, Math.min(1, value.height - y));
  return horizontal * vertical;
}

export function diffPixels(a, widthA, heightA, b, widthB, heightB, options = {}) {
  validateImage(a, widthA, heightA);
  validateImage(b, widthB, heightB);
  const width = Math.max(widthA, widthB);
  const height = Math.max(heightA, heightB);
  const totalPixels = width * height;
  const maxPixels = options.maxPixels ?? DEFAULT_MAX_PIXELS;
  if (!Number.isSafeInteger(maxPixels) || maxPixels < 1 || totalPixels > maxPixels) {
    throw visualError("live-pixel-limit", "pixel comparison exceeds budget", RangeError);
  }
  const threshold = options.threshold ?? 0;
  if (!Number.isFinite(threshold) || threshold < 0 || threshold > 255) {
    throw visualError("invalid-options", "threshold must be between 0 and 255", RangeError);
  }
  const antialiasTolerance = options.antialiasTolerance ?? 0;
  if (!Number.isFinite(antialiasTolerance) || antialiasTolerance < 0 || antialiasTolerance > 255) {
    throw visualError("invalid-options", "anti-alias tolerance must be between 0 and 255", RangeError);
  }
  const extentA = extent(options.extentA, widthA, heightA, "left physical page extent");
  const extentB = extent(options.extentB, widthB, heightB, "right physical page extent");
  const heatmap = new Uint8ClampedArray(totalPixels * 4);
  let changedPixels = 0;
  let left = width;
  let top = height;
  let right = 0;
  let bottom = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      let changed = x >= widthA || y >= heightA || x >= widthB || y >= heightB;
      if (!changed) {
        changed = Math.abs(pixelCoverage(extentA, x, y) - pixelCoverage(extentB, x, y)) > 1e-9;
      }
      if (!changed) {
        const ai = (y * widthA + x) * 4;
        const bi = (y * widthB + x) * 4;
        const difference = pixelDifference(a, ai, b, bi);
        changed = difference > threshold &&
          !isAntialiasChange(a, widthA, heightA, b, widthB, heightB, x, y, difference, antialiasTolerance, threshold);
      }
      if (!changed) continue;
      changedPixels++;
      left = Math.min(left, x);
      top = Math.min(top, y);
      right = Math.max(right, x + 1);
      bottom = Math.max(bottom, y + 1);
      const hi = (y * width + x) * 4;
      heatmap[hi] = 255;
      heatmap[hi + 3] = 255;
    }
  }
  return {
    width,
    height,
    totalPixels,
    changedPixels,
    ratio: totalPixels ? changedPixels / totalPixels : 0,
    bounds: changedPixels ? { left, top, right, bottom } : null,
    heatmap,
  };
}
