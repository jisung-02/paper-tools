"use strict";

/* app.js — shared boot / dropzone / run-button plumbing for every tool page.
   Loaded via a plain <script> tag after wasm_exec.js, so no imports/exports:
   everything below is a global function. Also owns the 7-language (English
   default, plus Korean/Japanese/Chinese/Spanish/French/German) i18n engine
   used by every page. Brand and format names (Paper Tools, PDF, PNG, JPG,
   JPEG, GIF, ZIP, Word, Hangul, .docx, .hwpx, .hwp, .txt, AES-128, A4, N-up)
   are never translated. */

/* ------------------------------------------------------------------ i18n --- */

const LANGS = [
  ["en", "English"],
  ["ko", "한국어"],
  ["ja", "日本語"],
  ["zh", "中文(简体)"],
  ["es", "Español"],
  ["fr", "Français"],
  ["de", "Deutsch"],
];
const LANG_CODES = LANGS.map((l) => l[0]);

// localStorage can throw synchronously — Safari's "Block All Cookies" throws
// on getItem, and private-mode Safari throws on setItem once the quota is
// (deliberately) zero. Route every access through these so a blocked/absent
// store degrades to "no preference" instead of crashing page boot.
function storeGet(k) {
  try {
    return localStorage.getItem(k);
  } catch (e) {
    return null;
  }
}
function storeSet(k, v) {
  try {
    localStorage.setItem(k, v);
  } catch (e) {}
}

// Set to your EthicalAds publisher id to enable ads. Empty string keeps the
// site fully local/private — initAds() below no-ops when this is empty.
const AD_PUBLISHER = "";

// Set to your Cloudflare Web Analytics token to enable cookieless traffic
// stats. Empty string keeps the site fully local/private — initAnalytics()
// below no-ops when this is empty.
const CF_ANALYTICS_TOKEN = "";

// The per-language dict lives in web/i18n/<lang>.js (ja/zh/es/fr/de), loaded
// via a <script> tag before this one on generated fixed-lang pages, or lazily
// by ensureDict() below on English-URL pages with a stored foreign preference.
const I18N = (window.I18N = window.I18N || {});

function sanitizeLang(lang) {
  return LANG_CODES.indexOf(lang) !== -1 ? lang : "en";
}

const FIXED = window.__FIXED_LANG || "";
let LANG = FIXED || sanitizeLang(storeGet("lang"));

function t(en, ko) {
  if (LANG === "en") return en;
  if (LANG === "ko") return ko != null ? ko : en;
  return (I18N[LANG] && I18N[LANG][en]) || en;
}

function applyLang() {
  document.documentElement.lang = LANG;

  document.querySelectorAll("[data-i18n]").forEach((el) => {
    if (el.classList.contains("wordmark")) return; // brand stays literal
    const en = el.dataset.en != null ? el.dataset.en : el.textContent;
    let v;
    if (LANG === "en") v = en;
    else if (LANG === "ko") v = el.dataset.ko != null ? el.dataset.ko : en;
    else v = (I18N[LANG] && I18N[LANG][en]) || en;
    el.textContent = v;
  });

  document.querySelectorAll("[data-en-placeholder]").forEach((el) => {
    const en = el.dataset.enPlaceholder;
    el.placeholder = LANG === "en" ? en : LANG === "ko" ? el.dataset.koPlaceholder || en : (I18N[LANG] && I18N[LANG][en]) || en;
  });

  document.querySelectorAll("[data-en-aria]").forEach((el) => {
    const en = el.dataset.enAria;
    el.setAttribute("aria-label", LANG === "en" ? en : LANG === "ko" ? el.dataset.koAria || en : (I18N[LANG] && I18N[LANG][en]) || en);
  });

  const sel = document.querySelector(".langsel");
  if (sel) sel.value = LANG;

  updateThemeToggle();
}

