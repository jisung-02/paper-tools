import * as pdfjsLib from "/vendor/pdfjs/pdf.mjs";
import { imageFileName, imageMime, renderScale } from "./names.mjs";
import { zipStore } from "./zip.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";

const fileDz = window.dropzone("fileDrop", { multiple: false });
const btn = document.getElementById("run");
const err = document.getElementById("err");
const fmt = document.getElementById("fmt");
const scale = document.getElementById("scale");
const statusEl = document.getElementById("status");

if (statusEl) statusEl.hidden = true;
btn.disabled = false;

btn.addEventListener("click", () => window.run(btn, async () => {
  if (fileDz.files.length < 1) {
    window.showErr(err, window.t("Select a file.", "파일을 선택하세요."));
    return;
  }
  const bytes = await window.fileBytes(fileDz.files[0]);
  const zip = await renderPdfToZip(bytes, fmt.value, renderScale(scale.value));
  window.download(zip, "pdf-pages.zip", "application/zip");
}));

async function renderPdfToZip(bytes, format, scaleValue) {
  let doc;
  let task;
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

  try {
    const files = [];
    const canvas = document.createElement("canvas");
    const ctx = canvas.getContext("2d", { alpha: false });
    if (!ctx) throw new Error("Canvas is not available.");

    for (let pageNumber = 1; pageNumber <= doc.numPages; pageNumber++) {
      const page = await doc.getPage(pageNumber);
      const viewport = page.getViewport({ scale: scaleValue });
      canvas.width = Math.ceil(viewport.width);
      canvas.height = Math.ceil(viewport.height);
      ctx.fillStyle = "#fff";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      await page.render({ canvasContext: ctx, viewport }).promise;
      files.push({
        name: imageFileName(pageNumber, format),
        data: await canvasBytes(canvas, imageMime(format)),
      });
    }

    canvas.width = 0;
    canvas.height = 0;
    return zipStore(files);
  } catch (e) {
    throw friendlyPdfError(e);
  } finally {
    if (doc && typeof doc.cleanup === "function") await doc.cleanup();
    if (task && typeof task.destroy === "function") await task.destroy();
  }
}

async function canvasBytes(canvas, mime) {
  const blob = await new Promise((resolve) => canvas.toBlob(resolve, mime, 0.9));
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
