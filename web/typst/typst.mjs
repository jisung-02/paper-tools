import { createTypstCompiler, CompileFormatEnum } from "/vendor/typst/compiler.mjs";
import { createTypstRenderer } from "/vendor/typst/renderer.mjs";
import { loadFonts } from "/vendor/typst/options.init.mjs";

// The in-memory path the compiler treats as the document's entry point;
// addSource() rewrites its contents on every compile.
const MAIN_FILE = "/main.typ";

const statusEl = document.getElementById("status");
const err = document.getElementById("err");
const src = document.getElementById("typstsrc");
const out = document.getElementById("typstout");
const dlTypBtn = document.getElementById("dltyp");
const dlPdfBtn = document.getElementById("dlpdf");

function setStatus(msg) {
  if (!statusEl) return;
  statusEl.textContent = msg;
}

function showLoadingPreview() {
  out.innerHTML = "";
  const p = document.createElement("p");
  p.className = "hint";
  p.textContent = window.t("Loading…", "불러오는 중…");
  out.appendChild(p);
}

function showDiagnostics(diagnostics) {
  err.style.whiteSpace = "pre-wrap";
  window.showErr(err, diagnostics.join("\n"));
}

// Plain (single-line) errors share this so they don't inherit the
// pre-wrap white-space style left behind by a prior showDiagnostics() call.
function showPlainErr(msg) {
  err.style.whiteSpace = "";
  window.showErr(err, msg);
}

function clearErr() {
  if (!err) return;
  err.style.whiteSpace = "";
  err.textContent = "";
}

let compiler;
let renderer;
let engineReady = false;

/* -------------------------------------------------------- engine boot --- */

// Lazy singleton init: exactly one in-flight promise, started immediately on
// module load (this tool live-previews as you type, so the engine needs to
// be warm as soon as possible — unlike click-triggered tools such as OCR).
// Mirrors app.js boot()'s status text / [data-needs-wasm] handling, since
// this module-engine tool can't call boot() itself (that's Go-wasm only).
const engineInit = (async () => {
  if (statusEl) {
    statusEl.setAttribute("role", "status");
    statusEl.setAttribute("aria-live", "polite");
    statusEl.setAttribute("aria-atomic", "true");
  }
  setStatus(window.t(
    "Loading Typst engine (~10 MB, cached after first use)…",
    "타이프스트 엔진 다운로드 중 (~10 MB, 최초 1회만)…"
  ));

  compiler = createTypstCompiler();
  await compiler.init({
    getWrapper: () => import("/vendor/typst/pkg-compiler/typst_ts_web_compiler.mjs"),
    // The compiler wasm is ~27 MiB uncompressed, over Cloudflare Pages' 25 MiB
    // per-file limit, so it's vendored gzipped and decompressed here at
    // runtime. The Response we hand back carries an explicit application/wasm
    // Content-Type so __wbg_load()'s WebAssembly.instantiateStreaming() path
    // still applies to the decompressed bytes (see wasm.mjs's
    // WebAssemblyModuleRef type: a Response is an accepted getModule return).
    getModule: async () => {
      const resp = await fetch("/vendor/typst/pkg-compiler/typst_ts_web_compiler_bg.wasm.gz");
      if (!resp.ok) throw new Error("engine download failed");
      if (typeof DecompressionStream === "undefined") {
        throw new Error("this browser cannot decompress the Typst engine");
      }
      const stream = resp.body.pipeThrough(new DecompressionStream("gzip"));
      return new Response(stream, { headers: { "Content-Type": "application/wasm" } });
    },
    beforeBuild: [loadFonts([
      "/vendor/typst/fonts/LibertinusSerif-Regular.otf",
      "/vendor/typst/fonts/LibertinusSerif-Bold.otf",
      "/vendor/typst/fonts/LibertinusSerif-Italic.otf",
      "/vendor/typst/fonts/LibertinusSerif-BoldItalic.otf",
      "/vendor/typst/fonts/NewCMMath-Regular.otf",
      "/vendor/typst/fonts/DejaVuSansMono.ttf",
      "/NanumGothic-Regular.ttf",
    ], { assets: false })], // assets:false suppresses the default jsdelivr font fetch — required for offline use
  });

  renderer = createTypstRenderer();
  await renderer.init({
    getWrapper: () => import("/vendor/typst/pkg-renderer/typst_ts_renderer.mjs"),
    getModule: () => "/vendor/typst/pkg-renderer/typst_ts_renderer_bg.wasm",
  });

  engineReady = true;
  setStatus("");
  if (statusEl) statusEl.hidden = true;
  document.querySelectorAll("[data-needs-wasm]").forEach((el) => {
    el.disabled = false;
  });
})();

engineInit.catch((e) => {
  setStatus(window.t("Failed to load tool: ", "도구를 불러오지 못했습니다: ") + (e && e.message ? e.message : String(e)));
});