// Lazy-loads the ja/zh/es/fr/de dict on English-URL pages that have a stored
// foreign-language preference (I18N[lang] is only pre-populated there via a
// static <script> tag on generated fixed-lang pages). No-ops — and never
// double-injects — once the dict is present or loading.
const dictLoading = {};
function ensureDict(lang) {
  if (lang === "en" || lang === "ko" || I18N[lang] || dictLoading[lang]) return;
  dictLoading[lang] = true;
  const script = document.createElement("script");
  script.src = "/i18n/" + lang + ".js";
  script.onload = applyLang;
  document.head.appendChild(script);
}

function setLang(lang) {
  const sanitized = sanitizeLang(lang);
  storeSet("lang", sanitized);

  if (FIXED) {
    if (sanitized === FIXED) {
      LANG = sanitized;
      applyLang();
      return;
    }
    const rest = location.pathname.slice(("/" + FIXED).length) || "/";
    location.href = sanitized === "en" ? rest : "/" + sanitized + rest;
    return;
  }

  if (sanitized !== "en") {
    location.href = "/" + sanitized + location.pathname;
    return;
  }

  LANG = sanitized;
  applyLang();
}

window.t = t;
window.setLang = setLang;

// On a visitor's very first load (no stored language preference yet), infer
// a supported language from the browser and land them on that language's
// page. The detected value is persisted BEFORE any navigation, so this can
// never run more than once — every later load has localStorage['lang'] set,
// whether by this function or by the language dropdown, and skips straight
// past it.
function detectBrowserLang() {
  if (storeGet("lang")) return;

  const langs = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || "en"];
  const prefix = String(langs[0] || "en").toLowerCase().slice(0, 2);
  const detected = LANG_CODES.indexOf(prefix) !== -1 ? prefix : "en";

  storeSet("lang", detected); // persisted first: loop guard

  const current = FIXED || "en";
  if (detected !== current) setLang(detected);
}

// Replace the plain EN/KO toggle markup with a <select> covering all 7
// languages. Falls back to a harmless click-listener for any legacy
// [data-lang] element that might still be around.
function initLangSelector() {
  const nav = document.querySelector("nav.langtoggle");
  if (nav) {
    const select = document.createElement("select");
    select.className = "langsel";
    select.setAttribute("aria-label", "Language");
    LANGS.forEach(([code, label]) => {
      const opt = document.createElement("option");
      opt.value = code;
      opt.textContent = label;
      if (code === LANG) opt.selected = true;
      select.appendChild(opt);
    });
    select.addEventListener("change", () => setLang(select.value));
    nav.innerHTML = "";
    nav.appendChild(select);
  }

  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-lang]");
    if (b) {
      e.preventDefault();
      setLang(b.dataset.lang);
    }
  });
}

/* ------------------------------------------------------- theme toggle --- */

// The effective theme: an explicit localStorage choice always wins; absent
// one, this mirrors the `prefers-color-scheme` media query in style.css so
// the icon matches what's actually on screen.
function effectiveTheme() {
  const stored = storeGet("theme");
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

let themeToggleBtn = null;

function updateThemeToggle() {
  if (!themeToggleBtn) return;
  const theme = effectiveTheme();
  // Icon shows the theme a click switches TO, not the current one.
  themeToggleBtn.textContent = theme === "dark" ? "☀" : "☾";
  const label =
    theme === "dark"
      ? t("Switch to light theme", "밝은 테마로 전환")
      : t("Switch to dark theme", "어두운 테마로 전환");
  themeToggleBtn.setAttribute("aria-label", label);
}

// The <meta name="theme-color"> tags are split into a light and a dark
// variant, each gated on a `prefers-color-scheme` media attribute — that
// alone keeps the browser chrome color in sync with the OS. But once the
// visitor makes an explicit in-page choice (or one is already stored on
// load), that choice must override the OS media query the same way
// data-theme does, so both metas are stamped with the effective color
// regardless of which one the media attribute would otherwise pick.
function updateThemeColorMetas(theme) {
  const color = theme === "dark" ? "#09090b" : "#ffffff";
  document.querySelectorAll('meta[name="theme-color"]').forEach((m) => {
    m.setAttribute("content", color);
  });
}

function setTheme(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  storeSet("theme", theme);
  updateThemeToggle();
  updateThemeColorMetas(theme);
}

// Injects a toggle button next to the language <select>, inside the same
// nav.langtoggle container so it shares its layout and visual weight.
function initThemeToggle() {
  // A stored theme (from a previous explicit choice) must win over the OS
  // media query on this load too, same as theme.js does for data-theme.
  const stored = storeGet("theme");
  if (stored === "light" || stored === "dark") updateThemeColorMetas(stored);

  const nav = document.querySelector("nav.langtoggle");
  if (!nav) return;

  themeToggleBtn = document.createElement("button");
  themeToggleBtn.type = "button";
  themeToggleBtn.className = "themetoggle";
  themeToggleBtn.addEventListener("click", () => {
    setTheme(effectiveTheme() === "dark" ? "light" : "dark");
  });
  nav.appendChild(themeToggleBtn);
  updateThemeToggle();

  // Nothing stored: follow the OS live, same as the CSS media query does.
  if (window.matchMedia) {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      if (!storeGet("theme")) updateThemeToggle();
    };
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else if (mq.addListener) mq.addListener(onChange); // Safari < 14
  }
}

