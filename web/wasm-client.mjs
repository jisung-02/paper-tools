function abortError() {
  return new DOMException("Aborted", "AbortError");
}

function copyValue(value, transfers, seen = new Map()) {
  if (value instanceof ArrayBuffer) {
    const copy = value.slice(0);
    if (transfers) transfers.push(copy);
    return copy;
  }
  if (ArrayBuffer.isView(value)) {
    const bytes = value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength);
    const copy = value instanceof DataView
      ? new DataView(bytes)
      : new value.constructor(bytes, 0, value.length);
    if (transfers) transfers.push(bytes);
    return copy;
  }
  if (!value || typeof value !== "object") return value;
  if (seen.has(value)) return seen.get(value);
  if (Array.isArray(value)) {
    const copy = [];
    seen.set(value, copy);
    for (const item of value) copy.push(copyValue(item, transfers, seen));
    return copy;
  }
  if (Object.getPrototypeOf(value) === Object.prototype) {
    const copy = {};
    seen.set(value, copy);
    for (const [key, item] of Object.entries(value)) copy[key] = copyValue(item, transfers, seen);
    return copy;
  }
  return value;
}

function copyArgs(args, transfers = null) {
  return args.map((arg) => copyValue(arg, transfers));
}

export function createWasmClient(operation, { worker = null } = {}) {
  let active = null;
  let workerInstance = null;
  let nextId = 1;
  const pending = new Map();
  const settlements = new Set();

  function trackSettlement(promise) {
    const settlement = Promise.resolve(promise).then(() => {}, () => {});
    settlements.add(settlement);
    settlement.finally(() => settlements.delete(settlement));
    return promise;
  }

  async function settle() {
    while (settlements.size) await Promise.allSettled([...settlements]);
  }

  function rejectPending(error) {
    for (const item of pending.values()) {
      item.cleanup();
      item.reject(error);
    }
    pending.clear();
  }

  function stopWorker(error = abortError()) {
    if (workerInstance) workerInstance.terminate();
    workerInstance = null;
    rejectPending(error);
  }

  function getWorker() {
    if (!worker || typeof Worker === "undefined") return null;
    if (workerInstance) return workerInstance;
    workerInstance = new Worker(worker.host, { type: worker.type || "classic" });
    workerInstance.onmessage = ({ data }) => {
      const item = pending.get(data?.id);
      if (!item) return;
      if (data.type === "progress") {
        if (typeof worker.onProgress === "function") worker.onProgress(data.phase);
        return;
      }
      pending.delete(data.id);
      item.cleanup();
      if (data.type === "done") item.resolve(data.result);
      else item.reject(new Error(data.error || "Worker failed"));
    };
    workerInstance.onerror = (event) => {
      const error = event.error || new Error(event.message || "Worker failed");
      stopWorker(error);
    };
    return workerInstance;
  }

  function inWorker(instance, args, signal) {
    const id = nextId++;
    const transfers = [];
    const payloadArgs = copyArgs(args, transfers);
    return new Promise((resolve, reject) => {
      const onAbort = () => {
        pending.delete(id);
        stopWorker(abortError());
        reject(abortError());
      };
      const cleanup = () => signal.removeEventListener("abort", onAbort);
      pending.set(id, { cleanup, reject, resolve });
      signal.addEventListener("abort", onAbort, { once: true });
      try {
        instance.postMessage({
          type: "run",
          id,
          wasm: worker.wasm,
          args: payloadArgs,
        }, transfers);
      } catch (error) {
        pending.delete(id);
        cleanup();
        reject(error);
      }
    });
  }

  function fallback(args, signal) {
    const result = trackSettlement(Promise.resolve().then(() => operation(...args)));
    return new Promise((resolve, reject) => {
      const onAbort = () => reject(abortError());
      signal.addEventListener("abort", onAbort, { once: true });
      result.then(resolve, reject).finally(() => signal.removeEventListener("abort", onAbort));
    });
  }

  function cancel() {
    if (active) active.abort();
    active = null;
    stopWorker(abortError());
    return settle();
  }

  return {
    async run(...args) {
      const previous = cancel();
      if (settlements.size) await previous;
      const controller = new AbortController();
      active = controller;
      try {
        const instance = getWorker();
        if (!instance) return await fallback(copyArgs(args), controller.signal);
        try {
          return await inWorker(instance, args, controller.signal);
        } catch (error) {
          if (controller.signal.aborted) throw abortError();
          stopWorker(error);
          return await fallback(args, controller.signal);
        }
      } finally {
        if (active === controller) active = null;
      }
    },
    cancel,
    settle,
    async dispose() {
      if (active) active.abort();
      active = null;
      stopWorker(abortError());
      await settle();
    },
  };
}
