#!/bin/sh
set -e
cd "$(dirname "$0")"

command -v tinygo >/dev/null 2>&1 || {
  echo "error: tinygo not found on PATH (required to build wasm tools)" >&2
  exit 1
}

WASM_EXEC="$(tinygo env TINYGOROOT)/targets/wasm_exec.js"
cp "$WASM_EXEC" web/
cp tools/operation-catalog.json web/operation-catalog.json

ALL_TOOLS="$(node -e 'const c=require("./tools/operation-catalog.json"); process.stdout.write(c.filter(x=>x.engine==="wasm").map(x=>x.id).join(" "))')"
if [ "$#" -gt 0 ]; then
  TOOLS=""
  for t in "$@"; do
    case " $ALL_TOOLS " in
      *" $t "*) ;;
      *)
        echo "error: unknown WASM tool: $t" >&2
        exit 1
        ;;
    esac
    case " $TOOLS " in
      *" $t "*) ;;
      *) TOOLS="${TOOLS:+$TOOLS }$t" ;;
    esac
  done
else
  TOOLS="$ALL_TOOLS"
fi

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
