import { artifactCacheKey } from "./artifact.mjs";

const defaultCacheMaxBytes = 64 * 1024 * 1024;

function descriptorFor(catalog, operationId) {
  const descriptor = catalog instanceof Map ? catalog.get(operationId) : catalog[operationId];
  if (!descriptor) throw new Error(`unknown operation: ${operationId}`);
  if (!descriptor.capabilities?.pipeline) throw new Error(`${operationId} is not available in pipelines`);
  return descriptor;
}

function cardinality(input, count, operationId) {
  const min = input.min ?? 1;
  const max = input.max ?? min;
  if (count < min) throw new Error(`${operationId} requires at least ${min} input(s)`);
  if (count > max) throw new Error(`${operationId} accepts at most ${max} input(s)`);
}

function outputRange(output, operationId) {
  const value = output.cardinality || "one";
  if (value === "one") return { min: 1, max: 1 };
  if (value === "many") return { min: output.min ?? 1, max: output.max ?? Infinity };
  throw new Error(`${operationId} has invalid output cardinality`);
}

function validateSidecars(descriptor, step) {
  for (const [name, definition] of Object.entries(descriptor.sidecars || {})) {
    const value = step.sidecars?.[name];
    if (definition.required && (value == null || (Array.isArray(value) && value.length === 0))) {
      throw new Error(`${descriptor.id} requires sidecar ${name}`);
    }
    if (value == null) continue;
    const artifacts = Array.isArray(value) ? value : [value];
    cardinality(definition, artifacts.length, `${descriptor.id} sidecar ${name}`);
    for (const artifact of artifacts) {
      if (artifact?.kind !== definition.kind) {
        throw new Error(`${descriptor.id} sidecar ${name} expects ${definition.kind}, got ${artifact?.kind}`);
      }
    }
  }
}

function validateOutputCardinality(output, count, operationId) {
  const value = output.cardinality || "one";
  if (value === "one") {
    if (count !== 1) throw new Error(`${operationId} must return exactly 1 output`);
    return;
  }
  const range = outputRange(output, operationId);
  if (count < range.min) throw new Error(`${operationId} must return at least ${range.min} output(s)`);
  if (count > range.max) throw new Error(`${operationId} must return at most ${range.max} output(s)`);
}

function validateOutput(descriptor, artifacts) {
  validateOutputCardinality(descriptor.output, artifacts.length, descriptor.id);
  if (artifacts.some((artifact) => artifact?.kind !== descriptor.output.kind)) {
    throw new Error(`${descriptor.id} returned an invalid artifact`);
  }
}

function hasStrongIdentity(artifact) {
  return typeof artifact?.contentIdentity === "string" &&
    /^sha256-tree-v1:[0-9a-f]{64}$/.test(artifact.contentIdentity);
}

function sidecarsHaveStrongIdentities(sidecars) {
  return Object.values(sidecars).every((value) => {
    const artifacts = Array.isArray(value) ? value : [value];
    return artifacts.every(hasStrongIdentity);
  });
}

function artifactBytes(artifacts) {
  let total = 0;
  for (const artifact of artifacts) {
    if (!Number.isSafeInteger(artifact?.size) || artifact.size < 0) return Infinity;
    total += artifact.size;
    if (!Number.isSafeInteger(total)) return Infinity;
  }
  return total;
}

function cacheBytes(cache) {
  let total = 0;
  for (const artifacts of cache.values()) {
    const size = artifactBytes(artifacts);
    if (!Number.isFinite(size)) return Infinity;
    total += size;
  }
  return total;
}

function trimCache(cache, maxBytes) {
  let total = cacheBytes(cache);
  while (total > maxBytes && cache.size) {
    cache.delete(cache.keys().next().value);
    total = cacheBytes(cache);
  }
  return total;
}

function cacheResult(cache, key, artifacts, maxBytes) {
  const size = artifactBytes(artifacts);
  if (size > maxBytes) return;
  cache.delete(key);
  let total = trimCache(cache, maxBytes);
  while (total + size > maxBytes && cache.size) {
    cache.delete(cache.keys().next().value);
    total = cacheBytes(cache);
  }
  cache.set(key, Object.freeze(artifacts.slice()));
}

