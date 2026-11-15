#!/usr/bin/env bash
# Runs testdata/gen/membench.ts (the TS embedder) twice in a Linux arm64
# node:24-bookworm-slim container with the TS image's malloc settings, to set
# next to tools/membench in the Go spike image. Run from apps/server.
set -euo pipefail
here="$(cd "$(dirname "$0")/../.." && pwd)"
app="$here/../app"
work="$(mktemp -d /tmp/recall-membench-linux.XXXX)"
trap 'rm -rf "$work"' EXIT
cp "$app/src/server/embed-model.ts" "$app/src/server/tokenizer-compact.ts" "$work/"
sed 's#"../../../app/src/server/embed-model.ts"#"./embed-model.ts"#' "$here/testdata/gen/membench.ts" > "$work/membench.ts"
cat > "$work/package.json" <<'JSON'
{
  "private": true,
  "type": "module",
  "dependencies": {
    "@huggingface/transformers": "4.3.2",
    "@huggingface/tokenizers": "0.2.0",
    "@huggingface/jinja": "0.5.11",
    "onnxruntime-node": "1.30.0",
    "sharp": "0.35.5"
  }
}
JSON
docker run --rm --name recall-go-dev-tsmembench --platform linux/arm64 \
  -e MALLOC_ARENA_MAX=2 -e GLIBC_TUNABLES=glibc.malloc.mmap_threshold=131072 \
  -v "$work":/work -v "$app/data/models":/models:ro -w /work \
  node:24-bookworm-slim \
  sh -c 'npm install --no-audit --no-fund --loglevel=error >/dev/null 2>&1 && node membench.ts /models && node membench.ts /models'
