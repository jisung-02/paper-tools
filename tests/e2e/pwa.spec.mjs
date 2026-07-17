import { chromium, expect, test } from "@playwright/test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromiumLaunchOptions } from "./chromium-launch.mjs";

function previewPdf() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
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

test("automatic language redirect does not cause a page error", async ({ browser }) => {
  const context = await browser.newContext({ locale: "ko-KR" });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/txt2pdf/");
  await page.waitForURL("**/ko/txt2pdf/");
  await page.waitForTimeout(250);
  expect(errors).toEqual([]);
  await context.close();
});

test("service worker cache survives a browser restart for offline use", async () => {
  test.skip(test.info().project.name !== "chromium", "persistent offline coverage uses Chromium");
  test.setTimeout(60_000);
  const profile = await mkdtemp(join(tmpdir(), "papertools-e2e-"));
  try {
    let context = await chromium.launchPersistentContext(profile, chromiumLaunchOptions());
    let page = context.pages()[0] || (await context.newPage());
    await page.goto("/txt2pdf/");
    await page.waitForFunction(() => navigator.serviceWorker.controller !== null);
    await page.reload();
    // Closing right after the reload races the SW's async cache.put of the
    // wasm and font; if it loses, the offline restart below can never
    // enable the button. Wait until the page is usable AND both heavyweight
    // assets are provably in a SW cache.
    await expect(page.locator("#run")).toBeEnabled({ timeout: 20_000 });
    await page.waitForFunction(async () => {
      for (const name of await caches.keys()) {
        const cache = await caches.open(name);
        if ((await cache.match("/txt2pdf/txt2pdf.wasm")) && (await cache.match("/NanumGothic-Regular.ttf"))) return true;
      }
      return false;
    }, undefined, { timeout: 20_000 });
    await context.close();

    context = await chromium.launchPersistentContext(profile, chromiumLaunchOptions());
    await context.setOffline(true);
    page = context.pages()[0] || (await context.newPage());
    await page.goto("/txt2pdf/");
    // Cold browser launch + offline wasm instantiation from the SW cache can
    // exceed the default 5s expect timeout under parallel CI load.
    await expect(page.locator("#run")).toBeEnabled({ timeout: 20_000 });
    await context.close();
  } finally {
    await rm(profile, { force: true, recursive: true });
  }
});

test("one online page load prewarms preview assets for an offline PDF result", async () => {
  test.skip(test.info().project.name !== "chromium", "persistent offline coverage uses Chromium");
  const profile = await mkdtemp(join(tmpdir(), "papertools-preview-offline-"));
  let context;
  try {
    context = await chromium.launchPersistentContext(profile, chromiumLaunchOptions());
    const page = context.pages()[0] || (await context.newPage());
    const pageErrors = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.goto("/txt2pdf/");
    await page.waitForFunction(() => navigator.serviceWorker.controller !== null);
    const required = [
      "/preview.css",
      "/preview-controller.mjs",
      "/preview-elements.mjs",
      "/artifact.mjs",
      "/operation-catalog.mjs",
      "/operation-catalog.json",
      "/pdf-renderer.mjs",
      "/vendor/pdfjs/pdf.mjs",
      "/vendor/pdfjs/pdf.worker.mjs",
    ];
    await expect.poll(() => page.evaluate(async () => {
      const paths = [];
      for (const name of await caches.keys()) {
        const cache = await caches.open(name);
        for (const request of await cache.keys()) paths.push(new URL(request.url).pathname);
      }
      return [...new Set(paths)];
    }), { timeout: 10_000 }).toEqual(expect.arrayContaining(required));

    await context.setOffline(true);
    const pdf = previewPdf();
    await page.evaluate((bytes) => {
      window.__offlinePreviewRun = window.run(document.getElementById("run"), async () => {
        window.finish({ data: Uint8Array.from(bytes) }, "offline.pdf", document.getElementById("err"), "application/pdf");
      });
    }, [...pdf]);
    await page.evaluate(() => window.__offlinePreviewRun);
    const preview = page.locator(".result-preview");
    await expect(preview.locator("canvas")).toHaveCount(1);
    const downloadPromise = page.waitForEvent("download");
    await preview.getByRole("button", { name: "Download result" }).click();
    const downloaded = await downloadPromise;
    expect(await readFile(await downloaded.path())).toEqual(pdf);
    expect(pageErrors).toEqual([]);
  } finally {
    await context?.close();
    await rm(profile, { force: true, recursive: true });
  }
});
