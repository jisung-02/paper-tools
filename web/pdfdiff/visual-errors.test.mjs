import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import vm from "node:vm";

test("visual runtime errors expose stable codes and localized user messages", async () => {
  const module = await import("./visual-errors.mjs").catch(() => ({}));
  assert.equal(typeof module.visualError, "function");
  assert.equal(typeof module.visualErrorMessage, "function");
  assert.ok(Object.keys(module.VISUAL_ERROR_MESSAGES || {}).length >= 8);

  const error = module.visualError("worker-failed", "low-level worker stack");
  assert.equal(error.code, "worker-failed");
  assert.equal(module.visualErrorMessage(error, (english, korean) => korean), "시각 비교 Worker를 실행하지 못했습니다.");
  assert.equal(module.visualErrorMessage(new Error("secret implementation detail"), (english) => english), "Visual comparison failed.");
});

test("every visual runtime error code resolves through each generated locale dictionary", async () => {
  const module = await import("./visual-errors.mjs").catch(() => ({}));
  assert.ok(module.VISUAL_ERROR_MESSAGES);
  for (const language of ["ja", "zh", "es", "fr", "de"]) {
    const source = await readFile(new URL(`../i18n/${language}.js`, import.meta.url), "utf8");
    const context = { window: {} };
    vm.createContext(context);
    vm.runInContext(source, context);
    const dictionary = context.window.I18N[language];
    for (const [code, [english]] of Object.entries(module.VISUAL_ERROR_MESSAGES)) {
      assert.ok(dictionary[english], `${language} missing ${code}: ${english}`);
      assert.notEqual(dictionary[english], english, `${language} did not localize ${code}`);
    }
  }
});
