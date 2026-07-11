import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { test } from "node:test";

const root = join(process.cwd(), "web");

test("tool pages load shared accessibility plumbing", async () => {
  const names = await readdir(root, { withFileTypes: true });
  const pages = names.filter((entry) => entry.isDirectory()).map((entry) => join(root, entry.name, "index.html"));
  let checked = 0;
  for (const file of pages) {
    let html;
    try { html = await readFile(file, "utf8"); } catch { continue; }
    if (!html.includes('id="status"') && !html.includes('id="err"')) continue;
    assert.match(html, /(?:\.\.?\/)?app\.js/);
    checked++;
  }
  assert.ok(checked >= 30, `expected at least 30 tool pages, found ${checked}`);
});

test("worker transfer keeps an intact fallback input", async () => {
  const [app, client] = await Promise.all([
    readFile(join(root, "app.js"), "utf8"),
    readFile(join(root, "wasm-client.mjs"), "utf8"),
  ]);
  assert.match(client, /value instanceof ArrayBuffer/);
  assert.match(app, /return window\.__syncPdfRun\(\.\.\.args\)/);
});
