import path from "node:path"

// Read env lazily so tests can set process.env before the first call.
export function config() {
  const url = (process.env.RECALL_URL || "http://localhost:4800").replace(/\/+$/, "")
  const dataDir = process.env.DATA_DIR ?? path.resolve("data")
  return {
    dataDir,
    dbPath: process.env.RECALL_DB_PATH ?? path.join(dataDir, "recall.db"),
    modelsDir: process.env.MODELS_DIR ?? path.join(dataDir, "models"),
    // Where the embedding model runs (embed.ts). "child" frees its memory
    // between bursts of work; production defaults to it.
    embedMode: (process.env.RECALL_EMBED === "child" || process.env.RECALL_EMBED === "inline"
      ? process.env.RECALL_EMBED
      : process.env.NODE_ENV === "production"
        ? "child"
        : "inline") as "child" | "inline",
    // How long the embedding child may sit idle before it's stopped.
    embedIdleMs: Number(process.env.RECALL_EMBED_IDLE_MS) || 10 * 60_000,
    gatewayUrl: (process.env.GATEWAY_URL ?? "http://127.0.0.1:8793").replace(/\/+$/, ""),
    gatewayKey: process.env.GATEWAY_KEY ?? "",
    extractModel: process.env.EXTRACT_MODEL || "claude-haiku-5-5",
    fallbackModel: process.env.FALLBACK_MODEL || "claude-haiku-4-5",
    defaultContainer: process.env.RECALL_DEFAULT_CONTAINER || "default",
    adminPasswordHash: process.env.ADMIN_PASSWORD_HASH ?? "",
    sessionSecret: process.env.SESSION_SECRET ?? "",
    // Public base URL: Better Auth's base URL, the trusted origin and the WebAuthn origin.
    url,
    passkeyRpId: process.env.PASSKEY_RP_ID || new URL(url).hostname,
    // Turned off only by tests that call the auth endpoints many times on purpose.
    authRateLimit: process.env.RECALL_AUTH_RATE_LIMIT !== "off",
    jobsEnabled: process.env.RECALL_JOBS !== "off",
    // Gmail connector. All three must be set, or the Connectors page says "not configured".
    googleClientId: process.env.GOOGLE_CLIENT_ID?.trim() ?? "",
    googleClientSecret: process.env.GOOGLE_CLIENT_SECRET?.trim() ?? "",
    connectorSecret: process.env.CONNECTOR_SECRET?.trim() ?? "",
    // Dev only: a fake Google (OAuth and Gmail API from fixtures) so the page can be
    // tried without credentials. Never on in a production build.
    // import.meta.env is missing under tsx (the CLI), which reads as "not dev".
    gmailFake: import.meta.env?.DEV === true && process.env.RECALL_GMAIL_FAKE === "1",
    // Reminder email through Resend. Both are needed; without them reminders
    // show "email not configured" and nothing else changes.
    resendApiKey: process.env.RESEND_API_KEY ?? "",
    // A sender on a domain verified in Resend: "Recall <reminders@zakiego.com>".
    emailFrom: process.env.RECALL_EMAIL_FROM ?? "",
  }
}
