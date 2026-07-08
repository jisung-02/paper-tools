import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";

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

  let doc;
  let task;
  let client;
  try {
    let totalPages;
    let getPageBitmap;

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
      getPageBitmap = (i) => renderPdfPage(doc, i + 1);
    } else {
      totalPages = files.length;
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
    for (let i = 0; i < totalPages; i++) {
      btn.textContent = window.t("Recognizing…", "인식 중…") + " (" + (i + 1) + "/" + totalPages + ")";
      const bitmap = await getPageBitmap(i);
      try {
        await client.loadImage(bitmap);
      } finally {
        if (bitmap.close) bitmap.close();
      }
      const text = await client.getText();
      pageTexts.push(text.trim());
    }

    setStatus("");
    if (statusEl) statusEl.hidden = true;

    const finalText = pageTexts.join("\n\n");
    output.value = finalText;
    window.download(new TextEncoder().encode(finalText), "ocr-text.txt", "text/plain;charset=utf-8");
  } finally {
    if (client) await client.destroy();
    if (doc && typeof doc.cleanup === "function") await doc.cleanup();
    if (task && typeof task.destroy === "function") await task.destroy();
    if (statusEl) statusEl.hidden = true;
  }
}));

async function renderPdfPage(doc, pageNumber) {
  const page = await doc.getPage(pageNumber);
  const viewport = page.getViewport({ scale: RENDER_SCALE });
  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(viewport.width);
  canvas.height = Math.ceil(viewport.height);
  const ctx = canvas.getContext("2d", { alpha: false });
  if (!ctx) throw new Error("Canvas is not available.");
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  await page.render({ canvasContext: ctx, viewport }).promise;
  const bitmap = await createImageBitmap(canvas);
  canvas.width = 0;
  canvas.height = 0;
  return bitmap;
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