engineInit.then(() => {
  // Picks up whatever's already in the textarea (e.g. a value the browser
  // restored across a reload) the moment the engine finishes loading.
  triggerRender();
}).catch(() => {});

/* --------------------------------------------------- compiler mutex --- */

// Every call that touches `compiler` (preview compiles and PDF-export
// compiles alike) is serialized through this chain — addSource() mutates
// shared compiler state, so a preview compile and a Download PDF click
// racing each other could otherwise interleave two different documents'
// sources and results.
let compilerChain = Promise.resolve();
function withCompiler(fn) {
  const result = compilerChain.then(fn, fn);
  compilerChain = result.then(() => {}, () => {});
  return result;
}

/* --------------------------------------------------------- preview --- */

let previewSeq = 0;
let debounceTimer = null;
let previewBusy = false;
let queuedPreviewText = null;

function scheduleRender() {
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(triggerRender, 250);
}

function triggerRender() {
  const text = src.value;
  if (!text.trim()) {
    out.innerHTML = "";
    clearErr();
    queuedPreviewText = null;
    return;
  }
  if (!engineReady) {
    showLoadingPreview();
    return;
  }
  if (previewBusy) {
    // Single in-flight compile: a request that arrives while one is
    // running replaces any earlier queued request rather than piling up.
    queuedPreviewText = text;
    return;
  }
  runPreview(text);
}

async function runPreview(text) {
  previewBusy = true;
  const seq = ++previewSeq;
  try {
    await withCompiler(() => compilePreview(text, seq));
  } finally {
    previewBusy = false;
    const next = queuedPreviewText;
    queuedPreviewText = null;
    if (next != null && next.trim()) runPreview(next);
  }
}

async function compilePreview(text, seq) {
  let ir;
  let diagnostics;
  try {
    compiler.addSource(MAIN_FILE, text);
    ({ result: ir, diagnostics } = await compiler.compile({
      mainFilePath: MAIN_FILE,
      format: CompileFormatEnum.vector,
      diagnostics: "unix",
    }));
  } catch (e) {
    if (seq !== previewSeq) return;
    showPlainErr(e && e.message ? e.message : String(e));
    return;
  }
  if (seq !== previewSeq) return;

  if (ir) {
    let svg;
    try {
      svg = await renderer.runWithSession(async (session) => {
        renderer.manipulateData({ renderSession: session, action: "reset", data: ir });
        return renderer.renderSvg({ renderSession: session });
      });
    } catch (e) {
      if (seq !== previewSeq) return;
      showPlainErr(e && e.message ? e.message : String(e));
      return;
    }
    if (seq !== previewSeq) return;
    out.innerHTML = svg;
    const svgEl = out.querySelector("svg");
    if (svgEl) {
      // The rendered <svg> carries fixed width/height attributes sized to
      // the document; scale it to the preview pane instead.
      svgEl.style.width = "100%";
      svgEl.style.height = "auto";
      // The typeset page has no background of its own and its text is
      // black; paint it as a white sheet in both themes (same rule as the
      // signature canvas and PDF preview surfaces).
      svgEl.style.background = "#fff";
    }
    clearErr();
    return;
  }

  // No result: keep the last good preview on screen and surface the
  // diagnostics instead of clearing anything.
  if (diagnostics && diagnostics.length) showDiagnostics(diagnostics);
}

src.addEventListener("input", scheduleRender);

const fileDz = window.dropzone("fileDrop", { multiple: false });
document.getElementById("fileDrop").addEventListener("dz:files", async ({ detail }) => {
  const f = detail.files[0];
  if (!f) return;
  const buf = await f.arrayBuffer();
  src.value = new TextDecoder().decode(buf);
  triggerRender();
});

/* --------------------------------------------------------- downloads --- */

dlTypBtn.addEventListener("click", () => window.run(dlTypBtn, async () => {
  const text = src.value;
  if (!text.trim()) {
    showPlainErr(window.t("Write some Typst first.", "먼저 타이프스트를 작성하세요."));
    return;
  }
  window.download(new TextEncoder().encode(text), "document.typ", "text/x-typst;charset=utf-8");
}));

dlPdfBtn.addEventListener("click", () => window.run(dlPdfBtn, async () => {
  const text = src.value;
  if (!text.trim()) {
    showPlainErr(window.t("Write some Typst first.", "먼저 타이프스트를 작성하세요."));
    return;
  }
  const { result: pdfBytes, diagnostics } = await withCompiler(async () => {
    compiler.addSource(MAIN_FILE, text);
    return compiler.compile({
      mainFilePath: MAIN_FILE,
      format: CompileFormatEnum.pdf,
      diagnostics: "unix",
    });
  });
  if (!pdfBytes) {
    if (diagnostics && diagnostics.length) showDiagnostics(diagnostics);
    else showPlainErr(window.t("Failed to compile Typst to PDF.", "PDF로 컴파일하지 못했습니다."));
    return;
  }
  window.download(pdfBytes, "document.pdf", "application/pdf");
}));
