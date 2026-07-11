function abortError() {
  return new DOMException("Batch aborted", "AbortError");
}

export const MAX_BATCH_INPUTS = 500;

async function attempt(operation, value, retries, context) {
  let lastError;
  for (let attemptNumber = 0; attemptNumber <= retries; attemptNumber++) {
    if (context.signal?.aborted) throw abortError();
    try {
      return await operation(value, { ...context, attempt: attemptNumber });
    } catch (error) {
      lastError = error instanceof Error ? error : new Error(String(error));
      if (context.signal?.aborted) throw abortError();
    }
  }
  throw lastError;
}

export async function runBatch(inputs, operation, options = {}) {
  if (options.onOutput !== undefined && typeof options.onOutput !== "function") throw new TypeError("batch output callback must be a function");
  const failures = [];
  let succeeded = 0;
  for await (const event of iterateBatch(inputs, operation, options)) {
    if (event.ok) {
      await options.onOutput?.(event);
      if (options.signal?.aborted) throw abortError();
      succeeded++;
    } else {
      const { index, name, error, attempts } = event;
      failures.push({ index, name, error, attempts });
    }
  }
  return { succeeded, failed: failures.length, failures };
}

function inputName(input, index) {
  if (input && typeof input.name === "string" && input.name) return input.name;
  const value = String(input ?? "");
  return value || `input-${index + 1}`;
}

function validateBatch(inputs, operation, options) {
  if (!Array.isArray(inputs) || !inputs.length) throw new Error("batch requires at least one input");
  if (inputs.length > MAX_BATCH_INPUTS) throw new Error(`batch accepts at most ${MAX_BATCH_INPUTS} inputs`);
  if (typeof operation !== "function") throw new TypeError("batch operation is required");
  const mode = options.mode || "independent";
  if (mode !== "independent" && mode !== "grouped") throw new Error("invalid batch mode");
  const retries = options.retries ?? 0;
  if (!Number.isSafeInteger(retries) || retries < 0 || retries > 3) throw new RangeError("invalid retry count");
  return { mode, retries };
}

export async function* iterateBatch(inputs, operation, options = {}) {
  const { mode, retries } = validateBatch(inputs, operation, options);
  const total = mode === "grouped" ? 1 : inputs.length;
  let failureCount = 0;

  if (mode === "grouped") {
    let event;
    try {
      const value = await attempt(operation, inputs, retries, { signal: options.signal, index: 0, total });
      if (options.signal?.aborted) throw abortError();
      event = { ok: true, index: 0, value, mode };
    } catch (error) {
      if (error?.name === "AbortError") throw error;
      failureCount = 1;
      event = { ok: false, index: 0, name: "group", error, attempts: retries + 1, mode };
    }
    yield event;
    options.onProgress?.({ completed: 1, total, failures: failureCount });
    return;
  }

  for (let index = 0; index < inputs.length; index++) {
    if (options.signal?.aborted) throw abortError();
    let event;
    try {
      const value = await attempt(operation, inputs[index], retries, { signal: options.signal, index, total });
      if (options.signal?.aborted) throw abortError();
      event = { ok: true, index, value, mode };
    } catch (error) {
      if (error?.name === "AbortError") throw error;
      failureCount++;
      event = {
        ok: false,
        index,
        name: inputName(inputs[index], index),
        error,
        attempts: retries + 1,
        mode,
      };
    }
    yield event;
    options.onProgress?.({ completed: index + 1, total, failures: failureCount });
  }
}

export function failureManifestBlob(failures) {
  if (!Array.isArray(failures)) throw new TypeError("batch failures must be an array");
  const entries = failures.map((failure) => ({
    index: failure.index,
    name: String(failure.name || `input-${Number(failure.index) + 1}`),
    attempts: failure.attempts,
    message: String(failure.error?.message || failure.error || "unknown failure"),
  }));
  const json = JSON.stringify({ version: 1, failed: entries.length, failures: entries }, null, 2) + "\n";
  return new Blob([json], { type: "application/json" });
}

export function uniqueOutputName(name, used) {
  if (!(used instanceof Set)) throw new TypeError("used names must be a Set");
  const safe = String(name || "output").replace(/[\\/\0-\x1f]/g, "_");
  const has = (candidate) => {
    const normalized = candidate.normalize("NFC").toLocaleLowerCase("en-US");
    return [...used].some((value) => String(value).normalize("NFC").toLocaleLowerCase("en-US") === normalized);
  };
  if (!has(safe)) { used.add(safe); return safe; }
  const dot = safe.lastIndexOf(".");
  const base = dot > 0 ? safe.slice(0, dot) : safe;
  const ext = dot > 0 ? safe.slice(dot) : "";
  for (let n = 2; ; n++) {
    const candidate = `${base} (${n})${ext}`;
    if (!has(candidate)) { used.add(candidate); return candidate; }
  }
}
