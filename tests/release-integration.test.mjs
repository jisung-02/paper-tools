import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import vm from "node:vm";
import catalog from "../tools/operation-catalog.json" with { type: "json" };
import metadata from "../tools/meta-i18n.json" with { type: "json" };

const visible = catalog.filter((entry) => entry.page !== false);

test("landing metadata and documentation use the visible catalog count", async () => {
  const [landing, readme, readmeKO, llms] = await Promise.all([
    readFile(new URL("../web/index.html", import.meta.url), "utf8"),
    readFile(new URL("../README.md", import.meta.url), "utf8"),
    readFile(new URL("../README.ko.md", import.meta.url), "utf8"),
    readFile(new URL("../web/llms.txt", import.meta.url), "utf8"),
  ]);
  assert.equal(visible.length, 41);
  assert.match(landing, /41 file tools/);
  assert.doesNotMatch(landing, /38 (?:file )?tools/);
  assert.match(readme, /41 client-side tools/);
  assert.match(readmeKO, /도구 41종/);
  assert.match(llms, /41 client-side file tools/);

  const itemListSource = [...landing.matchAll(/<script type="application\/ld\+json">\s*([\s\S]*?)\s*<\/script>/g)]
    .map((match) => JSON.parse(match[1]))
    .find((value) => value["@type"] === "ItemList");
  const listed = itemListSource.itemListElement.map((item) => new URL(item.url).pathname.split("/").filter(Boolean).at(-1));
  assert.deepEqual(new Set(listed), new Set(visible.map((entry) => entry.id)));
  assert.equal(listed.length, visible.length);
});

test("every visible catalog page has localized metadata with the current count", () => {
  for (const entry of visible) {
    assert.ok(metadata[entry.id], `missing metadata for ${entry.id}`);
    for (const language of ["ko", "ja", "zh", "es", "fr", "de"]) {
      assert.equal(typeof metadata[entry.id][language]?.title, "string", `${entry.id}.${language}.title`);
      assert.equal(typeof metadata[entry.id][language]?.description, "string", `${entry.id}.${language}.description`);
    }
  }
  for (const language of ["ko", "ja", "zh", "es", "fr", "de"]) {
    assert.match(metadata[""][language].description, /41/);
  }
});

test("every localized source string has a dictionary entry in all five generated languages", async () => {
  const files = [
    new URL("../web/index.html", import.meta.url),
    new URL("../web/privacy/index.html", import.meta.url),
    ...visible.map((entry) => new URL(`../web/${entry.id}/index.html`, import.meta.url)),
  ];
  const keys = new Set();
  for (const file of files) {
    const html = await readFile(file, "utf8");
    for (const match of html.matchAll(/data-en(?:-placeholder)?="([^"]*)"/g)) {
      keys.add(match[1].replaceAll("&quot;", '"').replaceAll("&amp;", "&").replaceAll("&#39;", "'"));
    }
  }
  for (const file of [
    new URL("../web/app.js", import.meta.url),
    new URL("../web/ocr/ocr.mjs", import.meta.url),
    new URL("../web/send/send.mjs", import.meta.url),
    new URL("../web/pdfdiff/visual.mjs", import.meta.url),
  ]) {
    const source = await readFile(file, "utf8");
    for (const match of source.matchAll(/\bt\(\s*["']([^"']+)["']\s*,/g)) keys.add(match[1]);
  }
  for (const language of ["ja", "zh", "es", "fr", "de"]) {
    const source = await readFile(new URL(`../web/i18n/${language}.js`, import.meta.url), "utf8");
    const context = { window: {} };
    vm.createContext(context);
    vm.runInContext(source, context);
    const dictionary = context.window.I18N[language];
    const missing = [...keys].filter((key) => !dictionary[key]);
    assert.deepEqual(missing, [], `${language} is missing localized source strings`);
  }
});

test("tool-local scripts and styles use locale-safe absolute paths", async () => {
  for (const entry of visible) {
    const html = await readFile(new URL(`../web/${entry.id}/index.html`, import.meta.url), "utf8");
    assert.doesNotMatch(html, /(?:href|src)="\.\/[^"?#]+\.(?:css|js|mjs)(?:[?"#])/, entry.id);
    assert.doesNotMatch(html, /from\s+["']\.\/[^"']+\.mjs["']/, entry.id);
  }
});

test("CI builds optimized TinyGo WASM before running browser E2E", async () => {
  const [ci, deploy] = await Promise.all([
    readFile(new URL("../.github/workflows/ci.yml", import.meta.url), "utf8"),
    readFile(new URL("../.github/workflows/deploy.yml", import.meta.url), "utf8"),
  ]);
  const build = ci.indexOf("./build.sh");
  const e2e = ci.indexOf("npm run test:e2e");
  assert.ok(build >= 0 && e2e > build, "browser E2E must run after the actual deployment WASM build");
  assert.match(ci, /Install TinyGo/);
  assert.match(ci, /binaryen/);
  assert.match(ci, /check-wasm-size/);
  assert.match(ci, /go test -race \.\/\.\.\./);
  assert.match(deploy, /hashFiles\([^\n]*tools\/operation-catalog\.json/);
  assert.match(deploy, /cp tools\/operation-catalog\.json web\/operation-catalog\.json/);
  assert.ok(
    deploy.indexOf("node --test") > deploy.indexOf("cp tools/operation-catalog.json web/operation-catalog.json"),
    "deploy Node tests must run after the generated catalog is available",
  );
});

test("Playwright uses bundled browsers when local Chrome is unavailable", async () => {
  const [config, pwa] = await Promise.all([
    readFile(new URL("../playwright.config.mjs", import.meta.url), "utf8"),
    readFile(new URL("./e2e/pwa.spec.mjs", import.meta.url), "utf8"),
  ]);
  assert.doesNotMatch(config, /executablePath:\s*["']\/Applications/);
  assert.doesNotMatch(pwa, /executablePath:\s*["']\/Applications/);
  assert.match(config, /chromiumLaunchOptions/);
  assert.match(pwa, /chromiumLaunchOptions/);
});

test("security policy permits same-origin module Workers", async () => {
  const headers = await readFile(new URL("../web/_headers", import.meta.url), "utf8");
  assert.match(headers, /worker-src 'self' blob:/);
});
