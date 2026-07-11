import { selectionPixels } from "./geometry.mjs";

export const REDACT_LIMITS = Object.freeze({
  maxInputBytes: 256 * 1024 * 1024,
  maxPages: 500,
  maxPagePixels: 16 * 1024 * 1024,
  maxTotalPixels: 64 * 1024 * 1024,
  maxPagePNGBytes: 64 * 1024 * 1024,
  maxTotalPNGBytes: 256 * 1024 * 1024,
  maxOutputBytes: 256 * 1024 * 1024,
  exportScale: 2,
});

function resolvedLimits(overrides) {
  const limits = { ...REDACT_LIMITS, ...(overrides || {}) };
  for (const key of [
    "maxInputBytes",
    "maxPages",
    "maxPagePixels",
    "maxTotalPixels",
    "maxPagePNGBytes",
    "maxTotalPNGBytes",
    "maxOutputBytes",
  ]) {
    if (!Number.isSafeInteger(limits[key]) || limits[key] <= 0) {
      throw new Error(`Invalid redaction limit: ${key}`);
    }
  }
  if (!Number.isFinite(limits.exportScale) || limits.exportScale <= 0) {
    throw new Error("Invalid redaction limit: exportScale");
  }
  return limits;
}

function abortError() {
  if (typeof DOMException === "function") return new DOMException("Aborted", "AbortError");
  const error = new Error("Aborted");
  error.name = "AbortError";
  return error;
}

function checkedPixels(width, height) {
  if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width <= 0 || height <= 0) {
    throw new Error("Invalid page pixel dimensions");
  }
  const pixels = width * height;
  if (!Number.isSafeInteger(pixels)) throw new Error("Page pixel count overflow");
  return pixels;
}

function viewportDimensions(viewport, label) {
  if (!viewport || !Number.isFinite(viewport.width) || !Number.isFinite(viewport.height) ||
      viewport.width <= 0 || viewport.height <= 0) {
    throw new Error(`Invalid ${label} page geometry`);
  }
  return viewport;
}

function responseData(response) {
  const data = response?.data;
  if (!(data instanceof Uint8Array)) throw new Error("Redaction encoder returned no PDF output");
  return data;
}

export function validateRedactSource(file, overrides) {
  const limits = resolvedLimits(overrides);
  if (!file || !Number.isSafeInteger(file.size) || file.size < 0) {
    throw new Error("Invalid input PDF size");
  }
  if (file.size > limits.maxInputBytes) {
    throw new Error(`Input PDF exceeds the ${limits.maxInputBytes}-byte limit`);
  }
  return file;
}

