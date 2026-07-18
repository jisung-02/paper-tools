import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";
import { REDACT_LIMITS, streamRedactedPDF, validateRedactSource } from "./exporter.mjs";
import { normalizeSelection, selectionPixels } from "./geometry.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";

const fileDrop = window.dropzone("redactDrop", { multiple: false });
const editor = document.getElementById("redactEditor");
const pageCanvas = document.getElementById("redactPageCanvas");
const overlay = document.getElementById("redactOverlay");
const status = document.getElementById("status");
const error = document.getElementById("err");
const count = document.getElementById("redactCount");
const runButton = document.getElementById("redactRun");
const cancelButton = document.getElementById("redactCancel");
const verified = document.getElementById("redactVerified");

let loadingTask = null;
let sourceDoc = null;
let sourceURL = null;
let pageNumber = 1;
let previewRenderTask = null;
let previewGeneration = 0;
let loadGeneration = 0;
let activeExportController = null;
let startPoint = null;
const selections = new Map();

function currentSelections() {
  if (!selections.has(pageNumber)) selections.set(pageNumber, []);
  return selections.get(pageNumber);
}

function totalSelections() {
  let total = 0;
  for (const values of selections.values()) total += values.length;
  return total;
}

function clearPreviewCanvases() {
  pageCanvas.width = 0;
  pageCanvas.height = 0;
  overlay.width = 0;
  overlay.height = 0;
}

function terminateRedactionWorker() {
  // cancel handles an in-flight command; dispose also terminates an idle
  // persistent worker. The same client lazily creates a fresh worker next run.
  window.__wasmClient?.cancel();
  window.__wasmClient?.dispose();
}

async function cleanupSource({ cancelExport = false, hideEditor = true } = {}) {
  if (cancelExport && activeExportController) activeExportController.abort();
  previewGeneration++;
  try {
    previewRenderTask?.cancel();
  } catch {}
  previewRenderTask = null;
  const task = loadingTask;
  loadingTask = null;
  sourceDoc = null;
  if (task) {
    try {
      await task.destroy();
    } catch {}
  }
  if (sourceURL) {
    URL.revokeObjectURL(sourceURL);
    sourceURL = null;
  }
  clearPreviewCanvases();
  if (hideEditor) editor.hidden = true;
}

function drawSelections() {
  overlay.width = pageCanvas.width;
  overlay.height = pageCanvas.height;
  const context = overlay.getContext("2d");
  context.clearRect(0, 0, overlay.width, overlay.height);
  context.fillStyle = "rgba(0,0,0,.78)";
  for (const selection of currentSelections()) {
    const rect = selectionPixels(selection, overlay.width, overlay.height);
    context.fillRect(rect.x, rect.y, rect.width, rect.height);
  }
  count.textContent = totalSelections() + window.t(" redaction area(s)", "개 삭제 영역");
}

async function renderCurrent() {
  if (!sourceDoc) return;
  const generation = ++previewGeneration;
  try {
    previewRenderTask?.cancel();
  } catch {}
  const activeDoc = sourceDoc;
  const activePageNumber = pageNumber;
  const page = await activeDoc.getPage(activePageNumber);
  try {
    const base = page.getViewport({ scale: 1 });
    const area = base.width * base.height;
    const pixelScale = Number.isFinite(area) && area > 0
      ? Math.sqrt(REDACT_LIMITS.maxPagePixels / area)
      : 0;
    const scale = Math.min(1.5, 1000 / base.width, pixelScale);
    if (!Number.isFinite(scale) || scale <= 0) throw new Error(window.t("Invalid PDF page geometry", "PDF 페이지 크기 정보가 올바르지 않습니다."));
    const viewport = page.getViewport({ scale });
    pageCanvas.width = Math.ceil(viewport.width);
    pageCanvas.height = Math.ceil(viewport.height);
    const context = pageCanvas.getContext("2d", { alpha: false });
    context.fillStyle = "#fff";
    context.fillRect(0, 0, pageCanvas.width, pageCanvas.height);
    const current = page.render({ canvasContext: context, viewport });
    previewRenderTask = current;
    await current.promise;
    if (generation !== previewGeneration || activeDoc !== sourceDoc) return;
    document.getElementById("redactPage").textContent = `${activePageNumber}/${activeDoc.numPages}`;
    document.getElementById("redactPrev").disabled = activePageNumber === 1;
    document.getElementById("redactNext").disabled = activePageNumber === activeDoc.numPages;
    drawSelections();
  } catch (cause) {
    if (generation === previewGeneration && cause?.name !== "RenderingCancelledException" && cause?.name !== "AbortError") {
      throw cause;
    }
  } finally {
    if (generation === previewGeneration) previewRenderTask = null;
    try {
      page.cleanup();
    } catch {}
  }
}

