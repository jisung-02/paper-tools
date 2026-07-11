import assert from "node:assert/strict";
import { test } from "node:test";
import { validatePipeline, executePipeline } from "./pipeline.mjs";
import { createArtifact } from "./artifact.mjs";

const strongIdentity = (digit) => `sha256-tree-v1:${digit.repeat(64)}`;

const catalog = new Map([
  ["merge", { id: "merge", input: { kind: "pdf", min: 2, max: 20 }, output: { kind: "pdf" }, capabilities: { pipeline: true } }],
  ["compress", { id: "compress", input: { kind: "pdf", min: 1, max: 1 }, output: { kind: "pdf" }, capabilities: { pipeline: true } }],
  ["protect", { id: "protect", input: { kind: "pdf", min: 1, max: 1 }, output: { kind: "pdf" }, capabilities: { pipeline: true, terminal: true } }],
  ["text", { id: "text", input: { kind: "text", min: 1, max: 1 }, output: { kind: "pdf" }, capabilities: { pipeline: true } }],
  ["overlay", {
    id: "overlay",
    input: { kind: "pdf", min: 1, max: 1 },
    output: { kind: "pdf" },
    sidecars: { image: { kind: "image", required: true, min: 1, max: 1 } },
    capabilities: { pipeline: true },
  }],
  ["fanout", {
    id: "fanout",
    input: { kind: "pdf", min: 1, max: 1 },
    output: { kind: "pdf", cardinality: "many" },
    capabilities: { pipeline: true },
  }],
  ["bounded-fanout", {
    id: "bounded-fanout",
    input: { kind: "pdf", min: 1, max: 1 },
    output: { kind: "pdf", cardinality: "many", min: 2, max: 2 },
    capabilities: { pipeline: true },
  }],
]);

test("pipeline rejects type, cardinality and terminal violations", () => {
  assert.throws(() => validatePipeline([{ id: "1", operationId: "merge", params: {} }], catalog, { kind: "pdf", count: 1 }), /at least 2/);
  assert.throws(() => validatePipeline([{ id: "1", operationId: "text", params: {} }], catalog, { kind: "pdf", count: 1 }), /expects text/);
  assert.throws(() => validatePipeline([
    { id: "1", operationId: "protect", params: {} },
    { id: "2", operationId: "compress", params: {} },
  ], catalog, { kind: "pdf", count: 1 }), /terminal/);
});

test("pipeline rejects a missing required sidecar", () => {
  assert.throws(() => validatePipeline([
    { id: "1", operationId: "overlay", params: {} },
  ], catalog, { kind: "pdf", count: 1 }), /overlay.*sidecar.*image/i);
});

test("pipeline rejects incompatible cardinality after a many-output step", () => {
  assert.throws(() => validatePipeline([
    { id: "1", operationId: "fanout", params: {} },
    { id: "2", operationId: "compress", params: {} },
  ], catalog, { kind: "pdf", count: 1 }), /compress.*cardinality.*fanout/i);
});

test("pipeline validates sidecar kind and cardinality", () => {
  const text = createArtifact(new Blob(["x"]), { name: "note.txt", kind: "text" });
  const first = createArtifact(new Blob(["a"]), { name: "a.png", kind: "image" });
  const second = createArtifact(new Blob(["b"]), { name: "b.png", kind: "image" });

  assert.throws(() => validatePipeline([
    { id: "1", operationId: "overlay", params: {}, sidecars: { image: text } },
  ], catalog, { kind: "pdf", count: 1 }), /sidecar image expects image, got text/i);
  assert.throws(() => validatePipeline([
    { id: "1", operationId: "overlay", params: {}, sidecars: { image: [first, second] } },
  ], catalog, { kind: "pdf", count: 1 }), /sidecar image.*at most 1/i);
});

