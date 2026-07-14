import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import catalog from "../tools/operation-catalog.json" with { type: "json" };
import { operationArgs } from "../web/operation-adapters.mjs";

test("operation catalog is the complete unique tool inventory", () => {
  assert.equal(catalog.length, 44);
  const ids = catalog.map((entry) => entry.id);
  assert.equal(new Set(ids).size, ids.length);
  assert.equal(catalog.filter((entry) => entry.engine === "wasm").length, 38);
  for (const entry of catalog) {
    assert.match(entry.id, /^[a-z][a-z0-9]*$/);
    assert.ok(["wasm", "module", "transport"].includes(entry.engine));
    assert.equal(typeof entry.input.kind, "string");
    assert.equal(typeof entry.output.kind, "string");
    assert.equal(typeof entry.capabilities.preview, "boolean");
    assert.equal(typeof entry.capabilities.pipeline, "boolean");
    assert.equal(typeof entry.capabilities.batch, "boolean");
    assert.equal(typeof entry.capabilities.terminal, "boolean");
    if (entry.engine === "wasm") {
      assert.equal(entry.build.package, `./wasm/${entry.id}`);
      assert.equal(entry.build.output, `web/${entry.id}/${entry.id}.wasm`);
    }
  }
});

test("build and i18n scripts consume the catalog instead of hard-coded inventories", async () => {
  const [build, i18n] = await Promise.all([
    readFile(new URL("../build.sh", import.meta.url), "utf8"),
    readFile(new URL("../tools/gen-i18n.mjs", import.meta.url), "utf8"),
  ]);
  assert.match(build, /operation-catalog/);
  assert.doesNotMatch(build, /TOOLS="merge interleave/);
  assert.match(i18n, /operation-catalog/);
  assert.doesNotMatch(i18n, /const TOOL_SLUGS = \[/);
});

test("generated web catalog is the build copy of the source catalog", async () => {
  const [source, generated] = await Promise.all([
    readFile(new URL("../tools/operation-catalog.json", import.meta.url), "utf8"),
    readFile(new URL("../web/operation-catalog.json", import.meta.url), "utf8"),
  ]);
  assert.equal(generated, source);
});

test("hidden OCR PDF helper is not exposed as a batch operation", () => {
  const helper = catalog.find((entry) => entry.id === "ocrpdf");
  assert.ok(helper);
  assert.equal(helper.page, false);
  assert.equal(helper.capabilities.batch, false);
});

test("interleave catalog cardinality matches its executable adapter", () => {
  const descriptor = catalog.find((entry) => entry.id === "interleave");
  const a = new Uint8Array([1]);
  const b = new Uint8Array([2]);
  assert.deepEqual(descriptor.input, { kind: "pdf", min: 2, max: 2 });
  assert.deepEqual(operationArgs("interleave", [a, b], {}), [a, b, false]);
  assert.throws(() => operationArgs("interleave", [a], {}), /two inputs/);
  assert.throws(() => operationArgs("interleave", [a, b, a], {}), /two inputs/);
});

test("batch page derives interleave bounds from the catalog", async () => {
  const page = await readFile(new URL("../web/batch/batch-page.mjs", import.meta.url), "utf8");
  assert.doesNotMatch(page, /operationId === ["']interleave["']/);
});

test("PWA manifest uses the visible catalog count", async () => {
  const manifest = JSON.parse(await readFile(new URL("../web/manifest.webmanifest", import.meta.url), "utf8"));
  const visibleCount = catalog.filter((entry) => entry.page !== false).length;
  assert.match(manifest.description, new RegExp(`\\b${visibleCount}\\b`));
});
