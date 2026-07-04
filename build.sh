#!/bin/sh
set -e
cd "$(dirname "$0")"

WASM_EXEC="$(go env GOROOT)/lib/wasm/wasm_exec.js"
[ -f "$WASM_EXEC" ] || WASM_EXEC="$(go env GOROOT)/misc/wasm/wasm_exec.js"
cp "$WASM_EXEC" web/

TOOLS="merge interleave split remove reorder blank rotate crop resize nup img2pdf watermark pagenum compress metadata info protect unlock imgconv pdftext pdfimages txt2pdf docx2pdf hwpx2pdf hwp2pdf docx2hwpx hwpx2docx"

for t in $TOOLS; do
  echo "building $t..."
  GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "web/$t/$t.wasm" "./wasm/$t"
done

echo
echo "=== web/ total ==="
du -sh web
echo
echo "=== per-tool wasm sizes ==="
for t in $TOOLS; do
  ls -lh "web/$t/$t.wasm"
done

echo
echo "=== generating i18n pages ==="
node tools/gen-i18n.mjs
