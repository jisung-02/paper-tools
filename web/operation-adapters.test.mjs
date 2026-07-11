import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import catalog from "../tools/operation-catalog.json" with { type: "json" };
import * as adapters from "./operation-adapters.mjs";
import { operationArgs, workflowOperations } from "./operation-adapters.mjs";

const a = new Uint8Array([1]);
const b = new Uint8Array([2]);

test("workflow adapters produce exact positional WASM arguments", () => {
  assert.deepEqual(operationArgs("merge", [a, b], {}), [[a, b]]);
  assert.deepEqual(operationArgs("interleave", [a, b], { reverseB: true }), [a, b, true]);
  assert.deepEqual(operationArgs("rotate", [a], { pages: "1-2", degrees: 90 }), [a, "1-2", 90]);
  assert.deepEqual(operationArgs("compress", [a], {}), [a, 80, 1600, false]);
  assert.deepEqual(operationArgs("metadata", [a], { strip: true }), [a, "", "", "", "", true]);
  assert.deepEqual(operationArgs("flatten", [a], {}), [a]);
});

test("workflow operation inventory contains only supported adapters", () => {
  assert.ok(workflowOperations.includes("merge"));
  assert.ok(workflowOperations.includes("compress"));
  assert.ok(workflowOperations.includes("metadata"));
  assert.ok(workflowOperations.includes("protect"));
  assert.throws(() => operationArgs("pdfdiff", [a, b], {}), /not supported/);
});

test("adapter validation rejects dangerous or malformed parameters", () => {
  assert.throws(() => operationArgs("rotate", [a], { degrees: 45 }), /degrees/);
  assert.throws(() => operationArgs("compress", [a], { quality: 101 }), /quality/);
  assert.throws(() => operationArgs("protect", [a], { userPassword: "" }), /password/);
});

test("explicit batch operation inventory is catalog-backed and executable", () => {
  const expected = [
    "merge", "interleave", "remove", "rotate", "flatten", "compress",
    "metadata", "watermark", "pagenum", "protect", "unlock", "img2pdf",
  ];
  assert.deepEqual(adapters.batchOperations, expected);
  assert.equal(new Set(adapters.batchOperations).size, adapters.batchOperations.length);
  const params = {
    remove: { pages: "1" },
    watermark: { text: "DRAFT" },
    protect: { userPassword: "secret" },
    unlock: { password: "secret" },
    img2pdf: { pageSize: "a4" },
  };
  for (const id of adapters.batchOperations) {
    const descriptor = catalog.find((entry) => entry.id === id);
    assert.ok(descriptor, `${id} must exist in the catalog`);
    assert.equal(descriptor.capabilities.batch, true, `${id} must be batch-capable`);
    assert.ok(["pdf", "image"].includes(descriptor.input.kind), `${id} input kind must be supported`);
    const inputCount = descriptor.input.min > 1 ? descriptor.input.min : 1;
    const inputs = Array.from({ length: inputCount }, (_, index) => new Uint8Array([index + 1]));
    assert.doesNotThrow(() => operationArgs(id, inputs, params[id] || {}), `${id} must have an adapter`);
  }
});

test("batch page consumes the explicit batch operation inventory", async () => {
  const page = await readFile(new URL("./batch/batch-page.mjs", import.meta.url), "utf8");
  assert.match(page, /import \{[^}]*batchOperations[^}]*\} from ["']\.\.\/operation-adapters\.mjs["']/s);
  assert.doesNotMatch(page, /workflowOperations/);
  assert.doesNotMatch(page, /function batchOperationArgs/);
});
