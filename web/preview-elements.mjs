const dependencyURL = new URL("./artifact.mjs", import.meta.url);
const dependencyRetry = new URL(import.meta.url).searchParams.get("preview-retry");
if (dependencyRetry) dependencyURL.searchParams.set("preview-retry", dependencyRetry);
const { ArtifactURL } = await import(dependencyURL.href);

const OFFICE_KINDS = new Set(["docx", "hwpx", "hwp", "xlsx", "office"]);
const DEFAULT_TEXT_LIMIT = 200_000;
export const DEFAULT_RICH_PREVIEW_BYTES = 128 * 1024 * 1024;
export const DEFAULT_IMAGE_PREVIEW_PIXELS = 16_000_000;
export const DEFAULT_PREVIEW_DIMENSION = 16_384;
const DEFAULT_SUMMARY_LABELS = Object.freeze({
  encryptedPDF: "Encrypted PDF",
  richUnavailable: "Rich preview unavailable",
  zipArchive: "ZIP archive",
  entryCountUnavailable: "entry count unavailable",
  officeDocument: "Office document",
  officeStructure: "document structure is preserved in the download",
  entry: "entry",
  entries: "entries",
});

function abortError() {
  return new DOMException("Preview aborted", "AbortError");
}

function clearCanvas(canvas) {
  canvas.width = 0;
  canvas.height = 0;
}

export function classifyPreview(name, mime) {
  const type = String(mime || "").toLowerCase();
  const lower = String(name || "").toLowerCase();
  if (type === "application/pdf" || lower.endsWith(".pdf")) return "pdf";
  if (type.startsWith("image/") || /\.(png|jpe?g|gif|webp|avif)$/.test(lower)) return "image";
  if (type.startsWith("text/") || /\.(txt|csv|json|md)$/.test(lower)) return "text";
  return "summary";
}

export function previewModeFor(operation, artifact) {
  if (operation?.capabilities?.preview !== true) return "summary";
  const kind = String(artifact?.kind || "").toLowerCase();
  const mime = String(artifact?.mime || artifact?.blob?.type || "").toLowerCase();
  const name = String(artifact?.name || "").toLowerCase();
  if (artifact?.metadata?.encrypted) return "summary";
  if (kind === "zip" || mime.includes("zip") || name.endsWith(".zip")) return "summary";
  if (OFFICE_KINDS.has(kind) || mime.includes("officedocument") || /\.(docx|hwpx|hwp|xlsx)$/.test(name)) return "summary";
  return classifyPreview(name, mime);
}

export function previewModesForArtifacts(operation, artifacts, options = {}) {
  const byteLimit = Number(options.byteLimit ?? DEFAULT_RICH_PREVIEW_BYTES);
  if (!Number.isFinite(byteLimit) || byteLimit < 0) throw new RangeError("invalid rich preview byte limit");
  const totalBytes = artifacts.reduce((total, artifact) => total + Number(artifact?.size ?? artifact?.blob?.size ?? 0), 0);
  if (!Number.isSafeInteger(totalBytes) || totalBytes < 0 || totalBytes > byteLimit) {
    return artifacts.map(() => "summary");
  }
  return artifacts.map((artifact) => previewModeFor(operation, artifact));
}

export function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) throw new RangeError("invalid byte count");
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
}

function artifactType(artifact) {
  return String(artifact?.mime || artifact?.blob?.type || "application/octet-stream").toLowerCase();
}

function isZipArtifact(artifact) {
  return String(artifact?.kind || "").toLowerCase() === "zip"
    || artifactType(artifact).includes("zip")
    || String(artifact?.name || "").toLowerCase().endsWith(".zip");
}

function isOfficeArtifact(artifact) {
  const kind = String(artifact?.kind || "").toLowerCase();
  const name = String(artifact?.name || "").toLowerCase();
  return OFFICE_KINDS.has(kind) || artifactType(artifact).includes("officedocument") || /\.(docx|hwpx|hwp|xlsx)$/.test(name);
}

