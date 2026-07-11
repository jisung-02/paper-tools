import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

const pixel = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  "base64",
);

function twoPagePdf() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
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

function tallPdf() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1000000] >>",
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

async function startGatedImagePreview(page, name = "gated.png") {
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    const decode = window.createImageBitmap;
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    window.__releasePreviewDecode = release;
    window.__previewDecodeStarts = 0;
    window.createImageBitmap = async (...args) => {
      window.__previewDecodeStarts++;
      if (window.__previewDecodeStarts === 1) await gate;
      return decode(...args);
    };
    window.finish({ data: Uint8Array.from(bytes) }, name, document.getElementById("err"), "image/png");
  }, [...pixel]);
  await page.waitForFunction(() => window.__previewDecodeStarts === 1);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    const nativeBitmap = window.createImageBitmap.bind(window);
    const nativeObjectURL = URL.createObjectURL.bind(URL);
    const nativeRevokeObjectURL = URL.revokeObjectURL.bind(URL);
    window.__previewBitmapBlobs = [];
    window.__previewObjectURLBlobs = [];
    window.__previewObjectURLs = [];
    window.__previewRevokedURLs = [];
    window.createImageBitmap = async (...args) => {
      window.__previewBitmapBlobs.push(args[0]);
      return nativeBitmap(...args);
    };
    URL.createObjectURL = (blob) => {
      window.__previewObjectURLBlobs.push(blob);
      const url = nativeObjectURL(blob);
      window.__previewObjectURLs.push(url);
      return url;
    };
    URL.revokeObjectURL = (url) => {
      window.__previewRevokedURLs.push(url);
      nativeRevokeObjectURL(url);
    };
  });
});

test("preview and explicit download reuse the exact output Blob and settings changes mark it stale", async ({ page }) => {
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "pixel-resized.png", document.getElementById("err"), "image/png");
  }, [...pixel]);

  const preview = page.locator(".result-preview");
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute("data-stale", "false");
  await expect(preview.locator("canvas")).toHaveCount(2);

  const downloadPromise = page.waitForEvent("download");
  await preview.getByRole("button", { name: "Download result" }).click();
  const downloaded = await downloadPromise;
  expect(await readFile(await downloaded.path())).toEqual(pixel);
  expect(await page.evaluate(() => {
    const downloadedBlob = window.__previewObjectURLBlobs.at(-1);
    return window.__previewBitmapBlobs.some((blob) => blob === downloadedBlob);
  })).toBe(true);

  await page.locator("#maxW").fill("800");
  await expect(preview).toHaveAttribute("data-stale", "true");
  await expect(preview.locator(".result-preview-stale")).toBeVisible();
  await expect(preview.getByRole("button", { name: "Download result" })).toBeDisabled();

  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "pixel-rerun.png", document.getElementById("err"), "image/png");
  }, [...pixel]);
  await expect(page.locator(".result-preview")).toHaveCount(1);
  await expect(page.locator(".result-preview")).toHaveAttribute("data-stale", "false");
  await expect(page.getByRole("button", { name: "Download result" })).toBeEnabled();
});

test("operations without preview capability render summary only", async ({ page }) => {
  await page.goto("/protect/");
  await page.evaluate(() => {
    window.finish({ data: new Uint8Array([1, 2, 3]) }, "protected.pdf", document.getElementById("err"), "application/pdf");
  });
  const preview = page.locator(".result-preview");
  await expect(preview).toBeVisible();
  await expect(preview.locator(".result-preview-summary")).toHaveCount(1);
  await expect(preview.locator("canvas, iframe, img")).toHaveCount(0);
});

test("a settings change while an operation is running makes its eventual result stale", async ({ page }) => {
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    window.__releasePreviewRun = release;
    window.__previewRun = window.run(document.getElementById("run"), async () => {
      window.__previewOperationStarted = true;
      await gate;
      window.finish({ data: Uint8Array.from(bytes) }, "pixel-resized.png", document.getElementById("err"), "image/png");
    });
  }, [...pixel]);
  await page.waitForFunction(() => window.__previewOperationStarted === true);
  await page.locator("#maxW").fill("640");
  await page.evaluate(() => window.__releasePreviewRun());

  const preview = page.locator(".result-preview");
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute("data-stale", "true");
  await expect(preview.getByRole("button", { name: "Download result" })).toBeDisabled();
});

