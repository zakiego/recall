# syntax=docker/dockerfile:1
# zaki-selfhost Dockerfile.nitro, on bookworm-slim instead of alpine: the native
# modules (onnxruntime-node, sqlite-vec, better-sqlite3) ship glibc builds only.
FROM node:24-bookworm-slim AS build
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml* .npmrc* ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store pnpm install --frozen-lockfile
COPY . .
RUN pnpm build \
  # onnxruntime-node ships every platform's binaries (~150 MB). Keep this one.
  && ORT=.output/server/node_modules/onnxruntime-node/bin/napi-v6 \
  && ARCH=$(node -p "process.arch") \
  && find "$ORT" -mindepth 1 -maxdepth 1 ! -name linux -exec rm -rf {} + \
  && find "$ORT/linux" -mindepth 1 -maxdepth 1 ! -name "$ARCH" -exec rm -rf {} + \
  # better-sqlite3 carries prebuilds for every platform too.
  && find .output/server/node_modules/better-sqlite3/prebuilds -type f ! -name "linux-$ARCH.node" -delete 2>/dev/null || true

FROM node:24-bookworm-slim
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOST=0.0.0.0 DATA_DIR=/data
# /data is the volume: the SQLite file and the cached embedding model live there.
RUN mkdir -p /data && chown node:node /data
USER node
# .output is self-contained, no node_modules needed.
COPY --from=build --chown=node:node /app/.output ./.output
EXPOSE 3000
VOLUME ["/data"]
CMD ["node", ".output/server/index.mjs"]
