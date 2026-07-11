import { expect, test } from "@playwright/test";

function pdfFixture(text) {
  const stream = `BT\n/F1 24 Tf\n72 720 Td\n(${text}) Tj\nET\n`;
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
    `<< /Length ${Buffer.byteLength(stream)} >>\nstream\n${stream}endstream`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
  ];
  let pdf = "%PDF-1.4\n";
  const offsets = [0];
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(pdf));
    pdf += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(pdf);
  pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  pdf += offsets.slice(1).map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`).join("");
  pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(pdf);
}

async function setInputFile(page, bytes, lastModified) {
  await page.locator("#workflowInput").evaluate((input, value) => {
    const transfer = new DataTransfer();
    transfer.items.add(new File([Uint8Array.from(value.bytes)], "input.pdf", {
      type: "application/pdf",
      lastModified: value.lastModified,
    }));
    input.files = transfer.files;
    input.dispatchEvent(new Event("change", { bubbles: true }));
  }, { bytes: [...bytes], lastModified });
}

async function runWorkflow(page) {
  const previous = await page.locator("#resultPreview").getAttribute("src") || "";
  await page.locator("#runWorkflow").click();
  await page.waitForFunction((oldSource) => {
    const button = document.querySelector("#runWorkflow");
    const frame = document.querySelector("#resultPreview");
    return !button.disabled && frame.src.startsWith("blob:") && frame.getAttribute("src") !== oldSource;
  }, previous);
  await expect(page.locator("#err")).toHaveText("");
  return page.locator("#resultPreview").evaluate(async (frame) => {
    const response = await fetch(frame.src);
    return [...new Uint8Array(await response.arrayBuffer())];
  });
}

test("Reorder to Compress to Metadata does not reuse results across content or revision changes", async ({ page }) => {
  test.setTimeout(120_000);
  const requestedWasm = new Map();
  page.on("request", (request) => {
    const match = new URL(request.url()).pathname.match(/^\/(reorder|compress|metadata)\/\1\.wasm$/);
    if (match) requestedWasm.set(match[1], (requestedWasm.get(match[1]) || 0) + 1);
  });
  const firstInput = pdfFixture("ALPHA");
  const secondInput = pdfFixture("BRAVO");
  expect(firstInput.byteLength).toBe(secondInput.byteLength);

  await page.goto("/workflow/");
  for (const operation of ["reorder", "compress", "metadata"]) {
    await page.locator("#operationSelect").selectOption(operation);
    await page.locator("#addStep").click();
  }
  await expect(page.locator("#steps strong")).toHaveText([
    "1. Reorder",
    "2. Compress",
    "3. Metadata",
  ]);

  const lastModified = 1_700_000_000_000;
  await setInputFile(page, firstInput, lastModified);
  const firstOutput = await runWorkflow(page);
  await setInputFile(page, secondInput, lastModified);
  const secondOutput = await runWorkflow(page);
  await setInputFile(page, secondInput, lastModified + 1);
  await runWorkflow(page);

  expect(Buffer.from(secondOutput)).not.toEqual(Buffer.from(firstOutput));
  expect(Object.fromEntries([...requestedWasm].sort())).toEqual({ compress: 3, metadata: 3, reorder: 3 });
});