/* --------------------------------------------------------------- head --- */

// initFavicon() injects the shared favicon link into every page's <head>,
// so individual HTML files don't each need the same <link> tag.
function initFavicon() {
  if (document.querySelector('link[rel="icon"]')) return;
  const link = document.createElement("link");
  link.rel = "icon";
  link.href = "/favicon.svg";
  document.head.appendChild(link);
}

/* ---------------------------------------------------------------- ads --- */

// initAds() is a no-op while AD_PUBLISHER is empty: no external script
// loads and no DOM changes happen, keeping the site fully local/private by
// default. Set AD_PUBLISHER (above) to an EthicalAds publisher id to opt
// in — this injects the EthicalAds script once, plus a single "stickybox"
// ad unit (an unobtrusive floating format that needs no layout slot).
// EthicalAds is contextual and cookieless.
let adsInited = false;
function initAds() {
  if (!AD_PUBLISHER || adsInited) return;
  adsInited = true;

  const script = document.createElement("script");
  script.src = "https://media.ethicalads.io/media/client/ethicalads.min.js";
  script.async = true;
  document.head.appendChild(script);

  const ad = document.createElement("div");
  ad.setAttribute("data-ea-publisher", AD_PUBLISHER);
  ad.setAttribute("data-ea-type", "image");
  ad.setAttribute("data-ea-style", "stickybox");
  document.body.appendChild(ad);
}

/* ----------------------------------------------------------- analytics --- */

// initAnalytics() is a no-op while CF_ANALYTICS_TOKEN is empty: no external
// script loads and no DOM changes happen, keeping the site fully local/
// private by default. Set CF_ANALYTICS_TOKEN (above) to a Cloudflare Web
// Analytics token to opt in — this injects the Cloudflare beacon script
// once. Cloudflare Web Analytics is cookieless and collects no personal
// data; opt-in via CF_ANALYTICS_TOKEN, so no consent banner needed.
let analyticsInited = false;
function initAnalytics() {
  if (!CF_ANALYTICS_TOKEN || analyticsInited) return;
  analyticsInited = true;

  const script = document.createElement("script");
  script.defer = true;
  script.src = "https://static.cloudflareinsights.com/beacon.min.js";
  script.setAttribute("data-cf-beacon", JSON.stringify({ token: CF_ANALYTICS_TOKEN }));
  document.head.appendChild(script);
}

detectBrowserLang();
initLangSelector();
initThemeToggle();
ensureDict(LANG);
applyLang();
initFavicon();
initAds();
initAnalytics();
if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js");

/* ------------------------------------------------ PWA file-handler launch --- */

