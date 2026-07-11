import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import vm from "node:vm";
import { test } from "node:test";

const webRoot = join(process.cwd(), "web");

async function waitFor(predicate) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("worker response timeout");
}

test("generic worker forwards positional arguments and stays bound to one WASM", async () => {
  const source = await readFile(join(webRoot, "wasm-worker.js"), "utf8");
  const messages = [];
  const workerGlobal = {
    location: { href: "https://example.test/wasm-worker.js" },
    postMessage(message) { messages.push(message); },
  };
  class Go {
    constructor() { this.importObject = {}; }
    run() {
      workerGlobal.pdfRun = (...args) => ({ json: JSON.stringify(args) });
    }
  }
  const context = vm.createContext({
    ArrayBuffer,
    Error,
    Go,
    Promise,
    Uint8Array,
    URL,
    WebAssembly: { instantiate: async () => ({ instance: {} }) },
    fetch: async (url) => String(url).includes("wasm_exec.js")
      ? { ok: true, text: async () => "" }
      : { ok: true, arrayBuffer: async () => new ArrayBuffer(1) },
    importScripts() {},
    self: workerGlobal,
    setTimeout,
  });
  vm.runInContext(source, context, { filename: "wasm-worker.js" });

  await workerGlobal.onmessage({ data: {
    type: "run",
    id: 1,
    wasm: "https://example.test/rotate.wasm",
    args: [new Uint8Array([7]), "2-4", 90],
  } });
  await waitFor(() => messages.some((message) => message.id === 1 && message.type === "done"));
  const done = messages.find((message) => message.id === 1 && message.type === "done");
  assert.equal(done.result.json, '[{"0":7},"2-4",90]');

  await workerGlobal.onmessage({ data: {
    type: "run",
    id: 2,
    wasm: "https://example.test/split.wasm",
    args: [new Uint8Array([8]), "1"],
  } });
  await waitFor(() => messages.some((message) => message.id === 2 && message.type === "error"));
  assert.match(messages.find((message) => message.id === 2 && message.type === "error").error, /bound|different/i);
});

test("operation worker delegates to the dedicated WASM worker", async () => {
  const source = await readFile(join(webRoot, "operation-worker.js"), "utf8").catch(() => "");
  assert.match(source, /importScripts/);
  assert.match(source, /wasm-worker\.js/);

  for (const file of ["workflow/workflow.mjs", "batch/batch-page.mjs"]) {
    assert.match(await readFile(join(webRoot, file), "utf8"), /host:\s*"\/operation-worker\.js"/);
  }
});

test("all WASM tool pages await the variadic runWasm contract", async () => {
  const [entries, catalog] = await Promise.all([
    readdir(webRoot, { withFileTypes: true }),
    readFile(join(process.cwd(), "tools", "operation-catalog.json"), "utf8").then(JSON.parse),
  ]);
  const pages = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const file = join(webRoot, entry.name, "index.html");
    let html;
    try { html = await readFile(file, "utf8"); } catch { continue; }
    if (html.includes("boot(\"./")) {
      const sources = [{ body: html, file }];
      for (const match of html.matchAll(/<script[^>]+src="([^"]+\.mjs)"/g)) {
        const moduleFile = match[1].startsWith("/")
          ? join(webRoot, match[1].replace(/^\/+/, ""))
          : join(webRoot, entry.name, match[1]);
        sources.push({ body: await readFile(moduleFile, "utf8"), file: moduleFile });
      }
      pages.push({ id: entry.name, sources });
    }
  }

  const expected = catalog
    .filter((entry) => entry.engine === "wasm" && entry.page !== false)
    .map((entry) => entry.id)
    .sort();
  assert.deepEqual(pages.map((page) => page.id).sort(), expected);
  for (const { id, sources } of pages) {
    let callCount = 0;
    for (const { body, file } of sources) {
      assert.doesNotMatch(body, /mergeWasmClient/, `${file} must use the shared client`);
      const calls = [...body.matchAll(/\brunWasm\s*\(/g)];
      callCount += calls.length;
      for (const call of calls) {
        const prefix = body.slice(Math.max(0, call.index - 24), call.index);
        assert.match(prefix, /await\s+(?:window\.)?$/, `${file}:${body.slice(0, call.index).split("\n").length} must await runWasm`);
      }
    }
    assert.ok(callCount > 0, `${id} must call runWasm`);
  }
});

test("browser bridge preserves variadic arguments in worker and fallback paths", async () => {
  const source = await readFile(join(webRoot, "app.js"), "utf8");
  assert.match(source, /import\("\/wasm-client\.mjs"\)/);
  assert.doesNotMatch(source, /function createBrowserWasmClient/);
  assert.match(source, /window\.runWasm\s*=\s*\(\.\.\.args\)/);
  assert.match(source, /__syncPdfRun\(\.\.\.args\)/);
  assert.match(source, /proxy\s*=\s*\(\.\.\.args\)\s*=>\s*window\.runWasm/);
  assert.match(source, /window\.pdfRun\s*=\s*proxy/);
  assert.match(source, /ensureMainRuntime/);
});
