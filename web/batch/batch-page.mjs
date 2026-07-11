import { operationsById } from "../operation-catalog.mjs";
import { createWasmClient } from "../wasm-client.mjs";
import { OperationRunner } from "../operation-runner.mjs";
import { failureManifestBlob, iterateBatch, uniqueOutputName } from "../batch.mjs";
import { createOutputSink } from "../output-sinks.mjs";
import { batchOperations, operationArgs } from "../operation-adapters.mjs";
import { zipStoreStream } from "../pdf2img/zip.mjs";

const labels = {
  merge: "Merge", interleave: "Interleave", remove: "Remove pages", rotate: "Rotate",
  flatten: "Flatten", compress: "Compress", metadata: "Metadata", watermark: "Watermark",
  pagenum: "Page numbers", protect: "Protect", unlock: "Unlock",
  img2pdf: "Images to PDF",
};
const defaults = {
  merge: {}, interleave: { reverseB: false }, remove: { pages: "1" }, rotate: { pages: "all", degrees: 90 },
  flatten: {}, compress: { quality: 80, maxWidth: 1600, grayscale: false },
  metadata: { title: "", author: "", subject: "", keywords: "", strip: false },
  watermark: { text: "DRAFT", fontSize: 48, opacity: 0.2, diagonal: true },
  pagenum: { format: "{n}", fontSize: 11 }, protect: { userPassword: "", ownerPassword: "", cipher: "aes256" },
  unlock: { password: "" },
  img2pdf: { pageSize: "a4", orientation: "auto", fit: "fit", marginPt: 0, autoRotate: true },
};
const supported = batchOperations
  .map((id) => {
    const descriptor = operationsById.get(id);
    if (!descriptor || !descriptor.capabilities.batch || !["pdf", "image"].includes(descriptor.input.kind)) {
      throw new Error(`invalid batch operation: ${id}`);
    }
    return id;
  })
  .sort((left, right) => {
    const leftGroupedOnly = (operationsById.get(left).input.min ?? 1) > 1;
    const rightGroupedOnly = (operationsById.get(right).input.min ?? 1) > 1;
    return Number(leftGroupedOnly) - Number(rightGroupedOnly);
  });

const select = document.getElementById("batchOperation");
const modeSelect = document.getElementById("batchMode");
const independentOption = modeSelect.querySelector("option[value=independent]");
const groupedOption = modeSelect.querySelector("option[value=grouped]");
const params = document.getElementById("batchParams");
const input = document.getElementById("batchInput");
const runButton = document.getElementById("batchRun");
const cancelButton = document.getElementById("batchCancel");
const folderButton = document.getElementById("batchFolder");
const downloadButton = document.getElementById("batchDownload");
const status = document.getElementById("status");
const error = document.getElementById("err");
const resultSection = document.getElementById("batchResult");
const summary = document.getElementById("batchSummary");
const failureList = document.getElementById("batchFailures");
let directory;
let controller;
let activeSink;
let completedSink;
let cleaningSink;
let pendingCleanup = Promise.resolve();
let archiveBlob;
let archiveName = "paper-tools-batch.zip";

window.dropzone("batchDrop", { multiple: true });
for (const id of supported) {
  const option = document.createElement("option");
  option.value = id;
  option.textContent = labels[id] || id;
  select.appendChild(option);
}

function updateOperationOptions() {
  const descriptor = operationsById.get(select.value);
  params.value = JSON.stringify(defaults[select.value] || {}, null, 2);
  const { minimum, maximum } = inputBounds(descriptor);
  const canGroup = maximum > 1;
  input.accept = descriptor?.input?.kind === "image" ? "image/png,image/jpeg" : "application/pdf";
  independentOption.disabled = minimum > 1;
  groupedOption.disabled = !canGroup;
  if (independentOption.disabled) modeSelect.value = "grouped";
  else if (!canGroup) modeSelect.value = "independent";
}

function inputBounds(descriptor) {
  const minimum = descriptor?.input?.min ?? 1;
  return { minimum, maximum: descriptor?.input?.max ?? minimum };
}

select.addEventListener("change", updateOperationOptions);
updateOperationOptions();

const runner = new OperationRunner(operationsById, {
  clientFactory: (descriptor) => createWasmClient(() => {
    throw new Error("Worker execution is required for batch processing");
  }, { worker: { host: "/operation-worker.js", wasm: descriptor.entry } }),
});

folderButton.addEventListener("click", async () => {
  if (typeof window.showDirectoryPicker !== "function") {
    window.showErr(error, "Folder output is not available in this browser.");
    return;
  }
  try {
    directory = await window.showDirectoryPicker({ mode: "readwrite" });
    folderButton.textContent = `Folder: ${directory.name}`;
  } catch (cause) {
    if (cause?.name !== "AbortError") window.showErr(error, cause.message || cause);
  }
});

function outputName(file, operationId, used) {
  const stem = file.name.replace(/\.[^.]+$/, "");
  return uniqueOutputName(`${stem}-${operationId}.pdf`, used);
}