// PWA file handling ("Open with Paper Tools" on a .pdf): Windows/Chrome only,
// entirely feature-detected below — everywhere else this section is inert.
// stashLaunchFile/takeLaunchFile is a tiny consume-once handoff (one IndexedDB
// store, one key) used when the launch lands on the index page (no dropzone
// to feed directly): the file is stashed, the visitor picks a tool, and that
// tool page's own dropzone() calls takeLaunchFile() on init — a plain read
// that needs no launchQueue support on the tool page itself.
function openLaunchDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open("pt-launch", 1);
    req.onupgradeneeded = () => req.result.createObjectStore("files");
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function stashLaunchFile(file) {
  let db;
  try {
    db = await openLaunchDB();
    await new Promise((resolve, reject) => {
      const tx = db.transaction("files", "readwrite");
      tx.objectStore("files").put({ file, ts: Date.now() }, "pending");
      tx.oncomplete = resolve;
      tx.onerror = () => reject(tx.error);
    });
  } catch (e) {
    // ignored — stashing is best-effort
  } finally {
    if (db) db.close();
  }
}

async function takeLaunchFile() {
  let db;
  try {
    db = await openLaunchDB();
    // Resolve only once the transaction commits, so the delete below is
    // durable by the time the caller treats the file as consumed.
    const entry = await new Promise((resolve, reject) => {
      const tx = db.transaction("files", "readwrite");
      const store = tx.objectStore("files");
      const getReq = store.get("pending");
      getReq.onsuccess = () => {
        store.delete("pending"); // consume-once, even if stale
      };
      getReq.onerror = () => reject(getReq.error);
      tx.oncomplete = () => resolve(getReq.result || null);
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
    return entry && Date.now() - entry.ts <= 2 * 60 * 1000 ? entry.file : null;
  } catch (e) {
    return null;
  } finally {
    if (db) db.close();
  }
}

// A launched file targets whichever dropzone claims itself first (every tool
// page has exactly one relevant dropzone); if none has initialized yet, it
// waits in pendingLaunchFiles until dropzone() below picks it up.
let pendingLaunchFiles = null;
let activeDropzoneFeed = null;
function deliverLaunchFiles(files) {
  if (!files || !files.length) return;
  if (activeDropzoneFeed) activeDropzoneFeed(files);
  else pendingLaunchFiles = files;
}

// The tools showLaunchChooser() below offers — the single source of truth
// for which tool pages are allowed to consume a stashed launch file (see
// the dropzone() integration further down).
const LAUNCH_TOOLS = [
  ["/merge/", "Merge", "병합"],
  ["/split/", "Split & Extract", "분할·추출"],
  ["/remove/", "Remove Pages", "페이지 삭제"],
  ["/compress/", "Compress", "압축"],
  ["/reorder/", "Reorder", "순서 변경"],
  ["/pdf2img/", "PDF → Images", "PDF → 이미지"],
];
const LAUNCH_TOOL_SLUGS = LAUNCH_TOOLS.map(([href]) => href.replace(/\//g, ""));

// The tool slug for the current page, with any fixed-language prefix
// (/ko/, /ja/, …) stripped — same slicing setLang() uses to find "rest".
function currentToolSlug() {
  const path = FIXED ? location.pathname.slice(("/" + FIXED).length) || "/" : location.pathname;
  return path.split("/").filter(Boolean)[0] || "";
}

// Shows a lightweight "Open with which tool?" chooser on the index page
// (which has no dropzone of its own to feed directly).
function showLaunchChooser() {
  if (document.querySelector(".launch-overlay")) return;
  const overlay = document.createElement("div");
  overlay.className = "launch-overlay";
  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) overlay.remove();
  });
  const box = document.createElement("div");
  box.className = "launch-box";
  const h2 = document.createElement("h2");
  h2.textContent = t("Open with which tool?", "어떤 도구로 열까요?");
  box.appendChild(h2);
  const list = document.createElement("div");
  list.className = "launch-list";
  LAUNCH_TOOLS.forEach(([href, en, ko]) => {
    const a = document.createElement("a");
    a.className = "card-link";
    a.href = href;
    a.textContent = t(en, ko);
    list.appendChild(a);
  });
  box.appendChild(list);
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "secondary launch-cancel";
  cancel.textContent = t("Cancel", "취소");
  cancel.addEventListener("click", () => overlay.remove());
  box.appendChild(cancel);
  overlay.appendChild(box);
  document.body.appendChild(overlay);
}

// Fires on a File Handling API launch (Windows/Chrome PWA "Open with…").
// A tool page (has a .drop dropzone) feeds the file straight in; the index
// page has nothing to feed, so it stashes the file and offers a chooser.
if ("launchQueue" in window) {
  window.launchQueue.setConsumer(async (launchParams) => {
    if (!launchParams.files || !launchParams.files.length) return;
    let files;
    try {
      files = await Promise.all(launchParams.files.map((h) => h.getFile()));
    } catch (e) {
      return;
    }
    if (!files.length) return;
    if (document.querySelector(".drop")) {
      deliverLaunchFiles(files);
    } else {
      await stashLaunchFile(files[0]);
      showLaunchChooser();
    }
  });
}

/* ---------------------------------------------------------- boot / wasm --- */

// boot(wasmFile) instantiates the page's wasm binary, flips #status from
// "Loading tool…" to hidden once ready, and enables every [data-needs-wasm]
// control. Returns a promise that resolves once the module is running.
function boot(wasmFile) {
  const statusEl = document.getElementById("status");
  const setStatus = (msg) => {
    if (statusEl) statusEl.textContent = msg;
  };
  setStatus(t("Loading tool…", "도구 준비 중…"));

  const go = new Go();
  const ready = (async () => {
    let result;
    try {
      result = await WebAssembly.instantiateStreaming(fetch(wasmFile), go.importObject);
    } catch (e) {
      // Some static hosts serve .wasm with the wrong MIME type, which
      // breaks instantiateStreaming. Fall back to fetch + arrayBuffer.
      const resp = await fetch(wasmFile);
      const buf = await resp.arrayBuffer();
      result = await WebAssembly.instantiate(buf, go.importObject);
    }
    go.run(result.instance);
    setStatus("");
    if (statusEl) statusEl.hidden = true;
    document.querySelectorAll("[data-needs-wasm]").forEach((el) => {
      el.disabled = false;
    });
  })();

  ready.catch((err) => {
    setStatus(t("Failed to load tool: ", "도구를 불러오지 못했습니다: ") + err);
  });

  return ready;
}

/* -------------------------------------------------------------- dropzone --- */

// dropzone(id, {multiple}) wires up a .drop container: click and drag/drop
// both feed the hidden file input inside it, and the chosen files render as
// a .filelist. Returns { get files() }.
function dropzone(id, opts) {
  opts = opts || {};
  const el = document.getElementById(id);
  const input = el.querySelector("input[type=file]");
  const listEl = el.querySelector(".filelist");
  // The static "drag/drop or click" prompt; not every dropzone markup has
  // one, so this may be null.
  const promptEl = el.querySelector(":scope > span[data-i18n]");
  const promptOrig = promptEl ? { en: promptEl.dataset.en, ko: promptEl.dataset.ko } : null;
  let files = [];

  function render() {
    if (!listEl) return;
    listEl.innerHTML = "";
    for (const f of files) {
      const li = document.createElement("li");
      const kb = Math.max(1, Math.round(f.size / 1024));
      li.textContent = f.name + " (" + kb + " KB)";
      listEl.appendChild(li);
    }
  }

  // Once files are picked, the drag/drop prompt no longer applies — swap it
  // for a "replace" message. Updates data-en/data-ko too, so a language
  // switch (which re-reads those attributes) doesn't revert the swap.
  function updatePrompt() {
    if (!promptEl) return;
    if (files.length > 0) {
      promptEl.dataset.en = "Click to choose a different file";
      promptEl.dataset.ko = "클릭해서 다른 파일로 교체";
    } else {
      promptEl.dataset.en = promptOrig.en;
      promptEl.dataset.ko = promptOrig.ko;
    }
    promptEl.textContent = t(promptEl.dataset.en, promptEl.dataset.ko);
  }

  function setFiles(list) {
    const arr = Array.from(list);
    files = opts.multiple ? arr : arr.slice(0, 1);
    render();
    updatePrompt();
    // Single funnel for every path that sets files (click-pick, drop): lets
    // page-specific enhancements (e.g. web/thumbs.js) react without this
    // file needing to know they exist.
    el.dispatchEvent(new CustomEvent("dz:files", { detail: { files } }));
  }

  el.setAttribute("role", "button");
  if (!el.hasAttribute("tabindex")) el.setAttribute("tabindex", "0");

  el.addEventListener("click", () => input.click());
  el.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      input.click();
    }
  });
  el.addEventListener("dragover", (e) => {
    e.preventDefault();
    el.classList.add("over");
  });
  el.addEventListener("dragleave", () => el.classList.remove("over"));
  el.addEventListener("drop", (e) => {
    e.preventDefault();
    el.classList.remove("over");
    if (e.dataTransfer && e.dataTransfer.files) setFiles(e.dataTransfer.files);
  });
  // The input sits inside the clickable zone; stop its own click from
  // bubbling back into el's click handler and reopening the picker.
  input.addEventListener("click", (e) => e.stopPropagation());
  input.addEventListener("change", () => setFiles(input.files));

  // Claim any PWA-launched file: one already waiting for a dropzone (same
  // page raced a launchQueue event ahead of this dropzone() call), or one
  // stashed by the index page's tool chooser — the latter needs no
  // launchQueue support on this page, just a plain IndexedDB read. The
  // stash is only consumed on the tools showLaunchChooser() actually
  // offered — otherwise it stays put for whichever of those tools the
  // visitor eventually navigates to (or expires past its TTL).
  if (!activeDropzoneFeed) {
    activeDropzoneFeed = setFiles;
    if (pendingLaunchFiles) {
      const f = pendingLaunchFiles;
      pendingLaunchFiles = null;
      setFiles(f);
    } else if (LAUNCH_TOOL_SLUGS.indexOf(currentToolSlug()) !== -1) {
      takeLaunchFile().then((f) => {
        if (f) deliverLaunchFiles([f]);
      });
    }
  }

  return {
    get files() {
      return files;
    },
  };
}

