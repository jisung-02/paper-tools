const dependencyURL = new URL("./artifact.mjs", import.meta.url);
const dependencyRetry = new URL(import.meta.url).searchParams.get("preview-retry");
if (dependencyRetry) dependencyURL.searchParams.set("preview-retry", dependencyRetry);
const {
  ArtifactURL,
  artifactCacheKey,
  contentIdentityForBlob,
  createArtifact,
} = await import(dependencyURL.href);

function abortError() {
  return new DOMException("Preview aborted", "AbortError");
}

function artifactKind(name, mime) {
  const type = String(mime || "").toLowerCase();
  const lower = String(name || "").toLowerCase();
  if (type === "application/pdf" || lower.endsWith(".pdf")) return "pdf";
  if (type.startsWith("image/") || /\.(png|jpe?g|gif|webp|avif)$/.test(lower)) return "image";
  if (type.startsWith("text/") || /\.(txt|csv|json|md)$/.test(lower)) return lower.endsWith(".csv") ? "csv" : "text";
  if (type.includes("zip") || lower.endsWith(".zip")) return "zip";
  if (lower.endsWith(".docx")) return "docx";
  if (lower.endsWith(".hwpx")) return "hwpx";
  if (lower.endsWith(".hwp")) return "hwp";
  if (lower.endsWith(".xlsx")) return "xlsx";
  return "binary";
}

function assertArtifact(value) {
  if (!value || !(value.blob instanceof Blob)) throw new TypeError("preview result must be an Artifact");
  return value;
}

export class PreviewController {
  constructor(execute) {
    if (execute != null && typeof execute !== "function") throw new TypeError("preview executor must be a function");
    this.execute = execute || null;
    this.key = null;
    this.result = null;
    this.active = null;
    this.state = "idle";
  }

  isStale(inputs, params = {}) {
    if (!this.result || this.state === "stale") return true;
    return this.key !== artifactCacheKey(inputs, params);
  }

  cached(inputs, params = {}) {
    return !this.isStale(inputs, params) ? this.result : null;
  }

  commit(inputs, params = {}, result) {
    const artifact = assertArtifact(result);
    this.cancel();
    this.key = artifactCacheKey(inputs, params);
    this.result = artifact;
    this.state = "current";
    return artifact;
  }

  markStale() {
    this.cancel();
    if (this.result) this.state = "stale";
    return this.state === "stale";
  }

  preview(inputs, params = {}, options = {}) {
    if (!this.execute) throw new Error("preview executor is unavailable");
    if (options.signal?.aborted) return Promise.reject(abortError());
    const key = artifactCacheKey(inputs, params);
    const cached = this.cached(inputs, params);
    if (cached) return Promise.resolve(cached);
    if (this.active?.key === key) return this.active.promise;

    this.cancel();
    const controller = new AbortController();
    const forwardAbort = () => controller.abort();
    options.signal?.addEventListener("abort", forwardAbort, { once: true });
    this.state = "running";

    const active = { controller, key, promise: null };
    active.promise = (async () => {
      try {
        await Promise.resolve();
        if (controller.signal.aborted) throw abortError();
        const result = assertArtifact(await this.execute(inputs, params, {
          signal: controller.signal,
          onProgress: options.onProgress,
        }));
        if (controller.signal.aborted) throw abortError();
        this.key = key;
        this.result = result;
        this.state = "current";
        return result;
      } catch (error) {
        if (this.active === active && this.state === "running") {
          this.state = this.result ? "stale" : "idle";
        }
        if (controller.signal.aborted && error?.name !== "AbortError") throw abortError();
        throw error;
      } finally {
        options.signal?.removeEventListener("abort", forwardAbort);
        if (this.active === active) this.active = null;
      }
    })();
    this.active = active;
    return active.promise;
  }

  cancel() {
    this.active?.controller.abort();
    this.active = null;
    if (this.state === "running") this.state = this.result ? "stale" : "idle";
  }

  invalidate() {
    this.cancel();
    this.key = null;
    this.result = null;
    this.state = "idle";
  }
}