async function invokeOperation(operationId, value, parsed, context, mode, used) {
  const files = mode === "grouped" ? value : [value];
  const inputs = [];
  for (const file of files) inputs.push(new Uint8Array(await file.arrayBuffer()));
  const descriptor = operationsById.get(operationId);
  const args = operationArgs(operationId, inputs, parsed);
  const output = await runner.invoke(operationId, args, {
    inputCount: files.length,
    inputKinds: files.map(() => descriptor.input.kind),
    params: parsed,
    signal: context.signal,
  });
  if (output?.error) throw new Error(output.error);
  if (!(output?.data instanceof Uint8Array)) throw new Error("Operation returned no PDF data");
  const name = mode === "grouped"
    ? uniqueOutputName(`paper-tools-${operationId}.pdf`, used)
    : outputName(files[0], operationId, used);
  return { name, data: output.data };
}

function showFailure(failure) {
  const item = document.createElement("li");
  item.textContent = `${failure.name}: ${failure.error.message}`;
  failureList.appendChild(item);
}

function cleanupSink(sink) {
  if (!sink) return pendingCleanup;
  if (cleaningSink === sink) return pendingCleanup;
  cleaningSink = sink;
  pendingCleanup = sink.cleanup().finally(() => {
    if (cleaningSink === sink) cleaningSink = null;
    if (completedSink === sink) completedSink = null;
  });
  return pendingCleanup;
}

async function cleanupCompletedSink() {
  await cleanupSink(completedSink);
}

runButton.addEventListener("click", async () => {
  error.textContent = "";
  failureList.replaceChildren();
  resultSection.hidden = true;
  downloadButton.hidden = true;
  archiveBlob = null;
  await cleanupCompletedSink();

  const files = [...input.files];
  if (!files.length) {
    window.showErr(error, "Select at least one PDF.");
    return;
  }
  let parsed;
  try {
    parsed = JSON.parse(params.value);
  } catch {
    window.showErr(error, "Parameters must be valid JSON.");
    return;
  }

  const operationId = select.value;
  const descriptor = operationsById.get(operationId);
  const mode = modeSelect.value;
  if (mode === "grouped") {
    const { minimum, maximum } = inputBounds(descriptor);
    if (files.length < minimum || files.length > maximum) {
      const requirement = minimum === maximum ? `exactly ${minimum}` : `${minimum}-${maximum}`;
      window.showErr(error, `${labels[operationId] || operationId} requires ${requirement} inputs.`);
      return;
    }
  }

  controller?.abort();
  controller = new AbortController();
  runButton.disabled = true;
  cancelButton.disabled = false;
  status.hidden = false;
  const failures = [];
  const used = new Set();
  let succeeded = 0;
  let sink;

  try {
    sink = await createOutputSink({
      directory,
      name: "paper-tools-batch.zip",
      storage: navigator.storage,
    });
    activeSink = sink;

    async function* archiveEntries() {
      for await (const event of iterateBatch(files, (value, context) => (
        invokeOperation(operationId, value, parsed, context, mode, used)
      ), {
        mode,
        retries: Number(document.getElementById("batchRetries").value),
        signal: controller.signal,
        onProgress: ({ completed, total, failures: failed }) => {
          status.textContent = `${completed}/${total} · ${failed} failed`;
        },
      })) {
        if (event.ok) {
          succeeded++;
          yield event.value;
        } else {
          failures.push(event);
          showFailure(event);
        }
      }
      if (failures.length) {
        yield {
          name: uniqueOutputName("paper-tools-failures.json", used),
          data: failureManifestBlob(failures),
        };
      }
    }

    await zipStoreStream(archiveEntries(), (chunk) => sink.write(chunk));
    archiveBlob = await sink.close();
    archiveName = sink.name || "paper-tools-batch.zip";
    activeSink = null;
    completedSink = sink;
    summary.textContent = `${succeeded} succeeded · ${failures.length} failed${sink.kind === "directory" ? ` · saved as ${archiveName}` : ""}`;
    downloadButton.hidden = !archiveBlob;
    resultSection.hidden = false;
  } catch (cause) {
    activeSink = null;
    await sink?.abort();
    if (cause?.name !== "AbortError") window.showErr(error, cause.message || cause);
  } finally {
    runButton.disabled = false;
    cancelButton.disabled = true;
    status.hidden = true;
  }
});

cancelButton.addEventListener("click", () => {
  controller?.abort();
  activeSink?.abort();
  runner.dispose();
});

downloadButton.addEventListener("click", () => {
  if (!archiveBlob) return;
  const sink = completedSink;
  const url = URL.createObjectURL(archiveBlob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = archiveName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => {
    URL.revokeObjectURL(url);
    cleanupSink(sink);
  }, 1000);
});

window.addEventListener("pagehide", () => {
  controller?.abort();
  activeSink?.abort();
  cleanupCompletedSink();
  runner.dispose();
}, { once: true });
