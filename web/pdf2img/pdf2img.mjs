import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";
import { createPdfRenderer } from "../pdf-renderer.mjs";
import { imageFileName, imageMime, jpegQuality, pageNumbers, renderScale } from "./names.mjs";
import { zipStore } from "./zip.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
const pdfRenderer = createPdfRenderer(pdfjsLib, {
  createCanvas: () => document.createElement("canvas"),
});

const fileDz = window.dropzone("fileDrop", { multiple: false });
const btn = document.getElementById("run");
const err = document.getElementById("err");
const fmt = document.getElementById("fmt");
const scale = document.getElementById("scale");
const pages = document.getElementById("pages");
const quality = document.getElementById("quality");
const statusEl = document.getElementById("status");

if (statusEl) statusEl.hidden = true;
btn.disabled = false;

btn.addEventListener("click", () => window.run(btn, async () => {
  if (fileDz.files.length < 1) {
    window.showErr(err, window.t("Select a file.", "파일을 선택하세요."));
    return;
  }
  const bytes = await window.fileBytes(fileDz.files[0]);
  const zip = await renderPdfToZip(bytes, fmt.value, renderScale(scale.value), pages.value, jpegQuality(quality.value));
  window.download(zip, "pdf-pages.zip", "application/zip");
}));

async function renderPdfToZip(bytes, format, scaleValue, pageRange, qualityValue) {
  let session;
  let canvas;
  try {
    session = await pdfRenderer.open(bytes);
  } catch (e) {
    throw friendlyPdfError(e);
  }

  try {
    const files = [];
    canvas = document.createElement("canvas");

    for (const pageNumber of pageNumbers(pageRange, session.numPages)) {
      await session.renderPage(pageNumber, { canvas, scale: scaleValue });
      files.push({
        name: imageFileName(pageNumber, format),
        data: await canvasBytes(canvas, imageMime(format), qualityValue),
      });
    }

    return zipStore(files);
  } catch (e) {
    throw friendlyPdfError(e);
  } finally {
    if (canvas) {
      canvas.width = 0;
      canvas.height = 0;
    }
    await session?.destroy();
  }
}

async function canvasBytes(canvas, mime, qualityValue) {
  const blob = await new Promise((resolve) => canvas.toBlob(resolve, mime, qualityValue));
  if (!blob) throw new Error("Image export failed.");
  return new Uint8Array(await blob.arrayBuffer());
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
