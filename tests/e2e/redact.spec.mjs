import { readFile } from "node:fs/promises";

import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

const sourceCanaries = [
  "REDACT-SOURCE-TEXT",
  "REDACT-LINK-CANARY",
  "REDACT-JS-CANARY",
  "REDACT-ATTACHMENT-CANARY",
  "REDACT-FORM-CANARY",
  "REDACT-XMP-CANARY",
];

function streamObject(dictionary, data) {
  return `<< ${dictionary} /Length ${Buffer.byteLength(data)} >>\nstream\n${data}\nendstream`;
}

function classicPDF(objects) {
  let result = "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n";
  const offsets = [0];
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(result, "latin1"));
    result += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const xrefOffset = Buffer.byteLength(result, "latin1");
  result += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  result += offsets.slice(1).map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`).join("");
  result += `trailer\n<< /Root 1 0 R /Size ${objects.length + 1} >>\nstartxref\n${xrefOffset}\n%%EOF\n`;
  return Buffer.from(result, "latin1");
}

function canaryPDF() {
  const page = (contentRef, rotation, userUnit = 1, annotations = "") => [
    "<< /Type /Page /Parent 2 0 R",
    "/MediaBox [10 20 210 120] /CropBox [20 30 180 110]",
    `/Rotate ${rotation} /UserUnit ${userUnit}`,
    "/Resources << /Font << /F1 7 0 R >> >>",
    `/Contents ${contentRef} 0 R ${annotations} >>`,
  ].join(" ");
  const content = (number) => [
    "q 0.85 0.70 0.55 rg 10 20 200 100 re f Q",
    `BT /F1 12 Tf 35 65 Td (REDACT-SOURCE-TEXT-${number}) Tj ET`,
  ].join("\n");
  const metadata = [
    '<?xpacket begin=""?>',
    '<x:xmpmeta xmlns:x="adobe:ns:meta/">',
    '<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">',
    '<rdf:Description xmlns:dc="http://purl.org/dc/elements/1.1/">',
    '<dc:description><rdf:Alt><rdf:li xml:lang="x-default">REDACT-XMP-CANARY</rdf:li></rdf:Alt></dc:description>',
    "</rdf:Description></rdf:RDF></x:xmpmeta>",
    '<?xpacket end="w"?>',
  ].join("");
  return classicPDF([
    "<< /Type /Catalog /Pages 2 0 R /OpenAction 12 0 R " +
      "/Names << /JavaScript << /Names [(startup) 12 0 R] >> " +
      "/EmbeddedFiles << /Names [(canary.txt) 14 0 R] >> >> " +
      "/AcroForm << /Fields [15 0 R] /NeedAppearances true >> /Metadata 18 0 R >>",
    "<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R 6 0 R] /Count 4 >>",
    page(8, 0, 1, "/Annots [15 0 R 16 0 R]"),
    page(9, 90),
    page(10, 180, 2),
    page(11, 270, 2),
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    streamObject("", content(1)),
    streamObject("", content(2)),
    streamObject("", content(3)),
    streamObject("", content(4)),
    "<< /S /JavaScript /JS (REDACT-JS-CANARY) >>",
    streamObject("/Type /EmbeddedFile", "REDACT-ATTACHMENT-CANARY"),
    "<< /Type /Filespec /F (canary.txt) /EF << /F 13 0 R >> >>",
    "<< /Type /Annot /Subtype /Widget /FT /Tx /T (REDACT-FORM-CANARY) " +
      "/V (REDACT-FORM-CANARY) /Rect [30 35 90 50] /P 3 0 R >>",
    "<< /Type /Annot /Subtype /Link /Rect [100 35 170 50] /A 17 0 R >>",
    "<< /S /URI /URI (https://example.invalid/REDACT-LINK-CANARY) >>",
    streamObject("/Type /Metadata /Subtype /XML", metadata),
  ]);
}

async function inspectPDF(page, bytes, { renderPixels = false } = {}) {
  return page.evaluate(async ({ values, renderPixels }) => {
    const pdfjs = await import("/vendor/pdfjs/pdf.mjs");
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/pdf.worker.mjs";
    const loadingTask = pdfjs.getDocument({
      data: Uint8Array.from(values),
      cMapUrl: "/vendor/pdfjs/cmaps/",
      cMapPacked: true,
      standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
    });
    const doc = await loadingTask.promise;
    const pages = [];
    for (let number = 1; number <= doc.numPages; number++) {
      const pdfPage = await doc.getPage(number);
      const viewport = pdfPage.getViewport({ scale: 1 });
      const annotations = await pdfPage.getAnnotations({ intent: "display" });
      const text = (await pdfPage.getTextContent()).items.map((item) => item.str).join(" ");
      let center = null;
      if (renderPixels) {
        const canvas = document.createElement("canvas");
        canvas.width = Math.ceil(viewport.width);
        canvas.height = Math.ceil(viewport.height);
        const context = canvas.getContext("2d", { alpha: false });
        context.fillStyle = "#fff";
        context.fillRect(0, 0, canvas.width, canvas.height);
        await pdfPage.render({ canvasContext: context, viewport }).promise;
        center = [...context.getImageData(
          Math.floor(canvas.width / 2),
          Math.floor(canvas.height / 2),
          1,
          1,
        ).data];
        canvas.width = 0;
        canvas.height = 0;
      }
      pages.push({ width: viewport.width, height: viewport.height, annotations, text, center });
      pdfPage.cleanup();
    }
    const metadataResult = await doc.getMetadata();
    const metadata = metadataResult.metadata?.getRaw?.() || "";
    const javaScript = await doc.getJSActions();
    const attachments = await doc.getAttachments();
    const attachmentEntries = attachments instanceof Map
      ? [...attachments]
      : Object.entries(attachments || {});
    const attachmentText = (await Promise.all(attachmentEntries.map(async ([name]) => {
      const content = await doc.getAttachmentContent(name);
      return content ? new TextDecoder().decode(content) : "";
    }))).join("|");
    const fields = await doc.getFieldObjects();
    await loadingTask.destroy();
    return {
      numPages: pages.length,
      pages,
      metadata,
      javaScript: JSON.stringify(javaScript),
      attachmentText,
      fields: JSON.stringify(fields),
    };
  }, { values: [...bytes], renderPixels });
}

async function selectPageCenter(page, number) {
  await expect(page.locator("#redactPage")).toHaveText(`${number}/4`);
  const overlay = page.locator("#redactOverlay");
  await overlay.scrollIntoViewIfNeeded();
  const box = await overlay.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box.x + box.width * 0.4, box.y + box.height * 0.4);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.6, box.y + box.height * 0.6);
  await page.mouse.up();
  await expect(page.locator("#redactCount")).toContainText(`${number} redaction area`);
}

test("stateful redaction drops source structures and bakes geometry into black raster pixels", async ({ page }) => {
  test.setTimeout(120_000);
  const source = canaryPDF();
  await page.addInitScript(() => {
    const createObjectURL = URL.createObjectURL.bind(URL);
    const revokeObjectURL = URL.revokeObjectURL.bind(URL);
    window.__redactObjectURLs = [];
    window.__redactRevokedURLs = [];
    URL.createObjectURL = (blob) => {
      const url = createObjectURL(blob);
      window.__redactObjectURLs.push({ url, name: blob?.name || "", type: blob?.type || "" });
      return url;
    };
    URL.revokeObjectURL = (url) => {
      window.__redactRevokedURLs.push(url);
      revokeObjectURL(url);
    };
  });
  await page.goto("/redact/");

  const sourceInspection = await inspectPDF(page, source);
  expect(sourceInspection.numPages).toBe(4);
  expect(sourceInspection.pages.map(({ width, height }) => [width, height])).toEqual([
    [160, 80],
    [80, 160],
    [320, 160],
    [160, 320],
  ]);
  expect(sourceInspection.pages[0].annotations.some((item) => item.subtype === "Link" && item.url?.includes(sourceCanaries[1]))).toBe(true);
  expect(sourceInspection.pages[0].annotations.some((item) => item.subtype === "Widget" && item.fieldName === sourceCanaries[4])).toBe(true);
  expect(sourceInspection.pages.map((item) => item.text).join("|")).toContain(sourceCanaries[0]);
  expect(sourceInspection.javaScript).toContain(sourceCanaries[2]);
  expect(sourceInspection.attachmentText).toContain(sourceCanaries[3]);
  expect(sourceInspection.fields).toContain(sourceCanaries[4]);
  expect(sourceInspection.metadata).toContain(sourceCanaries[5]);

  await page.locator("#redactInput").setInputFiles({
    name: "canary.pdf",
    mimeType: "application/pdf",
    buffer: source,
  });
  await expect(page.locator("#redactEditor")).toBeVisible();
  for (let number = 1; number <= 4; number++) {
    await selectPageCenter(page, number);
    if (number < 4) await page.locator("#redactNext").click();
  }
  await page.locator("#redactConfirm").check();
  await expect(page.locator("#redactRun")).toBeEnabled();
  await page.locator("#redactRun").click();
  await expect(page.locator("#err")).toHaveText("");
  await expect(page.locator("#redactVerified")).toBeVisible({ timeout: 60_000 });
  expect(await page.evaluate(() => {
    const sourceURL = window.__redactObjectURLs.find((item) => item.name === "canary.pdf")?.url;
    return Boolean(sourceURL && window.__redactRevokedURLs.includes(sourceURL));
  })).toBe(true);
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download result" }).click();
  const download = await downloadPromise;
  const output = await readFile(await download.path());

  const outputInspection = await inspectPDF(page, output, { renderPixels: true });
  expect(outputInspection.numPages).toBe(4);
  expect(outputInspection.pages.map(({ width, height }) => [width, height])).toEqual([
    [160, 80],
    [80, 160],
    [320, 160],
    [160, 320],
  ]);
  for (const outputPage of outputInspection.pages) {
    expect(outputPage.annotations).toEqual([]);
    expect(outputPage.text).toBe("");
    expect(outputPage.center[0]).toBeLessThanOrEqual(2);
    expect(outputPage.center[1]).toBeLessThanOrEqual(2);
    expect(outputPage.center[2]).toBeLessThanOrEqual(2);
    expect(outputPage.center[3]).toBe(255);
  }
  expect(outputInspection.metadata).not.toContain("REDACT-");
  expect(outputInspection.javaScript).not.toContain("REDACT-");
  expect(outputInspection.attachmentText).toBe("");
  expect(outputInspection.fields).not.toContain("REDACT-");

  const raw = output.toString("latin1");
  for (const canary of sourceCanaries) expect(raw).not.toContain(canary);
  for (const forbidden of [
    "/Annots", "/OpenAction", "/JavaScript", "/EmbeddedFiles", "/AcroForm", "/Metadata",
    "/XFA", "/StructTreeRoot", "/CropBox", "/Rotate", "/UserUnit",
  ]) expect(raw).not.toContain(forbidden);
  expect(raw.match(/\d+ 0 obj/g)).toHaveLength(14);
});

test("localized redaction pages load the absolute shared module and stylesheet", async ({ page }) => {
  const badAssets = [];
  page.on("response", (response) => {
    const path = new URL(response.url()).pathname;
    if (path.includes("/redact/redact.") && response.status() >= 400) badAssets.push(path);
  });
  for (const locale of ["ko", "ja", "zh", "es", "fr", "de"]) {
    await page.goto(`/${locale}/redact/`);
    await expect(page.locator("#redactCancel")).toBeAttached();
    const assets = await page.locator('link[href$="redact.css"], script[src$="redact.mjs"]').evaluateAll((elements) =>
      elements.map((element) => new URL(element.href || element.src, location.href).pathname));
    expect(assets).toEqual(expect.arrayContaining(["/redact/redact.css", "/redact/redact.mjs"]));
  }
  expect(badAssets).toEqual([]);
});