/* ---------------------------------------------------------------- bytes --- */

async function fileBytes(f) {
  const buf = await f.arrayBuffer();
  return new Uint8Array(buf);
}

async function allFiles(files) {
  const out = [];
  for (const f of files) out.push(await fileBytes(f));
  return out;
}

/* --------------------------------------------------------------- errors --- */

// Go-error substrings mapped to friendlier messages in every supported
// language. Anything unrecognized is shown as-is.
const ERROR_MAP = [
  { needle: "encrypted files are not supported", en: "This PDF is password-protected. Use the Unlock tool first.", ko: "암호가 걸린 PDF입니다. 먼저 [암호 해제] 도구를 사용하세요.", ja: "このPDFはパスワードで保護されています。先に[ロック解除]ツールを使ってください。", zh: "此 PDF 受密码保护。请先使用[解锁]工具。", es: "Este PDF está protegido con contraseña. Usa primero la herramienta Desbloquear.", fr: "Ce PDF est protégé par mot de passe. Utilisez d'abord l'outil Déverrouiller.", de: "Diese PDF-Datei ist passwortgeschützt. Verwenden Sie zuerst das Tool „Entsperren\"." },
  { needle: "wrong password", en: "Incorrect password.", ko: "암호가 올바르지 않습니다.", ja: "パスワードが正しくありません。", zh: "密码不正确。", es: "Contraseña incorrecta.", fr: "Mot de passe incorrect.", de: "Falsches Passwort." },
  { needle: "only Latin-1 text is supported", en: "Watermark supports Latin letters, numbers and symbols only.", ko: "워터마크는 영문·숫자·기호만 지원합니다.", ja: "ウォーターマークは英数字・記号のみ対応しています。", zh: "水印仅支持拉丁字母、数字和符号。", es: "La marca de agua solo admite letras, números y símbolos latinos.", fr: "Le filigrane ne prend en charge que les lettres, chiffres et symboles latins.", de: "Wasserzeichen unterstützen nur lateinische Buchstaben, Zahlen und Symbole." },
  { needle: "not a PDF", en: "This doesn't look like a PDF file.", ko: "PDF 파일이 아닌 것 같습니다.", ja: "PDFファイルではないようです。", zh: "这看起来不是 PDF 文件。", es: "Esto no parece un archivo PDF.", fr: "Cela ne ressemble pas à un fichier PDF.", de: "Das sieht nicht nach einer PDF-Datei aus." },
  { needle: "unsupported format", en: "Only PNG or JPG is supported.", ko: "PNG 또는 JPG만 지원합니다.", ja: "PNGまたはJPGのみ対応しています。", zh: "仅支持 PNG 或 JPG。", es: "Solo se admiten PNG o JPG.", fr: "Seuls PNG ou JPG sont pris en charge.", de: "Nur PNG oder JPG werden unterstützt." },
  { needle: "CMYK", en: "CMYK JPEG is not supported.", ko: "CMYK JPEG는 지원하지 않습니다.", ja: "CMYKのJPEGには対応していません。", zh: "不支持 CMYK JPEG。", es: "No se admite JPEG en CMYK.", fr: "Le JPEG CMYK n'est pas pris en charge.", de: "CMYK-JPEG wird nicht unterstützt." },
  { needle: "유효한 docx", en: "This isn't a valid .docx file.", ko: "유효한 docx 파일이 아닙니다.", ja: "有効な.docxファイルではありません。", zh: "这不是有效的 .docx 文件。", es: "Este no es un archivo .docx válido.", fr: "Ce n'est pas un fichier .docx valide.", de: "Dies ist keine gültige .docx-Datei." },
  { needle: "유효한 hwpx", en: "This isn't a valid .hwpx file.", ko: "유효한 hwpx 파일이 아닙니다.", ja: "有効な.hwpxファイルではありません。", zh: "这不是有效的 .hwpx 文件。", es: "Este no es un archivo .hwpx válido.", fr: "Ce n'est pas un fichier .hwpx valide.", de: "Dies ist keine gültige .hwpx-Datei." },
  { needle: "유효한 hwp", en: "This isn't a valid .hwp file.", ko: "유효한 hwp 파일이 아닙니다.", ja: "有効な.hwpファイルではありません。", zh: "这不是有效的 .hwp 文件。", es: "Este no es un archivo .hwp válido.", fr: "Ce n'est pas un fichier .hwp valide.", de: "Dies ist keine gültige .hwp-Datei." },
  { needle: "암호가 걸린 한글", en: "This Hangul document is password-protected.", ko: "암호가 걸린 한글 문서입니다.", ja: "このHangul文書はパスワードで保護されています。", zh: "此 Hangul 文档受密码保护。", es: "Este documento Hangul está protegido con contraseña.", fr: "Ce document Hangul est protégé par mot de passe.", de: "Dieses Hangul-Dokument ist passwortgeschützt." },
  { needle: "처리 중 오류", en: "Something went wrong while processing the file.", ko: "처리 중 오류가 발생했습니다.", ja: "ファイルの処理中に問題が発生しました。", zh: "处理文件时出现问题。", es: "Algo salió mal al procesar el archivo.", fr: "Un problème est survenu lors du traitement du fichier.", de: "Beim Verarbeiten der Datei ist ein Fehler aufgetreten." },
  { needle: "no extractable images", en: "No extractable images were found.", ko: "추출할 수 있는 이미지가 없습니다.", ja: "抽出できる画像が見つかりませんでした。", zh: "未找到可提取的图片。", es: "No se encontraron imágenes extraíbles.", fr: "Aucune image extractible n'a été trouvée.", de: "Es wurden keine extrahierbaren Bilder gefunden." },
  { needle: "유효한 xlsx 파일이 아닙니다", en: "This isn't a valid .xlsx file.", ko: "유효한 xlsx 파일이 아닙니다.", ja: "有効な.xlsxファイルではありません。", zh: "这不是有效的 .xlsx 文件。", es: "Este no es un archivo .xlsx válido.", fr: "Ce n'est pas un fichier .xlsx valide.", de: "Dies ist keine gültige .xlsx-Datei." },
  { needle: "xlsx 파일에 시트가 없습니다", en: "This workbook has no sheets.", ko: "xlsx 파일에 시트가 없습니다.", ja: "xlsxファイルにシートがありません。", zh: "该 xlsx 文件中没有工作表。", es: "Este libro de Excel no tiene hojas.", fr: "Ce classeur ne contient aucune feuille.", de: "Diese Arbeitsmappe enthält keine Blätter." },
  { needle: "워크시트를 찾을 수 없습니다", en: "Worksheet not found.", ko: "워크시트를 찾을 수 없습니다.", ja: "ワークシートが見つかりません。", zh: "未找到该工作表。", es: "No se encontró la hoja de cálculo.", fr: "Feuille de calcul introuvable.", de: "Arbeitsblatt nicht gefunden." },
  { needle: "xlsx 파일에 유효한 워크시트가 없습니다", en: "This workbook has no valid worksheets.", ko: "xlsx 파일에 유효한 워크시트가 없습니다.", ja: "xlsxファイルに有効なワークシートがありません。", zh: "该 xlsx 文件中没有有效的工作表。", es: "Este libro de Excel no tiene hojas de cálculo válidas.", fr: "Ce classeur ne contient aucune feuille de calcul valide.", de: "Diese Arbeitsmappe enthält keine gültigen Arbeitsblätter." },
  { needle: "잘못된 셀 참조", en: "Invalid cell reference.", ko: "잘못된 셀 참조입니다.", ja: "無効なセル参照です。", zh: "无效的单元格引用。", es: "Referencia de celda no válida.", fr: "Référence de cellule non valide.", de: "Ungültiger Zellbezug." },
  { needle: "마크다운 내용이 비어 있습니다", en: "The Markdown content is empty.", ko: "마크다운 내용이 비어 있습니다.", ja: "Markdownの内容が空です。", zh: "Markdown 内容为空。", es: "El contenido de Markdown está vacío.", fr: "Le contenu Markdown est vide.", de: "Der Markdown-Inhalt ist leer." },
];

