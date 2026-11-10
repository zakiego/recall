// Server functions behind the Connectors page. Every call acts as the signed-in
// user in their active workspace, and only ever touches that user's own
// connection. Admins get to see who has connected, never anyone's mail.
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import {
  ConnectorError,
  connectorsConfigured,
  disconnect,
  getConnection,
  googleFetch,
  importGmail,
  listConnections,
  listGmailImports,
  MAX_IMPORT,
  MESSAGE_ID,
  missingKeys,
  searchGmail,
} from "@/server/connectors/gmail"
import { GoogleError } from "@/server/connectors/google"
import {
  AppConnectorError,
  appConnectorView,
  appSecretConfigured,
  getAppConnector,
  MAX_API_KEY_LEN,
  saveAppConnector,
  syncAppConnector,
} from "@/server/connectors/apps"
import { APP_PROVIDERS } from "@/server/db/schema"
import { MAX_CONTAINER_LEN } from "@/server/memories"
import { requireMember, requireSameOrigin, roleAtLeast } from "@/server/session"

const container = z.string().trim().min(1).max(MAX_CONTAINER_LEN)

// Errors the page can show as they are: they never carry mail content.
function friendly<T>(fn: () => Promise<T>): Promise<T> {
  return fn().catch((err: unknown) => {
    if (err instanceof ConnectorError) throw new Error(err.message)
    if (err instanceof GoogleError) {
      if (err.code === "rate_limited") throw new Error("Gmail is rate limiting this account. Wait a minute and try again.")
      if (err.code === "not_found") throw new Error("Gmail couldn't find that. It may have been deleted.")
      throw new Error(`Gmail didn't answer (HTTP ${err.status}). Try again in a moment.`)
    }
    throw err
  })
}

export const getConnectors = createServerFn({ method: "GET" })
  .validator(z.object({ container }))
  .handler(async ({ data }) => {
    const { db, ws, role, viewer } = await requireMember()
    const configured = connectorsConfigured()
    const canManage = roleAtLeast(role, "admin")
    const mine = getConnection(db, ws, viewer.user.id)
    return {
      configured,
      // Env key names only, for admins, so they know what to set.
      missing: canManage && !configured ? missingKeys() : [],
      canManage,
      gmail: mine
        ? { email: mine.accountEmail, status: mine.status, connectedAt: mine.createdAt, lastUsedAt: mine.lastUsedAt }
        : null,
      // Everyone sees their own row; admins see every row, without addresses.
      connections: canManage ? listConnections(db, ws, viewer.user.id) : [],
      imports: listGmailImports(db, ws, data.container, viewer.user.id),
      maxImport: MAX_IMPORT,
      // Pull connectors for Oslo and Finansial. Never the key.
      apps: APP_PROVIDERS.map((p) => appConnectorView(db, ws, p)),
      appSecretConfigured: appSecretConfigured(),
    }
  })

export const searchGmailMessages = createServerFn({ method: "POST" })
  .validator(z.object({ container, q: z.string().trim().max(500), pageToken: z.string().max(200).optional() }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, viewer } = await requireMember()
    return friendly(async () => searchGmail(db, { workspaceId: ws, userId: viewer.user.id, ...data }, await googleFetch()))
  })

export const importGmailMessages = createServerFn({ method: "POST" })
  .validator(
    z.object({
      container,
      messages: z
        .array(z.object({ id: z.string().regex(MESSAGE_ID), subject: z.string().max(300).nullish() }))
        .min(1)
        .max(MAX_IMPORT),
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, viewer } = await requireMember()
    return friendly(async () => {
      const r = importGmail(db, { workspaceId: ws, userId: viewer.user.id, container: data.container, messages: data.messages })
      return { queued: r.queued, duplicates: r.duplicates }
    })
  })

export const disconnectGmail = createServerFn({ method: "POST" }).handler(async () => {
  requireSameOrigin()
  const { db, ws, viewer } = await requireMember()
  return disconnect(db, ws, viewer.user.id, await googleFetch())
})

// --- app connectors (Oslo, Finansial) -----------------------------------------
// Workspace-wide, so owners and admins set them up. Members see the status.

const provider = z.enum(APP_PROVIDERS)

function appFriendly<T>(fn: () => Promise<T>): Promise<T> {
  return fn().catch((err: unknown) => {
    if (err instanceof AppConnectorError) throw new Error(err.message)
    throw err
  })
}

export const saveAppConnectorFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      provider,
      container,
      baseUrl: z.string().trim().min(1).max(500),
      // Write-only: omitted keeps the stored key, "" keeps it too, null removes it.
      apiKey: z.string().max(MAX_API_KEY_LEN).nullish(),
      enabled: z.boolean(),
      // Finansial: major units per currency code.
      thresholds: z.record(z.string().regex(/^[A-Z]{3}$/), z.number().min(0).max(1e15)).optional(),
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, viewer } = await requireMember("admin")
    return appFriendly(async () => {
      const apiKey = data.apiKey === null ? null : data.apiKey?.trim() ? data.apiKey.trim() : undefined
      const existing = getAppConnector(db, ws, data.provider)
      if (data.enabled && !apiKey && !existing?.apiKey) throw new AppConnectorError("Paste an API key to turn syncing on.", "invalid")
      saveAppConnector(db, {
        workspaceId: ws,
        provider: data.provider,
        userId: viewer.user.id,
        container: data.container,
        baseUrl: data.baseUrl,
        apiKey,
        enabled: data.enabled,
        settings: data.provider === "finansial" && data.thresholds ? { thresholds: data.thresholds } : undefined,
      })
      return appConnectorView(db, ws, data.provider)
    })
  })

export const syncAppConnectorFn = createServerFn({ method: "POST" })
  .validator(z.object({ provider }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember("admin")
    return appFriendly(async () => {
      const r = await syncAppConnector(db, ws, data.provider)
      return { ok: r.ok, items: r.items, documents: r.documents, staged: r.staged, caughtUp: r.caughtUp, error: r.error }
    })
  })