export function validatePipeline(steps, catalog, initial) {
  if (!Array.isArray(steps) || !steps.length) throw new Error("pipeline must contain at least one step");
  if (!initial || typeof initial.kind !== "string" || !Number.isSafeInteger(initial.count) || initial.count < 1) {
    throw new Error("invalid pipeline input");
  }
  let kind = initial.kind;
  let range = { min: initial.count, max: initial.count };
  let rangeSource = "pipeline input";
  let terminal = false;
  const ids = new Set();
  for (const step of steps) {
    if (!step || !step.id || !step.operationId || ids.has(step.id)) throw new Error("invalid or duplicate pipeline step");
    ids.add(step.id);
    if (terminal) throw new Error("terminal operation must be the final step");
    const descriptor = descriptorFor(catalog, step.operationId);
    if (descriptor.input.kind !== kind) throw new Error(`${step.operationId} expects ${descriptor.input.kind}, got ${kind}`);
    if (range.min === range.max) {
      cardinality(descriptor.input, range.min, step.operationId);
    } else {
      const min = descriptor.input.min ?? 1;
      const max = descriptor.input.max ?? min;
      if (range.min < min || range.max > max) {
        throw new Error(`${step.operationId} input cardinality is incompatible with ${rangeSource} output cardinality`);
      }
    }
    validateSidecars(descriptor, step);
    kind = descriptor.output.kind;
    range = outputRange(descriptor.output, step.operationId);
    rangeSource = step.operationId;
    terminal = Boolean(descriptor.capabilities.terminal);
  }
  return { kind, count: range.min === range.max ? range.min : null, terminal };
}

function abortError() {
  return new DOMException("Operation aborted", "AbortError");
}

export async function executePipeline(steps, inputArtifacts, options) {
  const {
    catalog, runner, signal, cache = new Map(), onProgress, cacheMaxBytes = defaultCacheMaxBytes,
  } = options || {};
  if (typeof runner !== "function") throw new TypeError("pipeline runner is required");
  if (!(cache instanceof Map)) throw new TypeError("pipeline cache must be a Map");
  if (!Number.isSafeInteger(cacheMaxBytes) || cacheMaxBytes < 0) throw new RangeError("invalid pipeline cache byte budget");
  trimCache(cache, cacheMaxBytes);
  validatePipeline(steps, catalog, { kind: inputArtifacts[0]?.kind, count: inputArtifacts.length });
  let artifacts = inputArtifacts.slice();
  let lineage = artifacts.every(hasStrongIdentity) ? artifactCacheKey(artifacts) : null;
  for (let index = 0; index < steps.length; index++) {
    if (signal?.aborted) throw abortError();
    const step = steps[index];
    const descriptor = descriptorFor(catalog, step.operationId);
    const sidecars = step.sidecars || {};
    const cacheable = lineage !== null && sidecarsHaveStrongIdentities(sidecars);
    const configKey = cacheable ? artifactCacheKey([], step.params || {}, sidecars) : null;
    const key = cacheable ? `${lineage}\n${JSON.stringify(step.operationId)}:${configKey}` : null;
    const cached = key === null ? undefined : cache.get(key);
    if (cached !== undefined) {
      validateOutput(descriptor, cached);
      cache.delete(key);
      cache.set(key, cached);
      artifacts = cached;
      lineage = key;
      onProgress?.({ phase: "cached", index, step });
      continue;
    }
    onProgress?.({ phase: "running", index, step });
    const result = await runner(step.operationId, artifacts, step.params || {}, { signal, sidecars: step.sidecars || {} });
    if (signal?.aborted) throw abortError();
    artifacts = Array.isArray(result) ? result : [result];
    validateOutput(descriptor, artifacts);
    lineage = key;
    if (key !== null) cacheResult(cache, key, artifacts, cacheMaxBytes);
  }
  return { artifacts, cache };
}
