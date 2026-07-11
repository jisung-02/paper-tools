import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { OCRBudget, renderGeometry, validateOCRSelection } from "./budget.mjs";

test("OCR selection rejects page and source-byte budgets before recognition", () => {
  assert.throws(() => validateOCRSelection([{ size: 6 }, { size: 5 }], 2, {
    maxPages: 2,
    maxSourceBytes: 10,
  }), /source bytes/);
  assert.throws(() => validateOCRSelection([{ size: 1 }], 3, {
    maxPages: 2,
    maxSourceBytes: 10,
  }), /pages/);
});

test("OCR render geometry caps one page without changing its aspect ratio", () => {
  const geometry = renderGeometry(1000, 500, 2, { maxPagePixels: 1_000_000 });
  assert.equal(geometry.width * geometry.height <= 1_000_000, true);
  assert.ok(Math.abs(geometry.width / geometry.height - 2) < 0.01);
  assert.ok(geometry.scale < 2);
});

test("OCR budget bounds total pixels, words, characters, and serialized boxes", () => {
  const budget = new OCRBudget(2, {
    maxPages: 2,
    maxPagePixels: 100,
    maxTotalPixels: 150,
    maxWords: 2,
    maxCharacters: 5,
    maxJSONBytes: 10,
  });
  budget.reservePage(0, 10, 10);
  assert.throws(() => budget.reservePage(1, 8, 8), /total pixels/);

  const second = new OCRBudget(1, {
    maxPages: 1,
    maxPagePixels: 100,
    maxTotalPixels: 100,
    maxWords: 2,
    maxCharacters: 5,
    maxJSONBytes: 10,
  });
  second.reservePage(0, 5, 5);
  second.addRecognition(0, [{ text: "ab" }, { text: "cd" }], "abcde");
  assert.throws(() => second.addRecognition(0, [], ""), /already recorded/);
  assert.throws(() => second.assertSerialized("01234567890"), /JSON bytes/);

  const words = new OCRBudget(1, { maxPages: 1, maxPagePixels: 100, maxTotalPixels: 100, maxWords: 1, maxCharacters: 5, maxJSONBytes: 100 });
  words.reservePage(0, 5, 5);
  assert.throws(() => words.addRecognition(0, [{ text: "a" }, { text: "b" }], "ab"), /words/);

  const characters = new OCRBudget(1, { maxPages: 1, maxPagePixels: 100, maxTotalPixels: 100, maxWords: 2, maxCharacters: 2, maxJSONBytes: 100 });
  characters.reservePage(0, 5, 5);
  assert.throws(() => characters.addRecognition(0, [{ text: "abc" }], "abc"), /characters/);
});

test("OCR UI and WASM apply budgets before expensive boundaries", async () => {
  const [ui, wasm] = await Promise.all([
    readFile(new URL("./ocr.mjs", import.meta.url), "utf8"),
    readFile(new URL("../../wasm/ocrpdf/main.go", import.meta.url), "utf8"),
  ]);
  assert.match(ui, /import\s+\{[^}]*OCRBudget[^}]*validateOCRSelection[^}]*\}\s+from\s+"\.\/budget\.mjs"/s);
  assert.ok(ui.indexOf("validateOCRSelection(") < ui.indexOf('import("/vendor/tesseract/lib.js")'));
  assert.ok(ui.indexOf("budget.reservePage(") < ui.indexOf("client.loadImage("));
  assert.ok(ui.indexOf("budget.assertSerialized(") < ui.indexOf("window.runWasm("));
  assert.match(wasm, /maxOCRPagesJSONBytes/);
  assert.ok(wasm.indexOf("len(pagesJSON)") < wasm.indexOf("json.Unmarshal"));
});
