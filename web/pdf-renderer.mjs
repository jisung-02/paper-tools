const dependencyURL = new URL("./text-fingerprint.mjs", import.meta.url);
const dependencyRetry = new URL(import.meta.url).searchParams.get("preview-retry");
if (dependencyRetry) dependencyURL.searchParams.set("preview-retry", dependencyRetry);
const { createTextFingerprint, utf8ByteLength } = await import(dependencyURL.href);

const LOAD_DEFAULTS = Object.freeze({
  cMapUrl: "/vendor/pdfjs/cmaps/",
  cMapPacked: true,
  standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
});

const TEXT_DEFAULTS = Object.freeze({
  maxItems: 100_000,
  maxChars: 1_048_576,
  maxBytes: 2_097_152,
});

class TextLimitExceeded extends Error {}

function abortError() {
  return new DOMException("PDF rendering aborted", "AbortError");
}

function clearCanvas(canvas) {
  canvas.width = 0;
  canvas.height = 0;
}

async function defaultCanvasToBlob(canvas, type, quality) {
  if (typeof canvas.convertToBlob === "function") return canvas.convertToBlob({ type, quality });
  if (typeof canvas.toBlob !== "function") throw new Error("Canvas export is unavailable");
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("Canvas export failed"));
    }, type, quality);
  });
}

function finitePositive(value, name) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) throw new RangeError(`${name} must be positive`);
  return number;
}

function positiveLimit(value, name) {
  if (value == null || value === Infinity) return Infinity;
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 1) throw new RangeError(`${name} must be a positive integer`);
  return number;
}

function nonNegativeLimit(value, fallback, name) {
  const number = value == null ? fallback : Number(value);
  if (!Number.isSafeInteger(number) || number < 0) throw new RangeError(`${name} must be a non-negative integer`);
  return number;
}

function unavailableTextFingerprint(limitExceeded = false) {
  return {
    textFingerprint: null,
    textItems: 0,
    textChars: 0,
    textBytes: 0,
    textLimitExceeded: limitExceeded,
  };
}

async function streamPageTextFingerprint(page, options, signal, isCancelled, activeReaders) {
  if (page.isPureXfa || typeof page.streamTextContent !== "function") return unavailableTextFingerprint();
  const stream = page.streamTextContent({ includeMarkedContent: false, disableNormalization: false });
  if (!stream || typeof stream.getReader !== "function") return unavailableTextFingerprint();
  const reader = stream.getReader();
  let cancelPromise = null;
  const cancelReader = (reason) => {
    if (!cancelPromise) cancelPromise = Promise.resolve(reader.cancel(reason)).catch(() => {});
    return cancelPromise;
  };
  const trackedReader = { cancel: cancelReader };
  activeReaders.add(trackedReader);
  const maxItems = nonNegativeLimit(options.maxTextItems, TEXT_DEFAULTS.maxItems, "maximum PDF text items");
  const maxChars = nonNegativeLimit(options.maxTextChars, TEXT_DEFAULTS.maxChars, "maximum PDF text characters");
  const maxBytes = nonNegativeLimit(options.maxTextBytes, TEXT_DEFAULTS.maxBytes, "maximum PDF text bytes");
  const fingerprint = createTextFingerprint();
  let itemCount = 0;
  let charCount = 0;
  let byteCount = 0;
  let hasText = false;
  let complete = false;
  let cancelReason = new Error("PDF text streaming cancelled");
  const onAbort = () => { void cancelReader(cancelReason); };
  signal?.addEventListener("abort", onAbort, { once: true });
  try {
    for (;;) {
      if (signal?.aborted || isCancelled()) throw abortError();
      const chunk = await reader.read();
      if (signal?.aborted || isCancelled()) throw abortError();
      if (chunk.done) {
        complete = true;
        break;
      }
      const items = Array.isArray(chunk.value?.items) ? chunk.value.items : [];
      if (itemCount > maxItems - items.length) throw new TextLimitExceeded("PDF text item limit exceeded");
      itemCount += items.length;
      for (const item of items) {
        const value = String(item?.str || "");
        if (!value) continue;
        const separator = hasText ? " " : "";
        const nextChars = charCount + separator.length + value.length;
        if (!Number.isSafeInteger(nextChars) || nextChars > maxChars) {
          throw new TextLimitExceeded("PDF text character limit exceeded");
        }
        const nextBytes = byteCount + separator.length + utf8ByteLength(value);
        if (!Number.isSafeInteger(nextBytes) || nextBytes > maxBytes) {
          throw new TextLimitExceeded("PDF text byte limit exceeded");
        }
        if (separator) fingerprint.update(separator);
        fingerprint.update(value);
        charCount = nextChars;
        byteCount = nextBytes;
        hasText = true;
      }
    }
    return {
      textFingerprint: fingerprint.digest(),
      textItems: itemCount,
      textChars: charCount,
      textBytes: byteCount,
      textLimitExceeded: false,
    };
  } catch (error) {
    cancelReason = error instanceof Error ? error : cancelReason;
    if (error instanceof TextLimitExceeded) return unavailableTextFingerprint(true);
    throw error;
  } finally {
    signal?.removeEventListener("abort", onAbort);
    if (!complete) {
      await cancelReader(cancelReason);
    }
    activeReaders.delete(trackedReader);
    reader.releaseLock?.();
  }
}

