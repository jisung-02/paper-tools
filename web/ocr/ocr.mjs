import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";
import { normalizeTextBoxes } from "./boxes.mjs";
import { OCRBudget, renderGeometry, validateOCRSelection } from "./budget.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";

// Rasterize scale for PDF pages — matches pdf2img's "2x (sharper)" preset;
// OCR accuracy benefits from the extra resolution.
const RENDER_SCALE = 2;

// tesseract-wasm and its language data are vendored same-origin; nothing
// here is ever fetched from a third-party CDN. Loaded lazily (dynamic
// import + fetch) on first Run click, never at page load.
const LANG_MODELS = {
  eng: "/vendor/tesseract/eng.traineddata",
  kor: "/vendor/tesseract/kor.traineddata",
};

const fileDz = window.dropzone("fileDrop", { multiple: true });
const btn = document.getElementById("run");
const err = document.getElementById("err");
const langSel = document.getElementById("lang");
const statusEl = document.getElementById("status");
const output = document.getElementById("output");
const outputType = document.getElementById("outputType");

let searchableRuntime;
if (statusEl) statusEl.hidden = true;
btn.disabled = false;

function isPdf(f) {
  return f.type === "application/pdf" || /\.pdf$/i.test(f.name);
}

function setStatus(msg) {
  if (!statusEl) return;
  statusEl.hidden = false;
  statusEl.textContent = msg;
}

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.onload = resolve;
    script.onerror = () => reject(new Error(window.t("Failed to load PDF runtime.", "PDF 런타임을 불러오지 못했습니다.")));
    document.head.appendChild(script);
  });
}

function loadSearchableRuntime() {
  if (!searchableRuntime) {
    const pending = (async () => {
      const fontPromise = fetch("/NanumGothic-Regular.ttf").then(async (response) => {
        if (!response.ok) throw new Error(window.t("Failed to load font.", "폰트를 불러오지 못했습니다."));
        return new Uint8Array(await response.arrayBuffer());
      });
      const [, fontBytes] = await Promise.all([
        typeof window.Go === "function" ? Promise.resolve() : loadScript("/wasm_exec.js?v=2"),
        fontPromise,
      ]);
      await window.boot("/ocrpdf/ocrpdf.wasm");
      return fontBytes;
    })();
    searchableRuntime = pending.catch((error) => {
      searchableRuntime = undefined;
      throw error;
    });
  }
  return searchableRuntime;
}

