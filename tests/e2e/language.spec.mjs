import { expect, test } from "@playwright/test";

test("automatic language redirect preserves query and hash", async ({ browser }) => {
  const context = await browser.newContext({ locale: "ko-KR" });
  const page = await context.newPage();
  await page.goto("/send/?x=1#r=uYWJj.deadbeef");
  await expect(page).toHaveURL("/ko/send/?x=1#r=uYWJj.deadbeef");
  await context.close();
});
