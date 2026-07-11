function abortError() {
  return new DOMException("Operation aborted", "AbortError");
}

async function settleClient(client) {
  if (typeof client?.settle === "function") await client.settle();
}

export class OperationRunner {
  constructor(catalog, options = {}) {
    this.catalog = catalog;
    this.clientFactory = options.clientFactory;
    this.moduleHandlers = options.moduleHandlers || {};
    this.parameterValidators = options.parameterValidators || {};
    this.activeClient = null;
    this.activeOperation = null;
    this.queue = [];
    this.running = null;
    this.draining = false;
    this.generation = 0;
  }

  descriptor(id) {
    const descriptor = this.catalog instanceof Map ? this.catalog.get(id) : this.catalog[id];
    if (!descriptor) throw new Error(`unknown operation: ${id}`);
    return descriptor;
  }

  validateDescriptor(descriptor) {
    if (descriptor.engine !== "wasm" && descriptor.engine !== "module") {
      throw new Error(`unsupported operation engine: ${descriptor.engine}`);
    }
    if (typeof descriptor.entry !== "string" || !descriptor.entry) {
      throw new Error(`${descriptor.id} has no operation entry`);
    }
  }

  validateInput(descriptor, count, kinds) {
    const min = descriptor.input.min ?? 1;
    const max = descriptor.input.max ?? min;
    if (!Number.isSafeInteger(count) || count < min) throw new Error(`${descriptor.id} requires at least ${min} input(s)`);
    if (count > max) throw new Error(`${descriptor.id} accepts at most ${max} input(s)`);
    if (kinds !== undefined) {
      if (!Array.isArray(kinds) || kinds.length !== count || kinds.some((kind) => typeof kind !== "string" || !kind)) {
        throw new Error(`${descriptor.id} input kinds do not match input count`);
      }
      const expected = descriptor.input.kind;
      if (expected !== "file" && kinds.some((kind) => kind !== expected)) {
        throw new Error(`${descriptor.id} requires ${expected} input`);
      }
    }
  }

  validateParams(descriptor, params) {
    if (params === undefined) return;
    if (!params || Array.isArray(params) || typeof params !== "object" ||
        (Object.getPrototypeOf(params) !== Object.prototype && Object.getPrototypeOf(params) !== null)) {
      throw new TypeError(`${descriptor.id} params must be a plain object`);
    }
    const validate = this.parameterValidators[descriptor.id];
    if (validate !== undefined) {
      if (typeof validate !== "function") throw new TypeError(`${descriptor.id} parameter validator is invalid`);
      validate(params);
    }
  }

  settleJob(job, outcome, value) {
    if (job.settled) return false;
    job.settled = true;
    job.signal?.removeEventListener("abort", job.onAbort);
    if (outcome === "resolve") job.resolve(value);
    else job.reject(value);
    return true;
  }

  abortJob(job) {
    if (job.state === "queued") {
      const index = this.queue.indexOf(job);
      if (index >= 0) this.queue.splice(index, 1);
    }
    job.controller.abort(abortError());
    if (job.state === "running" && typeof job.cancelActive === "function" && !job.cancelPromise) {
      job.cancelPromise = Promise.resolve().then(() => job.cancelActive()).catch(() => {});
    }
    this.settleJob(job, "reject", abortError());
  }

  async invoke(id, args, options = {}) {
    const descriptor = this.descriptor(id);
    if (!Array.isArray(args)) throw new TypeError("operation args must be an array");
    this.validateDescriptor(descriptor);
    this.validateInput(descriptor, options.inputCount ?? 1, options.inputKinds);
    this.validateParams(descriptor, options.params);
    if (options.signal?.aborted) throw abortError();
    const progress = options.onProgress || (() => {});
    progress("queued");

    return new Promise((resolve, reject) => {
      const job = {
        type: "invoke",
        id,
        args,
        descriptor,
        generation: this.generation,
        progress,
        signal: options.signal,
        controller: new AbortController(),
        cancelActive: null,
        cancelPromise: null,
        resolve,
        reject,
        settled: false,
        state: "queued",
      };
      job.onAbort = () => this.abortJob(job);
      options.signal?.addEventListener("abort", job.onAbort, { once: true });
      this.queue.push(job);
      this.kick();
    });
  }

