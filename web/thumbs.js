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
function loadPdfjs() {
  if (!pdfjsLibPromise) {
    pdfjsLibPromise = import("/vendor/pdfjs/pdf.mjs").then((mod) => {
      mod.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
      return mod;
    });
  }
  return pdfjsLibPromise;
}

const MAX_PAGES = 200;
const THUMB_WIDTH = 140;

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
  noticeEl.hidden = true;
  const cellsEl = document.createElement("div");
  cellsEl.className = "thumbcells";
  container.appendChild(hintEl);
  container.appendChild(noticeEl);
  container.appendChild(cellsEl);
  container.hidden = true;

  let currentDoc = null;
  let pageCount = 0;
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
      input.value = formatOrder(cells.map((c) => Number(c.dataset.page)));
    } else {
      const cls = config.mode === "remove" ? "removed" : "selected";
      const picked = cells.filter((c) => c.classList.contains(cls)).map((c) => Number(c.dataset.page));
      input.value = picked.length ? formatRanges(picked) : "";
    }
  }

  function syncGridFromInput() {
    if (!pageCount) return;
    const parsed = parseRanges(input.value, pageCount);
    if (config.mode === "reorder") {
      if (!parsed) return;
      if (parsed.length !== pageCount || new Set(parsed).size !== pageCount) return;
      const byPage = new Map(Array.from(cellsEl.children).map((c) => [Number(c.dataset.page), c]));
      const frag = document.createDocumentFragment();
      for (const n of parsed) {
        const c = byPage.get(n);
        if (c) frag.appendChild(c);
      }
      cellsEl.appendChild(frag);
    } else {
      if (!parsed) return;
      const set = new Set(parsed);
      const cls = config.mode === "remove" ? "removed" : "selected";
      Array.from(cellsEl.children).forEach((c) => {
        c.classList.toggle(cls, set.has(Number(c.dataset.page)));
      });
    }
  }

  function onCellClick(e) {
    const cell = e.currentTarget;
    const cls = config.mode === "remove" ? "removed" : "selected";
    cell.classList.toggle(cls);
    writeFromGrid();
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
      const page = await doc.getPage(n);
      const viewport1 = page.getViewport({ scale: 1 });
      const scale = THUMB_WIDTH / viewport1.width;
      const dpr = window.devicePixelRatio || 1;
      const viewport = page.getViewport({ scale: scale * dpr });
      canvas.width = Math.max(1, Math.ceil(viewport.width));
      canvas.height = Math.max(1, Math.ceil(viewport.height));
      canvas.style.width = THUMB_WIDTH + "px";
      canvas.style.height = Math.round(viewport1.height * scale) + "px";
      const ctx = canvas.getContext("2d", { alpha: false });
      if (!ctx) return;
      ctx.fillStyle = "#fff";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      await page.render({ canvasContext: ctx, viewport }).promise;
    } catch (e) {
      // Leave this one cell blank on a per-page render failure.
    }
  }

  async function handleFiles(files) {
    loadToken++;
    const token = loadToken;

    if (currentDoc) {
      try {
        currentDoc.destroy();
      } catch (e) {}
      currentDoc = null;
    }
    cellsEl.innerHTML = "";
    noticeEl.hidden = true;
    container.hidden = true;
    pageCount = 0;

    if (!files || files.length === 0) return;

    try {
      const pdfjsLib = await loadPdfjs();
      if (token !== loadToken) return;
      const buf = await files[0].arrayBuffer();
      const data = new Uint8Array(buf);
      const task = pdfjsLib.getDocument({
        data,
        cMapUrl: "/vendor/pdfjs/cmaps/",
        cMapPacked: true,
        standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
      });
      const doc = await task.promise;
      if (token !== loadToken) {
        doc.destroy();
        return;
      }
      currentDoc = doc;

      const total = doc.numPages;
      const count = Math.min(total, MAX_PAGES);
      pageCount = count;

      hintEl.textContent = HINTS[config.mode]();
      if (total > MAX_PAGES) {
        noticeEl.textContent = window.t("Showing first 200 pages.", "처음 200페이지만 표시합니다.");
        noticeEl.hidden = false;
      }
      container.hidden = false;

      for (let n = 1; n <= count; n++) {
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
          currentDoc.destroy();
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
}
