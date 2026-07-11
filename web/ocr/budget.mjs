export const OCR_LIMITS = Object.freeze({
  maxPages: 500,
  maxSourceBytes: 128 * 1024 * 1024,
  maxPagePixels: 16 * 1024 * 1024,
  maxTotalPixels: 64 * 1024 * 1024,
  maxWords: 250_000,
  maxCharacters: 4_000_000,
  maxJSONBytes: 64 * 1024 * 1024,
});

function limits(options = {}) {
  const resolved = { ...OCR_LIMITS, ...options };
  for (const [name, value] of Object.entries(resolved)) {
    if (!Number.isSafeInteger(value) || value < 1) throw new RangeError(`invalid OCR ${name}`);
  }
  return resolved;
}

function checkedAdd(total, value, maximum, label) {
  if (!Number.isSafeInteger(value) || value < 0 || total > maximum - value) {
    throw new RangeError(`OCR ${label} budget exceeded`);
  }
  return total + value;
}

function utf8Length(value) {
  let bytes = 0;
  for (const character of value) {
    const codePoint = character.codePointAt(0);
    bytes += codePoint <= 0x7f ? 1 : codePoint <= 0x7ff ? 2 : codePoint <= 0xffff ? 3 : 4;
  }
  return bytes;
}

export function validateOCRSelection(files, pageCount, options = {}) {
  const resolved = limits(options);
  if (!Array.isArray(files) || !files.length) throw new TypeError("OCR files are required");
  if (!Number.isSafeInteger(pageCount) || pageCount < 1) throw new RangeError("invalid OCR page count");
  if (pageCount > resolved.maxPages) throw new RangeError("OCR pages budget exceeded");
  let sourceBytes = 0;
  for (const file of files) {
    if (!Number.isSafeInteger(file?.size) || file.size < 0) throw new TypeError("invalid OCR source file");
    sourceBytes = checkedAdd(sourceBytes, file.size, resolved.maxSourceBytes, "source bytes");
  }
  return { pageCount, sourceBytes, limits: resolved };
}

export function renderGeometry(width, height, requestedScale = 2, options = {}) {
  const resolved = limits(options);
  if (!Number.isFinite(width) || width <= 0 || !Number.isFinite(height) || height <= 0 ||
      !Number.isFinite(requestedScale) || requestedScale <= 0) {
    throw new RangeError("invalid OCR render geometry");
  }
  const area = width * height;
  if (!Number.isFinite(area) || area <= 0) throw new RangeError("invalid OCR render area");
  let scale = Math.min(requestedScale, Math.sqrt(resolved.maxPagePixels / area));
  let pixelWidth = Math.max(1, Math.ceil(width * scale));
  let pixelHeight = Math.max(1, Math.ceil(height * scale));
  if (pixelWidth * pixelHeight > resolved.maxPagePixels) {
    scale *= Math.sqrt(resolved.maxPagePixels / (pixelWidth * pixelHeight)) * 0.999999;
    pixelWidth = Math.max(1, Math.ceil(width * scale));
    pixelHeight = Math.max(1, Math.ceil(height * scale));
  }
  if (!Number.isSafeInteger(pixelWidth * pixelHeight) || pixelWidth * pixelHeight > resolved.maxPagePixels) {
    throw new RangeError("OCR page pixels budget exceeded");
  }
  return { width: pixelWidth, height: pixelHeight, scale };
}

export class OCRBudget {
  constructor(pageCount, options = {}) {
    this.limits = limits(options);
    if (!Number.isSafeInteger(pageCount) || pageCount < 1 || pageCount > this.limits.maxPages) {
      throw new RangeError("OCR pages budget exceeded");
    }
    this.pageCount = pageCount;
    this.pages = new Map();
    this.recognized = new Set();
    this.totalPixels = 0;
    this.totalWords = 0;
    this.totalCharacters = 0;
  }

  reservePage(index, width, height) {
    if (!Number.isSafeInteger(index) || index < 0 || index >= this.pageCount) throw new RangeError("invalid OCR page index");
    if (this.pages.has(index)) throw new Error("OCR page already reserved");
    if (!Number.isSafeInteger(width) || width < 1 || !Number.isSafeInteger(height) || height < 1 ||
        width > Math.floor(Number.MAX_SAFE_INTEGER / height)) {
      throw new RangeError("invalid OCR page pixels");
    }
    const pixels = width * height;
    if (pixels > this.limits.maxPagePixels) throw new RangeError("OCR page pixels budget exceeded");
    this.totalPixels = checkedAdd(this.totalPixels, pixels, this.limits.maxTotalPixels, "total pixels");
    this.pages.set(index, { width, height, pixels });
    return this.pages.get(index);
  }

  addRecognition(index, words, text) {
    if (!this.pages.has(index)) throw new Error("OCR page pixels were not reserved");
    if (this.recognized.has(index)) throw new Error("OCR page recognition already recorded");
    if (!Array.isArray(words) || typeof text !== "string") throw new TypeError("invalid OCR recognition result");
    const wordCharacters = words.reduce((total, word) => {
      if (!word || typeof word.text !== "string") throw new TypeError("invalid OCR word text");
      return total + [...word.text].length;
    }, 0);
    const characters = Math.max([...text].length, wordCharacters);
    const nextWords = checkedAdd(this.totalWords, words.length, this.limits.maxWords, "words");
    const nextCharacters = checkedAdd(this.totalCharacters, characters, this.limits.maxCharacters, "characters");
    this.totalWords = nextWords;
    this.totalCharacters = nextCharacters;
    this.recognized.add(index);
  }

  assertSerialized(value) {
    if (typeof value !== "string") throw new TypeError("OCR page JSON must be a string");
    const bytes = utf8Length(value);
    if (bytes > this.limits.maxJSONBytes) throw new RangeError("OCR JSON bytes budget exceeded");
    return bytes;
  }
}
