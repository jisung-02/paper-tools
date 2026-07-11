import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

function pdfFixture(pages) {
  const objects = ["", ""];
  const kids = [];
  for (const page of pages) {
    const pageRef = objects.length + 1;
    const contentRef = pageRef + 1;
    kids.push(`${pageRef} 0 R`);
    const stream = page.stream || "";
    const rotate = page.rotate == null ? "" : ` /Rotate ${page.rotate}`;
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${page.width} ${page.height}]${rotate} /Resources << >> /Contents ${contentRef} 0 R >>`);
    objects.push(`<< /Length ${Buffer.byteLength(stream)} >>\nstream\n${stream}\nendstream`);
  }
  objects[0] = "<< /Type /Catalog /Pages 2 0 R >>";
  objects[1] = `<< /Type /Pages /Kids [${kids.join(" ")}] /Count ${kids.length} >>`;

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

test("visual PDF diff uses a Worker, filters changed pages and exports JSON plus heatmaps", async ({ page }) => {
  test.setTimeout(120_000);
  let workerRequests = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/pdfdiff/diff-worker.mjs") workerRequests++;
  });
  const common = "q 0 0 0 rg 20 20 40 40 re f Q";
  const original = pdfFixture([
    { width: 200, height: 200, stream: common },
    { width: 200, height: 200, stream: common },
  ]);
  const revised = pdfFixture([
    { width: 200, height: 200, stream: common },
    { width: 220, height: 200, stream: `${common}\nq 1 0 0 rg 120 80 30 30 re f Q` },
  ]);

  await page.goto("/pdfdiff/");
  await expect(page.locator("#status")).toHaveAttribute("aria-live", "polite");
  await expect(page.locator("#err")).toHaveAttribute("role", "alert");
  await expect(page.locator("#visualMode")).toHaveAttribute("aria-label", "Mode");
  await expect(page.locator("#visualThreshold")).toHaveAttribute("aria-label", "Threshold");
  await expect(page.locator("#visualChangedOnly")).toHaveAttribute("aria-label", "Changed pages only");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();

  await expect(page.locator("#visualResults")).toBeVisible();
  await expect(page.locator("#visualStats")).toContainText("1/2");
  await expect(page.locator("#visualStats")).toContainText("0 pixels");
  await expect(page.locator("#visualB")).toHaveCSS("opacity", "1");
  expect(workerRequests).toBeGreaterThan(0);

  await page.locator("#visualChangedOnly").check();
  await expect(page.locator("#visualStats")).toContainText("2/2");
  await expect(page.locator("#visualStats")).toContainText("page size changed");
  await expect(page.locator("#visualStats")).not.toContainText("0 pixels");

  await page.locator("#visualMode").selectOption("blink");
  await expect(page.locator("#visualResults")).toHaveAttribute("data-mode", "blink");
  await expect.poll(() => page.locator("#visualB").evaluate((canvas) => canvas.style.opacity), { timeout: 2_500 }).toBe("0");
  await expect.poll(() => page.locator("#visualB").evaluate((canvas) => canvas.style.opacity), { timeout: 1_500 }).toBe("1");
  await page.locator("#visualMode").selectOption("slider");
  await expect(page.locator("#visualResults")).toHaveAttribute("data-mode", "slider");
  await expect(page.locator("#visualB")).toHaveCSS("opacity", "0.5");
  await page.locator("#visualOpacity").evaluate((input) => {
    input.value = "25";
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await expect(page.locator("#visualB")).toHaveCSS("opacity", "0.25");
  await page.locator("#visualMode").selectOption("side");
  await expect(page.locator("#visualResults")).toHaveAttribute("data-mode", "side");
  await expect(page.locator("#visualB")).toHaveCSS("opacity", "1");
  await page.locator("#visualMode").selectOption("heatmap");
  await expect(page.locator("#visualHeatmap")).toBeVisible();

  const downloadPromise = page.waitForEvent("download");
  await page.locator("#visualExport").click();
  const archive = new Uint8Array(await readFile(await (await downloadPromise).path()));
  const entries = zipEntries(archive);
  expect(entries.map((entry) => entry.name)).toEqual(["heatmap-page-0002.png", "report.json"]);
  const report = JSON.parse(new TextDecoder().decode(entries[1].data));
  expect(report).toMatchObject({
    schema: "paper-tools-visual-diff-v1",
    comparedPages: 2,
    changedPages: 1,
  });
  expect(report.pages[1].pageSizeChanged).toBe(true);
  expect(report.pages[1].bounds).not.toBeNull();
});

test("changed-only mode reports an empty result with both navigation boundaries disabled", async ({ page }) => {
  const same = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: same });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: same });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualStats")).toContainText("0 pixels");

  await page.locator("#visualChangedOnly").check();
  await expect(page.locator("#visualNotice")).toContainText("No changed pages");
  await expect(page.locator("#visualStats")).toHaveText("");
  await expect(page.locator("#visualPrev")).toBeDisabled();
  await expect(page.locator("#visualNext")).toBeDisabled();
});

test("inserted pages align as a single changed page with exact changed-only boundaries", async ({ page }) => {
  const first = "q 0 0 0 rg 20 20 40 40 re f Q";
  const last = "q 0 0 0 rg 130 130 40 40 re f Q";
  const original = pdfFixture([
    { width: 200, height: 200, stream: first },
    { width: 200, height: 200, stream: last },
  ]);
  const revised = pdfFixture([
    { width: 200, height: 200, stream: first },
    { width: 300, height: 200, stream: "q 0 0 0 rg 70 70 40 40 re f Q" },
    { width: 200, height: 200, stream: last },
  ]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await page.locator("#visualChangedOnly").check();

  await expect(page.locator("#visualStats")).toContainText("2/3");
  await expect(page.locator("#visualStats")).not.toHaveText(/· 0 pixels/);
  await expect(page.locator("#visualPrev")).toBeDisabled();
  await expect(page.locator("#visualNext")).toBeDisabled();
});

test("rotated pages are detected through physical size and raster differences", async ({ page }) => {
  const stream = "q 0 0 0 rg 20 20 40 20 re f Q";
  const original = pdfFixture([{ width: 200, height: 100, stream }]);
  const rotated = pdfFixture([{ width: 200, height: 100, rotate: 90, stream }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: rotated });
  await page.locator("#visualRun").click();

  await expect(page.locator("#visualStats")).toContainText("page size changed");
  await expect(page.locator("#visualStats")).not.toContainText("0 pixels");
});

test("rapid setting changes export one internally consistent final option snapshot", async ({ page }) => {
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 41 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualStats")).toBeVisible();

  await page.evaluate(() => {
    const threshold = document.getElementById("visualThreshold");
    threshold.value = "1";
    threshold.dispatchEvent(new Event("change"));
    threshold.value = "64";
    threshold.dispatchEvent(new Event("change"));
  });
  await expect(page.locator("#visualExport")).toBeEnabled();
  const downloadPromise = page.waitForEvent("download");
  await page.locator("#visualExport").click();
  const archive = new Uint8Array(await readFile(await (await downloadPromise).path()));
  const reportEntry = zipEntries(archive).find((entry) => entry.name === "report.json");
  const report = JSON.parse(new TextDecoder().decode(reportEntry.data));
  expect(report.threshold).toBe(64);
});

test("cancelling an in-flight diff terminates its Worker and a new run uses a fresh Worker", async ({ page }) => {
  test.setTimeout(60_000);
  let workerRequests = 0;
  let workerStarted = 0;
  await page.route("**/__visual-worker-started", (route) => {
    workerStarted++;
    return route.fulfill({ status: 204, body: "" });
  });
  await page.route("**/pdfdiff/diff-worker.mjs", (route) => {
    workerRequests++;
    if (workerRequests === 1) {
      return route.fulfill({
        contentType: "text/javascript",
        body: 'self.onmessage = () => { fetch("/__visual-worker-started").catch(() => {}); };',
      });
    }
    return route.continue();
  });
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });

  await page.locator("#visualRun").click();
  await expect.poll(() => workerStarted).toBe(1);
  await page.locator("#visualCancel").click();
  await expect(page.locator("#visualNotice")).toHaveText("Visual comparison cancelled.");
  await expect(page.locator("#visualResults")).toBeHidden();

  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();
  await expect(page.locator("#visualStats")).toContainText("1/1");
  expect(workerRequests).toBeGreaterThan(1);
});

test("replacing either input invalidates rendered canvases and requires a fresh run", async ({ page }) => {
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  const replacement = pdfFixture([{ width: 220, height: 200, stream: "q 0 0 1 rg 30 30 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();

  await page.locator("#aInput").setInputFiles({ name: "replacement.pdf", mimeType: "application/pdf", buffer: replacement });
  await expect(page.locator("#visualResults")).toBeHidden();
  await expect(page.locator("#visualExport")).toBeDisabled();
  await expect(page.locator("#visualNotice")).toHaveText("Input PDFs changed. Run visual comparison again.");
  await expect(page.locator("#visualA")).toHaveJSProperty("width", 0);
  await expect(page.locator("#visualB")).toHaveJSProperty("width", 0);

  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();
  await expect(page.locator("#visualStats")).toContainText("1/1");
});

test("cancelling while an OPFS archive close is delayed removes the stale closed output", async ({ page, browserName }) => {
  test.skip(browserName === "webkit", "WebKit does not provide reliable native OPFS handles");
  test.setTimeout(60_000);
  await page.addInitScript(() => {
    const entries = new Set();
    const chunks = [];
    let releaseClose;
    window.__visualCloseStarted = false;
    window.__visualRemoved = [];
    window.__releaseVisualClose = () => releaseClose?.();
    const handle = {
      async createWritable() {
        return {
          async write(chunk) { chunks.push(new Uint8Array(chunk)); },
          async close() {
            window.__visualCloseStarted = true;
            await new Promise((resolve) => { releaseClose = resolve; });
          },
          async abort() {},
        };
      },
      async getFile() { return new Blob(chunks, { type: "application/zip" }); },
    };
    const root = {
      async getFileHandle(name, options = {}) {
        if (!options.create && !entries.has(name)) throw new DOMException("missing", "NotFoundError");
        entries.add(name);
        return handle;
      },
      async removeEntry(name) {
        entries.delete(name);
        window.__visualRemoved.push(name);
      },
    };
    Object.defineProperty(Object.getPrototypeOf(navigator.storage), "getDirectory", {
      configurable: true,
      value: async () => root,
    });
  });
  let downloads = 0;
  page.on("download", () => { downloads++; });
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();

  await page.locator("#visualExport").click();
  await expect.poll(() => page.evaluate(() => window.__visualCloseStarted)).toBe(true);
  await page.locator("#visualCancel").click();
  await page.evaluate(() => window.__releaseVisualClose());
  await expect.poll(() => page.evaluate(() => window.__visualRemoved)).toEqual(["paper-tools-visual-diff.zip"]);
  await page.waitForTimeout(500);
  expect(downloads).toBe(0);
  await expect(page.locator("#visualNotice")).toHaveText("Visual comparison cancelled.");
});

test("pagehide cleans a successfully downloaded OPFS archive before its delayed timer", async ({ page, browserName }) => {
  test.skip(browserName === "webkit", "WebKit does not provide reliable native OPFS handles");
  await page.addInitScript(() => {
    const entries = new Map();
    const records = [];
    window.__visualPagehideState = { records };
    const root = {
      async getFileHandle(name, options = {}) {
        if (!options.create) {
          const existing = entries.get(name);
          if (!existing) throw new DOMException("missing", "NotFoundError");
          return existing.handle;
        }
        const record = { name, closes: 0, aborts: 0, removes: 0, chunks: [], handle: null };
        record.handle = {
          async createWritable() {
            return {
              async write(chunk) { record.chunks.push(new Uint8Array(chunk)); },
              async close() { record.closes++; },
              async abort() { record.aborts++; },
            };
          },
          async getFile() { return new Blob(record.chunks, { type: "application/zip" }); },
        };
        entries.set(name, record);
        records.push(record);
        return record.handle;
      },
      async removeEntry(name) {
        const record = entries.get(name);
        if (record) record.removes++;
        entries.delete(name);
      },
    };
    Object.defineProperty(Object.getPrototypeOf(navigator.storage), "getDirectory", {
      configurable: true,
      value: async () => root,
    });
  });
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();

  const downloadPromise = page.waitForEvent("download");
  await page.locator("#visualExport").click();
  await downloadPromise;
  await expect.poll(() => page.evaluate(() => window.__visualPagehideState.records[0])).toMatchObject({
    closes: 1,
    aborts: 0,
    removes: 0,
  });

  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent("pagehide")));
  await expect.poll(() => page.evaluate(() => window.__visualPagehideState.records[0]?.removes)).toBe(1);
  const state = await page.evaluate(() => window.__visualPagehideState.records[0]);
  expect(state).toMatchObject({ closes: 1, aborts: 0, removes: 1 });
});

test("cancelled ZIP writes finish cleanup before an immediate re-export can start", async ({ page, browserName }) => {
  test.skip(browserName === "webkit", "WebKit does not provide reliable native OPFS handles");
  test.setTimeout(60_000);
  await page.addInitScript(() => {
    const records = new Map();
    const sinks = [];
    window.__zipWriteState = { getDirectoryCalls: 0, sinks };
    const root = {
      async getFileHandle(name, options = {}) {
        if (!options.create) {
          const existing = records.get(name);
          if (!existing) throw new DOMException("missing", "NotFoundError");
          return existing.handle;
        }
        const record = {
          name,
          writes: 0,
          aborts: 0,
          closes: 0,
          removes: 0,
          chunks: [],
          writeStarted: false,
          releaseWrite: null,
          handle: null,
        };
        record.handle = {
          async createWritable() {
            return {
              async write(chunk) {
                record.writes++;
                if (sinks.length === 1 && record.writes === 2) {
                  record.writeStarted = true;
                  await new Promise((resolve) => { record.releaseWrite = resolve; });
                }
                record.chunks.push(new Uint8Array(chunk));
              },
              async close() { record.closes++; },
              async abort() { record.aborts++; },
            };
          },
          async getFile() { return new Blob(record.chunks, { type: "application/zip" }); },
        };
        records.set(name, record);
        sinks.push(record);
        return record.handle;
      },
      async removeEntry(name) {
        const record = records.get(name);
        if (record) record.removes++;
        records.delete(name);
      },
    };
    window.__releaseFirstZipWrite = () => sinks[0]?.releaseWrite?.();
    Object.defineProperty(Object.getPrototypeOf(navigator.storage), "getDirectory", {
      configurable: true,
      value: async () => {
        window.__zipWriteState.getDirectoryCalls++;
        return root;
      },
    });
  });
  let downloads = 0;
  page.on("download", () => { downloads++; });
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();

  await page.locator("#visualExport").click();
  await expect.poll(() => page.evaluate(() => window.__zipWriteState.sinks[0]?.writeStarted)).toBe(true);
  await page.locator("#visualCancel").click();
  await expect(page.locator("#visualExport")).toBeDisabled();

  const immediateReexport = page.locator("#visualExport").click();
  await expect.poll(() => page.evaluate(() => window.__zipWriteState.sinks.length)).toBe(1);
  await page.evaluate(() => window.__releaseFirstZipWrite());
  await immediateReexport;
  await expect.poll(() => page.evaluate(() => window.__zipWriteState.sinks.length)).toBe(2);
  await expect.poll(() => downloads).toBe(1);

  const state = await page.evaluate(() => window.__zipWriteState);
  expect(state.sinks[0]).toMatchObject({ writes: 2, aborts: 1, closes: 0, removes: 1 });
  expect(state.sinks[1]).toMatchObject({ aborts: 0, closes: 1, removes: 0 });
  expect(state.getDirectoryCalls).toBe(2);
});

test("a failed restart clears an existing blink timer before the new Worker fails", async ({ page }) => {
  let workerRequests = 0;
  await page.route("**/pdfdiff/diff-worker.mjs", (route) => {
    workerRequests++;
    if (workerRequests === 1) return route.continue();
    return route.fulfill({
      contentType: "text/javascript",
      body: 'self.onmessage = () => { throw new Error("RESTART-FATAL-CANARY"); };',
    });
  });
  const original = pdfFixture([{ width: 200, height: 200, stream: "q 0 0 0 rg 20 20 40 40 re f Q" }]);
  const revised = pdfFixture([{ width: 200, height: 200, stream: "q 1 0 0 rg 20 20 40 40 re f Q" }]);
  await page.goto("/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: original });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: revised });
  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();
  await page.locator("#visualMode").selectOption("blink");

  await page.locator("#visualRun").click();
  await expect(page.locator("#err")).toHaveText("The visual comparison Worker failed.", { timeout: 20_000 });
  const opacitySamples = await page.locator("#visualB").evaluate(async (canvas) => {
    const values = [];
    for (let index = 0; index < 8; index++) {
      values.push(canvas.style.opacity);
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    return values;
  });
  expect(new Set(opacitySamples).size).toBe(1);

  await page.locator("#visualRun").click();
  await expect(page.locator("#visualResults")).toBeVisible();
  await expect(page.locator("#visualStats")).toContainText("1/1");
  expect(workerRequests).toBeGreaterThanOrEqual(3);
});

function zipEntries(bytes) {
  const eocd = findLastSignature(bytes, 0x06054b50);
  const count = readU16(bytes, eocd + 10);
  let centralOffset = readU32(bytes, eocd + 16);
  const entries = [];
  for (let index = 0; index < count; index++) {
    expect(readU32(bytes, centralOffset)).toBe(0x02014b50);
    const size = readU32(bytes, centralOffset + 20);
    const nameLength = readU16(bytes, centralOffset + 28);
    const extraLength = readU16(bytes, centralOffset + 30);
    const commentLength = readU16(bytes, centralOffset + 32);
    const localOffset = readU32(bytes, centralOffset + 42);
    const name = new TextDecoder().decode(bytes.subarray(centralOffset + 46, centralOffset + 46 + nameLength));
    const localNameLength = readU16(bytes, localOffset + 26);
    const localExtraLength = readU16(bytes, localOffset + 28);
    const dataOffset = localOffset + 30 + localNameLength + localExtraLength;
    entries.push({ name, data: bytes.slice(dataOffset, dataOffset + size) });
    centralOffset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

function readU16(bytes, offset) {
  return bytes[offset] | (bytes[offset + 1] << 8);
}

function readU32(bytes, offset) {
  return (
    bytes[offset]
    | (bytes[offset + 1] << 8)
    | (bytes[offset + 2] << 16)
    | (bytes[offset + 3] << 24)
  ) >>> 0;
}

function findLastSignature(bytes, signature) {
  for (let offset = bytes.length - 4; offset >= 0; offset--) {
    if (readU32(bytes, offset) === signature) return offset;
  }
  throw new Error("ZIP signature not found");
}
