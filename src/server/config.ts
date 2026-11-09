import path from "node:path"

// Read env lazily so tests can set process.env before the first call.
export function config() {
  const dataDir = process.env.DATA_DIR ?? path.resolve("data")
  return {
    dataDir,
    dbPath: process.env.RECALL_DB_PATH ?? path.join(dataDir, "recall.db"),
    modelsDir: process.env.MODELS_DIR ?? path.join(dataDir, "models"),
    gatewayUrl: (process.env.GATEWAY_URL ?? "http://127.0.0.1:8793").replace(/\/+$/, ""),
    gatewayKey: process.env.GATEWAY_KEY ?? "",
    extractModel: process.env.EXTRACT_MODEL || "claude-haiku-5-5",
    fallbackModel: process.env.FALLBACK_MODEL || "claude-haiku-4-5",
    defaultContainer: process.env.RECALL_DEFAULT_CONTAINER || "default",
    adminPasswordHash: process.env.ADMIN_PASSWORD_HASH ?? "",
    sessionSecret: process.env.SESSION_SECRET ?? "",
    jobsEnabled: process.env.RECALL_JOBS !== "off",
  }
}
