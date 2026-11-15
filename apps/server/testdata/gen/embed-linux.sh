#!/usr/bin/env bash
# Makes testdata/embed-linux-<arch>.json: the TS embedder's vectors for the
# golden corpus, computed on Linux (arm64 is the production and CI platform;
# amd64 on Apple silicon runs under emulation) in the same node:24-bookworm-slim base the TS image uses, with the
# dependency versions from pnpm-lock.yaml. Needs the model in
# apps/app/data/models. Run from apps/server: testdata/gen/embed-linux.sh arm64
# The scratch folder and container are removed afterwards.
set -euo pipefail
arch="${1:-arm64}"
here="$(cd "$(dirname "$0")/../.." && pwd)"
app="$here/../app"
work="$(mktemp -d /tmp/recall-embed-linux.XXXX)"
trap 'rm -rf "$work"' EXIT
cp "$app/src/server/embed-model.ts" "$app/src/server/tokenizer-compact.ts" "$here/testdata/gen/embed-linux.mjs" "$work/"
cp "$here/testdata/embed.json" "$work/"
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
docker run --rm --name recall-go-dev-tsembed --platform "linux/$arch" \
  -v "$work":/work -v "$app/data/models":/models:ro -w /work \
  node:24-bookworm-slim \
  sh -c 'npm install --no-audit --no-fund --loglevel=error >/dev/null && node embed-linux.mjs embed.json /models out.json'
cp "$work/out.json" "$here/testdata/embed-linux-$arch.json"
echo "wrote testdata/embed-linux-$arch.json"