test("a stale snapshot never auto-downloads when preview construction fails", async ({ page }) => {
  let downloads = 0;
  page.on("download", () => { downloads++; });
  const pdf = tallPdf();
  await page.goto("/rotate/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "tall.pdf",
    mimeType: "application/pdf",
    buffer: pdf,
  });
  await page.evaluate((bytes) => {
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    window.__releaseStaleFailure = release;
    window.__staleFailureRun = window.run(document.getElementById("run"), async () => {
      window.__staleFailureStarted = true;
      await gate;
      window.finish({ data: Uint8Array.from(bytes) }, "stale.pdf", document.getElementById("err"), "application/pdf");
    });
  }, [...pdf]);
  await page.waitForFunction(() => window.__staleFailureStarted === true);
  await page.locator("#ranges").fill("1");
  await page.evaluate(() => window.__releaseStaleFailure());
  await page.evaluate(() => window.__staleFailureRun);
  await page.waitForTimeout(150);
  expect(downloads).toBe(0);
  await expect(page.locator(".result-preview")).toHaveCount(0);
});

test("a failed snapshot module never downloads output made stale during the operation", async ({ page }) => {
  let downloads = 0;
  page.on("download", () => { downloads++; });
  await page.route(/\/preview-controller\.mjs(?:\?.*)?$/, (route) => route.abort("failed"));
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    window.__releaseFailedSnapshotRun = release;
    window.__failedSnapshotRun = window.run(document.getElementById("run"), async () => {
      window.__failedSnapshotOperationStarted = true;
      await gate;
      window.finish({ data: Uint8Array.from(bytes) }, "stale-module-failure.png", document.getElementById("err"), "image/png");
    });
  }, [...pixel]);
  await page.waitForFunction(() => window.__failedSnapshotOperationStarted === true);
  await page.locator("#maxW").fill("640");
  await page.evaluate(() => window.__releaseFailedSnapshotRun());
  await page.evaluate(() => window.__failedSnapshotRun);
  await page.waitForTimeout(150);

  expect(downloads).toBe(0);
  await expect(page.locator(".result-preview")).toHaveCount(0);
});

test("legacy download calls made by an operation enter the shared preview result path", async ({ page }) => {
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    window.__legacyOperationCalls = 0;
    window.__legacyDownloadRun = window.run(document.getElementById("run"), async () => {
      window.__legacyOperationCalls++;
      window.download(Uint8Array.from(bytes), "legacy.png", "image/png");
    });
  }, [...pixel]);
  await page.evaluate(() => window.__legacyDownloadRun);

  const preview = page.locator(".result-preview");
  await expect(preview).toBeVisible();
  await expect(preview.locator("canvas")).toHaveCount(2);
  await expect(preview.getByRole("button", { name: "Download result" })).toBeEnabled();
  expect(await page.evaluate(() => window.__legacyOperationCalls)).toBe(1);
});

test("OCR text and pdf2img ZIP legacy outputs keep their preview routes", async ({ page }) => {
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.goto("/ocr/");
  await page.evaluate(() => {
    window.__ocrLegacyRun = window.run(document.getElementById("run"), async () => {
      window.download(new TextEncoder().encode("ocr text"), "ocr-text.txt", "text/plain;charset=utf-8");
    });
  });
  await page.evaluate(() => window.__ocrLegacyRun);
  await expect(page.locator(".result-preview-text")).toHaveText("ocr text");

  await page.goto("/pdf2img/");
  await page.evaluate(() => {
    const zip = new Uint8Array(22);
    const view = new DataView(zip.buffer);
    view.setUint32(0, 0x06054b50, true);
    window.__pdf2imgLegacyRun = window.run(document.getElementById("run"), async () => {
      window.download(zip, "pdf-pages.zip", "application/zip");
    });
  });
  await page.evaluate(() => window.__pdf2imgLegacyRun);
  await expect(page.locator(".result-preview-summary")).toContainText("ZIP archive");
  await expect(page.locator(".result-preview-summary")).toContainText("0 entries");
  expect(pageErrors).toEqual([]);
});

test("rapid results retain only the newest output", async ({ page }) => {
  await page.goto("/pdftext/");
  await page.evaluate(() => {
    window.finish({ data: new TextEncoder().encode("first") }, "first.txt", document.getElementById("err"), "text/plain");
    window.finish({ data: new TextEncoder().encode("second") }, "second.txt", document.getElementById("err"), "text/plain");
  });
  await expect(page.locator(".result-preview")).toHaveCount(1);
  await expect(page.locator(".result-preview-text")).toHaveText("second");
});

test("pagehide invalidates a pending result presentation", async ({ page }) => {
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.route(/\/preview-controller\.mjs(?:\?.*)?$/, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 150));
    await route.continue();
  });
  await page.goto("/pdftext/");
  await page.evaluate(() => {
    window.finish({ data: new TextEncoder().encode("late") }, "late.txt", document.getElementById("err"), "text/plain");
    window.dispatchEvent(new PageTransitionEvent("pagehide"));
  });
  await page.waitForTimeout(300);
  await expect(page.locator(".result-preview")).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});

