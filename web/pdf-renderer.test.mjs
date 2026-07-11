import assert from "node:assert/strict";
import { test } from "node:test";
import { createPdfRenderer } from "./pdf-renderer.mjs";
import { fingerprintText } from "./pdfdiff/page-align.mjs";

function textStream(chunks, onCancel = () => {}) {
  return new ReadableStream({
    start(controller) {
      for (const items of chunks) controller.enqueue({ items });
      controller.close();
    },
    cancel: onCancel,
  });
}

function trackedTextStream(chunks, onCancel = () => {}) {
  return {
    getReader() {
      let index = 0;
      return {
        async read() {
          if (index >= chunks.length) return { done: true };
          return { done: false, value: { items: chunks[index++] } };
        },
        async cancel(reason) { onCancel(reason); },
        releaseLock() {},
      };
    },
  };
}

test("PDF renderer releases completed outputs and cancels active work", async () => {
  const module = await import("./pdf-renderer.mjs").catch(() => ({}));
  assert.equal(typeof module.createPdfRenderer, "function", "createPdfRenderer must be exported");

  const canvases = [];
  const renderTasks = [];
  const bitmaps = [];
  const revoked = [];
  let cleanupCalls = 0;
  let destroyCalls = 0;
  let renderCount = 0;

  const page = {
    getViewport: ({ scale }) => ({ width: 200 * scale, height: 100 * scale }),
    streamTextContent: () => textStream([[{ str: "Alpha" }], [{ str: "Beta" }]]),
    getTextContent: async () => { throw new Error("full TextContent must not be materialized"); },
    render() {
      const task = { cancelled: false };
      renderCount++;
      if (renderCount < 3) {
        task.promise = Promise.resolve();
      } else {
        task.promise = new Promise((resolve, reject) => {
          task.cancel = () => {
            task.cancelled = true;
            reject(new Error("render cancelled"));
          };
        });
      }
      task.cancel ||= () => { task.cancelled = true; };
      renderTasks.push(task);
      return task;
    },
  };
  const doc = {
    numPages: 1,
    getPage: async () => page,
    cleanup: async () => { cleanupCalls++; },
  };
  const loadingTask = {
    promise: Promise.resolve(doc),
    destroy: async () => { destroyCalls++; },
  };
  const pdfjs = { getDocument: () => loadingTask };
  const createCanvas = () => {
    const context = { fillRect() {}, fillStyle: "" };
    const canvas = { width: 0, height: 0, getContext: () => context };
    canvases.push(canvas);
    return canvas;
  };
  const createImageBitmap = async (canvas) => {
    const bitmap = { width: canvas.width, height: canvas.height, closed: false, close() { this.closed = true; } };
    bitmaps.push(bitmap);
    return bitmap;
  };

  const renderer = module.createPdfRenderer(pdfjs, {
    createCanvas,
    createImageBitmap,
    canvasToBlob: async () => new Blob(["page"]),
    createObjectURL: () => "blob:page-1",
    revokeObjectURL: (url) => revoked.push(url),
  });
  const session = await renderer.open(new Uint8Array([1, 2, 3]));
  assert.equal(session.numPages, 1);
  assert.deepEqual(await session.getPageInfo(1, { includeTextFingerprint: true }), {
    width: 200,
    height: 100,
    textFingerprint: fingerprintText("Alpha Beta"),
    textItems: 2,
    textChars: 10,
    textBytes: 10,
    textLimitExceeded: false,
  });

  const bitmapOutput = await session.renderPage(1, { width: 100, pixelRatio: 2, output: "bitmap" });
  assert.deepEqual([bitmapOutput.width, bitmapOutput.height], [200, 100]);
  bitmapOutput.dispose();
  assert.equal(bitmaps[0].closed, true);

  const urlOutput = await session.renderPage(1, { scale: 1, output: "object-url" });
  assert.equal(urlOutput.url, "blob:page-1");
  urlOutput.dispose();
  assert.deepEqual(revoked, ["blob:page-1"]);

  const pending = session.renderPage(1, { scale: 1 });
  while (renderTasks.length < 3) await new Promise((resolve) => setImmediate(resolve));
  session.cancel();
  await assert.rejects(pending, (error) => error?.name === "AbortError");
  assert.deepEqual(renderTasks.map((task) => task.cancelled), [false, false, true]);

  await session.destroy();
  assert.equal(cleanupCalls, 1);
  assert.equal(destroyCalls, 1);
  assert.ok(canvases.every((canvas) => canvas.width === 0 && canvas.height === 0));
});