test("pipeline executes in order and reuses cached prefix", async () => {
  const input = createArtifact(new Blob(["a"]), {
    id: "input", name: "a.pdf", kind: "pdf", revision: 1, contentIdentity: strongIdentity("1"),
  });
  const calls = [];
  const runner = async (operationId, artifacts) => {
    calls.push(operationId);
    return createArtifact(new Blob([operationId]), { id: operationId, name: `${operationId}.pdf`, kind: "pdf", revision: 1 });
  };
  const steps = [
    { id: "1", operationId: "compress", params: { quality: 1 } },
    { id: "2", operationId: "protect", params: { password: "x" } },
  ];
  const cache = new Map();
  await executePipeline(steps, [input], { catalog, runner, cache });
  await executePipeline(steps, [input], { catalog, runner, cache });
  steps[1].params = { password: "changed" };
  await executePipeline(steps, [input], { catalog, runner, cache });
  assert.deepEqual(calls, ["compress", "protect", "protect"]);
});

test("pipeline cache key includes sidecar content identity", async () => {
  const input = createArtifact(new Blob(["pdf"]), {
    id: "input", name: "input.pdf", kind: "pdf", contentIdentity: strongIdentity("1"),
  });
  const first = createArtifact(new Blob(["a"]), {
    id: "sidecar", name: "overlay.png", kind: "image", contentIdentity: strongIdentity("2"),
  });
  const second = createArtifact(new Blob(["b"]), {
    id: "sidecar", name: "overlay.png", kind: "image", contentIdentity: strongIdentity("3"),
  });
  const calls = [];
  const runner = async (_operationId, _artifacts, _params, context) => {
    calls.push(context.sidecars.image.contentIdentity);
    return createArtifact(new Blob([String(calls.length)]), { name: "result.pdf", kind: "pdf" });
  };
  const cache = new Map();
  const pipeline = (sidecar) => [{
    id: "1", operationId: "overlay", params: { opacity: 0.5 }, sidecars: { image: sidecar },
  }];

  await executePipeline(pipeline(first), [input], { catalog, runner, cache });
  await executePipeline(pipeline(first), [input], { catalog, runner, cache });
  await executePipeline(pipeline(second), [input], { catalog, runner, cache });

  assert.deepEqual(calls, [strongIdentity("2"), strongIdentity("3")]);
});

test("pipeline does not cache sidecars without a strong content identity", async () => {
  const input = createArtifact(new Blob(["pdf"]), {
    name: "input.pdf", kind: "pdf", contentIdentity: strongIdentity("1"),
  });
  const first = createArtifact(new Blob(["a"]), { id: "sidecar", name: "overlay.png", kind: "image" });
  const second = createArtifact(new Blob(["b"]), { id: "sidecar", name: "overlay.png", kind: "image" });
  const calls = [];
  const runner = async (_operationId, _artifacts, _params, context) => {
    calls.push(await context.sidecars.image.blob.text());
    return createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  };
  const cache = new Map();
  const pipeline = (sidecar) => [{
    id: "1", operationId: "overlay", params: {}, sidecars: { image: sidecar },
  }];

  await executePipeline(pipeline(first), [input], { catalog, runner, cache });
  await executePipeline(pipeline(second), [input], { catalog, runner, cache });

  assert.deepEqual(calls, ["a", "b"]);
  assert.equal(cache.size, 0);
});

test("pipeline does not cache inputs without a strong content identity", async () => {
  const first = createArtifact(new Blob(["a"]), { id: "input", name: "input.pdf", kind: "pdf", revision: 1 });
  const second = createArtifact(new Blob(["b"]), { id: "input", name: "input.pdf", kind: "pdf", revision: 1 });
  const calls = [];
  const runner = async (_operationId, artifacts) => {
    calls.push(await artifacts[0].blob.text());
    return createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  };
  const steps = [{ id: "1", operationId: "compress", params: {} }];
  const cache = new Map();

  await executePipeline(steps, [first], { catalog, runner, cache });
  await executePipeline(steps, [second], { catalog, runner, cache });

  assert.deepEqual(calls, ["a", "b"]);
  assert.equal(cache.size, 0);
});