function mapError(msg) {
  for (const e of ERROR_MAP) {
    if (msg.indexOf(e.needle) !== -1) return e[LANG] || e.en;
  }
  return msg;
}

function showErr(el, msg) {
  if (el) el.textContent = mapError(String(msg));
}

/* ----------------------------------------------------------------- run --- */

// run(btn, fn) disables btn for the duration of the async fn, showing
// "Working…" in the active language, yields one tick so the disabled state
// paints before any heavy synchronous wasm call, and routes thrown errors
// to #err.
async function run(btn, fn) {
  const original = btn.textContent;
  const errEl = document.getElementById("err");
  if (errEl) errEl.textContent = "";
  btn.disabled = true;
  btn.textContent = t("Working…", "처리 중…");
  await new Promise((resolve) => setTimeout(resolve, 0));
  try {
    await fn();
  } catch (e) {
    showErr(errEl, e && e.message ? e.message : String(e));
  } finally {
    btn.disabled = false;
    btn.textContent = original;
  }
}

/* -------------------------------------------------------------- results --- */

// finish(r, filename, errEl, mime) handles the {data|json|error} shape every
// pdfRun call returns: downloads on data, returns the parsed object on
// json, shows the (translated) message on error. mime defaults to "application/pdf".
function finish(r, filename, errEl, mime) {
  if (r.error) {
    showErr(errEl, r.error);
    return null;
  }
  if (r.data) {
    download(r.data, filename, mime);
    return null;
  }
  if (r.json) {
    return JSON.parse(r.json);
  }
  return null;
}

function download(u8, name, mime) {
  mime = mime || "application/pdf";
  const blob = new Blob([u8], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
