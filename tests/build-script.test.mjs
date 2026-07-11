import assert from "node:assert/strict";
import { chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

async function fixture() {
  const dir = await mkdtemp(path.join(os.tmpdir(), "file-utils-build-"));
  const bin = path.join(dir, "bin");
  const tinygoRoot = path.join(dir, "tinygo-root");
  await Promise.all([
    mkdir(bin, { recursive: true }),
    mkdir(path.join(tinygoRoot, "targets"), { recursive: true }),
    mkdir(path.join(dir, "tools"), { recursive: true }),
    mkdir(path.join(dir, "wasm", "redact"), { recursive: true }),
    mkdir(path.join(dir, "wasm", "split"), { recursive: true }),
    mkdir(path.join(dir, "web", "redact"), { recursive: true }),
    mkdir(path.join(dir, "web", "split"), { recursive: true }),
  ]);
  await cp(path.join(root, "build.sh"), path.join(dir, "build.sh"));
  await writeFile(path.join(tinygoRoot, "targets", "wasm_exec.js"), "fixture\n");
  await writeFile(path.join(dir, "tools", "operation-catalog.json"), JSON.stringify([
    { id: "redact", engine: "wasm" },
    { id: "split", engine: "wasm" },
    { id: "direct-send", engine: "browser" },
  ]));
  await writeFile(path.join(dir, "tools", "gen-i18n.mjs"), "console.log('generated fixture pages');\n");
  await writeFile(path.join(bin, "tinygo"), `#!/bin/sh
if [ "$1" = env ]; then
  printf '%s\\n' "$FAKE_TINYGO_ROOT"
  exit 0
fi
for arg do tool=$arg; done
tool=\${tool#./wasm/}
printf '%s\\n' "$tool" >> "$BUILD_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then
    shift
    mkdir -p "$(dirname "$1")"
    printf 'wasm' > "$1"
    exit 0
  fi
  shift
done
exit 2
`);
  await writeFile(path.join(bin, "wasm-opt"), `#!/bin/sh
input=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    *.wasm) [ -z "$input" ] && input="$1" ;;
    -o) shift; output="$1" ;;
  esac
  shift
done
cp "$input" "$output"
`);
  await Promise.all([
    chmod(path.join(bin, "tinygo"), 0o755),
    chmod(path.join(bin, "wasm-opt"), 0o755),
  ]);
  return { dir, bin, tinygoRoot, log: path.join(dir, "build.log") };
}

async function runBuild(args) {
  const setup = await fixture();
  try {
    const result = spawnSync("sh", ["build.sh", ...args], {
      cwd: setup.dir,
      encoding: "utf8",
      env: {
        ...process.env,
        BUILD_LOG: setup.log,
        FAKE_TINYGO_ROOT: setup.tinygoRoot,
        JOBS: "1",
        PATH: `${setup.bin}:${process.env.PATH}`,
      },
    });
    const built = await readFile(setup.log, "utf8").catch(() => "");
    return { ...result, built: built.trim().split("\n").filter(Boolean) };
  } finally {
    await rm(setup.dir, { recursive: true, force: true });
  }
}

test("positional tool IDs build only the requested WASM tools", async () => {
  const result = await runBuild(["redact"]);

  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.built, ["redact"]);
});

test("an unknown or non-WASM tool fails before compilation", async () => {
  for (const id of ["missing", "direct-send"]) {
    const result = await runBuild([id]);

    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`unknown WASM tool: ${id}`));
    assert.deepEqual(result.built, []);
  }
});