test("PDF text fingerprints stream the full page and distinguish suffixes beyond the old prefix", async () => {
  const prefix = "same-prefix-".repeat(800);
  let suffix = "alpha";
  let fullTextCalls = 0;
  const page = {
    getViewport: ({ scale }) => ({ width: 100 * scale, height: 100 * scale }),
    streamTextContent: () => textStream([
      [{ str: prefix.slice(0, 4_000) }],
      [{ str: prefix.slice(4_000) }],
      [{ str: suffix }],
    ]),
    async getTextContent() { fullTextCalls++; throw new Error("must not run"); },
  };
  const renderer = createPdfRenderer({
    getDocument: () => ({
      promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
      async destroy() {},
    }),
  });
  const session = await renderer.open(new Uint8Array([1]));
  const first = await session.getPageInfo(1, { includeTextFingerprint: true, cleanupPage: true });
  suffix = "bravo";
  const second = await session.getPageInfo(1, { includeTextFingerprint: true, cleanupPage: true });

  assert.notEqual(first.textFingerprint, second.textFingerprint);
  assert.equal(first.textChars, second.textChars);
  assert.equal(fullTextCalls, 0);
  await session.destroy();
});

test("PDF text streaming applies item, character and UTF-8 byte caps without returning a partial hash", async () => {
  const limits = [
    [{ maxTextItems: 1 }, [[{ str: "a" }, { str: "b" }]]],
    [{ maxTextChars: 3 }, [[{ str: "abcd" }]]],
    [{ maxTextBytes: 3 }, [[{ str: "한" }, { str: "a" }]]],
  ];
  for (const [options, chunks] of limits) {
    let cancelled = 0;
    const page = {
      getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }),
      streamTextContent: () => trackedTextStream(chunks, () => { cancelled++; }),
    };
    const renderer = createPdfRenderer({
      getDocument: () => ({
        promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
        async destroy() {},
      }),
    });
    const session = await renderer.open(new Uint8Array([1]));
    const info = await session.getPageInfo(1, {
      includeTextFingerprint: true,
      cleanupPage: true,
      ...options,
    });
    assert.equal(info.textFingerprint, null);
    assert.equal(info.textLimitExceeded, true);
    assert.equal(cancelled, 1);
    await session.destroy();
  }
});

test("PDF text streaming cancels pending readers on AbortSignal and session cancellation", async () => {
  for (const cancelWith of ["signal", "session"]) {
    let releaseRead;
    let cancelCalls = 0;
    let cleanupCalls = 0;
    const page = {
      getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }),
      cleanup() { cleanupCalls++; },
      streamTextContent() {
        return {
          getReader() {
            return {
              read: () => new Promise((resolve) => { releaseRead = resolve; }),
              async cancel() {
                cancelCalls++;
                releaseRead?.({ done: true });
              },
              releaseLock() {},
            };
          },
        };
      },
    };
    const renderer = createPdfRenderer({
      getDocument: () => ({
        promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
        async destroy() {},
      }),
    });
    const session = await renderer.open(new Uint8Array([1]));
    const controller = new AbortController();
    const pending = session.getPageInfo(1, {
      includeTextFingerprint: true,
      cleanupPage: true,
      signal: controller.signal,
    });
    while (!releaseRead) await new Promise((resolve) => setImmediate(resolve));
    if (cancelWith === "signal") controller.abort();
    else session.cancel();
    await assert.rejects(pending, (error) => error?.name === "AbortError");
    assert.equal(cancelCalls, 1);
    assert.equal(cleanupCalls, 1);
    await session.destroy();
  }
});

test("PDF text fingerprinting stays bounded when streaming is unavailable or the page is pure XFA", async () => {
  for (const page of [
    { getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }) },
    { isPureXfa: true, getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }), getTextContent() { throw new Error("must not materialize"); } },
  ]) {
    const renderer = createPdfRenderer({
      getDocument: () => ({
        promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
        async destroy() {},
      }),
    });
    const session = await renderer.open(new Uint8Array([1]));
    const info = await session.getPageInfo(1, { includeTextFingerprint: true });
    assert.equal(info.textFingerprint, null);
    assert.equal(info.textLimitExceeded, false);
    await session.destroy();
  }
});

test("PDF renderer clears owned canvases when output conversion fails", async () => {
  const created = [];
  const pdfjs = {
    getDocument() {
      return {
        promise: Promise.resolve({
          numPages: 1,
          getPage: async () => ({
            getViewport: ({ scale }) => ({ width: 20 * scale, height: 10 * scale }),
            render: () => ({ promise: Promise.resolve(), cancel() {} }),
          }),
          async cleanup() {},
        }),
        async destroy() {},
      };
    },
  };
  const createCanvas = () => {
    const canvas = { width: 0, height: 0, getContext: () => ({ fillRect() {}, fillStyle: "" }) };
    created.push(canvas);
    return canvas;
  };

  const bitmapRenderer = createPdfRenderer(pdfjs, {
    createCanvas,
    createImageBitmap: async () => { throw new Error("bitmap failed"); },
  });
  const bitmapSession = await bitmapRenderer.open(new Uint8Array([1]));
  await assert.rejects(bitmapSession.renderPage(1, { output: "bitmap" }), /bitmap failed/);
  assert.deepEqual([created.at(-1).width, created.at(-1).height], [0, 0]);
  await bitmapSession.destroy();

  const urlRenderer = createPdfRenderer(pdfjs, {
    createCanvas,
    canvasToBlob: async () => new Blob(["x"]),
    createObjectURL: () => { throw new Error("URL failed"); },
    revokeObjectURL() {},
  });
  const urlSession = await urlRenderer.open(new Uint8Array([1]));
  await assert.rejects(urlSession.renderPage(1, { output: "object-url" }), /URL failed/);
  assert.deepEqual([created.at(-1).width, created.at(-1).height], [0, 0]);
  await urlSession.destroy();
});