export async function streamRedactedPDF({
  doc,
  selections,
  invoke,
  terminateWorker = () => {},
  createCanvas,
  encodePNG,
  limits: limitOverrides,
  signal = null,
  onProgress = () => {},
}) {
  const limits = resolvedLimits(limitOverrides);
  if (!doc || !Number.isSafeInteger(doc.numPages) || doc.numPages <= 0) {
    throw new Error("Invalid PDF page count");
  }
  if (doc.numPages > limits.maxPages) {
    throw new Error(`PDF page count exceeds the ${limits.maxPages}-page limit`);
  }
  if (typeof invoke !== "function" || typeof createCanvas !== "function" || typeof encodePNG !== "function") {
    throw new Error("Redaction exporter dependencies are missing");
  }

  let currentRenderTask = null;
  let workerTerminated = false;
  let sessionOpen = false;
  let totalPixels = 0;
  let totalPNGBytes = 0;

  const stopWorker = () => {
    if (workerTerminated) return;
    workerTerminated = true;
    terminateWorker();
  };
  const onAbort = () => {
    try {
      currentRenderTask?.cancel();
    } catch {}
    stopWorker();
  };
  const throwIfAborted = () => {
    if (signal?.aborted) throw abortError();
  };
  if (signal) {
    if (signal.aborted) onAbort();
    signal.addEventListener("abort", onAbort, { once: true });
  }

  const call = async (request) => {
    throwIfAborted();
    const response = await invoke(request);
    throwIfAborted();
    if (!response || typeof response !== "object") throw new Error("Redaction encoder returned an invalid response");
    if (response.error) throw new Error(String(response.error));
    return response;
  };

  const addPage = async (number) => {
    let page = null;
    let resource = null;
    let blob = null;
    let pngData = null;
    let request = null;
    let pageRenderTask = null;
    try {
      throwIfAborted();
      page = await doc.getPage(number);
      throwIfAborted();
      const base = viewportDimensions(page.getViewport({ scale: 1 }), "display");
      const viewport = viewportDimensions(page.getViewport({ scale: limits.exportScale }), "raster");
      const width = Math.ceil(viewport.width);
      const height = Math.ceil(viewport.height);
      const pixels = checkedPixels(width, height);
      if (pixels > limits.maxPagePixels) {
        throw new Error(`Page ${number} pixel count exceeds the page pixel limit`);
      }
      if (totalPixels > limits.maxTotalPixels - pixels) {
        throw new Error("PDF pixel count exceeds the total pixel limit");
      }

      resource = createCanvas(width, height);
      if (!resource?.canvas || !resource?.context || typeof resource.dispose !== "function") {
        throw new Error("Canvas factory returned an invalid resource");
      }
      resource.context.fillStyle = "#fff";
      resource.context.fillRect(0, 0, width, height);
      pageRenderTask = page.render({ canvasContext: resource.context, viewport });
      if (!pageRenderTask || !pageRenderTask.promise || typeof pageRenderTask.cancel !== "function") {
        throw new Error("PDF.js returned an invalid render task");
      }
      currentRenderTask = pageRenderTask;
      await pageRenderTask.promise;
      if (currentRenderTask === pageRenderTask) currentRenderTask = null;
      throwIfAborted();

      resource.context.fillStyle = "#000";
      const pageSelections = selections?.get?.(number) || [];
      for (const selection of pageSelections) {
        const rect = selectionPixels(selection, width, height, 2);
        resource.context.fillRect(rect.x, rect.y, rect.width, rect.height);
      }

      blob = await encodePNG(resource.canvas);
      throwIfAborted();
      if (!blob || !Number.isSafeInteger(blob.size) || blob.size < 0 || typeof blob.arrayBuffer !== "function") {
        throw new Error("PNG encoder returned an invalid Blob");
      }
      if (blob.size > limits.maxPagePNGBytes) {
        throw new Error(`Page ${number} PNG exceeds the page PNG limit`);
      }
      if (totalPNGBytes > limits.maxTotalPNGBytes - blob.size) {
        throw new Error("PNG throughput exceeds the total PNG limit");
      }
      pngData = new Uint8Array(await blob.arrayBuffer());
      throwIfAborted();
      if (pngData.byteLength > limits.maxPagePNGBytes ||
          totalPNGBytes > limits.maxTotalPNGBytes - pngData.byteLength) {
        throw new Error("PNG bytes exceed the configured PNG limit");
      }
      request = {
        command: "add",
        page: { pngData, widthPt: base.width, heightPt: base.height },
      };
      await call(request);
      totalPixels += pixels;
      totalPNGBytes += pngData.byteLength;
    } catch (error) {
      if (signal?.aborted) throw abortError();
      throw error;
    } finally {
      if (request?.page) request.page.pngData = null;
      pngData = null;
      blob = null;
      if (currentRenderTask === pageRenderTask) currentRenderTask = null;
      try {
        resource?.dispose();
      } catch {}
      try {
        page?.cleanup?.();
      } catch {}
    }
  };

  try {
    throwIfAborted();
    await call({
      command: "start",
      pageCount: doc.numPages,
      opts: {
        maxPages: limits.maxPages,
        maxPagePixels: limits.maxPagePixels,
        maxPixels: limits.maxTotalPixels,
        maxPagePNGBytes: limits.maxPagePNGBytes,
        maxPNGBytes: limits.maxTotalPNGBytes,
        maxOutputBytes: limits.maxOutputBytes,
      },
    });
    sessionOpen = true;
    for (let number = 1; number <= doc.numPages; number++) {
      onProgress({ phase: "rasterizing", page: number, pages: doc.numPages });
      await addPage(number);
    }
    onProgress({ phase: "finishing", page: doc.numPages, pages: doc.numPages });
    const response = await call({ command: "finish" });
    sessionOpen = false;
    const data = responseData(response);
    if (data.byteLength > limits.maxOutputBytes) {
      throw new Error(`Redaction output exceeds the ${limits.maxOutputBytes}-byte output limit`);
    }
    return response;
  } catch (error) {
    if (signal?.aborted || error?.name === "AbortError") throw abortError();
    if (sessionOpen) {
      try {
        await invoke({ command: "abort" });
      } catch {}
      sessionOpen = false;
    }
    throw error;
  } finally {
    if (signal) signal.removeEventListener("abort", onAbort);
    currentRenderTask = null;
  }
}

