// Plain module tool: no wasm engine and no compiler to warm up, so there's
// nothing to await before the page is interactive — status is cleared as
// soon as this module has wired up its event listeners.

const statusEl = document.getElementById("status");
const err = document.getElementById("err");
const src = document.getElementById("htmlsrc");
const emptyHint = document.getElementById("htmlempty");
const out = document.getElementById("htmlout");
const dlHtmlBtn = document.getElementById("dlhtml");

function setStatus(msg) {
  if (!statusEl) return;
  statusEl.textContent = msg;
}

function showEmptyPreview() {
  out.hidden = true;
  out.srcdoc = "";
  emptyHint.hidden = false;
}

function showPreview(text) {
  emptyHint.hidden = true;
  if (out.hidden) {
    // An iframe that was display:none when first parsed never paints later
    // srcdoc navigations (Chromium); detaching and re-attaching it resets
    // the browsing context so the preview renders.
    out.hidden = false;
    const parent = out.parentElement;
    out.remove();
    parent.appendChild(out);
  }
  out.srcdoc = text;
}

function renderPreview() {
  const text = src.value;
  if (!text.trim()) {
    showEmptyPreview();
    return;
  }
  showPreview(text);
}

let debounceTimer = null;
src.addEventListener("input", () => {
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(renderPreview, 200);
});

const fileDz = window.dropzone("fileDrop", { multiple: false });
document.getElementById("fileDrop").addEventListener("dz:files", async ({ detail }) => {
  const f = detail.files[0];
  if (!f) return;
  const buf = await f.arrayBuffer();
  src.value = new TextDecoder().decode(buf);
  renderPreview();
});

dlHtmlBtn.addEventListener("click", () => window.run(dlHtmlBtn, async () => {
  const text = src.value;
  if (!text.trim()) {
    window.showErr(err, window.t("Write some HTML first.", "먼저 HTML을 작성하세요."));
    return;
  }
  window.download(new TextEncoder().encode(text), "document.html", "text/html;charset=utf-8");
}));

// Picks up whatever's already in the textarea (e.g. a value the browser
// restored across a reload) as soon as the module has finished wiring up.
renderPreview();
setStatus("");
if (statusEl) statusEl.hidden = true;
