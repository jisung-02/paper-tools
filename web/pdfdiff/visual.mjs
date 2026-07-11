import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";
import { createPdfRenderer } from "../pdf-renderer.mjs";
import { zipStoreStream } from "../pdf2img/zip.mjs";
import { createOutputSink } from "../output-sinks.mjs";
import { DiffWorkerClient } from "./diff-client.mjs";
import { alignPages } from "./page-align.mjs";
import {
  assertCombinedInputBytes,
  assertCombinedPageCount,
  createByteLimitedSink,
  scaleForLivePixelBudget,
} from "./visual-budget.mjs";
import { visualReportEntries } from "./visual-report.mjs";
import { visualError, visualErrorMessage } from "./visual-errors.mjs";
import {
  changedPageNavigation,
  collectChangedPages,
  commitLatestClosedOutput,
  createDelayedCleanupRegistry,
  createLatestActionController,
  inputRevisionsMatch,
  snapshotInputRevisions,
} from "./visual-state.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";

const MAX_PAGES = 500;
const MAX_INPUT_BYTES = 256 * 1024 * 1024;
const MAX_ALIGNMENT_CELLS = 50_000;
const MAX_COMPARISON_PIXELS = 8 * 1024 * 1024;
const MAX_LIVE_PIXELS = 16 * 1024 * 1024;
const MAX_CANVAS_DIMENSION = 16_384;
const MAX_EXPORT_BYTES = 256 * 1024 * 1024;
const MAX_TEXT_ITEMS = 100_000;
const MAX_TEXT_CHARS = 1_048_576;
const MAX_TEXT_BYTES = 2_097_152;

function createCanvas() {
  return document.createElement("canvas");
}

function sourceHandle(source) {
  if (source instanceof Blob) {
    const url = URL.createObjectURL(source);
    return { source: { url }, dispose: () => URL.revokeObjectURL(url) };
  }
  return { source, dispose() {} };
}

function linkedSignal(...signals) {
  const controller = new AbortController();
  const listeners = [];
  for (const signal of signals.filter(Boolean)) {
    if (signal.aborted) {
      controller.abort(signal.reason);
      break;
    }
    const abort = () => controller.abort(signal.reason);
    signal.addEventListener("abort", abort, { once: true });
    listeners.push(() => signal.removeEventListener("abort", abort));
  }
  return {
    signal: controller.signal,
    dispose() { for (const remove of listeners) remove(); },
  };
}

function grayscaleFingerprint(imageData, cells = 8) {
  const out = new Uint8Array(cells * cells);
  for (let cy = 0; cy < cells; cy++) {
    for (let cx = 0; cx < cells; cx++) {
      const x0 = Math.floor(cx * imageData.width / cells);
      const x1 = Math.max(x0 + 1, Math.floor((cx + 1) * imageData.width / cells));
      const y0 = Math.floor(cy * imageData.height / cells);
      const y1 = Math.max(y0 + 1, Math.floor((cy + 1) * imageData.height / cells));
      let sum = 0;
      let count = 0;
      for (let y = y0; y < y1; y++) {
        for (let x = x0; x < x1; x++) {
          const index = (y * imageData.width + x) * 4;
          sum += imageData.data[index] * 0.299 + imageData.data[index + 1] * 0.587 + imageData.data[index + 2] * 0.114;
          count++;
        }
      }
      out[cy * cells + cx] = Math.round(sum / count);
    }
  }
  return out;
}

async function describePage(session, pageNumber, signal) {
  const info = await session.getPageInfo(pageNumber, {
    includeTextFingerprint: true,
    maxTextItems: MAX_TEXT_ITEMS,
    maxTextChars: MAX_TEXT_CHARS,
    maxTextBytes: MAX_TEXT_BYTES,
    cleanupPage: true,
    signal,
  });
  const scale = Math.min(0.2, 128 / Math.max(info.width, info.height));
  const rendered = await session.renderPage(pageNumber, {
    scale,
    output: "canvas",
    signal,
    cleanupPage: true,
    maxPixels: 256 * 256,
    maxDimension: 256,
  });
  try {
    const context = rendered.canvas.getContext("2d", { willReadFrequently: true });
    if (!context) throw visualError("canvas-unavailable", "Canvas 2D context is unavailable");
    const image = context.getImageData(0, 0, rendered.width, rendered.height);
    return {
      fingerprint: grayscaleFingerprint(image),
      textFingerprint: info.textFingerprint,
      width: info.width,
      height: info.height,
    };
  } finally {
    rendered.dispose();
  }
}