async function zipEntryCount(blob) {
  const maximumTail = 65_535 + 22;
  const offset = Math.max(0, blob.size - maximumTail);
  const bytes = new Uint8Array(await blob.slice(offset).arrayBuffer());
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  for (let index = bytes.length - 22; index >= 0; index--) {
    if (view.getUint32(index, true) !== 0x06054b50) continue;
    const commentLength = view.getUint16(index + 20, true);
    if (index + 22 + commentLength !== bytes.length) continue;
    const disk = view.getUint16(index + 4, true);
    const centralDisk = view.getUint16(index + 6, true);
    const diskEntries = view.getUint16(index + 8, true);
    const totalEntries = view.getUint16(index + 10, true);
    if (disk !== 0 || centralDisk !== 0 || diskEntries !== totalEntries || totalEntries === 0xffff) return null;
    return totalEntries;
  }
  return null;
}

async function summaryText(artifact, providedLabels = {}) {
  const labels = { ...DEFAULT_SUMMARY_LABELS, ...providedLabels };
  const details = [];
  if (artifact?.metadata?.encrypted) {
    details.push(labels.encryptedPDF, labels.richUnavailable);
  } else if (isOfficeArtifact(artifact)) {
    details.push(labels.officeDocument, `${labels.richUnavailable}; ${labels.officeStructure}`);
  } else if (isZipArtifact(artifact)) {
    const entries = await zipEntryCount(artifact.blob).catch(() => null);
    details.push(labels.zipArchive, entries == null ? labels.entryCountUnavailable : `${entries} ${entries === 1 ? labels.entry : labels.entries}`);
  } else {
    details.push(labels.richUnavailable);
  }
  return `${artifact.name} · ${formatBytes(artifact.size ?? artifact.blob?.size ?? 0)} · ${artifact.mime || artifact.blob?.type || "application/octet-stream"} · ${details.join(" · ")}`;
}

function assertPixelBudget(width, height, maxPixels, maxDimension, label) {
  if (!Number.isSafeInteger(width) || width < 1 || !Number.isSafeInteger(height) || height < 1) {
    throw new RangeError(`${label} has invalid dimensions`);
  }
  if (width > maxDimension || height > maxDimension || width > Math.floor(maxPixels / height)) {
    throw new RangeError(`${label} exceeds the preview budget`);
  }
}