test("mutation aborts a pending image comparison before the second decode", async ({ page }) => {
  await startGatedImagePreview(page);
  await page.locator("#maxW").fill("640");
  await page.evaluate(() => window.__releasePreviewDecode());
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => window.__previewDecodeStarts)).toBe(1);
  await expect(page.locator(".result-preview")).toHaveCount(0);
});

test("pagehide aborts a pending image comparison before the second decode", async ({ page }) => {
  await startGatedImagePreview(page);
  await page.evaluate(() => {
    window.dispatchEvent(new PageTransitionEvent("pagehide"));
    window.__releasePreviewDecode();
  });
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => window.__previewDecodeStarts)).toBe(1);
  await expect(page.locator(".result-preview")).toHaveCount(0);
});

test("rapid replacement aborts old decoding and renders only the new comparison", async ({ page }) => {
  await startGatedImagePreview(page, "old.png");
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "new.png", document.getElementById("err"), "image/png");
  }, [...pixel]);
  await page.waitForFunction(() => window.__previewDecodeStarts >= 2);
  await page.evaluate(() => window.__releasePreviewDecode());
  await expect(page.locator(".result-preview")).toHaveCount(1);
  await expect(page.locator(".result-preview canvas")).toHaveCount(2);
  await page.waitForTimeout(100);
  expect(await page.evaluate(() => window.__previewDecodeStarts)).toBe(3);
});

test("preview module failure downloads the exact output Blob and a later attempt retries", async ({ page }) => {
  const previewControllerRequest = /\/preview-controller\.mjs(?:\?.*)?$/;
  await page.route(previewControllerRequest, (route) => route.abort("failed"));
  await page.goto("/imgresize/");
  const firstDownload = page.waitForEvent("download", { timeout: 3_000 });
  await page.evaluate((bytes) => {
    window.__fallbackOutputBlob = new Blob([Uint8Array.from(bytes)], { type: "image/png" });
    window.__fallbackRun = window.run(document.getElementById("run"), async () => {
      window.finish({ data: window.__fallbackOutputBlob }, "fallback.png", document.getElementById("err"), "image/png");
    });
  }, [...pixel]);
  const downloaded = await firstDownload;
  expect(await readFile(await downloaded.path())).toEqual(pixel);
  expect(await page.evaluate(() => window.__previewObjectURLBlobs.at(-1) === window.__fallbackOutputBlob)).toBe(true);
  await expect(page.locator(".result-preview")).toHaveCount(0);

  await page.unroute(previewControllerRequest);
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "retry.png", document.getElementById("err"), "image/png");
  }, [...pixel]);
  await expect(page.locator(".result-preview")).toBeVisible();
});

test("retry recovers after the shared artifact module failed", async ({ page }) => {
  let artifactRequests = 0;
  let failArtifact = true;
  const artifactRequest = /\/artifact\.mjs(?:\?.*)?$/;
  await page.route(artifactRequest, (route) => {
    artifactRequests++;
    return failArtifact ? route.abort("failed") : route.continue();
  });
  await page.goto("/imgresize/");
  const fallback = page.waitForEvent("download", { timeout: 3_000 });
  await page.evaluate((bytes) => {
    window.__artifactFailureRun = window.run(document.getElementById("run"), async () => {
      window.finish({ data: Uint8Array.from(bytes) }, "artifact-fallback.png", document.getElementById("err"), "image/png");
    });
  }, [...pixel]);
  await fallback;
  await page.evaluate(() => window.__artifactFailureRun);
  const failedRequests = artifactRequests;
  expect(failedRequests).toBeGreaterThan(0);

  failArtifact = false;
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "artifact-retry.png", document.getElementById("err"), "image/png");
  }, [...pixel]);
  await expect(page.locator(".result-preview")).toBeVisible();
  expect(artifactRequests).toBeGreaterThan(failedRequests);
});

test("retry recovers after the PDF renderer fingerprint dependency failed", async ({ page }) => {
  const pdf = twoPagePdf();
  let fingerprintRequests = 0;
  let failFingerprint = true;
  await page.route(/\/text-fingerprint\.mjs(?:\?.*)?$/, (route) => {
    fingerprintRequests++;
    return failFingerprint ? route.abort("failed") : route.continue();
  });
  await page.goto("/rotate/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "before.pdf",
    mimeType: "application/pdf",
    buffer: pdf,
  });
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "first.pdf", document.getElementById("err"), "application/pdf");
  }, [...pdf]);
  await expect(page.locator(".result-preview-summary")).toHaveCount(2);
  const failedRequests = fingerprintRequests;
  expect(failedRequests).toBeGreaterThan(0);

  failFingerprint = false;
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "second.pdf", document.getElementById("err"), "application/pdf");
  }, [...pdf]);
  await expect(page.locator(".result-preview canvas")).toHaveCount(2);
  expect(fingerprintRequests).toBeGreaterThan(failedRequests);
});

