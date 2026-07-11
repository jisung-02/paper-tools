import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

const workerSource = `
self.onmessage = ({ data }) => {
  const rawInput = data.args[0];
  const input = Array.isArray(rawInput) ? rawInput[0] : rawInput;
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
  if (bytes[0] === 255) {
    self.postMessage({ type: "done", id: data.id, result: { error: "fixture failure" } });
    return;
  }
  const output = bytes.slice();
  self.postMessage({ type: "done", id: data.id, result: { data: output } }, [output.buffer]);
};
`;

test.beforeEach(async ({ page }) => {
  await page.route("**/operation-worker.js", (route) => route.fulfill({
    body: workerSource,
    contentType: "text/javascript",
  }));
});

test("grouped mode is offered only for operations whose catalog accepts multiple inputs", async ({ page }) => {
  await page.goto("/batch/");

  await page.locator("#batchOperation").selectOption("merge");
  await expect(page.locator("#batchMode option[value=grouped]")).toBeEnabled();
  await page.locator("#batchMode").selectOption("grouped");

  await page.locator("#batchOperation").selectOption("rotate");
  await expect(page.locator("#batchMode")).toHaveValue("independent");
  await expect(page.locator("#batchMode option[value=grouped]")).toHaveAttribute("disabled", "");
});

test("independent batch downloads streamed successes and a failure manifest", async ({ page }) => {
  await page.goto("/batch/");
  await page.locator("#batchOperation").selectOption("rotate");
  await page.locator("#batchRetries").selectOption("0");
  await page.locator("#batchInput").setInputFiles([
    { name: "same.pdf", mimeType: "application/pdf", buffer: Buffer.from([1, 2, 3]) },
    { name: "same.pdf", mimeType: "application/pdf", buffer: Buffer.from([255, 2, 3]) },
  ]);

  await page.locator("#batchRun").click();
  await expect(page.locator("#batchResult")).toBeVisible();
  await expect(page.locator("#batchSummary")).toContainText("1 succeeded · 1 failed");

  const downloadPromise = page.waitForEvent("download");
  await page.locator("#batchDownload").click();
  const download = await downloadPromise;
  const archive = new Uint8Array(await readFile(await download.path()));
  const entries = zipEntries(archive);

  expect(entries.map((entry) => entry.name)).toEqual([
    "same-rotate.pdf",
    "paper-tools-failures.json",
  ]);
  const manifestEntry = entries.find((entry) => entry.name === "paper-tools-failures.json");
  const manifest = JSON.parse(new TextDecoder().decode(manifestEntry.data));
  expect(manifest).toEqual({
    version: 1,
    failed: 1,
    failures: [{ index: 1, name: "same.pdf", attempts: 1, message: "fixture failure" }],
  });
});

test("cleanup for a downloaded OPFS archive does not remove the next batch archive", async ({ page }) => {
  await page.addInitScript(() => {
    const removeEntry = FileSystemDirectoryHandle.prototype.removeEntry;
    const getFileHandle = FileSystemDirectoryHandle.prototype.getFileHandle;
    window.__removePending = false;
    window.__createdDuringRemove = 0;
    FileSystemDirectoryHandle.prototype.removeEntry = async function (...args) {
      window.__removePending = true;
      try {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        return await removeEntry.apply(this, args);
      } finally {
        window.__removePending = false;
      }
    };
    FileSystemDirectoryHandle.prototype.getFileHandle = function (name, options) {
      if (options?.create && window.__removePending) window.__createdDuringRemove++;
      return getFileHandle.call(this, name, options);
    };
  });
  await page.goto("/batch/");
  await page.locator("#batchOperation").selectOption("rotate");
  await page.locator("#batchRetries").selectOption("0");
  await page.locator("#batchInput").setInputFiles({
    name: "first.pdf",
    mimeType: "application/pdf",
    buffer: Buffer.from([1]),
  });
  await page.locator("#batchRun").click();
  await expect(page.locator("#batchSummary")).toContainText("1 succeeded · 0 failed");

  const downloadPromise = page.waitForEvent("download");
  await page.locator("#batchDownload").click();
  await downloadPromise;
  await page.waitForFunction(() => window.__removePending === true);

  await page.locator("#batchInput").setInputFiles({
    name: "second.pdf",
    mimeType: "application/pdf",
    buffer: Buffer.from([255]),
  });
  await page.locator("#batchRun").click();
  await expect(page.locator("#batchSummary")).toContainText("0 succeeded · 1 failed");
  expect(await page.evaluate(() => window.__createdDuringRemove)).toBe(0);

  const archives = await page.evaluate(async () => {
    const root = await navigator.storage.getDirectory();
    const names = [];
    for await (const name of root.keys()) if (name.endsWith(".zip")) names.push(name);
    return names;
  });
  expect(archives).toHaveLength(1);
});