  kick() {
    if (this.draining) return;
    this.draining = true;
    queueMicrotask(() => { void this.drain(); });
  }

  async drain() {
    try {
      while (this.queue.length) {
        const item = this.queue.shift();
        if (item.type === "dispose") {
          try {
            await this.disposeClient();
            item.resolve();
          } catch (error) {
            item.reject(error);
          }
          continue;
        }
        if (item.settled) continue;
        if (item.generation !== this.generation) {
          this.settleJob(item, "reject", abortError());
          continue;
        }
        item.state = "running";
        this.running = item;
        await this.runJob(item);
        if (this.running === item) this.running = null;
      }
    } finally {
      this.draining = false;
      if (this.queue.length) this.kick();
    }
  }

  async prepareWasm(job) {
    if (typeof this.clientFactory !== "function") throw new Error("WASM client factory is unavailable");
    if (this.activeOperation === job.id && this.activeClient) return this.activeClient;
    const nextClient = this.clientFactory(job.descriptor);
    if (!nextClient || typeof nextClient.run !== "function") throw new Error(`${job.id} client is invalid`);
    const previousClient = this.activeClient;
    if (previousClient) {
      try {
        await previousClient.dispose?.();
        await settleClient(previousClient);
      } catch (error) {
        await nextClient.dispose?.();
        throw error;
      }
    }
    this.activeClient = nextClient;
    this.activeOperation = job.id;
    return nextClient;
  }

  moduleHandler(id) {
    const value = this.moduleHandlers[id];
    if (typeof value === "function") return { cooperative: false, owner: value, run: value, cancel: value.cancel };
    if (value && typeof value.run === "function") {
      return { cooperative: true, owner: value, run: value.run, cancel: value.cancel };
    }
    throw new Error(`module handler is unavailable: ${id}`);
  }

  async runJob(job) {
    let client = null;
    let execution;
    try {
      job.progress("loading");
      await Promise.resolve();
      if (job.controller.signal.aborted || job.generation !== this.generation) throw abortError();
      if (job.descriptor.engine === "wasm") {
        client = await this.prepareWasm(job);
        job.cancelActive = () => client.cancel?.();
        execution = () => client.run(...job.args);
      } else {
        await this.disposeClient();
        const handler = this.moduleHandler(job.id);
        const context = Object.freeze({ signal: job.controller.signal, onProgress: job.progress });
        job.cancelActive = () => handler.cancel?.call(handler.owner, ...(handler.cooperative ? [context] : []));
        execution = () => handler.run.call(handler.owner, ...job.args, ...(handler.cooperative ? [context] : []));
      }
      if (job.controller.signal.aborted || job.generation !== this.generation) throw abortError();
      job.progress("running");
      const result = await execution();
      if (job.controller.signal.aborted || job.generation !== this.generation) throw abortError();
      job.progress("finalizing");
      await Promise.resolve();
      if (job.controller.signal.aborted || job.generation !== this.generation) throw abortError();
      job.progress("done");
      this.settleJob(job, "resolve", result);
    } catch (error) {
      const cause = job.controller.signal.aborted || job.generation !== this.generation ? abortError() : error;
      this.settleJob(job, "reject", cause);
    } finally {
      await Promise.allSettled([
        job.cancelPromise,
        client && typeof client.settle === "function" ? client.settle() : null,
      ].filter(Boolean));
      job.signal?.removeEventListener("abort", job.onAbort);
      job.state = "settled";
    }
  }

  async disposeClient() {
    const client = this.activeClient;
    this.activeClient = null;
    this.activeOperation = null;
    if (!client) return;
    await client.dispose?.();
    await settleClient(client);
  }

  dispose() {
    this.generation++;
    for (const item of [...this.queue]) {
      if (item.type === "invoke") this.abortJob(item);
    }
    if (this.running) this.abortJob(this.running);
    const marker = { type: "dispose" };
    const promise = new Promise((resolve, reject) => Object.assign(marker, { resolve, reject }));
    this.queue.push(marker);
    this.kick();
    return promise;
  }
}
