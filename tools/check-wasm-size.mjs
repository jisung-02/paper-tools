import { readdir, stat } from "node:fs/promises";
import { join } from "node:path";

const root = process.argv[2] || "web";
const maxTotal = Number(process.env.MAX_WASM_BYTES || 30 * 1024 * 1024);
const maxEach = Number(process.env.MAX_WASM_FILE_BYTES || 2 * 1024 * 1024);
const files = [];

async function walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) await walk(path);
    else if (entry.name.endsWith(".wasm")) files.push([path, (await stat(path)).size]);
  }
}

await walk(root);
if (!files.length) throw new Error(`no WASM files found under ${root}`);
const total = files.reduce((sum, [, size]) => sum + size, 0);
const oversized = files.filter(([, size]) => size > maxEach);
if (oversized.length || total > maxTotal) {
  throw new Error(`WASM size budget exceeded: total=${total}, maxTotal=${maxTotal}, oversized=${oversized.map(([path, size]) => `${path}=${size}`).join(", ")}`);
}
console.log(`WASM size budget passed: ${files.length} files, ${total} bytes`);
