#!/bin/sh
set -e
cd "$(dirname "$0")"

command -v tinygo >/dev/null 2>&1 || {
  echo "error: tinygo not found on PATH (required to build wasm tools)" >&2
  exit 1
}

WASM_EXEC="$(tinygo env TINYGOROOT)/targets/wasm_exec.js"
cp "$WASM_EXEC" web/

TOOLS="merge interleave split remove reorder blank rotate crop resize nup img2pdf watermark stamp flatten pagenum compress metadata info protect unlock pdfdiff imgconv imgresize pdftext pdfimages txt2pdf md2pdf docx2pdf hwpx2pdf hwp2pdf docx2hwpx hwpx2docx pdf2docx pdf2hwpx xlsx2csv"

JOBS="${JOBS:-$(command -v nproc >/dev/null 2>&1 && nproc || sysctl -n hw.ncpu)}"
echo "building with ${JOBS} parallel jobs..."

printf '%s\n' $TOOLS | xargs -P "$JOBS" -n1 sh -c '
  set -e
  t="$1"
  echo "building $t..."
  tinygo build -target wasm -no-debug -o "web/$t/$t.wasm" "./wasm/$t"
  if command -v wasm-opt >/dev/null 2>&1; then
    tmp="web/$t/$t.wasm.opt"
    wasm-opt -Oz --enable-bulk-memory "web/$t/$t.wasm" -o "$tmp"
    mv "$tmp" "web/$t/$t.wasm"
  else
    echo "warning: wasm-opt not found on PATH, skipping optimization for $t"
  fi
' sh

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
