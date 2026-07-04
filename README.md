# 종이도구 (file-utils)

Eighteen client-side PDF tools — merge, split, rotate, crop, resize, n-up,
image-to-PDF, watermark, page numbers, compress, metadata, info, password
protect/unlock, and more. Every tool runs entirely inside the browser tab:
files are never uploaded anywhere.

Built on a single dependency-free Go package (`pdf/`) — a from-scratch PDF
reader/writer, no C libraries, no third-party modules — compiled to
WebAssembly. The static site (`web/`) has zero JS frameworks, zero
third-party CSS, and zero webfonts.

## Build

```sh
./build.sh
```

Compiles one `.wasm` binary per tool into `web/<tool>/<tool>.wasm` and
copies `wasm_exec.js` (the Go↔wasm shim) into `web/`. Requires a Go
toolchain; no other build tooling.

## Run locally

```sh
python3 -m http.server -d web
```

Then open http://localhost:8000.

## Deploy

`web/` is fully static — host it on any static file server (S3 + CloudFront,
GitHub Pages, nginx, Netlify, ...). No backend, no server-side processing.

If you serve it from S3/CloudFront (or anything similarly picky):

- Make sure `.wasm` files are served with `Content-Type: application/wasm`.
  Wrong MIME types break `WebAssembly.instantiateStreaming`; `app.js` falls
  back to `fetch` + `arrayBuffer` automatically, but the fast path needs the
  right header.
- Enable gzip or brotli compression — the wasm binaries compress well
  (Go's runtime is mostly template code) and this materially shrinks
  first-load time.

## Architecture

Each tool page loads its own small wasm binary rather than one shared
monolith. `wasm/jsu` holds the handful of `syscall/js` helpers (byte
marshaling, the `{data|json|error}` result envelope) shared by all 18
binaries; each `wasm/<tool>/main.go` is a ~15-line wrapper that destructures
JS arguments and calls straight into `pdf`. The upshot: visiting `/merge/`
downloads only the merge binary, not all eighteen tools' worth of code.

## Limitations

- Encrypted PDFs must go through **암호 해제 (Unlock)** first — every other
  tool refuses encrypted input.
- Only AES-128 / RC4-128 (PDF standard security handler, revisions 3–4) is
  supported for both reading and writing. AES-256 (revision 5/6) is not
  supported.
- **워터마크 (Watermark)** text is limited to Latin-1 (English letters,
  digits, and symbols) — there's no embedded font for Hangul or other
  non-Latin-1 scripts.
- **N-UP** does not bake in each source page's `/Rotate` value; pages that
  rely on viewer-applied rotation may appear sideways in the merged sheet.

## Tests

```sh
go test ./pdf/...
```

The `pdf` package (40 tests) is the only place PDF semantics live; the wasm
and web layers are thin wrappers around it.
