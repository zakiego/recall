# syntax=docker/dockerfile:1
# One image, one process: `recall serve` is the API, the MCP server, the job
# loop and the dashboard (docs/GO-PORT.md).
#
#   1. dashboard  Node + pnpm build the static SPA (apps/app/dist)
#   2. server     Go builds the binary with cgo (SQLite with FTS5, sqlite-vec,
#                 the ONNX Runtime binding) and that dist embedded, and fetches
#                 the ONNX Runtime library (version and SHA-256 pinned in
#                 apps/server/scripts/fetch-onnxruntime.sh)
#   3. final      distroless cc (glibc and libstdc++ for ONNX Runtime, no shell,
#                 no Node), user 1000:1000
#
# The context is the repo root, trimmed by .dockerignore: docs, the site and
# the edge worker never enter it (they only send their package.json, which the
# shared lockfile needs). A Go-only change doesn't touch stage 1.

# --- 1. dashboard ------------------------------------------------------------
FROM node:26-bookworm-slim AS dashboard
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /repo
# Every workspace manifest, so the shared lockfile matches; nothing else yet,
# so the install layer stays cached until dependencies change. Scripts are
# skipped: the SPA build needs no native module.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc* ./
COPY --parents apps/*/package.json ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
  pnpm install --frozen-lockfile --ignore-scripts --filter @recall/app...
COPY apps/app ./apps/app
RUN pnpm --filter @recall/app build

# --- 2. server ---------------------------------------------------------------
# go.mod asks for Go 1.26.0 or newer; the CI server job pins the same 1.26.11.
FROM golang:1.26.11-bookworm AS server
WORKDIR /src/apps/server
COPY apps/server/go.mod apps/server/go.sum ./
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod go mod download
COPY apps/server/scripts ./scripts
RUN ./scripts/fetch-onnxruntime.sh "linux-$(go env GOARCH)" /out/lib
COPY apps/server ./
# The dashboard build replaces the placeholder that internal/web embeds.
COPY --from=dashboard /repo/apps/app/dist ./internal/web/dist
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
  --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
  CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -ldflags "-s -w" -o /out/recall ./cmd/recall \
  && mkdir -p /out/data

# --- 3. final ----------------------------------------------------------------
FROM gcr.io/distroless/cc-debian12:nonroot
# ONNX Runtime where embed.LibPath looks first.
COPY --from=server /out/lib/ /usr/lib/recall/
COPY --from=server /out/recall /usr/local/bin/recall
# /data is the volume: recall.db (SQLite WAL), the embedding model, uploads,
# backups. The production volume was written by the old image's `node` user,
# 1000:1000, so the process runs as those ids.
COPY --from=server --chown=1000:1000 /out/data /data
# NODE_ENV=production is still the server's production switch (as in the old
# image): the embedding model runs in a child process that stops when idle,
# and the dev-only fakes stay off.
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000 DATA_DIR=/data
USER 1000:1000
EXPOSE 3000
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["recall", "healthcheck"]
# `recall` is also the admin CLI: docker compose exec app recall create-user <name>
ENTRYPOINT ["recall"]
CMD ["serve"]
