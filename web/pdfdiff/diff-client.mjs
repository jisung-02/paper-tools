import { visualError } from "./visual-errors.mjs";

function errorFrom(value, fallback = "visual comparison failed") {
  return value instanceof Error ? value : new Error(String(value || fallback));
}

function abortError() {
  return new DOMException("Visual comparison aborted", "AbortError");
}

export class DiffWorkerClient {
  constructor({ workerFactory = () => new Worker(new URL("./diff-worker.mjs", import.meta.url), { type: "module" }) } = {}) {
    this.workerFactory = workerFactory;
    this.worker = null;
    this.workerFailure = null;
    this.pending = new Map();
    this.nextID = 1;
    this.closed = false;
    this.startWorker();
  }

  startWorker() {
    if (this.closed || this.worker) return this.worker;
    try {
      const worker = this.workerFactory();
      this.worker = worker;
      this.workerFailure = null;
      worker.onmessage = (event) => {
        if (this.worker === worker) this.handleMessage(event.data);
      };
      worker.onerror = (event) => {
        if (this.worker !== worker) return;
        event?.preventDefault?.();
        this.restart(visualError("worker-failed", event?.error?.message || event?.message || "visual comparison worker failed"));
      };
      worker.onmessageerror = () => {
        if (this.worker === worker) this.restart(visualError("worker-failed", "visual comparison Worker message failed"));
      };
      return worker;
    } catch (error) {
      this.workerFailure = errorFrom(error);
      return null;
    }
  }

  rejectPending(reason) {
    const error = errorFrom(reason);
    for (const pending of this.pending.values()) {
      pending.cleanup?.();
      pending.reject(error);
    }
    this.pending.clear();
  }

  restart(reason = abortError()) {
    if (this.closed) return;
    const worker = this.worker;
    this.worker = null;
    worker?.terminate();
    this.rejectPending(reason);
    this.startWorker();
  }

  handleMessage(message) {
    const pending = this.pending.get(message?.id);
    if (!pending) return;
    this.pending.delete(message.id);
    pending.cleanup?.();
    if (!message.ok) {
      pending.reject(visualError(message.code || "worker-failed", message.error));
      return;
    }
    const result = message.result || {};
    pending.resolve({
      ...result,
      heatmap: result.heatmap instanceof Uint8ClampedArray
        ? result.heatmap
        : new Uint8ClampedArray(result.heatmap),
    });
  }

  diff({ a, widthA, heightA, b, widthB, heightB, options = {}, signal }) {
    if (this.closed) return Promise.reject(visualError("worker-failed", "visual comparison worker is closed"));
    if (signal?.aborted) return Promise.reject(abortError());
    const isRGBA = (value) => value instanceof Uint8Array || value instanceof Uint8ClampedArray;
    if (!isRGBA(a) || !isRGBA(b)) {
      return Promise.reject(visualError("invalid-input", "visual comparison requires RGBA byte arrays", TypeError));
    }
    const id = this.nextID++;
    let cleanup;
    const promise = new Promise((resolve, reject) => {
      const onAbort = () => {
        if (this.pending.has(id)) this.restart(abortError());
      };
      if (signal) {
        signal.addEventListener("abort", onAbort, { once: true });
        cleanup = () => signal.removeEventListener("abort", onAbort);
      }
      this.pending.set(id, { resolve, reject, cleanup });
    });
    const transfers = a.buffer === b.buffer ? [a.buffer] : [a.buffer, b.buffer];
    try {
      const worker = this.worker || this.startWorker();
      if (!worker) throw this.workerFailure || visualError("worker-failed", "visual comparison worker failed");
      worker.postMessage({ id, a, widthA, heightA, b, widthB, heightB, options }, transfers);
    } catch (error) {
      this.restart(error);
    }
    return promise;
  }

  terminate(reason = "visual comparison cancelled") {
    if (this.closed) return;
    this.closed = true;
    const worker = this.worker;
    this.worker = null;
    worker?.terminate();
    this.rejectPending(reason);
  }
}
