import { operationsById } from "../operation-catalog.mjs";
import { createWasmClient } from "../wasm-client.mjs";
import { OperationRunner } from "../operation-runner.mjs";
import { createArtifact, contentIdentityForBlob, artifactCacheKey, ArtifactURL } from "../artifact.mjs";
import { executePipeline } from "../pipeline.mjs";
import { operationArgs, workflowOperations } from "../operation-adapters.mjs";

const defaults = {
  merge: {}, interleave: { reverseB: false }, remove: { pages: "1" }, reorder: { order: "1" },
  rotate: { pages: "all", degrees: 90 }, flatten: {}, compress: { quality: 80, maxWidth: 1600, grayscale: false },
  metadata: { title: "", author: "", subject: "", keywords: "", strip: false },
  watermark: { text: "DRAFT", fontSize: 48, opacity: 0.2, diagonal: true },
  pagenum: { format: "{n}", fontSize: 11 }, protect: { userPassword: "", ownerPassword: "", cipher: "aes256" },
  unlock: { password: "" },
};

const labels = {
  merge: window.t("Merge", "병합"), interleave: window.t("Interleave", "교차 병합"),
  remove: window.t("Remove pages", "페이지 삭제"), reorder: window.t("Reorder", "순서 변경"),
  rotate: window.t("Rotate", "회전"), flatten: window.t("Flatten", "평면화"),
  compress: window.t("Compress", "압축"), metadata: window.t("Metadata", "메타데이터"),
  watermark: window.t("Watermark", "워터마크"), pagenum: window.t("Page numbers", "페이지 번호"),
  protect: window.t("Protect", "암호 설정"), unlock: window.t("Unlock", "암호 해제"),
};

const fileInput = document.getElementById("workflowInput");
const select = document.getElementById("operationSelect");
const list = document.getElementById("steps");
const runButton = document.getElementById("runWorkflow");
const cancelButton = document.getElementById("cancelWorkflow");
const status = document.getElementById("status");
const error = document.getElementById("err");
const resultSection = document.getElementById("workflowResult");
const resultSummary = document.getElementById("resultSummary");
const preview = document.getElementById("resultPreview");
let steps = [];
let nextStep = 1;
let activeController;
let resultArtifact;
let resultURL;
const cache = new Map();
let cacheInputKey;

window.dropzone("workflowDrop", { multiple: true });

for (const id of workflowOperations) {
  const option = document.createElement("option");
  option.value = id;
  option.textContent = labels[id] || id;
  select.appendChild(option);
}

const runner = new OperationRunner(operationsById, {
  clientFactory: (descriptor) => createWasmClient(() => { throw new Error(window.t("Worker execution is required for workflows", "워크플로우에는 워커 실행이 필요합니다.")); }, {
    worker: { host: "/operation-worker.js", wasm: descriptor.entry },
  }),
});

function renderSteps() {
  list.replaceChildren();
  steps.forEach((step, index) => {
    const item = document.createElement("li");
    item.className = "workflow-step";
    const header = document.createElement("header");
    const title = document.createElement("strong");
    title.textContent = `${index + 1}. ${labels[step.operationId] || step.operationId}`;
    const actions = document.createElement("div");
    actions.className = "step-actions";
    for (const [label, delta] of [["↑", -1], ["↓", 1]]) {
      const move = document.createElement("button");
      move.type = "button";
      move.className = "secondary";
      move.textContent = label;
      move.disabled = index + delta < 0 || index + delta >= steps.length;
      move.addEventListener("click", () => { const target = index + delta; [steps[index], steps[target]] = [steps[target], steps[index]]; renderSteps(); });
      actions.appendChild(move);
    }
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "secondary";
    remove.textContent = "×";
    remove.setAttribute("aria-label", window.t("Remove step", "단계 삭제"));
    remove.addEventListener("click", () => { steps.splice(index, 1); renderSteps(); });
    actions.appendChild(remove);
    header.append(title, actions);
    const params = document.createElement("textarea");
    params.value = JSON.stringify(step.params, null, 2);
    params.setAttribute("aria-label", `${title.textContent} ${window.t("parameters JSON", "매개변수 JSON")}`);
    params.addEventListener("change", () => {
      try { step.params = JSON.parse(params.value); params.setCustomValidity(""); }
      catch { params.setCustomValidity(window.t("Invalid JSON", "올바르지 않은 JSON입니다.")); params.reportValidity(); }
    });
    item.append(header, params);
    list.appendChild(item);
  });
}

document.getElementById("addStep").addEventListener("click", () => {
  const id = select.value;
  steps.push({ id: `step-${nextStep++}`, operationId: id, params: structuredClone(defaults[id] || {}) });
  renderSteps();
});