test("PDF comparison shares page controls and releases both document URLs on close", async ({ page }) => {
  const pdf = twoPagePdf();
  await page.goto("/rotate/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "two-pages.pdf",
    mimeType: "application/pdf",
    buffer: pdf,
  });
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "rotated.pdf", document.getElementById("err"), "application/pdf");
  }, [...pdf]);

  const preview = page.locator(".result-preview");
  await expect(preview.locator("canvas")).toHaveCount(2);
  const pageNumber = preview.getByRole("spinbutton", { name: "Page" });
  await expect(pageNumber).toHaveValue("1");
  await preview.getByRole("button", { name: "Next page" }).click();
  await expect(pageNumber).toHaveValue("2");
  const urls = await page.evaluate(() => [...window.__previewObjectURLs]);
  expect(urls).toHaveLength(2);

  await preview.getByRole("button", { name: "Close" }).click();
  await expect(preview).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => [...window.__previewRevokedURLs])).toEqual(expect.arrayContaining(urls));
});

test("PDF page navigation reports render errors without an unhandled rejection", async ({ page }) => {
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.goto("/imgresize/");
  await page.evaluate(async () => {
    const { createPreviewComparison } = await import("/preview-elements.mjs");
    const blob = new Blob(["pdf"], { type: "application/pdf" });
    const artifact = { blob, name: "two-pages.pdf", mime: "application/pdf", kind: "pdf", size: blob.size, metadata: {} };
    const renderer = {
      async open() {
        return {
          numPages: 2,
          cancel() {},
          async renderPage(pageNumber, { canvas }) {
            if (pageNumber === 2) throw new Error("forced page render failure");
            canvas.width = 1;
            canvas.height = 1;
            return { canvas, dispose() {} };
          },
          async destroy() {},
        };
      },
    };
    const view = await createPreviewComparison({
      before: artifact,
      after: artifact,
      operation: { capabilities: { preview: true } },
      pdfRenderer: renderer,
    });
    document.querySelector("main").append(view.element);
  });

  const preview = page.locator(".result-preview");
  await preview.getByRole("button", { name: "Next page" }).click();
  await expect(preview.locator(".result-preview-render-error")).toContainText("forced page render failure");
  await expect(preview.getByRole("spinbutton", { name: "Page" })).toHaveValue("1");
  await expect(preview.getByRole("button", { name: "Next page" })).toBeEnabled();
  await page.waitForTimeout(100);
  expect(pageErrors).toEqual([]);
});

test("mobile preview switches before and after panes with tabs", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/imgresize/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "pixel.png",
    mimeType: "image/png",
    buffer: pixel,
  });
  await page.evaluate((bytes) => {
    window.finish({ data: Uint8Array.from(bytes) }, "pixel-resized.png", document.getElementById("err"), "image/png");
  }, [...pixel]);

  const preview = page.locator(".result-preview");
  const tabs = preview.getByRole("tab");
  await expect(tabs).toHaveCount(2);
  await expect(tabs.nth(1)).toHaveAttribute("aria-selected", "true");
  const beforeId = await tabs.nth(0).getAttribute("id");
  const afterId = await tabs.nth(1).getAttribute("id");
  expect(beforeId).toBeTruthy();
  expect(afterId).toBeTruthy();
  await expect(preview.locator('[data-preview-pane="before"]')).toHaveAttribute("aria-labelledby", beforeId);
  await expect(preview.locator('[data-preview-pane="after"]')).toHaveAttribute("aria-labelledby", afterId);
  await tabs.nth(1).focus();
  await page.keyboard.press("ArrowLeft");
  await expect(tabs.nth(0)).toBeFocused();
  await expect(tabs.nth(0)).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("End");
  await expect(tabs.nth(1)).toBeFocused();
  await expect(tabs.nth(1)).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Home");
  await expect(tabs.nth(0)).toBeFocused();
  await tabs.nth(0).click();
  await expect(tabs.nth(0)).toHaveAttribute("aria-selected", "true");
  await expect(preview.locator('[data-preview-pane="before"]')).toBeVisible();
  await expect(preview.locator('[data-preview-pane="after"]')).toBeHidden();
});
