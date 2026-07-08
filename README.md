# Paper Tools

**English** | [한국어](README.ko.md)

Privacy-first PDF & file tools that run **entirely in your browser**. Files
are never uploaded — every conversion happens locally on your device.

**Live:** https://papertools.dev

> "Paper Tools" is the product name. `file-utils` is just the Go
> module / repository name used internally by imports (`file-utils/pdf`).

---

## What it is

38 client-side tools for PDFs, images, and office documents. Open a tool,
drop a file, get a result — nothing leaves the browser tab. No server, no
uploads, no account.

## Tools

| Group | Tools |
|-------|-------|
| **Organize** | Merge · Interleave · Split & Extract · Remove Pages · Reorder · Insert Blank |
| **Transform** | Rotate · Crop · Resize · N-up |
| **Content** | Images → PDF · Watermark · Page Numbers · Stamp / Signature (draw or upload) / Text · Flatten PDF |
| **Convert** | Image Convert (PNG/JPG/GIF) · Image Resize · PDF → Text · OCR (scanned PDFs/images, English/Korean) · PDF → Images (page ranges, PNG/JPG quality, ZIP) · Extract Images (ZIP) · Text → PDF · Markdown → PDF · Word → PDF · Hangul(.hwpx) → PDF · Old Hangul(.hwp) → PDF · Word ↔ Hangul · PDF → Word · PDF → Hangul · Excel → CSV |
| **Document** | Compress (quality/DPI/grayscale) · Metadata · PDF Info · Protect (AES-256/AES-128) · Unlock · Compare PDFs · Direct Send (device-to-device, never uploaded) |

## Highlights

- **Korean text renders correctly** in generated PDFs, including `Text → PDF`,
  `Markdown → PDF` and the document converters.
- **Legacy `.hwp` files are supported.**
- **Dark mode, on by default.** Follows your OS or saved light/dark
  preference with no flash of the wrong theme; the UI language also
  auto-detects from your browser's locale on first visit.
- **7 UI languages**, English default: English · 한국어 · 日本語 · 中文(简体) ·
  Español · Français · Deutsch. The brand and technical tokens (PDF, Word,
  `.docx`, …) stay untranslated.
- **Offline & installable.** After a tool page has loaded once, it keeps
  working offline; the site can be installed as an app, and on desktop
  Chrome an installed Paper Tools appears in "Open with" for PDF files.
- **Visual page management.** `Reorder`, `Remove Pages` and `Split &
  Extract` show clickable page thumbnails — drag to reorder, click to
  select — while the text inputs keep working as before.
- **Batch processing.** `Compress` and `Image Convert` accept multiple
  files at once and download the results as a single ZIP.
- **Private by default.** No tracking scripts load unless you opt in
  (EthicalAds / Cloudflare Web Analytics are gated behind config flags).

## Build

```sh
./build.sh
```

Compiles one `.wasm` for each Go-backed tool into `web/<tool>/<tool>.wasm`,
copies `wasm_exec.js` into `web/`, and regenerates localized static pages.
Requires a Go toolchain (1.26+), [TinyGo](https://tinygo.org) (0.41+) and
[Binaryen](https://github.com/WebAssembly/binaryen)'s `wasm-opt`, plus Node
for the page generator. The `.wasm` binaries are git-ignored and rebuilt by
CI on every deploy.

## Performance

Each tool ships as its own WebAssembly binary, compiled with **TinyGo** and
post-optimized with Binaryen's `wasm-opt -Oz`. A per-tool binary is
~0.4–0.8 MB on disk — down from ~4 MB with the standard Go toolchain, an
~84% reduction (~24 MB total across all 35 Go-backed tools, down from
~144 MB). Over the wire, Brotli compression on the CDN brings a typical
tool down to roughly 130–250 KB.

Measured against the previous production deploy (standard Go toolchain,
same CDN): **raw binary size −83.5%**, **over-the-wire (Brotli) size
−77%**, and **download time −45%**. Output equivalence with the previous
toolchain was verified (byte-identical outputs for representative tools),
and the test suite also runs under TinyGo in CI.

## Run locally

```sh
./build.sh
python3 -m http.server -d web 8000
```

Open http://localhost:8000.

## Deploy

`web/` is fully static. It's hosted on **Cloudflare Pages**, and CI
auto-deploys on every push to `main` (see `.github/workflows/deploy.yml`):
GitHub Actions sets up Go, runs `./build.sh`, then `wrangler pages deploy`.

For CI to deploy, add two repository secrets (Settings → Secrets and
variables → Actions):

- `CLOUDFLARE_API_TOKEN` — a token with the **Cloudflare Pages: Edit**
  permission (Cloudflare dashboard → My Profile → API Tokens).
- `CLOUDFLARE_ACCOUNT_ID` — your Cloudflare account ID.

Any static host works too — just serve `.wasm` as `application/wasm` and
enable brotli/gzip.

## Tests

```sh
go test ./pdf ./imgconv
```

## Limitations

- Encrypted PDFs must go through **Unlock** first; other tools reject
  encrypted input. AES-256, AES-128 and RC4-128 (revisions 2–4) are all
  supported.
- Document → PDF (`.docx` / `.hwpx` / `.hwp`) is a **best-effort text reflow**:
  paragraph text only, no layout, tables, images, or styling.
- `PDF → Word` and `PDF → Hangul` are also **text-only**: the PDF's text is
  extracted and rebuilt as paragraphs, with no layout, images, or tables
  preserved.
- `Word ↔ Hangul`'s `.hwpx` output is structurally valid but **unverified in
  real Hancom Office** (no `.hwpx` import filter was available to test with).
- `Markdown → PDF` is a plain-text subset: headings, lists, blockquotes, code
  blocks and rules are laid out, but tables and images aren't supported, and
  inline bold/italic/link markers are flattened to plain text.
- `Excel → CSV` copies date cells as their raw Excel serial number (e.g.
  `45000`), not a calendar date.
- `Compare PDFs` diffs each file's extracted text only; visual layout,
  images and formatting differences aren't detected.
- `Image Resize` (and `Image Convert`) only read/write the first frame of an
  animated GIF; animation isn't preserved.
- `OCR` supports English and Korean text and works best on clean,
  high-resolution scans of printed text; handwriting recognition is
  hit-or-miss.
- `Direct Send` needs both devices to be reachable from each other directly
  (usually the same Wi-Fi or local network); it moves the file straight from
  device to device with no server relay, and has no way to help two devices
  on different networks find each other.