async function artifactBytes(artifacts) {
  return Promise.all(artifacts.map(async (artifact) => new Uint8Array(await artifact.blob.arrayBuffer())));
}

async function executeOperation(operationId, artifacts, params, context) {
  const args = operationArgs(operationId, await artifactBytes(artifacts), params);
  const result = await runner.invoke(operationId, args, {
    inputCount: artifacts.length,
    inputKinds: artifacts.map((artifact) => artifact.kind),
    params,
    signal: context.signal,
    onProgress: (phase) => { status.textContent = `${labels[operationId] || operationId}: ${phase}`; },
  });
  if (result?.error) throw new Error(result.error);
  if (!(result?.data instanceof Uint8Array)) throw new Error(`${operationId} ${window.t("returned no PDF data", "PDF 데이터를 반환하지 않았습니다.")}`);
  return createArtifact(new Blob([result.data], { type: "application/pdf" }), {
    name: `${operationId}.pdf`, kind: "pdf", mime: "application/pdf",
  });
}

runButton.addEventListener("click", async () => {
  error.textContent = "";
  // A failed re-run must not leave the previous run's result/Download
  // sitting next to the new error.
  resultSection.hidden = true;
  resultURL?.dispose();
  resultURL = undefined;
  resultArtifact = undefined;
  const files = [...fileInput.files];
  if (!files.length || !steps.length) { window.showErr(error, window.t("Select PDFs and add at least one step.", "PDF를 선택하고 최소 한 단계를 추가하세요.")); return; }
  activeController?.abort();
  activeController = new AbortController();
  runButton.disabled = true;
  cancelButton.disabled = false;
  status.hidden = false;
  try {
    const artifacts = await Promise.all(files.map(async (file, index) => createArtifact(file, {
      id: `input-${index}`,
      name: file.name,
      kind: "pdf",
      mime: "application/pdf",
      revision: file.lastModified || 0,
      contentIdentity: await contentIdentityForBlob(file),
    })));
    const inputKey = artifactCacheKey(artifacts);
    if (cacheInputKey !== undefined && cacheInputKey !== inputKey) cache.clear();
    cacheInputKey = inputKey;
    const executed = await executePipeline(steps, artifacts, {
      catalog: operationsById, runner: executeOperation, signal: activeController.signal, cache,
      onProgress: ({ phase, index }) => { status.textContent = `${index + 1}/${steps.length}: ${phase}`; },
    });
    resultArtifact = executed.artifacts[0];
    resultURL?.dispose();
    resultURL = new ArtifactURL(resultArtifact.blob);
    preview.src = resultURL.url;
    resultSummary.textContent = `${resultArtifact.name} · ${resultArtifact.size.toLocaleString()} ${window.t("bytes", "바이트")}`;
    resultSection.hidden = false;
  } catch (cause) {
    if (cause?.name !== "AbortError") window.showErr(error, cause.message || cause);
  } finally {
    runButton.disabled = false;
    cancelButton.disabled = true;
    status.hidden = true;
  }
});

cancelButton.addEventListener("click", () => { activeController?.abort(); runner.dispose(); });
document.getElementById("downloadResult").addEventListener("click", () => {
  if (resultArtifact) resultArtifact.blob.arrayBuffer().then((bytes) => window.download(new Uint8Array(bytes), "workflow-result.pdf", "application/pdf"));
});
document.getElementById("exportWorkflow").addEventListener("click", () => {
  const bytes = new TextEncoder().encode(JSON.stringify({ version: 1, steps }, null, 2));
  window.download(bytes, "paper-tools-workflow.json", "application/json");
});
document.getElementById("importWorkflow").addEventListener("change", async (event) => {
  try {
    const parsed = JSON.parse(await event.target.files[0].text());
    if (parsed.version !== 1 || !Array.isArray(parsed.steps)) throw new Error(window.t("Invalid workflow file.", "올바르지 않은 워크플로우 파일입니다."));
    steps = parsed.steps.map((step) => ({ id: `step-${nextStep++}`, operationId: step.operationId, params: step.params || {} }));
    if (steps.some((step) => !workflowOperations.includes(step.operationId))) throw new Error(window.t("Workflow contains an unsupported operation.", "워크플로우에 지원되지 않는 작업이 포함되어 있습니다."));
    renderSteps();
  } catch (cause) { window.showErr(error, cause.message || cause); }
});
window.addEventListener("pagehide", () => { activeController?.abort(); runner.dispose(); resultURL?.dispose(); }, { once: true });
