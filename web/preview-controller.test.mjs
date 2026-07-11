import assert from "node:assert/strict";
import { test } from "node:test";
import * as previewControllerModule from "./preview-controller.mjs";
import { createArtifact } from "./artifact.mjs";

const {
  PreviewController,
  captureInputArtifacts,
  downloadArtifact,
  normalizeOperationOutput,
  snapshotInputSources,
  snapshotFormSettings,
} = previewControllerModule;

const artifact = (revision = 0) => createArtifact(new Blob([String(revision)]), {
  id: "input", revision, name: "input.pdf", kind: "pdf",
});

test("preview reuses the exact result for unchanged input and params", async () => {
  let calls = 0;
  const result = createArtifact(new Blob(["result"]), { id: "result", name: "result.pdf", kind: "pdf" });
  const controller = new PreviewController(async () => { calls++; return result; });
  assert.equal(await controller.preview([artifact()], { quality: 1 }), result);
  assert.equal(await controller.preview([artifact()], { quality: 1 }), result);
  assert.equal(controller.result, result);
  assert.equal(calls, 1);
});

test("revision or settings changes mark cached preview stale", async () => {
  const controller = new PreviewController(async (inputs) => inputs[0]);
  await controller.preview([artifact(1)], { quality: 1 });
  assert.equal(controller.isStale([artifact(1)], { quality: 1 }), false);
  assert.equal(controller.isStale([artifact(2)], { quality: 1 }), true);
  assert.equal(controller.isStale([artifact(1)], { quality: 2 }), true);
});

test("superseded preview aborts the previous execution", async () => {
  let aborted = false;
  const controller = new PreviewController((inputs, params, { signal }) => new Promise((resolve, reject) => {
    signal.addEventListener("abort", () => { aborted = true; reject(new DOMException("aborted", "AbortError")); }, { once: true });
  }));
  const first = controller.preview([artifact(1)], {}).catch(() => null);
  const second = controller.preview([artifact(2)], {}).catch(() => null);
  await first;
  controller.cancel();
  await second;
  assert.equal(aborted, true);
});

test("an already-aborted preview signal rejects before executing", async () => {
  let calls = 0;
  const controller = new PreviewController(async () => {
    calls++;
    return artifact();
  });
  const abort = new AbortController();
  abort.abort();

  await assert.rejects(
    controller.preview([artifact()], {}, { signal: abort.signal }),
    (error) => error?.name === "AbortError",
  );
  assert.equal(calls, 0);
  assert.equal(controller.state, "idle");
});

test("a synchronous executor failure restores idle state and permits retry", async () => {
  let calls = 0;
  const result = createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  const controller = new PreviewController(() => {
    calls++;
    if (calls === 1) throw new Error("synchronous validation failed");
    return result;
  });
  const input = artifact(7);

  await assert.rejects(controller.preview([input]), /synchronous validation failed/);
  assert.equal(controller.state, "idle");
  assert.equal(controller.active, null);
  assert.equal(await controller.preview([input]), result);
  assert.equal(calls, 2);
});

test("concurrent requests for one snapshot share a single execution", async () => {
  let calls = 0;
  let release;
  const result = createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  const gate = new Promise((resolve) => { release = resolve; });
  const controller = new PreviewController(async () => {
    calls++;
    await gate;
    return result;
  });
  const input = artifact(3);
  const first = controller.preview([input], { quality: 80 });
  const second = controller.preview([input], { quality: 80 });
  release();
  assert.equal(await first, result);
  assert.equal(await second, result);
  assert.equal(calls, 1);
});

test("committed result becomes unavailable when stale and rerun replaces it", () => {
  const first = createArtifact(new Blob(["first"]), { name: "first.pdf", kind: "pdf" });
  const second = createArtifact(new Blob(["second"]), { name: "second.pdf", kind: "pdf" });
  const input = artifact(4);
  const controller = new PreviewController();

  controller.commit([input], { quality: 70 }, first);
  assert.equal(controller.state, "current");
  assert.equal(controller.cached([input], { quality: 70 }), first);

  controller.markStale();
  assert.equal(controller.state, "stale");
  assert.equal(controller.result, first, "stale UI retains the previous result until rerun");
  assert.equal(controller.cached([input], { quality: 70 }), null, "stale cache cannot be downloaded as current");

  controller.commit([input], { quality: 85 }, second);
  assert.equal(controller.state, "current");
  assert.equal(controller.result, second);
  assert.equal(controller.cached([input], { quality: 85 }), second);
});

test("operation output is normalized once and preview/download receive the exact Blob", () => {
  const bytes = new Uint8Array([11, 22, 33, 44]);
  const output = normalizeOperationOutput(bytes, {
    id: "result-1",
    revision: 1,
    name: "result.bin",
    kind: "binary",
    mime: "application/octet-stream",
  });
  const previewBlob = output.blob;
  const objectURLBlobs = [];
  const revoked = [];
  let clicked = 0;
  const anchor = { click() { clicked++; }, remove() {} };
  const document = {
    body: { appendChild(value) { assert.equal(value, anchor); } },
    createElement(tag) { assert.equal(tag, "a"); return anchor; },
  };
  const dispose = downloadArtifact(output, {
    document,
    schedule: (fn) => { fn(); return 1; },
    urlAPI: {
      createObjectURL(blob) { objectURLBlobs.push(blob); return "blob:download"; },
      revokeObjectURL(url) { revoked.push(url); },
    },
  });

  assert.equal(output.blob, previewBlob);
  assert.equal(objectURLBlobs[0], previewBlob);
  assert.equal(clicked, 1);
  assert.deepEqual(revoked, ["blob:download"]);
  dispose();
});

