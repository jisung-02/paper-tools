#!/usr/bin/env node
// Stage static assets with hard links when possible; this keeps staging from
// doubling disk usage while giving deploy tooling an isolated directory.
import { cp, lstat, mkdir, readdir, link, rm, stat, readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { join, relative, resolve } from "node:path";

const immutable = /\.(?:js|mjs|css|wasm|woff2?|ttf|otf|png|jpe?g|gif|svg|ico)$/i;
const fixed = new Set(["app.js", "sw.js", "wasm_exec.js"]);
const files = async dir => (await readdir(dir, { withFileTypes: true })).flatMap(async e => {
  const p = join(dir, e.name);
  return e.isDirectory() ? (await Promise.all(await files(p))).flat() : [p];
}).reduce(async (a, p) => (await a).concat(await p), Promise.resolve([]));

async function stage(src, dst, hash) {
  await mkdir(dst, { recursive: true });
  for (const name of await readdir(src)) {
    const from = join(src, name), to = join(dst, name);
    const info = await lstat(from);
    if (info.isDirectory()) await stage(from, to, hash);
    else if (info.isFile()) {
      let target = to;
      if (hash && immutable.test(name) && !fixed.has(name)) {
        const digest = createHash("sha256").update(await readFile(from)).digest("hex").slice(0, 12);
        const dot = name.lastIndexOf(".");
        target = join(dst, `${name.slice(0, dot)}-${digest}${name.slice(dot)}`);
      }
      // Rewritten text assets must not be hard-linked: writing the staged
      // rewrite through a hard link would mutate the source tree as well.
      const rewritten = hash && /\.(?:html?|js|mjs|css)$/i.test(name);
      if (rewritten) await cp(from, target);
      else {
        try { await link(from, target); } catch { await cp(from, target); }
      }
    }
  }
}

const hash = process.argv.includes("--hash");
const args = process.argv.filter(a => a !== "--hash");
const src = resolve(args[2] || "web");
const dst = resolve(args[3] || ".staging/web");
if (src === dst || relative(src, dst) === "") throw new Error("source and destination must differ");
await rm(dst, { recursive: true, force: true });
await stage(src, dst, hash);
if (hash) {
  const sourceFiles = await files(src);
  const mapping = new Map();
  for (const from of sourceFiles) {
    const name = from.split("/").pop();
    if (!immutable.test(name) || fixed.has(name)) continue;
    const digest = createHash("sha256").update(await readFile(from)).digest("hex").slice(0, 12);
    const dot = name.lastIndexOf(".");
    mapping.set(name, `${name.slice(0, dot)}-${digest}${name.slice(dot)}`);
  }
  // Longest names first: a basename that is a suffix of another
  // ("init.mjs" in "options.init.mjs") must not fire before the longer
  // one, or the longer reference is rewritten with the wrong digest.
  const ordered = [...mapping].sort(([a], [b]) => b.length - a.length);
  for (const file of await files(dst)) {
    if (!/\.(?:html?|js|mjs|css)$/i.test(file)) continue;
    if (file.split("/").pop() === "sw.js") continue;
    let body = await readFile(file, "utf8");
    for (const [from, to] of ordered) body = body.replaceAll(from, to);
    await writeFile(file, body);
  }
}
const bytes = async dir => (await Promise.all((await readdir(dir, { withFileTypes: true })).map(async e => e.isDirectory() ? bytes(join(dir, e.name)) : (await stat(join(dir, e.name))).size))).reduce((a, b) => a + b, 0);
console.log(`staged ${src} -> ${dst} (${await bytes(dst)} bytes; hard links preferred)`);