export async function createPreviewSurface(artifact, options = {}) {
  if (!(artifact?.blob instanceof Blob)) throw new TypeError("preview surface requires an Artifact");
  const document = options.document || globalThis.document;
  const mode = options.mode || classifyPreview(artifact.name, artifact.mime || artifact.blob.type);
  const signal = options.signal;
  if (signal?.aborted) throw abortError();
  let disposed = false;

  if (mode === "summary") {
    const element = document.createElement("p");
    element.className = "result-preview-summary";
    element.textContent = await summaryText(artifact, options.summaryLabels);
    return { element, kind: "summary", numPages: 0, async dispose() {} };
  }

  if (mode === "text") {
    const limit = Math.max(1, Number(options.textLimit ?? DEFAULT_TEXT_LIMIT));
    const text = await artifact.blob.slice(0, limit).text();
    if (signal?.aborted) throw abortError();
    const element = document.createElement("pre");
    element.className = "result-preview-text";
    element.textContent = artifact.blob.size > limit ? `${text}\n…` : text;
    return { element, kind: "text", numPages: 0, async dispose() {} };
  }

  if (mode === "image") {
    const makeBitmap = options.createImageBitmap || globalThis.createImageBitmap;
    if (typeof makeBitmap !== "function") throw new Error("ImageBitmap preview is unavailable");
    const canvas = document.createElement("canvas");
    canvas.className = "result-preview-canvas";
    let bitmap;
    try {
      bitmap = await makeBitmap(artifact.blob);
      if (signal?.aborted) throw abortError();
      assertPixelBudget(
        bitmap.width,
        bitmap.height,
        Number(options.imageMaxPixels ?? DEFAULT_IMAGE_PREVIEW_PIXELS),
        Number(options.imageMaxDimension ?? DEFAULT_PREVIEW_DIMENSION),
        "image",
      );
      canvas.width = bitmap.width;
      canvas.height = bitmap.height;
      const context = canvas.getContext("2d");
      if (!context) throw new Error("Canvas 2D context is unavailable");
      context.drawImage(bitmap, 0, 0);
    } catch (error) {
      bitmap?.close?.();
      clearCanvas(canvas);
      throw error;
    }
    return {
      element: canvas,
      kind: "image",
      numPages: 0,
      async dispose() {
        if (disposed) return;
        disposed = true;
        bitmap.close?.();
        clearCanvas(canvas);
      },
    };
  }

  if (mode !== "pdf") throw new Error(`unsupported preview mode: ${mode}`);
  const renderer = options.pdfRenderer;
  const urlAPI = options.urlAPI || globalThis.URL;
  if (!renderer?.open) throw new Error("PDF renderer is unavailable");
  if (!urlAPI?.createObjectURL || !urlAPI?.revokeObjectURL) throw new Error("PDF object URL is unavailable");
  const canvas = document.createElement("canvas");
  canvas.className = "result-preview-canvas";
  const resource = new ArtifactURL(artifact.blob, urlAPI);
  let session;
  try {
    session = await renderer.open({ url: resource.url }, { signal });
    if (signal?.aborted) throw abortError();
  } catch (error) {
    await session?.destroy?.();
    resource.dispose();
    clearCanvas(canvas);
    throw error;
  }
  return {
    element: canvas,
    kind: "pdf",
    numPages: session.numPages,
    async renderPage(page) {
      if (disposed) throw new Error("PDF preview is disposed");
      session.cancel();
      return session.renderPage(page, {
        canvas,
        pixelRatio: options.pixelRatio || globalThis.devicePixelRatio || 1,
        width: options.width || canvas.parentElement?.clientWidth || 640,
        maxPixels: options.pdfMaxPixels ?? DEFAULT_IMAGE_PREVIEW_PIXELS,
        maxDimension: options.pdfMaxDimension ?? DEFAULT_PREVIEW_DIMENSION,
        cleanupPage: true,
        signal,
      });
    },
    async dispose() {
      if (disposed) return;
      disposed = true;
      session.cancel();
      resource.dispose();
      clearCanvas(canvas);
      await session.destroy();
    },
  };
}

export function createPageSynchronizer(surfaces) {
  const pages = surfaces.filter((surface) => typeof surface?.renderPage === "function" && surface.numPages > 0);
  const total = Math.max(1, ...pages.map((surface) => surface.numPages));
  let page = 1;
  return {
    get page() { return page; },
    total,
    async setPage(requested) {
      const numeric = Number(requested);
      const nextPage = Math.min(total, Math.max(1, Number.isFinite(numeric) ? Math.round(numeric) : 1));
      await Promise.all(pages.map((surface) => surface.renderPage(Math.min(nextPage, surface.numPages))));
      page = nextPage;
      return page;
    },
  };
}

let comparisonSequence = 0;

function appendHeading(document, pane, label) {
  const heading = document.createElement("h3");
  heading.textContent = label;
  pane.appendChild(heading);
}