export function createPdfRenderer(pdfjs, options = {}) {
  if (!pdfjs || typeof pdfjs.getDocument !== "function") throw new TypeError("PDF.js getDocument is required");
  const createCanvas = options.createCanvas || (() => {
    if (typeof OffscreenCanvas === "undefined") throw new Error("Canvas factory is unavailable");
    return new OffscreenCanvas(1, 1);
  });
  const makeBitmap = options.createImageBitmap || globalThis.createImageBitmap;
  const canvasToBlob = options.canvasToBlob || defaultCanvasToBlob;
  const makeObjectURL = options.createObjectURL || globalThis.URL?.createObjectURL?.bind(globalThis.URL);
  const dropObjectURL = options.revokeObjectURL || globalThis.URL?.revokeObjectURL?.bind(globalThis.URL);
  const defaultMaxPixels = positiveLimit(options.maxPixels, "maximum PDF canvas pixels");
  const defaultMaxDimension = positiveLimit(options.maxDimension, "maximum PDF canvas dimension");
  const sessions = new Set();

  async function open(source, loadOptions = {}) {
    const { signal, ...overrides } = loadOptions;
    if (signal?.aborted) throw abortError();
    const request = source && Object.getPrototypeOf(source) === Object.prototype
      ? { ...LOAD_DEFAULTS, ...source, ...overrides }
      : { ...LOAD_DEFAULTS, ...overrides, data: source };
    const loadingTask = pdfjs.getDocument(request);
    let aborted = false;
    let rejectAbort;
    let loadingDestroy;
    const destroyLoadingTask = () => {
      if (!loadingDestroy) {
        loadingDestroy = Promise.resolve()
          .then(() => loadingTask.destroy?.())
          .catch(() => undefined);
      }
      return loadingDestroy;
    };
    const abortedPromise = new Promise((resolve, reject) => { rejectAbort = reject; });
    const onAbort = () => {
      aborted = true;
      void destroyLoadingTask();
      rejectAbort(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
    let doc;
    try {
      doc = await (signal ? Promise.race([loadingTask.promise, abortedPromise]) : loadingTask.promise);
    } catch (error) {
      await destroyLoadingTask();
      throw aborted ? abortError() : error;
    } finally {
      signal?.removeEventListener("abort", onAbort);
    }

    let destroyed = false;
    let generation = 0;
    const activeTasks = new Set();
    const activeTextReaders = new Set();
    const resources = new Set();
    const pageUses = new Map();
    const pageCleanupRequests = new Set();

    function assertActive() {
      if (destroyed) throw new Error("PDF renderer session is destroyed");
    }

    function trackResource(dispose) {
      let disposed = false;
      const release = () => {
        if (disposed) return;
        disposed = true;
        resources.delete(release);
        dispose();
      };
      resources.add(release);
      return release;
    }

    function retainPage(page) {
      pageUses.set(page, (pageUses.get(page) || 0) + 1);
    }

    async function releasePage(page, cleanup) {
      if (cleanup) pageCleanupRequests.add(page);
      const remaining = (pageUses.get(page) || 1) - 1;
      if (remaining > 0) {
        pageUses.set(page, remaining);
        return;
      }
      pageUses.delete(page);
      if (!pageCleanupRequests.delete(page)) return;
      try { await page.cleanup?.(); } catch {}
    }

    function cancel() {
      if (destroyed) return;
      generation++;
      for (const task of activeTasks) task.cancel?.();
      for (const reader of activeTextReaders) void reader.cancel(new Error("PDF text streaming cancelled")).catch(() => {});
      for (const release of [...resources]) release();
    }

    async function getPageInfo(pageNumber, infoOptions = {}) {
      assertActive();
      if (!Number.isSafeInteger(pageNumber) || pageNumber < 1 || pageNumber > doc.numPages) {
        throw new RangeError("PDF page number is out of range");
      }
      const signal = infoOptions.signal;
      if (signal?.aborted) throw abortError();
      const started = generation;
      const page = await doc.getPage(pageNumber);
      retainPage(page);
      try {
        if (signal?.aborted || started !== generation) throw abortError();
        const viewport = page.getViewport({ scale: 1 });
        const info = { width: viewport.width, height: viewport.height };
        if (infoOptions.includeTextFingerprint) {
          Object.assign(info, await streamPageTextFingerprint(
            page,
            infoOptions,
            signal,
            () => started !== generation,
            activeTextReaders,
          ));
        }
        return info;
      } finally {
        await releasePage(page, infoOptions.cleanupPage === true);
      }
    }

    async function renderPage(pageNumber, renderOptions = {}) {
      assertActive();
      if (!Number.isSafeInteger(pageNumber) || pageNumber < 1 || pageNumber > doc.numPages) {
        throw new RangeError("PDF page number is out of range");
      }
      const output = renderOptions.output || "canvas";
      if (!new Set(["canvas", "bitmap", "object-url"]).has(output)) throw new Error(`unsupported PDF render output: ${output}`);
      const signal = renderOptions.signal;
      if (signal?.aborted) throw abortError();
      const started = generation;
      const page = await doc.getPage(pageNumber);
      retainPage(page);
      try {
      if (signal?.aborted || started !== generation) throw abortError();
      const base = page.getViewport({ scale: 1 });
      const displayScale = renderOptions.width == null
        ? finitePositive(renderOptions.scale ?? 1, "scale")
        : finitePositive(renderOptions.width, "width") / base.width;
      const pixelRatio = finitePositive(renderOptions.pixelRatio ?? 1, "pixel ratio");
      const viewport = page.getViewport({ scale: displayScale * pixelRatio });
      const canvasWidth = Math.max(1, Math.ceil(viewport.width));
      const canvasHeight = Math.max(1, Math.ceil(viewport.height));
      if (!Number.isSafeInteger(canvasWidth) || !Number.isSafeInteger(canvasHeight)) {
        throw new RangeError("PDF canvas dimensions exceed the dimension budget");
      }
      const maxDimension = positiveLimit(renderOptions.maxDimension ?? defaultMaxDimension, "maximum PDF canvas dimension");
      if (canvasWidth > maxDimension || canvasHeight > maxDimension) {
        throw new RangeError("PDF canvas dimensions exceed the dimension budget");
      }
      const maxPixels = positiveLimit(renderOptions.maxPixels ?? defaultMaxPixels, "maximum PDF canvas pixels");
      if (canvasWidth > Math.floor(maxPixels / canvasHeight)) {
        throw new RangeError("PDF canvas pixels exceed the pixel budget");
      }
      const canvas = renderOptions.canvas || createCanvas();
      const ownsCanvas = !renderOptions.canvas;
      canvas.width = canvasWidth;
      canvas.height = canvasHeight;
      const context = canvas.getContext("2d", { alpha: false });
      if (!context) {
        if (ownsCanvas) clearCanvas(canvas);
        throw new Error("Canvas 2D context is unavailable");
      }
      context.fillStyle = renderOptions.background || "#fff";
      context.fillRect(0, 0, canvas.width, canvas.height);
      const task = page.render({ canvasContext: context, viewport });
      activeTasks.add(task);
      let cancelled = false;
      const onRenderAbort = () => {
        cancelled = true;
        task.cancel?.();
      };
      signal?.addEventListener("abort", onRenderAbort, { once: true });
      try {
        await task.promise;
        if (signal?.aborted || started !== generation) throw abortError();
      } catch (error) {
        if (ownsCanvas) clearCanvas(canvas);
        if (cancelled || signal?.aborted || started !== generation) throw abortError();
        throw error;
      } finally {
        signal?.removeEventListener("abort", onRenderAbort);
        activeTasks.delete(task);
      }

      const displayWidth = base.width * displayScale;
      const displayHeight = base.height * displayScale;
      if (output === "canvas") {
        const dispose = ownsCanvas ? trackResource(() => clearCanvas(canvas)) : () => {};
        return { canvas, viewport, width: canvas.width, height: canvas.height, displayWidth, displayHeight, dispose };
      }
      if (output === "bitmap") {
        if (typeof makeBitmap !== "function") {
          if (ownsCanvas) clearCanvas(canvas);
          throw new Error("ImageBitmap creation is unavailable");
        }
        let bitmap;
        try {
          bitmap = await makeBitmap(canvas);
        } finally {
          if (ownsCanvas) clearCanvas(canvas);
        }
        if (signal?.aborted || started !== generation) {
          bitmap.close?.();
          throw abortError();
        }
        const dispose = trackResource(() => bitmap.close?.());
        return { bitmap, viewport, width: bitmap.width, height: bitmap.height, displayWidth, displayHeight, dispose };
      }
      if (typeof makeObjectURL !== "function" || typeof dropObjectURL !== "function") {
        if (ownsCanvas) clearCanvas(canvas);
        throw new Error("Object URL creation is unavailable");
      }
      let blob;
      let url;
      try {
        blob = await canvasToBlob(canvas, renderOptions.mimeType || "image/png", renderOptions.quality);
        url = makeObjectURL(blob);
      } finally {
        if (ownsCanvas) clearCanvas(canvas);
      }
      if (signal?.aborted || started !== generation) {
        dropObjectURL(url);
        throw abortError();
      }
      const dispose = trackResource(() => dropObjectURL(url));
      return { blob, url, viewport, width: viewport.width, height: viewport.height, displayWidth, displayHeight, dispose };
      } finally {
        await releasePage(page, renderOptions.cleanupPage === true);
      }
    }

    const session = {
      numPages: doc.numPages,
      getPageInfo,
      renderPage,
      cancel,
      async destroy() {
        if (destroyed) return;
        cancel();
        destroyed = true;
        sessions.delete(session);
        await Promise.allSettled([doc.cleanup?.(), destroyLoadingTask()]);
      },
    };
    sessions.add(session);
    return session;
  }

  return {
    open,
    async destroy() {
      await Promise.allSettled([...sessions].map((session) => session.destroy()));
    },
  };
}
