import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
const run = promisify(execFile);

test("staging preserves bytes without requiring a second copy", async () => {
  const root = await mkdtemp(join(tmpdir(), "stage-"));
  const src = join(root, "src"), dst = join(root, "dst");
  await mkdir(src); await writeFile(join(src, "asset.js"), "ok");
  await run(process.execPath, ["tools/stage-assets.mjs", src, dst]);
  assert.equal(await readFile(join(dst, "asset.js"), "utf8"), "ok");
  const [a, b] = await Promise.all([stat(join(src, "asset.js")), stat(join(dst, "asset.js"))]);
  assert.equal(a.size, b.size);
  assert.equal(a.ino, b.ino, "same-filesystem staging should hard-link");
});

test("--hash rewrites immutable references and keeps bootstrap URLs fixed", async () => {
  const root = await mkdtemp(join(tmpdir(), "stage-hash-"));
  const src = join(root, "src"), dst = join(root, "dst");
  await mkdir(src); await writeFile(join(src, "app.js"), "import './chunk.js';");
  await writeFile(join(src, "chunk.js"), "export const ok = true;");
  await writeFile(join(src, "sw.js"), "importScripts('chunk.js');");
  await writeFile(join(src, "index.html"), '<script src="app.js"></script><script src="chunk.js"></script>');
  await run(process.execPath, ["tools/stage-assets.mjs", "--hash", src, dst]);
  const names = (await import("node:fs/promises")).readdir(dst);
  assert.ok((await names).some(n => /^chunk-[0-9a-f]{12}\.js$/.test(n)));
  assert.match(await readFile(join(dst, "index.html"), "utf8"), /chunk-[0-9a-f]{12}\.js/);
  assert.match(await readFile(join(dst, "app.js"), "utf8"), /chunk-[0-9a-f]{12}\.js/);
  assert.equal(await readFile(join(src, "app.js"), "utf8"), "import './chunk.js';", "rewrite must not mutate source hard links");
  assert.equal(await readFile(join(dst, "sw.js"), "utf8"), "importScripts('chunk.js');");
});

test("--hash applies to nested immutable assets", async () => {
  const root = await mkdtemp(join(tmpdir(), "stage-nested-"));
  const src = join(root, "src"), dst = join(root, "dst");
  await mkdir(join(src, "tool"), { recursive: true });
  await writeFile(join(src, "tool", "tool.wasm"), "wasm");
  await run(process.execPath, ["tools/stage-assets.mjs", "--hash", src, dst]);
  const names = await (await import("node:fs/promises")).readdir(join(dst, "tool"));
  assert.ok(names.some((n) => /^tool-[0-9a-f]{12}\.wasm$/.test(n)));
});
