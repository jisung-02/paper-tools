import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

function onePagePDF() {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>",
    "<< /Length 0 >>\nstream\n\nendstream",
  ];
  let pdf = "%PDF-1.4\n";
  const offsets = [0];
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(pdf));
    pdf += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(pdf);
  pdf += "xref\n0 5\n0000000000 65535 f \n";
  pdf += offsets.slice(1).map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`).join("");
  pdf += `trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(pdf);
}

test("localized PDF diff pages load the shared visual module and complete control set", async ({ page }) => {
  const failedAssets = [];
  const pageErrors = [];
  page.on("response", (response) => {
    const path = new URL(response.url()).pathname;
    if (response.status() >= 400 && /\/pdfdiff\/(?:visual|diff-)/.test(path)) {
      failedAssets.push(`${response.status()} ${path}`);
    }
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/ko/pdfdiff/");
  await expect(page.locator("#visualRun")).toHaveText("시각 비교");
  await expect(page.locator("#visualAntialias")).toHaveCount(1);
  await expect(page.locator("#visualChangedOnly")).toHaveCount(1);
  await expect(page.locator("#visualExport")).toHaveCount(1);
  await expect.poll(() => failedAssets).toEqual([]);
  expect(pageErrors).toEqual([]);
  expect(await page.locator('link[href="/pdfdiff/visual.css"]').count()).toBe(1);
});

test("localized visual runtime errors expose translated codes instead of Worker details", async ({ page }) => {
  await page.route("**/pdfdiff/diff-worker.mjs", (route) => route.fulfill({
    contentType: "text/javascript",
    body: 'self.onmessage = () => { throw new Error("FATAL-LOCALIZATION-CANARY"); };',
  }));
  const pdf = onePagePDF();
  await page.goto("/ko/pdfdiff/");
  await page.locator("#aInput").setInputFiles({ name: "a.pdf", mimeType: "application/pdf", buffer: pdf });
  await page.locator("#bInput").setInputFiles({ name: "b.pdf", mimeType: "application/pdf", buffer: pdf });
  await page.locator("#visualRun").click();

  await expect(page.locator("#err")).toHaveText("시각 비교 Worker를 실행하지 못했습니다.", { timeout: 20_000 });
  await expect(page.locator("#err")).not.toContainText("FATAL-LOCALIZATION-CANARY");
});
