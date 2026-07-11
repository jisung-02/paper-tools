import { visualError } from "./visual-errors.mjs";

function positiveBudget(value, name) {
  if (!Number.isSafeInteger(value) || value < 1) throw visualError("invalid-options", `${name} must be a positive integer`, RangeError);
  return value;
}

function sourceBytes(source) {
  if (typeof Blob !== "undefined" && source instanceof Blob) return source.size;
  if (source instanceof ArrayBuffer) return source.byteLength;
  if (ArrayBuffer.isView(source)) return source.byteLength;
  throw visualError("invalid-input", "visual comparison inputs must be Blob or byte data", TypeError);
}

export function assertCombinedInputBytes(sources, maxBytes) {
  positiveBudget(maxBytes, "combined input byte limit");
  if (!Array.isArray(sources)) throw visualError("invalid-input", "visual comparison inputs must be an array", TypeError);
  let total = 0;
  for (const source of sources) {
    total += sourceBytes(source);
    if (!Number.isSafeInteger(total) || total > maxBytes) {
      throw visualError("input-limit", "visual comparison combined input byte limit exceeded", RangeError);
    }
  }
  return total;
}

export function assertCombinedPageCount(counts, maxPages) {
  positiveBudget(maxPages, "combined page limit");
  if (!Array.isArray(counts)) throw visualError("invalid-input", "visual comparison page counts must be an array", TypeError);
  let total = 0;
  for (const count of counts) {
    if (!Number.isSafeInteger(count) || count < 0) throw visualError("invalid-input", "invalid visual comparison page count", RangeError);
    total += count;
    if (!Number.isSafeInteger(total) || total > maxPages) {
      throw visualError("page-limit", "visual comparison combined page limit exceeded", RangeError);
    }
  }
  return total;
}

function dimensions(page) {
  if (page == null) return { width: 0, height: 0 };
  const width = Number(page.width);
  const height = Number(page.height);
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    throw visualError("invalid-input", "invalid visual comparison page dimensions", RangeError);
  }
  return { width, height };
}

export function scaleForLivePixelBudget(left, right, requestedScale, options = {}) {
  const a = dimensions(left);
  const b = dimensions(right);
  const requested = Number(requestedScale);
  if (!Number.isFinite(requested) || requested <= 0) throw visualError("invalid-options", "visual comparison scale must be positive", RangeError);
  const maxLivePixels = positiveBudget(options.maxLivePixels, "live RGBA pixel limit");
  const retainedPixels = options.retainedPixels ?? 0;
  if (!Number.isSafeInteger(retainedPixels) || retainedPixels < 0 || retainedPixels >= maxLivePixels) {
    throw visualError("live-pixel-limit", "retained visual comparison surfaces exceed the live pixel limit", RangeError);
  }
  const inputArea = a.width * a.height + b.width * b.height;
  const unionArea = Math.max(a.width, b.width) * Math.max(a.height, b.height);
  const coefficient = 2 * inputArea + 2 * unionArea;
  if (!Number.isFinite(coefficient) || coefficient <= 0) throw visualError("invalid-input", "invalid visual comparison surface area", RangeError);
  const allowed = Math.sqrt((maxLivePixels - retainedPixels) / coefficient);
  return Math.min(requested, allowed);
}

export function createByteLimitedSink(sink, maxBytes) {
  if (!sink || typeof sink.write !== "function" || typeof sink.close !== "function") {
    throw visualError("export-failed", "byte-limited output requires a writable sink", TypeError);
  }
  positiveBudget(maxBytes, "export byte limit");
  let bytesWritten = 0;
  return {
    kind: sink.kind,
    name: sink.name,
    get bytesWritten() { return bytesWritten; },
    async write(chunk) {
      if (!(chunk instanceof Uint8Array)) throw visualError("export-failed", "export sink writes require Uint8Array chunks", TypeError);
      const next = bytesWritten + chunk.byteLength;
      if (!Number.isSafeInteger(next) || next > maxBytes) {
        throw visualError("export-limit", "visual comparison export byte limit exceeded", RangeError);
      }
      await sink.write(chunk);
      bytesWritten = next;
    },
    close: () => sink.close(),
    abort: () => sink.abort?.(),
    cleanup: () => sink.cleanup?.(),
  };
}