export async function createPreviewComparison(options) {
  if (options.signal?.aborted) throw abortError();
  const document = options.document || globalThis.document;
  const labels = {
    title: "Review result",
    before: "Before",
    after: "After",
    previous: "Previous page",
    next: "Next page",
    page: "Page",
    stale: "Inputs or settings changed. Run the operation again to refresh this result.",
    renderError: "Page preview failed:",
    download: "Download result",
    close: "Close",
    ...(options.labels || {}),
  };
  labels.summary = { ...DEFAULT_SUMMARY_LABELS, ...(options.labels?.summary || {}) };
  const artifacts = [options.before || null, options.after].filter(Boolean);
  if (!options.after?.blob) throw new TypeError("comparison output Artifact is required");
  const modes = previewModesForArtifacts(options.operation, artifacts, {
    byteLimit: options.richPreviewByteLimit,
  });
  const controller = new AbortController();
  const forwardAbort = () => controller.abort();
  options.signal?.addEventListener("abort", forwardAbort, { once: true });
  const surfaceOptions = {
    document,
    pdfRenderer: options.pdfRenderer,
    createImageBitmap: options.createImageBitmap,
    urlAPI: options.urlAPI,
    signal: controller.signal,
    textLimit: options.textLimit,
    imageMaxPixels: options.imageMaxPixels,
    imageMaxDimension: options.imageMaxDimension,
    pdfMaxPixels: options.pdfMaxPixels,
    pdfMaxDimension: options.pdfMaxDimension,
    summaryLabels: labels.summary,
  };
  const surfaces = [];
  try {
    for (let index = 0; index < artifacts.length; index++) {
      const artifact = artifacts[index];
      const mode = modes[index];
      try {
        surfaces.push(await createPreviewSurface(artifact, { ...surfaceOptions, mode }));
      } catch (error) {
        if (error?.name === "AbortError") throw error;
        surfaces.push(await createPreviewSurface(artifact, { ...surfaceOptions, mode: "summary" }));
      }
    }
  } catch (error) {
    controller.abort();
    await Promise.allSettled(surfaces.map((surface) => surface.dispose()));
    options.signal?.removeEventListener("abort", forwardAbort);
    throw error;
  }

  const id = `result-preview-${++comparisonSequence}`;
  const section = document.createElement("section");
  section.className = "result-preview";
  section.dataset.stale = "false";
  section.dataset.mobileActive = "after";
  section.setAttribute("aria-labelledby", `${id}-title`);

  const title = document.createElement("h2");
  title.id = `${id}-title`;
  title.textContent = labels.title;
  const size = document.createElement("p");
  size.className = "result-preview-size";
  size.textContent = options.before
    ? `${formatBytes(options.before.size)} → ${formatBytes(options.after.size)}`
    : formatBytes(options.after.size);
  const stale = document.createElement("p");
  stale.className = "result-preview-stale";
  stale.setAttribute("role", "status");
  stale.setAttribute("aria-live", "polite");
  stale.textContent = labels.stale;
  stale.hidden = true;
  const renderError = document.createElement("p");
  renderError.className = "result-preview-render-error";
  renderError.setAttribute("role", "alert");
  renderError.setAttribute("aria-live", "assertive");
  renderError.hidden = true;
  section.append(title, size, stale, renderError);

  const panes = document.createElement("div");
  panes.className = "result-preview-panes";
  const paneEntries = [];
  let surfaceIndex = 0;
  if (options.before) {
    const pane = document.createElement("article");
    pane.id = `${id}-before-pane`;
    pane.dataset.previewPane = "before";
    pane.setAttribute("role", "tabpanel");
    appendHeading(document, pane, labels.before);
    pane.appendChild(surfaces[surfaceIndex++].element);
    paneEntries.push(["before", pane]);
    panes.appendChild(pane);
  }
  const afterPane = document.createElement("article");
  afterPane.id = `${id}-after-pane`;
  afterPane.dataset.previewPane = "after";
  afterPane.setAttribute("role", "tabpanel");
  appendHeading(document, afterPane, labels.after);
  afterPane.appendChild(surfaces[surfaceIndex].element);
  paneEntries.push(["after", afterPane]);
  panes.appendChild(afterPane);

  const tabButtons = new Map();
  const setActivePane = (name) => {
    section.dataset.mobileActive = name;
    for (const [key, button] of tabButtons) {
      const selected = key === name;
      button.setAttribute("aria-selected", String(selected));
      button.tabIndex = selected ? 0 : -1;
    }
  };
  if (options.before) {
    const tabs = document.createElement("div");
    tabs.className = "result-preview-tabs";
    tabs.setAttribute("role", "tablist");
    for (const [name, pane] of paneEntries) {
      const button = document.createElement("button");
      button.type = "button";
      button.id = `${id}-${name}-tab`;
      button.setAttribute("role", "tab");
      button.setAttribute("aria-controls", pane.id);
      pane.setAttribute("aria-labelledby", button.id);
      button.textContent = name === "before" ? labels.before : labels.after;
      button.addEventListener("click", () => setActivePane(name));
      tabButtons.set(name, button);
      tabs.appendChild(button);
    }
    const tabOrder = paneEntries.map(([name]) => name);
    tabs.addEventListener("keydown", (event) => {
      const current = tabOrder.indexOf(section.dataset.mobileActive);
      let nextIndex = current;
      if (event.key === "ArrowLeft") nextIndex = (current - 1 + tabOrder.length) % tabOrder.length;
      else if (event.key === "ArrowRight") nextIndex = (current + 1) % tabOrder.length;
      else if (event.key === "Home") nextIndex = 0;
      else if (event.key === "End") nextIndex = tabOrder.length - 1;
      else return;
      event.preventDefault();
      const nextName = tabOrder[nextIndex];
      setActivePane(nextName);
      tabButtons.get(nextName)?.focus();
    });
    setActivePane("after");
    section.appendChild(tabs);
  }

  const synchronizer = createPageSynchronizer(surfaces);
  let pageInput = null;
  let previous = null;
  let next = null;
  let renderGeneration = 0;
  const renderPage = async (requested, propagateError = false) => {
    const generation = ++renderGeneration;
    if (previous) previous.disabled = true;
    if (next) next.disabled = true;
    try {
      await synchronizer.setPage(requested);
      if (generation !== renderGeneration) return;
      if (pageInput) pageInput.value = String(synchronizer.page);
      renderError.hidden = true;
      renderError.textContent = "";
    } catch (error) {
      if (generation === renderGeneration && error?.name !== "AbortError") {
        renderError.textContent = `${labels.renderError} ${error?.message || error}`;
        renderError.hidden = false;
        if (propagateError) throw error;
      }
    } finally {
      if (generation === renderGeneration) {
        if (previous) previous.disabled = synchronizer.page <= 1;
        if (next) next.disabled = synchronizer.page >= synchronizer.total;
      }
    }
  };
  if (surfaces.some((surface) => surface.kind === "pdf")) {
    const controls = document.createElement("div");
    controls.className = "result-preview-page-controls";
    previous = document.createElement("button");
    previous.type = "button";
    previous.textContent = "‹";
    previous.setAttribute("aria-label", labels.previous);
    const pageLabel = document.createElement("label");
    pageLabel.textContent = labels.page;
    pageInput = document.createElement("input");
    pageInput.type = "number";
    pageInput.min = "1";
    pageInput.max = String(synchronizer.total);
    pageInput.value = "1";
    pageInput.setAttribute("aria-label", labels.page);
    const total = document.createElement("span");
    total.textContent = `/ ${synchronizer.total}`;
    next = document.createElement("button");
    next.type = "button";
    next.textContent = "›";
    next.setAttribute("aria-label", labels.next);
    previous.addEventListener("click", () => { void renderPage(synchronizer.page - 1); });
    next.addEventListener("click", () => { void renderPage(synchronizer.page + 1); });
    pageInput.addEventListener("change", () => { void renderPage(pageInput.value); });
    pageLabel.appendChild(pageInput);
    controls.append(previous, pageLabel, total, next);
    section.appendChild(controls);
  }
  section.appendChild(panes);

  const actions = document.createElement("div");
  actions.className = "result-preview-actions";
  const download = document.createElement("button");
  download.type = "button";
  download.className = "primary";
  download.textContent = labels.download;
  download.addEventListener("click", () => options.onDownload?.(options.after));
  const close = document.createElement("button");
  close.type = "button";
  close.textContent = labels.close;
  actions.append(download, close);
  section.appendChild(actions);

  let disposePromise = null;
  const dispose = () => {
    if (disposePromise) return disposePromise;
    controller.abort();
    renderGeneration++;
    section.remove();
    options.signal?.removeEventListener("abort", forwardAbort);
    disposePromise = Promise.allSettled(surfaces.map((surface) => surface.dispose()));
    return disposePromise;
  };
  close.addEventListener("click", () => {
    void dispose();
    options.onClose?.();
  });

  if (surfaces.some((surface) => surface.kind === "pdf")) {
    try {
      await renderPage(1, true);
    } catch (error) {
      await dispose();
      throw error;
    }
  }

  return {
    element: section,
    outputSurface: surfaces.at(-1),
    surfaces,
    setStale(value) {
      const isStale = Boolean(value);
      section.dataset.stale = String(isStale);
      stale.hidden = !isStale;
      download.disabled = isStale;
    },
    dispose,
  };
}