export function normalizeOperationOutput(data, options = {}) {
  if (data?.blob instanceof Blob && data.name != null && data.kind != null) return data;
  const blob = data instanceof Blob
    ? data
    : new Blob([data], { type: options.mime || "application/octet-stream" });
  const name = String(options.name || "output.bin");
  return createArtifact(blob, {
    ...options,
    name,
    kind: options.kind || artifactKind(name, options.mime || blob.type),
    mime: options.mime || blob.type || "application/octet-stream",
  });
}

export function downloadArtifact(artifact, options = {}) {
  assertArtifact(artifact);
  const document = options.document || globalThis.document;
  const urlAPI = options.urlAPI || globalThis.URL;
  const schedule = options.schedule || globalThis.setTimeout;
  if (!document?.createElement || !urlAPI?.createObjectURL || !urlAPI?.revokeObjectURL) {
    throw new Error("download is unavailable");
  }
  const resource = new ArtifactURL(artifact.blob, urlAPI);
  const dispose = () => resource.dispose();
  try {
    const anchor = document.createElement("a");
    anchor.href = resource.url;
    anchor.download = options.name || artifact.name;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    schedule(dispose, options.revokeDelay ?? 1000);
  } catch (error) {
    dispose();
    throw error;
  }
  return dispose;
}

function controlValue(control, type) {
  if (type === "checkbox" || type === "radio") {
    return { checked: Boolean(control.checked), value: String(control.value ?? "") };
  }
  if (control.multiple && control.selectedOptions) {
    return { value: [...control.selectedOptions].map((option) => String(option.value)) };
  }
  return { value: String(control.value ?? "") };
}

export function snapshotFormSettings(root = globalThis.document) {
  const controls = [...(root?.querySelectorAll?.("input,select,textarea,button") || [])];
  const snapshot = controls.flatMap((control, index) => {
    const tag = String(control.tagName || "").toLowerCase();
    const type = String(control.type || tag || "text").toLowerCase();
    if (control.disabled || control.readOnly || control.dataset?.previewIgnore === "true" || control.closest?.(".result-preview")) return [];
    if (tag === "button" || ["file", "button", "submit", "reset", "image"].includes(type)) return [];
    const key = String(control.id || control.name || `${tag || "control"}:${index}`);
    return [Object.freeze({ key, type, ...controlValue(control, type) })];
  });
  snapshot.sort((left, right) => {
    const keyOrder = left.key < right.key ? -1 : left.key > right.key ? 1 : 0;
    if (keyOrder) return keyOrder;
    const typeOrder = left.type < right.type ? -1 : left.type > right.type ? 1 : 0;
    if (typeOrder) return typeOrder;
    const leftJSON = JSON.stringify(left);
    const rightJSON = JSON.stringify(right);
    return leftJSON < rightJSON ? -1 : leftJSON > rightJSON ? 1 : 0;
  });
  return Object.freeze(snapshot);
}

export function snapshotInputSources(root = globalThis.document) {
  const drops = [...(root?.querySelectorAll?.(".drop") || [])];
  const sources = [];
  drops.forEach((drop, dropIndex) => {
    const files = [...(drop.__paperFiles || [])];
    files.forEach((file, fileIndex) => {
      if (!(file instanceof Blob)) throw new TypeError("preview input must be a Blob");
      const revision = Number.isSafeInteger(drop.__paperRevision) && drop.__paperRevision >= 0
        ? drop.__paperRevision
        : Number.isSafeInteger(file.lastModified) && file.lastModified >= 0 ? file.lastModified : 0;
      sources.push(Object.freeze({
        blob: file,
        id: `${drop.id || `drop-${dropIndex}`}:${fileIndex}`,
        revision,
        name: String(file.name || `input-${fileIndex}.bin`),
        mime: String(file.type || "application/octet-stream"),
      }));
    });
  });
  return Object.freeze(sources);
}

export async function captureInputArtifacts(root = globalThis.document, options = {}) {
  const identify = options.contentIdentityForBlob || contentIdentityForBlob;
  const sources = options.sources || snapshotInputSources(root);
  const artifacts = [];
  for (const { blob, id, revision, name, mime } of sources) {
    artifacts.push(createArtifact(blob, {
      id,
      revision,
      contentIdentity: await identify(blob),
      name,
      kind: artifactKind(name, mime),
      mime,
    }));
  }
  return artifacts;
}