async function loadPDF(file) {
  validateRedactSource(file);
  const generation = ++loadGeneration;
  await cleanupSource({ cancelExport: true });
  selections.clear();
  pageNumber = 1;
  verified.hidden = true;
  error.textContent = "";
  const url = URL.createObjectURL(file);
  sourceURL = url;
  const task = pdfjsLib.getDocument({
    url,
    cMapUrl: "/vendor/pdfjs/cmaps/",
    cMapPacked: true,
    standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
  });
  loadingTask = task;
  try {
    const loaded = await task.promise;
    if (generation !== loadGeneration || task !== loadingTask) {
      await task.destroy();
      return;
    }
    if (loaded.numPages > REDACT_LIMITS.maxPages) {
      throw new Error(`${window.t("PDF page count exceeds the", "PDF 페이지 수가")} ${REDACT_LIMITS.maxPages}${window.t("-page limit", "페이지 제한을 초과했습니다")}`);
    }
    sourceDoc = loaded;
    editor.hidden = false;
    await renderCurrent();
  } catch (cause) {
    if (task === loadingTask) await cleanupSource();
    throw cause;
  }
}

document.getElementById("redactDrop").addEventListener("dz:files", ({ detail }) => {
  if (detail.files[0]) loadPDF(detail.files[0]).catch((cause) => window.showErr(error, cause.message || cause));
});

function pointer(event) {
  const rect = overlay.getBoundingClientRect();
  return {
    x: (event.clientX - rect.left) * overlay.width / rect.width,
    y: (event.clientY - rect.top) * overlay.height / rect.height,
  };
}

overlay.addEventListener("pointerdown", (event) => {
  startPoint = pointer(event);
  overlay.setPointerCapture(event.pointerId);
});
overlay.addEventListener("pointerup", (event) => {
  if (!startPoint) return;
  try {
    const end = pointer(event);
    currentSelections().push(normalizeSelection(
      startPoint.x,
      startPoint.y,
      end.x,
      end.y,
      overlay.width,
      overlay.height,
    ));
    drawSelections();
  } catch {}
  startPoint = null;
});

document.getElementById("redactPrev").addEventListener("click", () => {
  if (pageNumber > 1) {
    pageNumber--;
    renderCurrent().catch((cause) => window.showErr(error, cause.message || cause));
  }
});
document.getElementById("redactNext").addEventListener("click", () => {
  if (sourceDoc && pageNumber < sourceDoc.numPages) {
    pageNumber++;
    renderCurrent().catch((cause) => window.showErr(error, cause.message || cause));
  }
});
document.getElementById("redactClear").addEventListener("click", () => {
  selections.set(pageNumber, []);
  drawSelections();
});
document.getElementById("redactClearAll").addEventListener("click", () => {
  selections.clear();
  drawSelections();
});

function createBrowserCanvas(width, height) {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d", { alpha: false });
  if (!context) throw new Error(window.t("Canvas 2D is unavailable", "캔버스(2D)를 사용할 수 없습니다."));
  return {
    canvas,
    context,
    dispose() {
      canvas.width = 0;
      canvas.height = 0;
    },
  };
}

function encodePNG(canvas) {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => blob ? resolve(blob) : reject(new Error(window.t("PNG export failed", "PNG 내보내기에 실패했습니다."))),
      "image/png",
    );
  });
}

cancelButton.addEventListener("click", () => {
  try {
    previewRenderTask?.cancel();
  } catch {}
  activeExportController?.abort();
  terminateRedactionWorker();
});

runButton.addEventListener("click", () => window.run(runButton, async () => {
  if (!sourceDoc || !totalSelections()) {
    window.showErr(error, window.t("Select at least one area to redact.", "삭제할 영역을 하나 이상 선택하세요."));
    return;
  }
  if (!document.getElementById("redactConfirm").checked) {
    window.showErr(error, window.t("Confirm the raster-only output warning.", "이미지 전용 출력 경고에 동의해 주세요."));
    return;
  }
  const activeDoc = sourceDoc;
  const controller = new AbortController();
  activeExportController = controller;
  cancelButton.disabled = false;
  verified.hidden = true;
  clearPreviewCanvases();
  let succeeded = false;
  try {
    const result = await streamRedactedPDF({
      doc: activeDoc,
      selections,
      invoke: async (request) => await window.runWasm(request),
      terminateWorker: terminateRedactionWorker,
      createCanvas: createBrowserCanvas,
      encodePNG,
      signal: controller.signal,
      onProgress({ phase, page, pages }) {
        status.hidden = false;
        status.textContent = phase === "finishing"
          ? window.t("Verifying raster-only PDF…", "이미지 전용 PDF 검증 중…")
          : `${window.t("Rasterizing", "래스터화 중")} ${page}/${pages}…`;
      },
    });
    window.finish(result, "redacted.pdf", error, "application/pdf");
    succeeded = true;
  } catch (cause) {
    if (cause?.name === "AbortError") {
      status.hidden = false;
      status.textContent = window.t("Redaction cancelled.", "삭제 작업이 취소되었습니다.");
      return;
    }
    throw cause;
  } finally {
    if (activeExportController === controller) activeExportController = null;
    cancelButton.disabled = true;
    await cleanupSource({ hideEditor: !succeeded });
  }
  verified.hidden = false;
}));

window.addEventListener("pagehide", () => {
  loadGeneration++;
  activeExportController?.abort();
  terminateRedactionWorker();
  void cleanupSource({ cancelExport: true });
}, { once: true });

void fileDrop;