function pageSizeChanged(left, right) {
  if (!left || !right) return true;
  return Math.abs(left.width - right.width) > 0.01 || Math.abs(left.height - right.height) > 0.01;
}

function rgba(rendered) {
  if (!rendered) return { data: new Uint8ClampedArray(0), width: 0, height: 0 };
  const context = rendered.canvas.getContext("2d", { willReadFrequently: true });
  if (!context) throw visualError("canvas-unavailable", "Canvas 2D context is unavailable");
  const image = context.getImageData(0, 0, rendered.width, rendered.height);
  return { data: image.data, width: image.width, height: image.height };
}

function disposeRendered(rendered) {
  rendered?.dispose?.();
}

export async function createVisualDiffSession(sourceA, sourceB, options = {}) {
  assertCombinedInputBytes([sourceA, sourceB], MAX_INPUT_BYTES);
  const leftSource = sourceHandle(sourceA);
  let rightSource;
  try {
    rightSource = sourceHandle(sourceB);
  } catch (error) {
    leftSource.dispose();
    throw error;
  }
  const controller = new AbortController();
  let renderer;
  let diffClient;
  let left;
  let right;
  let closed = false;
  let closePromise;
  const abortFromCaller = () => controller.abort(options.signal?.reason);
  if (options.signal?.aborted) controller.abort(options.signal.reason);
  else options.signal?.addEventListener("abort", abortFromCaller, { once: true });
  try {
    renderer = createPdfRenderer(pdfjsLib, {
      createCanvas,
      maxPixels: MAX_COMPARISON_PIXELS,
      maxDimension: MAX_CANVAS_DIMENSION,
    });
    diffClient = options.diffClient || new DiffWorkerClient();
    [left, right] = await Promise.all([
      renderer.open(leftSource.source, { signal: controller.signal }),
      renderer.open(rightSource.source, { signal: controller.signal }),
    ]);
    assertCombinedPageCount([left.numPages, right.numPages], MAX_PAGES);
    const descriptionsA = [];
    const descriptionsB = [];
    for (let page = 1; page <= left.numPages; page++) {
      options.onProgress?.({ side: "a", page, total: left.numPages });
      descriptionsA.push(await describePage(left, page, controller.signal));
    }
    for (let page = 1; page <= right.numPages; page++) {
      options.onProgress?.({ side: "b", page, total: right.numPages });
      descriptionsB.push(await describePage(right, page, controller.signal));
    }
    const alignment = alignPages(descriptionsA, descriptionsB, { maxCells: MAX_ALIGNMENT_CELLS });

    return {
      pairs: alignment.pairs,
      fallback: alignment.fallback,
      async render(index, {
        scale = 1.25,
        threshold = 12,
        antialiasTolerance = 8,
        signal,
        retainedPixels = 0,
        canvasA,
        canvasB,
      } = {}) {
        if (closed) throw new Error("visual comparison session is closed");
        const pair = alignment.pairs[index];
        if (!pair) throw new RangeError("visual diff page index is out of range");
        const descriptionA = pair.a == null ? null : descriptionsA[pair.a];
        const descriptionB = pair.b == null ? null : descriptionsB[pair.b];
        const renderScale = scaleForLivePixelBudget(descriptionA, descriptionB, scale, {
          maxLivePixels: MAX_LIVE_PIXELS,
          retainedPixels,
        });
        const operationSignal = linkedSignal(controller.signal, signal);
        let renderedA;
        let renderedB;
        try {
          [renderedA, renderedB] = await Promise.all([
            pair.a == null ? null : left.renderPage(pair.a + 1, {
              scale: renderScale,
              output: "canvas",
              signal: operationSignal.signal,
              cleanupPage: true,
              canvas: canvasA,
            }),
            pair.b == null ? null : right.renderPage(pair.b + 1, {
              scale: renderScale,
              output: "canvas",
              signal: operationSignal.signal,
              cleanupPage: true,
              canvas: canvasB,
            }),
          ]);
          const imageA = rgba(renderedA);
          const imageB = rgba(renderedB);
          const diff = await diffClient.diff({
            a: imageA.data,
            widthA: imageA.width,
            heightA: imageA.height,
            b: imageB.data,
            widthB: imageB.width,
            heightB: imageB.height,
            signal: operationSignal.signal,
            options: {
              threshold,
              antialiasTolerance,
              maxPixels: MAX_COMPARISON_PIXELS,
              extentA: descriptionA && {
                width: descriptionA.width * renderScale,
                height: descriptionA.height * renderScale,
              },
              extentB: descriptionB && {
                width: descriptionB.width * renderScale,
                height: descriptionB.height * renderScale,
              },
            },
          });
          return {
            pair,
            canvasA: renderedA?.canvas || null,
            canvasB: renderedB?.canvas || null,
            diff,
            pageSizeChanged: pageSizeChanged(descriptionA, descriptionB),
            leftSize: descriptionA && { width: descriptionA.width, height: descriptionA.height },
            rightSize: descriptionB && { width: descriptionB.width, height: descriptionB.height },
            dispose() {
              disposeRendered(renderedA);
              disposeRendered(renderedB);
            },
          };
        } catch (error) {
          disposeRendered(renderedA);
          disposeRendered(renderedB);
          if (canvasA) clearCanvas(canvasA);
          if (canvasB) clearCanvas(canvasB);
          throw error;
        } finally {
          operationSignal.dispose();
        }
      },
      close() {
        if (closePromise) return closePromise;
        closePromise = (async () => {
          closed = true;
          controller.abort();
          diffClient.terminate();
          await renderer.destroy();
          options.signal?.removeEventListener("abort", abortFromCaller);
          leftSource.dispose();
          rightSource.dispose();
        })();
        return closePromise;
      },
    };
  } catch (error) {
    closed = true;
    controller.abort();
    diffClient?.terminate();
    await renderer?.destroy();
    options.signal?.removeEventListener("abort", abortFromCaller);
    leftSource.dispose();
    rightSource.dispose();
    throw error;
  }
}

