import {
  FileSystemReceiveSink,
  MAX_STORAGE_WORKER_CHUNK_BYTES,
  MAX_STORAGE_WORKER_CONTROL_CHARS,
  storageWorkerControlChars,
} from "./storage.mjs";

export function createStorageWorkerHandler(options = {}) {
  const getDirectory = options.getDirectory || (() => navigator.storage.getDirectory());
  let sink = null;

  async function processStorageMessage(message) {
    const id = message?.id;
    try {
      if (!message || !Number.isSafeInteger(id) || typeof message.type !== "string" ||
          storageWorkerControlChars(message) > MAX_STORAGE_WORKER_CONTROL_CHARS) {
        throw new Error("invalid storage worker control message");
      }
      if (message.data && (!(message.data instanceof Uint8Array) ||
          message.data.byteLength > MAX_STORAGE_WORKER_CHUNK_BYTES)) {
        throw new Error("storage worker chunk message limit exceeded");
      }

      if (message.type === "init") {
        if (sink) throw new Error("storage worker already initialized");
        sink = new FileSystemReceiveSink(await getDirectory(), "opfs");
        return { id, ok: true, result: {} };
      }
      if (!sink) throw new Error("storage worker is not initialized");
      if (message.type === "prepare") {
        if (!Array.isArray(message.files)) throw new Error("invalid storage worker files");
        await sink.prepare(message.files);
        return {
          id,
          ok: true,
          result: { outputNames: Object.fromEntries(message.files.map((file) => [file.id, sink.outputName(file)])) },
        };
      }
      if (message.type === "write") {
        await sink.write(message.file, message.offset, message.data);
        return { id, ok: true, result: {} };
      }
      if (message.type === "finish") {
        return { id, ok: true, result: { value: await sink.finish(message.file) } };
      }
      if (message.type === "release") {
        await sink.release(message.file);
        return { id, ok: true, result: {} };
      }
      if (message.type === "abort") {
        await sink.abort();
        sink = null;
        return { id, ok: true, result: {} };
      }
      throw new Error("unsupported storage worker message");
    } catch (error) {
      return { id, ok: false, error: String(error?.message || error).slice(0, 512) };
    }
  }

  let queue = Promise.resolve();
  return function handleStorageMessage(message) {
    const result = queue.then(() => processStorageMessage(message));
    queue = result.then(() => undefined, () => undefined);
    return result;
  };
}

if (typeof self !== "undefined" && typeof self.postMessage === "function" && typeof document === "undefined") {
  const handleStorageMessage = createStorageWorkerHandler();
  self.addEventListener("message", async ({ data }) => {
    self.postMessage(await handleStorageMessage(data));
  });
}
