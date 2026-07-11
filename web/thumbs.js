import { createPdfRenderer } from "./pdf-renderer.mjs";

/* web/thumbs.js — progressive-enhancement page-thumbnail grid shared by the
   Reorder, Remove and Split & Extract tools. Loaded via a per-page
   <script type="module"> (pdf.js is ESM-only). The grid never becomes the
   source of truth: it only reads/writes the existing text input that the
   wasm tool actually parses (see pdf/ops.go ParseRanges — comma-separated
   "a", "a-b", or open-ended "a-"). If pdf.js fails to load, or the file
   can't be parsed (e.g. encrypted), every function here degrades to a
   no-op and the page behaves exactly as it did before this module existed.

   init({ mode, dropzoneId, input, container }) is the only entry point:
     mode        "reorder" | "remove" | "split"
     dropzoneId  id of the .drop element passed to window.dropzone(id, ...)
     input       the <input type="text"> the wasm tool reads on submit
     container   empty <div hidden> to render the grid into (per-page markup) */

let pdfjsLibPromise = null;
let pdfRendererPromise = null;
function loadPdfjs() {
  if (!pdfjsLibPromise) {
    pdfjsLibPromise = import("/vendor/pdfjs/pdf.mjs").then((mod) => {
      mod.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
      return mod;
    });
  }
  return pdfjsLibPromise;
}

function loadPdfRenderer() {
  if (!pdfRendererPromise) {
    pdfRendererPromise = loadPdfjs().then((pdfjs) => createPdfRenderer(pdfjs, {
      createCanvas: () => document.createElement("canvas"),
    }));
  }
  return pdfRendererPromise;
}

const MAX_PAGES = 200;
const THUMB_WIDTH = 140;

// The thumbnail grid is only a view. Keep the complete document state in
// plain arrays so pages beyond the 200-thumbnail preview cannot be lost.
export function createPageState(total, mode, value = "") {
  const count = Number.isInteger(total) && total > 0 ? total : 0;
  const order = Array.from({ length: count }, (_, i) => i + 1);
  const parsed = parseRanges(value, count);
  if (mode === "reorder" && parsed && parsed.length === count && new Set(parsed).size === count) order.splice(0, order.length, ...parsed);
  const selected = new Set(mode === "reorder" || !parsed ? [] : parsed);
  return { totalPageCount: count, order, selected };
}

export function pageStateValue(state, mode) {
  if (mode === "reorder") return formatOrder(state.order);
  return formatRanges(Array.from(state.selected));
}

export function pageStateSelect(state, page, selected = true) {
  if (page >= 1 && page <= state.totalPageCount) {
    if (selected) state.selected.add(page); else state.selected.delete(page);
  }
  return state;
}

export function pageStateMove(state, page, targetIndex) {
  const from = state.order.indexOf(page);
  if (from < 0 || targetIndex < 0 || targetIndex >= state.order.length) return state;
  state.order.splice(from, 1);
  state.order.splice(targetIndex, 0, page);
  return state;
}

/* ---------------------------------------------------------- range helpers --- */

// Mirrors pdf/ops.go ParseRanges: comma-separated "a", "a-b" or "a-" (open
// to `total`). Returns null (never throws) on anything unparseable or out
// of bounds, so callers can leave the grid untouched on bad input.
export function parseRanges(text, total) {
  const s = String(text == null ? "" : text).trim();
  if (!s || !total) return null;
  const out = [];
  for (const raw of s.split(",")) {
    const p = raw.trim();
    if (!p) continue;
    let lo, hi;
    const dash = p.indexOf("-");
    if (dash >= 0) {
      const loStr = p.slice(0, dash).trim();
      const hiStr = p.slice(dash + 1).trim();
      if (!/^\d+$/.test(loStr)) return null;
      lo = parseInt(loStr, 10);
      if (hiStr === "") {
        hi = total;
      } else {
        if (!/^\d+$/.test(hiStr)) return null;
        hi = parseInt(hiStr, 10);
      }
    } else {
      if (!/^\d+$/.test(p)) return null;
      lo = hi = parseInt(p, 10);
    }
    if (lo < 1 || hi > total || lo > hi) return null;
    for (let n = lo; n <= hi; n++) out.push(n);
  }
  return out.length ? out : null;
}

// Compacts a set of page numbers into "1-3,5" form (ascending, deduped) —
// the format Remove/Split's inputs expect.
export function formatRanges(pages) {
  const sorted = Array.from(new Set(pages)).sort((a, b) => a - b);
  const parts = [];
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) j++;
    parts.push(i === j ? String(sorted[i]) : `${sorted[i]}-${sorted[j]}`);
    i = j + 1;
  }
  return parts.join(",");
}