function drawHeatmap(diff, target) {
  target.width = diff.width;
  target.height = diff.height;
  const context = target.getContext("2d");
  if (!context) throw visualError("canvas-unavailable", "Canvas 2D context is unavailable");
  context.putImageData(new ImageData(diff.heatmap, diff.width, diff.height), 0, 0);
}

function clearCanvas(canvas) {
  canvas.width = 0;
  canvas.height = 0;
}

function summary(index, rendered) {
  return {
    index,
    pair: rendered.pair,
    changedPixels: rendered.diff.changedPixels,
    totalPixels: rendered.diff.totalPixels,
    ratio: rendered.diff.ratio,
    bounds: rendered.diff.bounds,
    pageSizeChanged: rendered.pageSizeChanged,
  };
}

function canvasBlob(canvas) {
  return new Promise((resolve, reject) => canvas.toBlob((blob) => {
    if (blob) resolve(blob);
    else reject(visualError("export-failed", "heatmap PNG export failed"));
  }, "image/png"));
}

async function heatmapPNG(diff) {
  const canvas = createCanvas();
  try {
    drawHeatmap(diff, canvas);
    return await canvasBlob(canvas);
  } finally {
    clearCanvas(canvas);
  }
}

function downloadBlob(blob, name, cleanupRegistry, cleanup) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  cleanupRegistry.schedule({ url, cleanup });
  anchor.click();
}

