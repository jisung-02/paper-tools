function abortError() {
  return new DOMException("Visual comparison aborted", "AbortError");
}

export function createDelayedCleanupRegistry(options = {}) {
  const setTimer = options.setTimer || globalThis.setTimeout;
  const clearTimer = options.clearTimer || globalThis.clearTimeout;
  const revokeObjectURL = options.revokeObjectURL || globalThis.URL?.revokeObjectURL?.bind(globalThis.URL);
  if (typeof setTimer !== "function" || typeof clearTimer !== "function" || typeof revokeObjectURL !== "function") {
    throw new TypeError("download cleanup requires timer and object URL functions");
  }
  const pending = new Set();
  return {
    schedule({ url, cleanup, delay = 10_000 }) {
      if (typeof cleanup !== "function") throw new TypeError("download cleanup callback is required");
      if (!Number.isSafeInteger(delay) || delay < 0) throw new RangeError("download cleanup delay is invalid");
      const entry = { timer: null, completion: null, run: null };
      entry.run = () => {
        if (entry.completion) return entry.completion;
        clearTimer(entry.timer);
        entry.timer = null;
        entry.completion = (async () => {
          try {
            revokeObjectURL(url);
          } finally {
            await cleanup();
          }
        })().finally(() => pending.delete(entry));
        return entry.completion;
      };
      pending.add(entry);
      entry.timer = setTimer(() => { void entry.run().catch(() => {}); }, delay);
      return entry.run;
    },
    cleanupAll() {
      return Promise.allSettled([...pending].map((entry) => entry.run()));
    },
    get pendingCount() {
      return pending.size;
    },
  };
}

export function createLatestActionController() {
  let current = null;
  let generation = 0;
  const completions = new Map();
  return {
    begin() {
      current?.controller.abort(abortError());
      const controller = new AbortController();
      let resolveCompletion;
      const completion = new Promise((resolve) => { resolveCompletion = resolve; });
      const token = {
        controller,
        completion,
        generation: ++generation,
        signal: controller.signal,
        isCurrent: () => current?.token === token && !controller.signal.aborted,
      };
      completions.set(token, resolveCompletion);
      current = { controller, token };
      return token;
    },
    finish(token) {
      completions.get(token)?.();
      completions.delete(token);
      if (current?.token !== token) return false;
      current = null;
      return true;
    },
    cancel() {
      if (!current) return false;
      current.controller.abort(abortError());
      current = null;
      return true;
    },
  };
}

export async function commitLatestClosedOutput({ token, sink, output, commit }) {
  if (!token || typeof token.isCurrent !== "function") throw new TypeError("latest action token is required");
  if (!sink || typeof sink.cleanup !== "function") throw new TypeError("closed output sink is required");
  if (typeof commit !== "function") throw new TypeError("closed output commit is required");
  if (!token.isCurrent()) {
    await sink.cleanup();
    return false;
  }
  commit(output);
  return true;
}

function inputRevision(target) {
  const revision = target?.__paperRevision ?? 0;
  if (!Number.isSafeInteger(revision) || revision < 0) throw new RangeError("invalid visual input revision");
  return revision;
}

export function snapshotInputRevisions(targets) {
  if (!Array.isArray(targets)) throw new TypeError("visual input dropzones must be an array");
  return Object.freeze(targets.map(inputRevision));
}

export function inputRevisionsMatch(snapshot, targets) {
  if (!Array.isArray(snapshot) || !Array.isArray(targets) || snapshot.length !== targets.length) return false;
  return snapshot.every((revision, index) => revision === inputRevision(targets[index]));
}

function changed(summary) {
  return Boolean(summary?.changedPixels || summary?.pageSizeChanged);
}

export async function collectChangedPages(count, getSummary, options = {}) {
  if (!Number.isSafeInteger(count) || count < 0) throw new RangeError("invalid visual comparison page count");
  if (typeof getSummary !== "function") throw new TypeError("visual comparison summary reader is required");
  const result = [];
  for (let index = 0; index < count; index++) {
    if (options.signal?.aborted) throw abortError();
    options.onProgress?.(index, count);
    const value = await getSummary(index);
    if (options.signal?.aborted) throw abortError();
    if (changed(value)) result.push(index);
  }
  return result;
}

export function changedPageNavigation(changedPages, index) {
  if (!Array.isArray(changedPages)) throw new TypeError("changed pages must be an array");
  let previous = null;
  let next = null;
  for (const page of changedPages) {
    if (!Number.isSafeInteger(page) || page < 0) throw new RangeError("invalid changed page index");
    if (page < index) previous = page;
    else if (page > index && next == null) next = page;
  }
  return { previous, next };
}
