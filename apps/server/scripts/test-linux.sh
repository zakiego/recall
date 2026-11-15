#!/usr/bin/env bash
# Runs the Go tests inside a Linux container (arm64 is the production platform,
# amd64 is CI), with this checkout, the host's Go module cache (read-only) and
# the model files mounted. Fetches the ONNX Runtime library for that platform
# into lib/ first. The container is removed when it exits.
#
#   scripts/test-linux.sh arm64|amd64 [go test args...]   (default ./...)
set -euo pipefail
arch="${1:?arch}"
shift
here="$(cd "$(dirname "$0")/.." && pwd)"
models="$(cd "$here/../app/data/models" && pwd)"
modcache="$(go env GOMODCACHE)"
args=("$@")
if [ ${#args[@]} -eq 0 ]; then args=(./...); fi
docker run --rm --name "recall-go-dev-test-$arch" --platform "linux/$arch" \
  -v "$here":/src -v "$modcache":/go/pkg/mod:ro -v "$models":/models:ro \
  -v "$here/../app/drizzle":/app/drizzle:ro \
  -v "$here/../app/src/server/gateway/fixtures":/app/src/server/gateway/fixtures:ro \
  -e MODELS_DIR=/models -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local -w /src \
  golang:1.26.11-bookworm \
  sh -c "./scripts/fetch-onnxruntime.sh linux-$arch >/dev/null && go test -tags sqlite_fts5 -count=1 ${args[*]}"
