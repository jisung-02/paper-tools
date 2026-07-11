/* A worker is permanently bound to one Go/WASM binary. Go tools register the
   same global name, so loading a second binary in this realm would be unsafe. */
let boundWasm = "";
let ready;
let runTool;
let queue = Promise.resolve();

async function loadRuntime(wasmURL) {
  const normalized = new URL(wasmURL, self.location.href).href;
  if (boundWasm && boundWasm !== normalized) {
    throw new Error("WASM worker is already bound to a different binary");
  }
  boundWasm = normalized;
  if (ready) return ready;

  ready = (async () => {
    importScripts(new URL("/wasm_exec.js?v=2", self.location.href).href);
    const go = new Go();
    const response = await fetch(normalized);
    if (!response.ok) throw new Error("WASM binary unavailable");
    const bytes = await response.arrayBuffer();
    const result = await WebAssembly.instantiate(bytes, go.importObject);
    go.run(result.instance);
    for (let i = 0; i < 200 && typeof self.pdfRun !== "function"; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    if (typeof self.pdfRun !== "function") throw new Error("WASM tool did not initialize");
    runTool = self.pdfRun;
  })();
  return ready;
}

async function handle(data) {
  try {
    if (!Array.isArray(data.args)) throw new Error("invalid WASM worker arguments");
    self.postMessage({ type: "progress", id: data.id, phase: "loading" });
    await loadRuntime(data.wasm);
    self.postMessage({ type: "progress", id: data.id, phase: "running" });
    const result = await runTool(...data.args);
    self.postMessage({ type: "progress", id: data.id, phase: "done" });
    const transfer = result?.data instanceof Uint8Array ? [result.data.buffer] : [];
    self.postMessage({ type: "done", id: data.id, result }, transfer);
  } catch (error) {
    self.postMessage({ type: "error", id: data.id, error: String(error?.message || error) });
  }
}

self.onmessage = ({ data }) => {
  if (!data || data.type !== "run") return Promise.resolve();
  const task = queue.then(() => handle(data));
  queue = task.catch(() => {});
  return task;
};