test("pagehide cleans a downloaded OPFS archive before its delayed cleanup timer", async ({ page }) => {
  await page.goto("/batch/");
  await page.locator("#batchOperation").selectOption("rotate");
  await page.locator("#batchRetries").selectOption("0");
  await page.locator("#batchInput").setInputFiles({
    name: "leave.pdf",
    mimeType: "application/pdf",
    buffer: Buffer.from([1]),
  });
  await page.locator("#batchRun").click();
  await expect(page.locator("#batchSummary")).toContainText("1 succeeded · 0 failed");
  const downloadPromise = page.waitForEvent("download");
  await page.locator("#batchDownload").click();
  await downloadPromise;

  await page.goto("/batch/");
  await expect.poll(() => page.evaluate(async () => {
    const root = await navigator.storage.getDirectory();
    const archives = [];
    for await (const name of root.keys()) if (name.endsWith(".zip")) archives.push(name);
    return archives;
  })).toEqual([]);
});

test("grouped Images to PDF accepts image inputs and writes one archive entry", async ({ page }) => {
  await page.goto("/batch/");
  await expect(page.locator("#batchOperation option[value=img2pdf]")).toHaveCount(1);
  await page.locator("#batchOperation").selectOption("img2pdf");
  await expect(page.locator("#batchInput")).toHaveAttribute("accept", "image/png,image/jpeg");
  await page.locator("#batchMode").selectOption("grouped");
  await page.locator("#batchRetries").selectOption("0");
  await page.locator("#batchInput").setInputFiles([
    { name: "one.png", mimeType: "image/png", buffer: Buffer.from([1]) },
    { name: "two.jpg", mimeType: "image/jpeg", buffer: Buffer.from([2]) },
  ]);

  await page.locator("#batchRun").click();
  await expect(page.locator("#batchSummary")).toContainText("1 succeeded · 0 failed");
  const downloadPromise = page.waitForEvent("download");
  await page.locator("#batchDownload").click();
  const archive = new Uint8Array(await readFile(await (await downloadPromise).path()));
  expect(zipEntries(archive).map((entry) => entry.name)).toEqual(["paper-tools-img2pdf.pdf"]);
});

test("grouped Interleave rejects more than its executable two-input cardinality", async ({ page }) => {
  await page.goto("/batch/");
  await page.locator("#batchOperation").selectOption("interleave");
  await page.locator("#batchRetries").selectOption("0");
  await page.locator("#batchInput").setInputFiles([
    { name: "one.pdf", mimeType: "application/pdf", buffer: Buffer.from([1]) },
    { name: "two.pdf", mimeType: "application/pdf", buffer: Buffer.from([2]) },
    { name: "three.pdf", mimeType: "application/pdf", buffer: Buffer.from([3]) },
  ]);

  await page.locator("#batchRun").click();
  await expect(page.locator("#err")).toContainText("Interleave requires exactly 2 inputs.");
});

function zipEntries(bytes) {
  const eocd = findLastSignature(bytes, 0x06054b50);
  const count = readU16(bytes, eocd + 10);
  let centralOffset = readU32(bytes, eocd + 16);
  const entries = [];
  for (let index = 0; index < count; index++) {
    expect(readU32(bytes, centralOffset)).toBe(0x02014b50);
    const size = readU32(bytes, centralOffset + 20);
    const nameLength = readU16(bytes, centralOffset + 28);
    const extraLength = readU16(bytes, centralOffset + 30);
    const commentLength = readU16(bytes, centralOffset + 32);
    const localOffset = readU32(bytes, centralOffset + 42);
    const name = new TextDecoder().decode(bytes.subarray(centralOffset + 46, centralOffset + 46 + nameLength));
    const localNameLength = readU16(bytes, localOffset + 26);
    const localExtraLength = readU16(bytes, localOffset + 28);
    const dataOffset = localOffset + 30 + localNameLength + localExtraLength;
    entries.push({ name, data: bytes.slice(dataOffset, dataOffset + size) });
    centralOffset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

function readU16(bytes, offset) {
  return bytes[offset] | (bytes[offset + 1] << 8);
}

function readU32(bytes, offset) {
  return (
    bytes[offset]
    | (bytes[offset + 1] << 8)
    | (bytes[offset + 2] << 16)
    | (bytes[offset + 3] << 24)
  ) >>> 0;
}

function findLastSignature(bytes, signature) {
  for (let offset = bytes.length - 4; offset >= 0; offset--) {
    if (readU32(bytes, offset) === signature) return offset;
  }
  throw new Error("ZIP signature not found");
}
