import assert from "node:assert/strict";
import { test } from "node:test";
import * as previewElementsModule from "./preview-elements.mjs";

const {
  classifyPreview,
  createPageSynchronizer,
  createPreviewComparison,
  createPreviewSurface,
  formatBytes,
  previewModeFor,
  previewModesForArtifacts,
} = previewElementsModule;

test("preview classification uses MIME then safe filename fallback", () => {
  assert.equal(classifyPreview("out.bin", "application/pdf"), "pdf");
  assert.equal(classifyPreview("out.png", ""), "image");
  assert.equal(classifyPreview("out.csv", "text/csv"), "text");
  assert.equal(classifyPreview("out.zip", "application/zip"), "summary");
  assert.equal(classifyPreview("out.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"), "summary");
});

test("byte formatting is deterministic", () => {
  assert.equal(formatBytes(0), "0 B");
  assert.equal(formatBytes(1024), "1.0 KiB");
  assert.equal(formatBytes(1536), "1.5 KiB");
});

test("capability routing permits rich adapters only for supported safe outputs", () => {
  const enabled = { capabilities: { preview: true } };
  const disabled = { capabilities: { preview: false } };
  assert.equal(previewModeFor(enabled, { name: "out.pdf", mime: "application/pdf", kind: "pdf" }), "pdf");
  assert.equal(previewModeFor(enabled, { name: "out.png", mime: "image/png", kind: "image" }), "image");
  assert.equal(previewModeFor(enabled, { name: "out.csv", mime: "text/csv", kind: "text" }), "text");
  assert.equal(previewModeFor(disabled, { name: "out.pdf", mime: "application/pdf", kind: "pdf" }), "summary");
  assert.equal(previewModeFor(enabled, { name: "out.zip", mime: "application/zip", kind: "zip" }), "summary");
  assert.equal(previewModeFor(enabled, { name: "out.docx", mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", kind: "docx" }), "summary");
  assert.equal(previewModeFor(enabled, { name: "locked.pdf", mime: "application/pdf", kind: "pdf", metadata: { encrypted: true } }), "summary");
});

test("combined rich preview bytes are bounded before decoding either pane", () => {
  const operation = { capabilities: { preview: true } };
  const artifacts = [
    { name: "before.pdf", mime: "application/pdf", kind: "pdf", size: 7, blob: new Blob(["1234567"]) },
    { name: "after.pdf", mime: "application/pdf", kind: "pdf", size: 6, blob: new Blob(["123456"]) },
  ];
  assert.deepEqual(previewModesForArtifacts(operation, artifacts, { byteLimit: 12 }), ["summary", "summary"]);
  assert.deepEqual(previewModesForArtifacts(operation, artifacts, { byteLimit: 13 }), ["pdf", "pdf"]);
});

test("comparison rejects an already-aborted signal before creating a surface", async () => {
  const abort = new AbortController();
  abort.abort();
  const blob = new Blob(["text"], { type: "text/plain" });
  await assert.rejects(createPreviewComparison({
    after: { blob, name: "out.txt", mime: "text/plain", kind: "text", size: blob.size, metadata: {} },
    operation: { capabilities: { preview: true } },
    signal: abort.signal,
    document: {},
  }), (error) => error?.name === "AbortError");
});

test("text preview uses textContent and truncates by a fixed byte limit", async () => {
  const elements = [];
  const document = {
    createElement(tag) {
      const element = { tagName: tag.toUpperCase(), textContent: "", className: "" };
      elements.push(element);
      return element;
    },
  };
  const surface = await createPreviewSurface({
    blob: new Blob(["<script>unsafe</script> and more"], { type: "text/plain" }),
    name: "out.txt",
    mime: "text/plain",
    size: 32,
  }, { document, mode: "text", textLimit: 12 });
  assert.equal(surface.element.tagName, "PRE");
  assert.equal(surface.element.textContent, "<script>unsa\n…");
  assert.equal("innerHTML" in surface.element, false);
  await surface.dispose();
});

test("image preview closes its ImageBitmap and clears its canvas", async () => {
  const bitmap = { width: 8, height: 6, closed: false, close() { this.closed = true; } };
  const drawn = [];
  const canvas = {
    width: 0,
    height: 0,
    className: "",
    getContext: () => ({ drawImage(value) { drawn.push(value); } }),
  };
  const surface = await createPreviewSurface({
    blob: new Blob(["image"], { type: "image/png" }),
    name: "out.png",
    mime: "image/png",
    size: 5,
  }, {
    document: { createElement: (tag) => { assert.equal(tag, "canvas"); return canvas; } },
    mode: "image",
    createImageBitmap: async () => bitmap,
  });
  assert.deepEqual(drawn, [bitmap]);
  await surface.dispose();
  assert.equal(bitmap.closed, true);
  assert.deepEqual([canvas.width, canvas.height], [0, 0]);
});

test("image preview rejects excessive pixels or dimensions before allocating a canvas", async () => {
  const bitmaps = [
    { width: 4_001, height: 4_000, closed: false, close() { this.closed = true; } },
    { width: 16_385, height: 1, closed: false, close() { this.closed = true; } },
  ];
  for (const bitmap of bitmaps) {
    const canvas = { width: 0, height: 0, className: "", getContext: () => ({ drawImage() {} }) };
    await assert.rejects(createPreviewSurface({
      blob: new Blob(["image"], { type: "image/png" }),
      name: "large.png",
      mime: "image/png",
      size: 5,
    }, {
      document: { createElement: () => canvas },
      mode: "image",
      createImageBitmap: async () => bitmap,
      imageMaxPixels: 16_000_000,
      imageMaxDimension: 16_384,
    }), /preview budget/);
    assert.equal(bitmap.closed, true);
    assert.deepEqual([canvas.width, canvas.height], [0, 0]);
  }
});

test("summary identifies encrypted PDFs, Office limitations, and bounded ZIP entry counts", async () => {
  const elements = [];
  const document = { createElement: () => { const element = { className: "", textContent: "" }; elements.push(element); return element; } };
  const encrypted = await createPreviewSurface({
    blob: new Blob(["pdf"]), name: "locked.pdf", mime: "application/pdf", size: 3, metadata: { encrypted: true },
  }, { document, mode: "summary" });
  assert.match(encrypted.element.textContent, /Encrypted PDF/);

  const office = await createPreviewSurface({
    blob: new Blob(["docx"]), name: "report.docx", mime: "application\/vnd.openxmlformats-officedocument.wordprocessingml.document", kind: "docx", size: 4,
  }, { document, mode: "summary" });
  assert.match(office.element.textContent, /Office document/);
  assert.match(office.element.textContent, /structure/i);
  const hwpx = await createPreviewSurface({
    blob: new Blob(["hwpx"]), name: "report.hwpx", mime: "application/hwp+zip", kind: "hwpx", size: 4,
  }, { document, mode: "summary" });
  assert.match(hwpx.element.textContent, /Office document/);
  assert.doesNotMatch(hwpx.element.textContent, /ZIP archive/);

  const eocd = new Uint8Array(22);
  const view = new DataView(eocd.buffer);
  view.setUint32(0, 0x06054b50, true);
  view.setUint16(8, 3, true);
  view.setUint16(10, 3, true);
  const zip = await createPreviewSurface({
    blob: new Blob([eocd], { type: "application/zip" }), name: "files.zip", mime: "application/zip", kind: "zip", size: eocd.length,
  }, { document, mode: "summary" });
  assert.match(zip.element.textContent, /ZIP archive/);
  assert.match(zip.element.textContent, /3 entries/);
});

test("summary text uses caller-provided localized labels", async () => {
  const element = { className: "", textContent: "" };
  const surface = await createPreviewSurface({
    blob: new Blob(["pdf"]), name: "locked.pdf", mime: "application/pdf", size: 3, metadata: { encrypted: true },
  }, {
    document: { createElement: () => element },
    mode: "summary",
    summaryLabels: {
      encryptedPDF: "암호화된 PDF",
      richUnavailable: "상세 미리보기 사용 불가",
    },
  });
  assert.match(surface.element.textContent, /암호화된 PDF/);
  assert.match(surface.element.textContent, /상세 미리보기 사용 불가/);
  assert.doesNotMatch(surface.element.textContent, /Encrypted PDF|Rich preview unavailable/);
});

test("PDF preview uses an object URL and destroys render work on dispose", async () => {
  const events = [];
  const canvas = { width: 0, height: 0, className: "", getContext() { return {}; } };
  const session = {
    numPages: 3,
    cancel() { events.push("cancel"); },
    async renderPage(page, options) {
      events.push(["render", page, options.canvas, options.maxPixels, options.maxDimension, options.cleanupPage]);
      return { canvas: options.canvas, dispose() {} };
    },
    async destroy() { events.push("destroy"); },
  };
  const renderer = {
    async open(source) {
      events.push(["open", source]);
      return session;
    },
  };
  const revoked = [];
  const surface = await createPreviewSurface({
    blob: new Blob(["pdf"], { type: "application/pdf" }),
    name: "out.pdf",
    mime: "application/pdf",
    size: 3,
  }, {
    document: { createElement: (tag) => { assert.equal(tag, "canvas"); return canvas; } },
    mode: "pdf",
    pdfRenderer: renderer,
    urlAPI: {
      createObjectURL: () => "blob:pdf",
      revokeObjectURL: (url) => revoked.push(url),
    },
  });
  assert.deepEqual(events[0], ["open", { url: "blob:pdf" }]);
  await surface.renderPage(2);
  assert.deepEqual(events.slice(1), ["cancel", ["render", 2, canvas, 16_000_000, 16_384, true]]);
  await surface.dispose();
  assert.deepEqual(events.slice(-2), ["cancel", "destroy"]);
  assert.deepEqual(revoked, ["blob:pdf"]);
  assert.deepEqual([canvas.width, canvas.height], [0, 0]);
});

test("page synchronizer clamps each PDF while keeping one shared page control", async () => {
  const pages = [];
  const first = { numPages: 4, renderPage: async (page) => pages.push(["before", page]) };
  const second = { numPages: 2, renderPage: async (page) => pages.push(["after", page]) };
  const sync = createPageSynchronizer([first, second]);
  assert.equal(sync.total, 4);
  await sync.setPage(3);
  assert.equal(sync.page, 3);
  assert.deepEqual(pages, [["before", 3], ["after", 2]]);
  await sync.setPage(99);
  assert.equal(sync.page, 4);
  assert.deepEqual(pages.slice(-2), [["before", 4], ["after", 2]]);
});

test("page synchronizer keeps the last displayed page when rendering fails", async () => {
  const sync = createPageSynchronizer([{
    numPages: 2,
    async renderPage(page) {
      if (page === 2) throw new Error("render failed");
    },
  }]);
  await sync.setPage(1);
  await assert.rejects(sync.setPage(2), /render failed/);
  assert.equal(sync.page, 1);
});