test("normalizing an existing Blob preserves its identity", () => {
  const blob = new Blob(["same"], { type: "text/plain" });
  const output = normalizeOperationOutput(blob, { name: "same.txt", kind: "text" });
  assert.equal(output.blob, blob);
});

test("form settings snapshot is deterministic and excludes files and action controls", () => {
  const controls = [
    { id: "quality", tagName: "INPUT", type: "range", value: "80", disabled: false, dataset: {} },
    { id: "grayscale", tagName: "INPUT", type: "checkbox", value: "on", checked: true, disabled: false, dataset: {} },
    { id: "format", tagName: "SELECT", type: "select-one", value: "png", disabled: false, multiple: false, dataset: {} },
    { id: "source", tagName: "INPUT", type: "file", value: "ignored", disabled: false, dataset: {} },
    { id: "run", tagName: "BUTTON", type: "submit", value: "ignored", disabled: false, dataset: {} },
    { id: "output", tagName: "TEXTAREA", type: "textarea", value: "generated", readOnly: true, disabled: false, dataset: {} },
    { id: "previewPage", tagName: "INPUT", type: "number", value: "2", disabled: false, dataset: {}, closest: () => ({}) },
  ];
  const first = snapshotFormSettings({ querySelectorAll: () => controls });
  const second = snapshotFormSettings({ querySelectorAll: () => [...controls].reverse() });
  assert.deepEqual(first, second);
  assert.deepEqual(first, [
    { key: "format", type: "select-one", value: "png" },
    { checked: true, key: "grayscale", type: "checkbox", value: "on" },
    { key: "quality", type: "range", value: "80" },
  ]);
  assert.equal(Object.isFrozen(first), true);
  assert.equal(Object.isFrozen(first[0]), true);
});

test("input capture includes dropzone revision and content identity", async () => {
  const first = new Blob(["one"], { type: "application/pdf" });
  Object.defineProperty(first, "name", { value: "one.pdf" });
  Object.defineProperty(first, "lastModified", { value: 10 });
  const second = new Blob(["two"], { type: "text/plain" });
  Object.defineProperty(second, "name", { value: "two.txt" });
  Object.defineProperty(second, "lastModified", { value: 20 });
  const drops = [
    { id: "aDrop", __paperFiles: [first], __paperRevision: 4 },
    { id: "bDrop", __paperFiles: [second], __paperRevision: 9 },
  ];
  const identities = [];
  const inputs = await captureInputArtifacts({ querySelectorAll: () => drops }, {
    contentIdentityForBlob: async (blob) => {
      identities.push(blob);
      return blob === first ? "sha256:first" : "sha256:second";
    },
  });

  assert.deepEqual(inputs.map(({ id, revision, contentIdentity, name, kind }) => ({ id, revision, contentIdentity, name, kind })), [
    { id: "aDrop:0", revision: 4, contentIdentity: "sha256:first", name: "one.pdf", kind: "pdf" },
    { id: "bDrop:0", revision: 9, contentIdentity: "sha256:second", name: "two.txt", kind: "text" },
  ]);
  assert.deepEqual(identities, [first, second]);
  assert.equal(inputs[0].blob, first);
});

test("input sources captured at run start survive a dropzone replacement", async () => {
  const original = new Blob(["original"], { type: "application/pdf" });
  Object.defineProperty(original, "name", { value: "original.pdf" });
  const replacement = new Blob(["replacement"], { type: "application/pdf" });
  Object.defineProperty(replacement, "name", { value: "replacement.pdf" });
  const drop = { id: "fileDrop", __paperFiles: [original], __paperRevision: 2 };
  const root = { querySelectorAll: () => [drop] };

  const sources = snapshotInputSources(root);
  drop.__paperFiles = [replacement];
  drop.__paperRevision = 3;
  const inputs = await captureInputArtifacts(root, {
    sources,
    contentIdentityForBlob: async (blob) => blob === original ? "sha256:original" : "sha256:replacement",
  });

  assert.equal(inputs[0].blob, original);
  assert.equal(inputs[0].name, "original.pdf");
  assert.equal(inputs[0].revision, 2);
  assert.equal(inputs[0].contentIdentity, "sha256:original");
});

test("input content identities are calculated sequentially", async () => {
  const first = new Blob(["one"]);
  const second = new Blob(["two"]);
  const sources = [
    { blob: first, id: "first", revision: 1, name: "one.bin", mime: "application/octet-stream" },
    { blob: second, id: "second", revision: 1, name: "two.bin", mime: "application/octet-stream" },
  ];
  const releases = [];
  let active = 0;
  let maximum = 0;
  let started = 0;
  const pending = captureInputArtifacts(null, {
    sources,
    contentIdentityForBlob: async () => {
      started++;
      active++;
      maximum = Math.max(maximum, active);
      await new Promise((resolve) => releases.push(resolve));
      active--;
      return `identity-${started}`;
    },
  });

  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(started, 1);
  releases.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(started, 2);
  releases.shift()();
  await pending;
  assert.equal(maximum, 1);
});