btn.addEventListener("click", () => window.run(btn, async () => {
  output.value = "";
  const files = fileDz.files;
  if (files.length < 1) {
    window.showErr(err, window.t("Select a file.", "파일을 선택하세요."));
    return;
  }

  const pdfCount = files.filter(isPdf).length;
  if (pdfCount > 0 && files.length > 1) {
    window.showErr(err, window.t(
      "Select a single PDF, or one or more images — not both.",
      "PDF 한 개만 선택하거나, 이미지를 한 개 이상 선택하세요 (섞어서 선택할 수 없습니다)."
    ));
    return;
  }
  const searchablePDF = outputType.value === "pdf";
  validateOCRSelection(files, pdfCount === 1 ? 1 : files.length);

  let doc;
  let task;
  let client;
  const releasePDF = async () => {
    const currentDoc = doc;
    const currentTask = task;
    doc = undefined;
    task = undefined;
    try {
      if (currentDoc && typeof currentDoc.cleanup === "function") await currentDoc.cleanup();
    } finally {
      if (currentTask && typeof currentTask.destroy === "function") await currentTask.destroy();
    }
  };
  try {
    let totalPages;
    let getPageBitmap;
    let budget;

    if (pdfCount === 1) {
      const bytes = await window.fileBytes(files[0]);
      try {
        task = pdfjsLib.getDocument({
          data: bytes,
          cMapUrl: "/vendor/pdfjs/cmaps/",
          cMapPacked: true,
          standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
        });
        doc = await task.promise;
      } catch (e) {
        throw friendlyPdfError(e);
      }
      totalPages = doc.numPages;
      validateOCRSelection(files, totalPages);
      budget = new OCRBudget(totalPages);
      const geometries = [];
      for (let i = 0; i < totalPages; i++) {
        const page = await doc.getPage(i + 1);
        const base = page.getViewport({ scale: 1 });
        const geometry = renderGeometry(base.width, base.height, RENDER_SCALE);
        budget.reservePage(i, geometry.width, geometry.height);
        geometries.push(geometry);
      }
      getPageBitmap = (i) => renderPdfPage(doc, i + 1, geometries[i]);
    } else {
      totalPages = files.length;
      budget = new OCRBudget(totalPages);
      getPageBitmap = async (i) => {
        try {
          return await createImageBitmap(files[i]);
        } catch (e) {
          throw new Error("unsupported format");
        }
      };
    }

    setStatus(window.t("Loading OCR engine…", "OCR 엔진 불러오는 중…"));
    const { OCRClient } = await import("/vendor/tesseract/lib.js");
    client = new OCRClient();

    setStatus(window.t("Loading language data…", "언어 데이터 불러오는 중…"));
    const modelUrl = LANG_MODELS[langSel.value] || LANG_MODELS.eng;
    const modelRes = await fetch(modelUrl);
    if (!modelRes.ok) {
      throw new Error(window.t("Failed to load language data.", "언어 데이터를 불러오지 못했습니다."));
    }
    await client.loadModel(await modelRes.arrayBuffer());

    const pageTexts = [];
    const ocrPages = [];
    for (let i = 0; i < totalPages; i++) {
      btn.textContent = window.t("Recognizing…", "인식 중…") + " (" + (i + 1) + "/" + totalPages + ")";
      const bitmap = await getPageBitmap(i);
      const width = bitmap.width;
      const height = bitmap.height;
      try {
        if (pdfCount === 0) budget.reservePage(i, width, height);
        await client.loadImage(bitmap);
      } finally {
        if (bitmap.close) bitmap.close();
      }
      let words = [];
      if (searchablePDF) {
        const boxes = await client.getTextBoxes("word");
        words = normalizeTextBoxes(boxes, width, height);
        ocrPages.push({ words });
      }
      const text = (await client.getText()).trim();
      budget.addRecognition(i, words, text);
      pageTexts.push(text);
    }
    const completedClient = client;
    client = undefined;
    await completedClient.destroy();

    setStatus("");
    if (statusEl) statusEl.hidden = true;

    const finalText = pageTexts.join("\n\n");
    output.value = finalText;
    if (!searchablePDF) {
      window.download(new TextEncoder().encode(finalText), "ocr-text.txt", "text/plain;charset=utf-8");
      return;
    }

    if (pdfCount === 1) await releasePDF();
    const fontBytes = await loadSearchableRuntime();
    const source = pdfCount === 1
      ? await window.fileBytes(files[0])
      : [];
    if (pdfCount === 0) {
      for (const file of files) source.push(await window.fileBytes(file));
    }
    const sourceKind = pdfCount === 1 ? "pdf" : "images";
    const pagesJSON = JSON.stringify(ocrPages);
    budget.assertSerialized(pagesJSON);
    const result = await window.runWasm(source, fontBytes, pagesJSON, sourceKind, 0);
    window.finish(result, searchablePDFName(files[0]), err);
  } finally {
    if (client) await client.destroy();
    await releasePDF();
    if (statusEl) statusEl.hidden = true;
  }
}));

function searchablePDFName(file) {
  const stem = String(file?.name || "ocr").replace(/\.[^.]+$/, "") || "ocr";
  return `${stem}-searchable.pdf`;
}

async function renderPdfPage(doc, pageNumber, geometry) {
  const page = await doc.getPage(pageNumber);
  const viewport = page.getViewport({ scale: geometry.scale });
  const canvas = document.createElement("canvas");
  canvas.width = geometry.width;
  canvas.height = geometry.height;
  const ctx = canvas.getContext("2d", { alpha: false });
  if (!ctx) throw new Error("Canvas is not available.");
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  try {
    await page.render({ canvasContext: ctx, viewport }).promise;
    return await createImageBitmap(canvas);
  } finally {
    canvas.width = 0;
    canvas.height = 0;
  }
}

function friendlyPdfError(e) {
  const name = e && e.name ? e.name : "";
  const message = e && e.message ? e.message : String(e);
  if (name === "PasswordException" || message.includes("password")) {
    return new Error("encrypted files are not supported");
  }
  if (name === "InvalidPDFException" || message.includes("Invalid PDF")) {
    return new Error("not a PDF");
  }
  return new Error(message);
}
