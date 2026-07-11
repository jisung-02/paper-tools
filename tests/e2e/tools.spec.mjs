import { expect, test } from "@playwright/test";

test("Crop page has no boot-time script error", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/crop/");
  await page.waitForTimeout(250);
  expect(errors).toEqual([]);
});
