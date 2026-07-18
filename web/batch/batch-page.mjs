import { operationsById } from "../operation-catalog.mjs";
import { createWasmClient } from "../wasm-client.mjs";
import { OperationRunner } from "../operation-runner.mjs";
import { failureManifestBlob, iterateBatch, uniqueOutputName } from "../batch.mjs";
import { createOutputSink } from "../output-sinks.mjs";
import { batchOperations, operationArgs } from "../operation-adapters.mjs";
import { zipStoreStream } from "../pdf2img/zip.mjs";

const labels = {
  merge: window.t("Merge", "병합"), interleave: window.t("Interleave", "교차 병합"),
  remove: window.t("Remove pages", "페이지 삭제"), rotate: window.t("Rotate", "회전"),
  flatten: window.t("Flatten", "평면화"), compress: window.t("Compress", "압축"),
  metadata: window.t("Metadata", "메타데이터"), watermark: window.t("Watermark", "워터마크"),
  pagenum: window.t("Page numbers", "페이지 번호"), protect: window.t("Protect", "암호 설정"),
  unlock: window.t("Unlock", "암호 해제"),
  img2pdf: window.t("Images to PDF", "이미지를 PDF로"),
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
    throw new Error(window.t("Worker execution is required for batch processing", "일괄 처리에는 워커 실행이 필요합니다."));
  }, { worker: { host: "/operation-worker.js", wasm: descriptor.entry } }),
});

folderButton.addEventListener("click", async () => {
  if (typeof window.showDirectoryPicker !== "function") {
    window.showErr(error, window.t("Folder output is not available in this browser.", "이 브라우저에서는 폴더 출력을 사용할 수 없습니다."));
    return;
  }
  try {
    directory = await window.showDirectoryPicker({ mode: "readwrite" });
    folderButton.textContent = `${window.t("Folder:", "폴더:")} ${directory.name}`;
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
  if (!(output?.data instanceof Uint8Array)) throw new Error(window.t("Operation returned no PDF data", "작업에서 PDF 데이터를 반환하지 않았습니다."));
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
    window.showErr(error, window.t("Select at least one PDF.", "PDF를 하나 이상 선택하세요."));
    return;
  }
  let parsed;
  try {
    parsed = JSON.parse(params.value);
  } catch {
    window.showErr(error, window.t("Parameters must be valid JSON.", "매개변수는 올바른 JSON 형식이어야 합니다."));
    return;
  }

  const operationId = select.value;
  const descriptor = operationsById.get(operationId);
  const mode = modeSelect.value;
  if (mode === "grouped") {
    const { minimum, maximum } = inputBounds(descriptor);
    if (files.length < minimum || files.length > maximum) {
      const requirement = minimum === maximum
        ? `${window.t("exactly", "정확히")} ${minimum}`
        : `${minimum}-${maximum}`;
      const operationLabel = labels[operationId] || operationId;
      window.showErr(error, `${operationLabel}${window.t(" requires", "에 필요한 입력은")} ${requirement}${window.t(" inputs.", "개입니다.")}`);
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
    summary.textContent = `${succeeded}${window.t(" succeeded", "개 성공")} · ${failures.length}${window.t(" failed", "개 실패")}${sink.kind === "directory" ? ` · ${window.t("saved as", "저장 위치:")} ${archiveName}` : ""}`;
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
