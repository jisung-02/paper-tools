import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const manifest = JSON.parse(await readFile(resolve(root, "tools/vendor-manifest.json"), "utf8"));
const failures = [];
for (const [relative, expected] of Object.entries(manifest)) {
  const digest = createHash("sha256").update(await readFile(resolve(root, relative))).digest("hex");
  if (!/^[a-f0-9]{64}$/.test(expected) || digest !== expected) {
    failures.push(`${relative}: expected ${expected}, got ${digest}`);
  }
}
if (failures.length) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`Verified ${Object.keys(manifest).length} vendored files.`);
}