test("pipeline does not trust a forged content identity string", async () => {
  const first = createArtifact(new Blob(["a"]), {
    id: "input", name: "input.pdf", kind: "pdf", revision: 1, contentIdentity: "sha256:forged",
  });
  const second = createArtifact(new Blob(["b"]), {
    id: "input", name: "input.pdf", kind: "pdf", revision: 1, contentIdentity: "sha256:forged",
  });
  const calls = [];
  const runner = async (_operationId, artifacts) => {
    calls.push(await artifacts[0].blob.text());
    return createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  };
  const steps = [{ id: "1", operationId: "compress", params: {} }];
  const cache = new Map();

  await executePipeline(steps, [first], { catalog, runner, cache });
  await executePipeline(steps, [second], { catalog, runner, cache });

  assert.deepEqual(calls, ["a", "b"]);
  assert.equal(cache.size, 0);
});

test("pipeline cache evicts least-recently-used results within its byte budget", async () => {
  const input = createArtifact(new Blob(["pdf"]), {
    name: "input.pdf", kind: "pdf", contentIdentity: strongIdentity("1"),
  });
  const calls = [];
  const runner = async (_operationId, _artifacts, params) => {
    calls.push(params.variant);
    return createArtifact(new Blob(["data"]), { name: "result.pdf", kind: "pdf" });
  };
  const cache = new Map();
  const run = (variant) => executePipeline([
    { id: "1", operationId: "compress", params: { variant } },
  ], [input], { catalog, runner, cache, cacheMaxBytes: 8 });

  await run("a");
  await run("b");
  await run("a");
  await run("c");
  await run("b");

  assert.deepEqual(calls, ["a", "b", "c", "b"]);
  assert.equal(cache.size, 2);
});

test("pipeline returns an oversized result without caching it", async () => {
  const input = createArtifact(new Blob(["pdf"]), {
    name: "input.pdf", kind: "pdf", contentIdentity: strongIdentity("1"),
  });
  let calls = 0;
  const runner = async () => {
    calls++;
    return createArtifact(new Blob(["data"]), { name: "result.pdf", kind: "pdf" });
  };
  const steps = [{ id: "1", operationId: "compress", params: {} }];
  const cache = new Map();

  const first = await executePipeline(steps, [input], { catalog, runner, cache, cacheMaxBytes: 3 });
  const second = await executePipeline(steps, [input], { catalog, runner, cache, cacheMaxBytes: 3 });

  assert.equal(await first.artifacts[0].blob.text(), "data");
  assert.equal(await second.artifacts[0].blob.text(), "data");
  assert.equal(calls, 2);
  assert.equal(cache.size, 0);
});

test("pipeline rejects results that violate descriptor output cardinality", async () => {
  const input = createArtifact(new Blob(["pdf"]), { name: "input.pdf", kind: "pdf" });
  const output = () => createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });

  await assert.rejects(executePipeline([
    { id: "1", operationId: "compress", params: {} },
  ], [input], {
    catalog,
    runner: async () => [output(), output()],
  }), /compress.*exactly 1 output/i);
});

test("pipeline enforces finite bounds for many-output descriptors", async () => {
  const input = createArtifact(new Blob(["pdf"]), { name: "input.pdf", kind: "pdf" });
  const output = () => createArtifact(new Blob(["result"]), { name: "result.pdf", kind: "pdf" });
  const execute = (results) => executePipeline([
    { id: "1", operationId: "bounded-fanout", params: {} },
  ], [input], { catalog, runner: async () => results });

  await assert.rejects(execute([output()]), /bounded-fanout.*at least 2 output/i);
  await assert.rejects(execute([output(), output(), output()]), /bounded-fanout.*at most 2 output/i);
});

test("pipeline stops after a failed or aborted step", async () => {
  const input = createArtifact(new Blob(["a"]), { id: "input", name: "a.pdf", kind: "pdf" });
  const calls = [];
  await assert.rejects(executePipeline([
    { id: "1", operationId: "compress", params: {} },
    { id: "2", operationId: "protect", params: {} },
  ], [input], {
    catalog,
    runner: async (id) => { calls.push(id); throw new Error("failed"); },
  }), /failed/);
  assert.deepEqual(calls, ["compress"]);

  const controller = new AbortController();
  controller.abort();
  await assert.rejects(executePipeline([{ id: "1", operationId: "compress", params: {} }], [input], {
    catalog,
    signal: controller.signal,
    runner: async () => input,
  }), /Abort/);
});