export function attachVisualDiff({ getFiles, inputDropzones = [], error, status }) {
  const start = document.getElementById("visualRun");
  const cancel = document.getElementById("visualCancel");
  const panel = document.getElementById("visualResults");
  const previous = document.getElementById("visualPrev");
  const next = document.getElementById("visualNext");
  const threshold = document.getElementById("visualThreshold");
  const antialias = document.getElementById("visualAntialias");
  const opacity = document.getElementById("visualOpacity");
  const mode = document.getElementById("visualMode");
  const changedOnly = document.getElementById("visualChangedOnly");
  const exportButton = document.getElementById("visualExport");
  const stats = document.getElementById("visualStats");
  const notice = document.getElementById("visualNotice");
  let canvasA = document.getElementById("visualA");
  let canvasB = document.getElementById("visualB");
  const heatmap = document.getElementById("visualHeatmap");
  let session;
  let sessionRevisions = null;
  let index = 0;
  let blinkTimer;
  let summaries = new Map();
  let changedPages = null;
  let busy = false;
  let cancelClosesSession = false;
  let activeExportToken = null;
  const actions = createLatestActionController();
  const downloadCleanups = createDelayedCleanupRegistry();

  const tr = (english, korean) => typeof window.t === "function" ? window.t(english, korean) : english;
  const isAbort = (cause) => cause?.name === "AbortError";
  const showVisualError = (cause) => window.showErr(error, visualErrorMessage(cause, tr));
  const assertCurrent = (token) => {
    if (!token.isCurrent() || token.signal.aborted) {
      throw new DOMException("Visual comparison aborted", "AbortError");
    }
  };

  previous.setAttribute("aria-label", tr("Previous page", "이전 페이지"));
  next.setAttribute("aria-label", tr("Next page", "다음 페이지"));
  mode.setAttribute("aria-label", tr("Mode", "모드"));
  threshold.setAttribute("aria-label", tr("Threshold", "임계값"));
  antialias.setAttribute("aria-label", tr("Anti-alias tolerance", "안티앨리어싱 허용값"));
  opacity.setAttribute("aria-label", tr("Opacity", "투명도"));
  changedOnly.setAttribute("aria-label", tr("Changed pages only", "변경된 페이지만"));
  canvasA.setAttribute("aria-label", tr("Original page", "원본 페이지"));
  canvasB.setAttribute("aria-label", tr("Revised page", "수정본 페이지"));
  heatmap.setAttribute("aria-label", tr("Difference heatmap", "차이 히트맵"));

  const readRenderOptions = () => Object.freeze({
    threshold: Number(threshold.value),
    antialiasTolerance: Number(antialias.value),
  });

  const stopBlink = () => {
    clearInterval(blinkTimer);
    blinkTimer = undefined;
  };

  const applyMode = () => {
    panel.dataset.mode = mode.value;
    stopBlink();
    canvasB.style.opacity = mode.value === "slider" ? String(Number(opacity.value) / 100) : "1";
    if (mode.value === "blink") {
      let showB = false;
      canvasB.style.opacity = "0";
      blinkTimer = setInterval(() => {
        showB = !showB;
        canvasB.style.opacity = showB ? "1" : "0";
      }, 600);
    }
  };

  const displayPixels = () => canvasA.width * canvasA.height + canvasB.width * canvasB.height + heatmap.width * heatmap.height;

  const clearDisplay = () => {
    clearCanvas(canvasA);
    clearCanvas(canvasB);
    clearCanvas(heatmap);
  };

  const installCanvas = (current, replacement, id, label, unionWidth) => {
    replacement.id = id;
    replacement.setAttribute("aria-label", label);
    const ratio = unionWidth ? Math.min(100, replacement.width / unionWidth * 100) : 0;
    replacement.style.setProperty("--visual-page-width", `${ratio}%`);
    current.replaceWith(replacement);
    clearCanvas(current);
    return replacement;
  };

  const updateNavigation = () => {
    if (!session || busy) {
      previous.disabled = true;
      next.disabled = true;
    } else if (changedOnly.checked) {
      const navigation = changedPageNavigation(changedPages || [], index);
      previous.disabled = navigation.previous == null;
      next.disabled = navigation.next == null;
    } else {
      previous.disabled = index <= 0;
      next.disabled = index + 1 >= session.pairs.length;
    }
    exportButton.disabled = busy || !session;
  };

  const setBusy = (value) => {
    busy = value;
    for (const element of [start, previous, next, threshold, antialias, opacity, mode, changedOnly, exportButton]) {
      element.disabled = value;
    }
    cancel.disabled = !value;
    updateNavigation();
  };

  const inputsMatchSession = () => sessionRevisions != null && inputRevisionsMatch(sessionRevisions, inputDropzones);

  const invalidateInputSession = () => {
    const exportPending = activeExportToken != null;
    actions.cancel();
    stopBlink();
    const staleSession = session;
    session = null;
    sessionRevisions = null;
    summaries = new Map();
    changedPages = null;
    index = 0;
    cancelClosesSession = false;
    panel.hidden = true;
    clearDisplay();
    setBusy(exportPending);
    if (exportPending) cancel.disabled = true;
    status.hidden = true;
    notice.textContent = tr("Input PDFs changed. Run visual comparison again.", "입력 PDF가 변경되었습니다. 시각 비교를 다시 실행하세요.");
    staleSession?.close().catch(() => {});
  };

  for (const dropzone of inputDropzones) dropzone?.addEventListener?.("dz:files", invalidateInputSession);

  const alignmentNotice = (activeSession) => activeSession.fallback
    ? tr(
      "Page alignment exceeded its bounded budget, so pages are matched by index.",
      "페이지 정렬이 제한된 예산을 넘어 페이지 번호 순서로 비교합니다.",
    )
    : "";

  const compareAt = async ({
    activeSession,
    target,
    paint,
    token,
    options,
    cache,
  }) => {
    let nextCanvasA;
    let nextCanvasB;
    let adopted = false;
    if (paint) {
      clearDisplay();
      nextCanvasA = createCanvas();
      nextCanvasB = createCanvas();
      clearCanvas(nextCanvasA);
      clearCanvas(nextCanvasB);
    }
    const rendered = await activeSession.render(target, {
      ...options,
      signal: token.signal,
      retainedPixels: paint ? 0 : displayPixels(),
      canvasA: nextCanvasA,
      canvasB: nextCanvasB,
    });
    try {
      if (!token.isCurrent()) throw new DOMException("Visual comparison aborted", "AbortError");
      const value = summary(target, rendered);
      cache.set(target, value);
      if (paint) {
        canvasA = installCanvas(
          canvasA,
          nextCanvasA,
          "visualA",
          tr("Original page", "원본 페이지"),
          rendered.diff.width,
        );
        canvasB = installCanvas(
          canvasB,
          nextCanvasB,
          "visualB",
          tr("Revised page", "수정본 페이지"),
          rendered.diff.width,
        );
        adopted = true;
        drawHeatmap(rendered.diff, heatmap);
        const bounds = rendered.diff.bounds;
        const box = bounds ? ` · bbox ${bounds.left},${bounds.top}–${bounds.right},${bounds.bottom}` : "";
        const size = rendered.pageSizeChanged ? ` · ${tr("page size changed", "페이지 크기 변경")}` : "";
        stats.textContent = `${target + 1}/${activeSession.pairs.length} · ${rendered.diff.changedPixels.toLocaleString()} ${tr("pixels", "픽셀")} · ${(rendered.diff.ratio * 100).toFixed(2)}%${box}${size}`;
        applyMode();
      }
      return value;
    } finally {
      rendered.dispose();
      if (paint && !adopted) {
        clearCanvas(nextCanvasA);
        clearCanvas(nextCanvasB);
      }
    }
  };

  const scanChangedPages = async (activeSession, token, options, cache) => collectChangedPages(
    activeSession.pairs.length,
    async (page) => cache.get(page) || compareAt({
      activeSession,
      target: page,
      paint: false,
      token,
      options,
      cache,
    }),
    {
      signal: token.signal,
      onProgress(page, total) {
        status.textContent = `${tr("Checking changed page", "변경 페이지 확인 중")} ${page + 1}/${total}…`;
      },
    },
  );

  const show = async (target = index, { rescan = false, preferFirst = false } = {}) => {
    if (!session) return;
    if (!inputsMatchSession()) {
      invalidateInputSession();
      return;
    }
    const activeSession = session;
    const options = readRenderOptions();
    const cache = summaries;
    const token = actions.begin();
    setBusy(true);
    status.hidden = false;
    try {
      let resolved = Math.max(0, Math.min(target, activeSession.pairs.length - 1));
      if (changedOnly.checked) {
        let pages = changedPages;
        if (rescan || pages == null) {
          pages = await scanChangedPages(activeSession, token, options, cache);
          if (!token.isCurrent()) return;
          changedPages = pages;
        }
        if (!pages.length) {
          clearDisplay();
          stats.textContent = "";
          notice.textContent = tr("No changed pages.", "변경된 페이지가 없습니다.");
          index = 0;
          return;
        }
        if (preferFirst) resolved = pages[0];
        else if (!pages.includes(resolved)) resolved = pages.find((page) => page >= resolved) ?? pages.at(-1);
      }
      await compareAt({ activeSession, target: resolved, paint: true, token, options, cache });
      if (!token.isCurrent()) return;
      index = resolved;
      notice.textContent = alignmentNotice(activeSession);
    } catch (cause) {
      if (!isAbort(cause)) showVisualError(cause);
    } finally {
      if (actions.finish(token)) {
        setBusy(false);
        status.hidden = true;
      }
    }
  };

  start.addEventListener("click", async () => {
    await activeExportToken?.completion;
    stopBlink();
    const token = actions.begin();
    const inputRevisions = snapshotInputRevisions(inputDropzones);
    setBusy(true);
    cancelClosesSession = true;
    let created;
    try {
      error.textContent = "";
      const [a, b] = getFiles();
      if (!a || !b) throw visualError("missing-input", "both PDF inputs are required");
      status.hidden = false;
      status.textContent = tr("Preparing visual comparison…", "시각 비교 준비 중…");
      await session?.close();
      session = null;
      sessionRevisions = null;
      panel.hidden = true;
      clearDisplay();
      created = await createVisualDiffSession(a, b, {
        signal: token.signal,
        onProgress: ({ side, page, total }) => {
          if (token.isCurrent()) status.textContent = `${tr("Indexing", "색인 중")} ${side.toUpperCase()} ${page}/${total}…`;
        },
      });
      if (!token.isCurrent() || !inputRevisionsMatch(inputRevisions, inputDropzones)) {
        await created.close();
        return;
      }
      session = created;
      sessionRevisions = inputRevisions;
      summaries = new Map();
      changedPages = null;
      index = 0;
      panel.hidden = false;
      await compareAt({
        activeSession: created,
        target: 0,
        paint: true,
        token,
        options: readRenderOptions(),
        cache: summaries,
      });
      if (token.isCurrent()) notice.textContent = alignmentNotice(created);
    } catch (cause) {
      if (created) await created.close();
      if (session === created) {
        session = null;
        sessionRevisions = null;
      }
      if (!isAbort(cause)) showVisualError(cause);
    } finally {
      if (actions.finish(token)) {
        cancelClosesSession = false;
        setBusy(false);
        status.hidden = true;
      }
    }
  });

  cancel.addEventListener("click", () => {
    const exportPending = activeExportToken != null;
    actions.cancel();
    stopBlink();
    if (cancelClosesSession) {
      session?.close().catch(() => {});
      session = null;
      sessionRevisions = null;
      panel.hidden = true;
      clearDisplay();
    }
    cancelClosesSession = false;
    setBusy(exportPending);
    if (exportPending) cancel.disabled = true;
    status.hidden = !exportPending;
    notice.textContent = tr("Visual comparison cancelled.", "시각 비교를 취소했습니다.");
  });

  previous.addEventListener("click", () => {
    const target = changedOnly.checked ? changedPageNavigation(changedPages || [], index).previous : index - 1;
    if (target != null) void show(target);
  });
  next.addEventListener("click", () => {
    const target = changedOnly.checked ? changedPageNavigation(changedPages || [], index).next : index + 1;
    if (target != null) void show(target);
  });
  const settingsChanged = () => {
    summaries = new Map();
    changedPages = null;
    void show(index, { rescan: changedOnly.checked });
  };
  threshold.addEventListener("change", settingsChanged);
  antialias.addEventListener("change", settingsChanged);
  opacity.addEventListener("input", applyMode);
  mode.addEventListener("change", applyMode);
  changedOnly.addEventListener("change", () => {
    changedPages = null;
    void show(index, { rescan: changedOnly.checked, preferFirst: changedOnly.checked });
  });

  exportButton.addEventListener("click", async () => {
    await activeExportToken?.completion;
    if (!session) return;
    if (!inputsMatchSession()) {
      invalidateInputSession();
      return;
    }
    const token = actions.begin();
    activeExportToken = token;
    setBusy(true);
    const exportSession = session;
    const options = readRenderOptions();
    const exportSummaries = new Map(summaries);
    let sink;
    try {
      status.hidden = false;
      const allSummaries = [];
      for (let page = 0; page < exportSession.pairs.length; page++) {
        status.textContent = `${tr("Summarizing", "요약 중")} ${page + 1}/${exportSession.pairs.length}…`;
        allSummaries.push(exportSummaries.get(page) || await compareAt({
          activeSession: exportSession,
          target: page,
          paint: false,
          token,
          options,
          cache: exportSummaries,
        }));
      }
      const rawSink = await createOutputSink({
        name: "paper-tools-visual-diff.zip",
        type: "application/zip",
        maxMemoryBytes: MAX_EXPORT_BYTES,
      });
      sink = createByteLimitedSink(rawSink, MAX_EXPORT_BYTES);
      const entries = visualReportEntries({
        fallback: exportSession.fallback,
        summaries: allSummaries,
        ...options,
        heatmap: async (page) => {
          assertCurrent(token);
          status.textContent = `${tr("Rendering heatmap", "히트맵 렌더링 중")} ${page + 1}/${exportSession.pairs.length}…`;
          const rendered = await exportSession.render(page, {
            ...options,
            signal: token.signal,
            retainedPixels: displayPixels(),
          });
          try {
            assertCurrent(token);
            const png = await heatmapPNG(rendered.diff);
            assertCurrent(token);
            return png;
          } finally {
            rendered.dispose();
          }
        },
      });
      await zipStoreStream(entries, {
        write: (chunk) => sink.write(chunk),
        signal: token.signal,
        isCurrent: token.isCurrent,
      });
      assertCurrent(token);
      const blob = await sink.close();
      const closedSink = sink;
      const committed = await commitLatestClosedOutput({
        token,
        sink: closedSink,
        output: blob,
        commit(output) {
          downloadBlob(output, "paper-tools-visual-diff.zip", downloadCleanups, () => closedSink.cleanup());
          summaries = exportSummaries;
        },
      });
      sink = null;
      if (!committed) throw new DOMException("Visual comparison aborted", "AbortError");
    } catch (cause) {
      await sink?.abort();
      if (!isAbort(cause)) showVisualError(cause);
    } finally {
      const current = actions.finish(token);
      const completedExport = activeExportToken === token;
      if (completedExport) activeExportToken = null;
      if (current || completedExport) {
        setBusy(false);
        status.hidden = true;
      }
    }
  });

  window.addEventListener("pagehide", () => {
    actions.cancel();
    void downloadCleanups.cleanupAll();
    stopBlink();
    session?.close().catch(() => {});
    session = null;
    sessionRevisions = null;
    clearCanvas(canvasA);
    clearCanvas(canvasB);
    clearCanvas(heatmap);
  }, { once: true });

  setBusy(false);
}
