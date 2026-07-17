import assert from "node:assert/strict";
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

async function listFiles(dir, base = dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const out = [];
  for (const e of entries) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) out.push(...await listFiles(p, base));
    else out.push(path.relative(base, p));
  }
  return out;
}

async function fixture() {
  const src = await mkdtemp(path.join(os.tmpdir(), "file-utils-stage-src-"));
  await mkdir(path.join(src, "i18n"), { recursive: true });
  await writeFile(path.join(src, "style.css"), "body{color:red}\n");
  await writeFile(path.join(src, "i18n", "de.js"), "export default {};\n");
  await writeFile(path.join(src, "pdf.worker.mjs"), "export function work(){}\n");
  await writeFile(path.join(src, "foo_bg.wasm"), Buffer.from([0, 1, 2, 3]));
  await writeFile(path.join(src, "foo_bg.wasm.gz"), Buffer.from([4, 5, 6, 7]));
  await writeFile(path.join(src, "app.mjs"), [
    'console.log("Please use the `legacy` build in Node.js environments.");',
    'el.style.cssText="position:absolute";',
    'const w = "./pdf.worker.mjs";',
    'fetch("/i18n/de.js"); fetch("/foo_bg.wasm"); fetch("/foo_bg.wasm.gz");',
    "",
  ].join("\n"));
  await writeFile(path.join(src, "index.html"), [
    "<!doctype html>",
    '<link rel="stylesheet" href="style.css">',
    '<link rel="icon" href="/favicon.svg">',
    '<link rel="apple-touch-icon" href="/icon-192.png">',
    "",
  ].join("\n"));
  await writeFile(path.join(src, "favicon.svg"), "<svg></svg>\n");
  await writeFile(path.join(src, "icon-192.png"), Buffer.from([137, 80, 78, 71]));
  return src;
}

test("stage-assets rewrites references at word boundaries and keeps canonical names stable", async () => {
  const src = await fixture();
  const dst = await mkdtemp(path.join(os.tmpdir(), "file-utils-stage-dst-"));
  try {
    const result = spawnSync("node", ["tools/stage-assets.mjs", "--hash", src, dst], {
      cwd: root,
      encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stderr);

    const staged = await listFiles(dst);

    const appFile = staged.find(f => /^app-[0-9a-f]{12}\.mjs$/.test(path.basename(f)));
    assert.ok(appFile, `expected hashed app.mjs among: ${staged.join(", ")}`);
    const appBody = await readFile(path.join(dst, appFile), "utf8");
    assert.match(appBody, /Node\.js environments/);
    assert.match(appBody, /el\.style\.cssText=/);
    assert.match(appBody, /pdf\.worker-[0-9a-f]{12}\.mjs/);
    assert.match(appBody, /\/i18n\/de-[0-9a-f]{12}\.js/);
    assert.match(appBody, /\/foo_bg-[0-9a-f]{12}\.wasm/);
    assert.match(appBody, /\/foo_bg\.wasm\.gz/);

    assert.ok(staged.includes("favicon.svg"));
    assert.ok(staged.includes("icon-192.png"));
    assert.ok(!staged.some(f => /^favicon-[0-9a-f]{12}\.svg$/.test(path.basename(f))));
    assert.ok(!staged.some(f => /^icon-192-[0-9a-f]{12}\.png$/.test(path.basename(f))));

    const indexBody = await readFile(path.join(dst, "index.html"), "utf8");
    assert.match(indexBody, /href="\/favicon\.svg"/);
    assert.match(indexBody, /href="\/icon-192\.png"/);
    assert.match(indexBody, /href="style-[0-9a-f]{12}\.css"/);

    assert.ok(staged.some(f => /^style-[0-9a-f]{12}\.css$/.test(path.basename(f))));
    assert.ok(staged.some(f => /^de-[0-9a-f]{12}\.js$/.test(path.basename(f))));
  } finally {
    await rm(src, { recursive: true, force: true });
    await rm(dst, { recursive: true, force: true });
  }
});
