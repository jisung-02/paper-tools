import { expect, test } from "@playwright/test";

function rotatedGeometryPDF() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R 6 0 R] /Count 4 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [10 20 210 120] /CropBox [20 30 180 110] >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [10 20 210 120] /CropBox [20 30 180 110] /Rotate 90 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [10 20 210 120] /CropBox [20 30 180 110] /Rotate 180 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [10 20 210 120] /CropBox [20 30 180 110] /Rotate 270 >>",
  ];
  let pdf = "%PDF-1.4\n";
  const offsets = [0];
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(pdf));
    pdf += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(pdf);
  pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  pdf += offsets.slice(1).map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`).join("");
  pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(pdf);
}

test("OCR exposes TXT and searchable PDF outputs", async ({ page }) => {
  const requests = [];
  page.on("request", (request) => requests.push(request.url()));
  await page.goto("/ocr/");
  await expect(page.locator("#outputType option")).toHaveText([
    "Plain text (.txt)",
    "Searchable PDF",
  ]);
  expect(requests.some(isSearchableRuntimeRequest)).toBe(false);
});

test("searchable OCR PDF preserves image pixels and exposes recognized text", async ({ page }) => {
  test.setTimeout(120_000);
  const requests = [];
  page.on("request", (request) => requests.push(request.url()));
  await page.goto("/ocr/");

  const png = await page.evaluate(async () => {
    const canvas = document.createElement("canvas");
    canvas.width = 800;
    canvas.height = 240;
    const context = canvas.getContext("2d", { alpha: false });
    context.fillStyle = "white";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = "black";
    context.font = "bold 96px Arial, sans-serif";
    context.textBaseline = "middle";
    context.fillText("HELLO OCR", 80, 120);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/png"));
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  });

  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "ocr-source.png",
    mimeType: "image/png",
    buffer: Buffer.from(png),
  });

  const downloadPromise = page.waitForEvent("download");
  await page.locator("#run").click();
  const download = await downloadPromise;
  const chunks = [];
  for await (const chunk of await download.createReadStream()) chunks.push(chunk);
  expect(Buffer.concat(chunks).toString("utf8")).toMatch(/HELLO\s+OCR/i);
  expect(requests.some(isSearchableRuntimeRequest)).toBe(false);

  await page.locator("#outputType").selectOption("pdf");
  await page.locator("#run").click();

  await expect(page.locator(".result-preview")).toBeVisible({ timeout: 120_000 });
  await expect(page.locator("#err")).toHaveText("");

  const result = await page.evaluate(async () => {
    const section = document.querySelector(".result-preview");
    const sourceImage = section.querySelector("img");
    const resultFrame = section.querySelector("iframe");
    await sourceImage.decode();

    const response = await fetch(resultFrame.src);
    const data = new Uint8Array(await response.arrayBuffer());
    const pdfBytes = Array.from(data);
    const pdfjs = await import("/vendor/pdfjs/pdf.mjs");
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
    const task = pdfjs.getDocument({ data });
    const doc = await task.promise;
    const pdfPage = await doc.getPage(1);
    const text = (await pdfPage.getTextContent()).items.map((item) => item.str).join(" ");

    const viewport = pdfPage.getViewport({ scale: 1 });
    const rendered = document.createElement("canvas");
    rendered.width = Math.ceil(viewport.width);
    rendered.height = Math.ceil(viewport.height);
    const renderedContext = rendered.getContext("2d", { alpha: false });
    await pdfPage.render({ canvasContext: renderedContext, viewport }).promise;

    const original = document.createElement("canvas");
    original.width = sourceImage.naturalWidth;
    original.height = sourceImage.naturalHeight;
    const originalContext = original.getContext("2d", { alpha: false });
    originalContext.drawImage(sourceImage, 0, 0);

    let changed = 0;
    if (rendered.width !== original.width || rendered.height !== original.height) {
      changed = -1;
    } else {
      const before = originalContext.getImageData(0, 0, original.width, original.height).data;
      const after = renderedContext.getImageData(0, 0, rendered.width, rendered.height).data;
      for (let i = 0; i < before.length; i += 4) {
        if (before[i] !== after[i] || before[i + 1] !== after[i + 1] || before[i + 2] !== after[i + 2]) changed++;
      }
    }

    await task.destroy();
    return { changed, pdf: pdfBytes, text };
  });

  expect(result.text).toMatch(/HELLO\s+OCR/i);
  expect(result.changed).toBe(0);
  expect(requests.some((url) => url.includes("/NanumGothic-Regular.ttf"))).toBe(true);
  expect(requests.some((url) => url.includes("/ocrpdf/ocrpdf.wasm"))).toBe(true);
  expect(requests.some((url) => url.includes("/wasm_exec.js"))).toBe(true);

  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "ocr-source.pdf",
    mimeType: "application/pdf",
    buffer: Buffer.from(result.pdf),
  });
  await page.locator("#run").click();
  await expect(page.locator(".result-preview iframe")).toHaveCount(2, { timeout: 120_000 });
  await expect(page.locator("#err")).toHaveText("");
  const pdfInputText = await page.evaluate(async () => {
    const frames = document.querySelectorAll(".result-preview iframe");
    const response = await fetch(frames[frames.length - 1].src);
    const pdfjs = await import("/vendor/pdfjs/pdf.mjs");
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
    const task = pdfjs.getDocument({ data: new Uint8Array(await response.arrayBuffer()) });
    const doc = await task.promise;
    const pdfPage = await doc.getPage(1);
    const text = (await pdfPage.getTextContent()).items.map((item) => item.str).join(" ");
    await task.destroy();
    return text;
  });
  expect(pdfInputText).toMatch(/HELLO\s+OCR/i);
});

test("searchable OCR positions stay visually normalized across rotated non-zero page boxes", async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto("/ocr/");
  const source = rotatedGeometryPDF();
  const positions = await page.evaluate(async (bytes) => {
    if (typeof window.Go !== "function") {
      await new Promise((resolve, reject) => {
        const script = document.createElement("script");
        script.src = "/wasm_exec.js?v=2";
        script.onload = resolve;
        script.onerror = reject;
        document.head.appendChild(script);
      });
    }
    const font = new Uint8Array(await (await fetch("/NanumGothic-Regular.ttf")).arrayBuffer());
    await window.boot("/ocrpdf/ocrpdf.wasm");
    const words = ["ZERO", "NINETY", "ONE EIGHTY", "TWO SEVENTY"];
    const pages = words.map((text) => ({ words: [{
      text,
      left: 0.1,
      top: 0.2,
      right: 0.6,
      bottom: 0.4,
      confidence: 1,
    }] }));
    const result = await window.runWasm(Uint8Array.from(bytes), font, JSON.stringify(pages), "pdf", 0);
    if (result.error) throw new Error(result.error);
    const pdfjs = await import("/vendor/pdfjs/pdf.mjs");
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
    const task = pdfjs.getDocument({ data: result.data });
    const doc = await task.promise;
    const output = [];
    for (let number = 1; number <= doc.numPages; number++) {
      const pdfPage = await doc.getPage(number);
      const viewport = pdfPage.getViewport({ scale: 1 });
      const content = await pdfPage.getTextContent();
      const item = content.items.find((candidate) => candidate.str.trim());
      const screen = pdfjs.Util.transform(viewport.transform, item.transform);
      output.push({
        text: item.str.trim(),
        x: screen[4] / viewport.width,
        y: screen[5] / viewport.height,
      });
    }
    await task.destroy();
    return output;
  }, [...source]);

  expect(positions.map(({ text }) => text)).toEqual(["ZERO", "NINETY", "ONE EIGHTY", "TWO SEVENTY"]);
  for (const position of positions) {
    expect(position.x).toBeGreaterThan(0.08);
    expect(position.x).toBeLessThan(0.12);
    expect(position.y).toBeGreaterThan(0.2);
    expect(position.y).toBeLessThan(0.5);
  }
});

function isSearchableRuntimeRequest(url) {
  return url.includes("/NanumGothic-Regular.ttf") || url.includes("/ocrpdf/") || url.includes("/wasm_exec.js");
}