test("PDF load abort waits for loading task destruction", async () => {
  let releaseDestroy;
  let settled = false;
  const destroyGate = new Promise((resolve) => { releaseDestroy = resolve; });
  const renderer = createPdfRenderer({
    getDocument: () => ({
      promise: new Promise(() => {}),
      destroy: () => destroyGate,
    }),
  });
  const controller = new AbortController();
  const pending = renderer.open(new Uint8Array([1]), { signal: controller.signal })
    .then((value) => ({ value }), (error) => ({ error }))
    .finally(() => { settled = true; });
  controller.abort();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(settled, false);
  releaseDestroy();
  const outcome = await pending;
  assert.equal(outcome.error?.name, "AbortError");
});

test("PDF renderer rejects pixel and dimension budgets before creating a canvas", async () => {
  let created = 0;
  let aspect = [1, 1];
  const page = {
    getViewport: ({ scale }) => ({ width: aspect[0] * scale, height: aspect[1] * scale }),
    render: () => { throw new Error("render must not start over budget"); },
  };
  const pdfjs = {
    getDocument: () => ({
      promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
      async destroy() {},
    }),
  };
  const renderer = createPdfRenderer(pdfjs, {
    createCanvas: () => {
      created++;
      return { width: 0, height: 0, getContext: () => ({ fillRect() {} }) };
    },
    maxPixels: 16_000_000,
    maxDimension: 16_384,
  });
  const session = await renderer.open(new Uint8Array([1]));

  aspect = [1, 1];
  await assert.rejects(session.renderPage(1, { width: 5_000 }), /pixel budget/);
  aspect = [1, 1_000];
  await assert.rejects(session.renderPage(1, { width: 20 }), /dimension budget/);
  assert.equal(created, 0);
  await session.destroy();
});

test("PDF render options can apply a stricter one-off budget", async () => {
  let created = 0;
  const page = {
    getViewport: ({ scale }) => ({ width: 100 * scale, height: 100 * scale }),
    render: () => ({ promise: Promise.resolve(), cancel() {} }),
  };
  const renderer = createPdfRenderer({
    getDocument: () => ({
      promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
      async destroy() {},
    }),
  }, {
    createCanvas: () => {
      created++;
      return { width: 0, height: 0, getContext: () => ({ fillRect() {}, fillStyle: "" }) };
    },
  });
  const session = await renderer.open(new Uint8Array([1]));
  await assert.rejects(session.renderPage(1, { width: 1_000, maxPixels: 500_000 }), /pixel budget/);
  assert.equal(created, 0);
  await session.destroy();
});

test("page cleanup waits until concurrent consumers of the same PDF page finish", async () => {
  const releases = [];
  let cleanupCalls = 0;
  const page = {
    getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }),
    cleanup() { cleanupCalls++; },
    render() {
      let resolve;
      const promise = new Promise((done) => { resolve = done; });
      releases.push(resolve);
      return { promise, cancel() {} };
    },
  };
  const renderer = createPdfRenderer({
    getDocument: () => ({
      promise: Promise.resolve({ numPages: 1, getPage: async () => page, async cleanup() {} }),
      async destroy() {},
    }),
  }, {
    createCanvas: () => ({
      width: 0,
      height: 0,
      getContext: () => ({ fillRect() {}, fillStyle: "" }),
    }),
  });
  const session = await renderer.open(new Uint8Array([1]));
  const first = session.renderPage(1, { cleanupPage: true });
  const second = session.renderPage(1);
  while (releases.length < 2) await new Promise((resolve) => setImmediate(resolve));

  releases[0]();
  const firstOutput = await first;
  assert.equal(cleanupCalls, 0);
  releases[1]();
  const secondOutput = await second;
  assert.equal(cleanupCalls, 1);

  firstOutput.dispose();
  secondOutput.dispose();
  await session.destroy();
});

test("an aborted page acquisition still honors an explicit cleanup request", async () => {
  let resolvePage;
  let cleanupCalls = 0;
  const page = {
    getViewport: ({ scale }) => ({ width: 10 * scale, height: 10 * scale }),
    cleanup() { cleanupCalls++; },
    render() { throw new Error("render must not start after abort"); },
  };
  const renderer = createPdfRenderer({
    getDocument: () => ({
      promise: Promise.resolve({
        numPages: 1,
        getPage: () => new Promise((resolve) => { resolvePage = resolve; }),
        async cleanup() {},
      }),
      async destroy() {},
    }),
  });
  const session = await renderer.open(new Uint8Array([1]));
  const controller = new AbortController();
  const pending = session.renderPage(1, { signal: controller.signal, cleanupPage: true });
  controller.abort();
  resolvePage(page);

  await assert.rejects(pending, (error) => error?.name === "AbortError");
  assert.equal(cleanupCalls, 1);
  await session.destroy();
});