// Reorder's input is a permutation, not a set — order matters, so this
// just joins the (already-ordered) page numbers with commas.
export function formatOrder(pages) {
  return pages.join(",");
}

/* -------------------------------------------------------------------- init --- */

export function init(config) {
  try {
    setup(config);
  } catch (e) {
    // Best-effort enhancement only — never break the base flow.
  }
}

function setup(config) {
  const dz = document.getElementById(config.dropzoneId);
  const container = config.container;
  const input = config.input;
  if (!dz || !container || !input) return;

  const HINTS = {
    reorder: () => window.t("Drag pages to reorder.", "페이지를 끌어서 순서를 바꾸세요."),
    remove: () => window.t("Click a page to mark it for removal.", "삭제할 페이지를 클릭하세요."),
    split: () => window.t("Click pages to select them for extraction.", "추출할 페이지를 클릭하세요."),
  };

  container.innerHTML = "";
  const hintEl = document.createElement("p");
  hintEl.className = "hint thumbhint";
  const noticeEl = document.createElement("p");
  noticeEl.className = "hint thumbnotice";
  noticeEl.setAttribute("role", "status");
  noticeEl.setAttribute("aria-live", "polite");
  noticeEl.hidden = true;
  const cellsEl = document.createElement("div");
  cellsEl.className = "thumbcells";
  container.appendChild(hintEl);
  container.appendChild(noticeEl);
  container.appendChild(cellsEl);
  container.hidden = true;

  let currentDoc = null;
  let pageCount = 0;
  let pageState = null;
  let loadToken = 0;
  let draggingEl = null;

  function clearDragMarkers() {
    cellsEl.querySelectorAll(".dragover-before, .dragover-after").forEach((el) => {
      el.classList.remove("dragover-before", "dragover-after");
    });
  }

  function writeFromGrid() {
    const cells = Array.from(cellsEl.children);
    if (config.mode === "reorder") {
      const visible = cells.map((c) => Number(c.dataset.page));
      const visibleSet = new Set(visible);
      const tail = pageState.order.filter((n) => !visibleSet.has(n));
      pageState.order = visible.concat(tail);
      input.value = pageStateValue(pageState, config.mode);
    } else {
      const cls = config.mode === "remove" ? "removed" : "selected";
      cells.forEach((c) => pageStateSelect(pageState, Number(c.dataset.page), c.classList.contains(cls)));
      input.value = pageStateValue(pageState, config.mode);
    }
  }

  function syncGridFromInput() {
    if (!pageCount) return;
    const parsed = parseRanges(input.value, pageCount);
    if (config.mode === "reorder") {
      if (!parsed) return;
      if (parsed.length !== pageCount || new Set(parsed).size !== pageCount) return;
      pageState.order = parsed;
      const byPage = new Map(Array.from(cellsEl.children).map((c) => [Number(c.dataset.page), c]));
      const frag = document.createDocumentFragment();
      for (const n of pageState.order.slice(0, MAX_PAGES)) {
        const c = byPage.get(n);
        if (c) frag.appendChild(c);
      }
      cellsEl.appendChild(frag);
    } else {
      if (!parsed) return;
      pageState.selected = new Set(parsed);
      const cls = config.mode === "remove" ? "removed" : "selected";
      Array.from(cellsEl.children).forEach((c) => {
        c.classList.toggle(cls, pageState.selected.has(Number(c.dataset.page)));
      });
    }
  }

  function onCellClick(e) {
    const cell = e.currentTarget;
    const cls = config.mode === "remove" ? "removed" : "selected";
    cell.classList.toggle(cls);
    cell.setAttribute("aria-pressed", String(cell.classList.contains(cls)));
    writeFromGrid();
  }

  function onCellKeydown(e) {
    const cell = e.currentTarget;
    const page = Number(cell.dataset.page);
    if (config.mode === "reorder" && (e.key === "ArrowLeft" || e.key === "ArrowUp" || e.key === "ArrowRight" || e.key === "ArrowDown")) {
      e.preventDefault();
      const delta = e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 1;
      const index = pageState.order.indexOf(page);
      pageStateMove(pageState, page, index + delta);
      const target = cellsEl.children[index + delta];
      if (target) target.after(cell);
      writeFromGrid();
      cell.focus();
      return;
    }
    if (config.mode !== "reorder" && (e.key === " " || e.key === "Enter")) {
      e.preventDefault();
      onCellClick(e);
    }
  }

  function onDragStart(e) {
    draggingEl = e.currentTarget;
    draggingEl.classList.add("dragging");
    e.dataTransfer.effectAllowed = "move";
    try {
      e.dataTransfer.setData("text/plain", draggingEl.dataset.page);
    } catch (err) {}
  }

  function onDragEnd() {
    if (draggingEl) draggingEl.classList.remove("dragging");
    draggingEl = null;
    clearDragMarkers();
  }

  cellsEl.addEventListener("dragover", (e) => {
    if (!draggingEl) return;
    e.preventDefault();
    clearDragMarkers();
    const target = e.target.closest(".thumbcell");
    if (!target || target === draggingEl) return;
    const rect = target.getBoundingClientRect();
    const before = e.clientX - rect.left < rect.width / 2;
    target.classList.add(before ? "dragover-before" : "dragover-after");
  });

  cellsEl.addEventListener("drop", (e) => {
    if (!draggingEl) return;
    e.preventDefault();
    const target = e.target.closest(".thumbcell");
    clearDragMarkers();
    if (!target || target === draggingEl) return;
    const rect = target.getBoundingClientRect();
    const before = e.clientX - rect.left < rect.width / 2;
    if (before) target.parentNode.insertBefore(draggingEl, target);
    else target.parentNode.insertBefore(draggingEl, target.nextSibling);
    writeFromGrid();
  });

  function buildCell(n) {
    const cell = document.createElement("div");
    cell.className = "thumbcell";
    cell.dataset.page = String(n);
    if (config.mode === "reorder") {
      cell.draggable = true;
      cell.addEventListener("dragstart", onDragStart);
      cell.addEventListener("dragend", onDragEnd);
    } else {
      cell.addEventListener("click", onCellClick);
    }
    cell.tabIndex = 0;
    cell.setAttribute("role", "button");
    cell.setAttribute("aria-label", window.t("Page " + n, "페이지 " + n));
    cell.addEventListener("keydown", onCellKeydown);
    if (config.mode !== "reorder") cell.setAttribute("aria-pressed", "false");
    const canvas = document.createElement("canvas");
    cell.appendChild(canvas);
    const num = document.createElement("span");
    num.className = "thumbnum";
    num.textContent = String(n);
    cell.appendChild(num);
    return cell;
  }

  async function renderThumb(doc, n, canvas) {
    try {
      const dpr = window.devicePixelRatio || 1;
      const rendered = await doc.renderPage(n, { canvas, width: THUMB_WIDTH, pixelRatio: dpr });
      canvas.style.width = THUMB_WIDTH + "px";
      canvas.style.height = Math.round(rendered.displayHeight) + "px";
    } catch (e) {
      // Leave this one cell blank on a per-page render failure.
    }
  }

  async function handleFiles(files) {
    loadToken++;
    const token = loadToken;

    if (currentDoc) {
      try {
        await currentDoc.destroy();
      } catch (e) {}
      currentDoc = null;
    }
    cellsEl.innerHTML = "";
    noticeEl.hidden = true;
    container.hidden = true;
    pageCount = 0;

    if (!files || files.length === 0) return;

    try {
      const renderer = await loadPdfRenderer();
      if (token !== loadToken) return;
      const buf = await files[0].arrayBuffer();
      const data = new Uint8Array(buf);
      const doc = await renderer.open(data);
      if (token !== loadToken) {
        await doc.destroy();
        return;
      }
      currentDoc = doc;

      const total = doc.numPages;
      pageCount = total;
      pageState = createPageState(total, config.mode, input.value);
      const visiblePages = pageState.order.slice(0, MAX_PAGES);

      hintEl.textContent = HINTS[config.mode]();
      if (total > MAX_PAGES) {
        noticeEl.textContent = window.t("Showing first 200 pages.", "처음 200페이지만 표시합니다.");
        noticeEl.hidden = false;
      }
      container.hidden = false;

      for (const n of visiblePages) {
        if (token !== loadToken) return;
        const cell = buildCell(n);
        cellsEl.appendChild(cell);
        await renderThumb(doc, n, cell.querySelector("canvas"));
      }
      if (token !== loadToken) return;
      syncGridFromInput();
    } catch (e) {
      if (currentDoc) {
        try {
          await currentDoc.destroy();
        } catch (err) {}
        currentDoc = null;
      }
      cellsEl.innerHTML = "";
      container.hidden = true;
      pageCount = 0;
    }
  }

  dz.addEventListener("dz:files", (e) => {
    handleFiles((e.detail && e.detail.files) || []).catch(() => {});
  });

  input.addEventListener("input", () => {
    try {
      syncGridFromInput();
    } catch (e) {}
  });
  window.addEventListener("pagehide", () => { currentDoc?.destroy(); }, { once: true });
}
