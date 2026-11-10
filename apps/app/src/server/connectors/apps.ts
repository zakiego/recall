// Pull connectors for Zaki's own apps: Oslo (todo) and Finansial (money). Each
// workspace has at most one of each. Recall pulls the app's changes feed
// (changes-feed.ts) every hour and on "Sync now"; the apps never push. The API
// key is sealed like the Gmail tokens. One connector failing never blocks the
// other: each run catches its own error into last_error and keeps its cursor.
import { and, eq, isNotNull } from "drizzle-orm"
import { addDays, isTimezone, todayIn } from "../../lib/dates"
import type { DB } from "../db"
import { appConnectors, jobs, reminderSettings, type AppConnector, type AppProvider } from "../db/schema"
import { newId } from "../ids"
import { enqueue } from "../jobs/queue"
import { createDocument, deleteDocumentAndMemories, findBySourceId } from "../memories"
import { SYSTEM_WORKSPACE, nextHour } from "../reminders"
import { FeedError, pullChanges, type Fetch } from "./changes-feed"
import { MIN_SECRET_LENGTH, SealError, seal, unseal } from "./crypto"
import type { ChangeItem } from "./changes-feed"
import { finansialDocument, finansialEventDate, finansialSettings, transactionSourceId, type FinansialSettings } from "./finansial"
import { connectorSecret } from "./gmail"
import { DEFAULT_TIMEZONE, closedDays, dayEvents, markDayWritten, osloDigest, osloSourceId, stageOsloItems } from "./oslo"

export const APP_DEFAULTS: Record<AppProvider, { name: string; baseUrl: string }> = {
  oslo: { name: "Oslo", baseUrl: "https://todo.zakiego.com" },
  finansial: { name: "Finansial", baseUrl: "https://finansial.zakiego.com" },
}

// The first sync only reaches this far back (Oslo days, Finansial transactions),
// so connecting doesn't send years of history to the model at once. Rules and
// loans always come in: they are current state.
export const BACKFILL_DAYS = 30
export const MAX_API_KEY_LEN = 500

export class AppConnectorError extends Error {
  constructor(
    message: string,
    readonly code: "not_configured" | "invalid" | "busy" | "not_found",
  ) {
    super(message)
    this.name = "AppConnectorError"
  }
}

export const appSecretConfigured = () => connectorSecret().length >= MIN_SECRET_LENGTH

// --- stored connectors ---------------------------------------------------------------

const keyContext = (row: Pick<AppConnector, "id" | "workspaceId" | "provider">) => `app-connector:${row.id}:${row.workspaceId}:${row.provider}`

function openKey(row: AppConnector): string {
  if (!row.apiKey) throw new AppConnectorError("Paste an API key first.", "invalid")
  return unseal(connectorSecret(), row.apiKey, keyContext(row))
}

export function getAppConnector(db: DB, workspaceId: string, provider: AppProvider) {
  return db
    .select()
    .from(appConnectors)
    .where(and(eq(appConnectors.workspaceId, workspaceId), eq(appConnectors.provider, provider)))
    .get()
}

export function normalizeBaseUrl(input: string): string {
  let url: URL
  try {
    url = new URL(input.trim())
  } catch {
    throw new AppConnectorError("Enter the app's address, like https://todo.zakiego.com.", "invalid")
  }
  // The key goes out as a bearer token, so plain http is only for an app on this machine.
  const local = url.hostname === "localhost" || url.hostname === "127.0.0.1"
  if (url.protocol !== "https:" && !(url.protocol === "http:" && local)) throw new AppConnectorError("The address has to start with https://.", "invalid")
  if (url.username || url.password) throw new AppConnectorError("Leave the user name and password out of the address.", "invalid")
  return `${url.origin}${url.pathname.replace(/\/+$/, "")}`
}

export type SaveAppConnector = {
  workspaceId: string
  provider: AppProvider
  userId: string | null
  container: string
  baseUrl: string
  // undefined keeps the stored key; a string replaces it; null removes it.
  apiKey?: string | null
  enabled: boolean
  settings?: Record<string, unknown>
  now?: number
}

// Creates or updates the workspace's connector. A new address starts the feed
// over from the beginning, since a cursor only means something to the app that
// issued it.
export function saveAppConnector(db: DB, input: SaveAppConnector): AppConnector {
  const now = input.now ?? Date.now()
  const baseUrl = normalizeBaseUrl(input.baseUrl)
  if (input.apiKey && input.apiKey.length > MAX_API_KEY_LEN) throw new AppConnectorError("That API key is too long.", "invalid")
  if (input.apiKey && !appSecretConfigured()) throw new AppConnectorError("Set CONNECTOR_SECRET on the server before saving a key.", "not_configured")
  return db.transaction(() => {
    const old = getAppConnector(db, input.workspaceId, input.provider)
    const id = old?.id ?? newId("con")
    const ids = { id, workspaceId: input.workspaceId, provider: input.provider }
    const moved = !!old && old.baseUrl !== baseUrl
    // The stored key was meant for the old host. Sending it to a new one would
    // hand it to whoever runs that host, so a new host needs the key again.
    const newHost = !!old && new URL(old.baseUrl).origin !== new URL(baseUrl).origin
    if (newHost && input.apiKey === undefined && old.apiKey && input.enabled) {
      throw new AppConnectorError("The address changed, so paste the API key again.", "invalid")
    }
    const apiKey =
      input.apiKey === undefined
        ? newHost
          ? null
          : (old?.apiKey ?? null)
        : input.apiKey === null || !input.apiKey.trim()
          ? null
          : seal(connectorSecret(), input.apiKey.trim(), keyContext(ids))
    const settings = { ...(old?.settings ?? {}), ...(input.settings ?? {}) }
    const values = {
      container: input.container,
      baseUrl,
      apiKey,
      enabled: input.enabled,
      settings,
      updatedAt: now,
      ...(moved ? { cursor: null, caughtUpAt: null } : {}),
      // A new key or address clears the old complaint about the old one.
      ...(moved || input.apiKey !== undefined ? { lastError: null } : {}),
    }
    if (old) return db.update(appConnectors).set(values).where(eq(appConnectors.id, old.id)).returning().get()
    return db
      .insert(appConnectors)
      .values({ ...ids, ...values, createdBy: input.userId, createdAt: now })
      .returning()
      .get()
  })
}

export type AppConnectorView = {
  provider: AppProvider
  name: string
  configured: boolean
  container: string | null
  baseUrl: string
  hasKey: boolean
  enabled: boolean
  lastSyncAt: number | null
  lastSuccessAt: number | null
  lastError: string | null
  caughtUp: boolean
  // Finansial only: major units per currency code.
  thresholds: Record<string, number> | null
}

// What the settings page shows. Never the key.
export function appConnectorView(db: DB, workspaceId: string, provider: AppProvider): AppConnectorView {
  const row = getAppConnector(db, workspaceId, provider)
  const defaults = APP_DEFAULTS[provider]
  return {
    provider,
    name: defaults.name,
    configured: !!row,
    container: row?.container ?? null,
    baseUrl: row?.baseUrl ?? defaults.baseUrl,
    hasKey: !!row?.apiKey,
    enabled: row?.enabled ?? false,
    lastSyncAt: row?.lastSyncAt ?? null,
    lastSuccessAt: row?.lastSuccessAt ?? null,
    lastError: row?.lastError ?? null,
    caughtUp: !!row?.caughtUpAt,
    thresholds: provider === "finansial" ? finansialSettings(row?.settings).thresholds : null,
  }
}

// The day boundary for Oslo digests: the connector's own setting, else the
// time zone whoever set it up chose for reminders, else Asia/Makassar.
export function connectorTimezone(db: DB, row: AppConnector): string {
  const own = row.settings?.timezone
  if (typeof own === "string" && isTimezone(own)) return own
  if (row.createdBy) {
    const tz = db.select({ tz: reminderSettings.timezone }).from(reminderSettings).where(eq(reminderSettings.userId, row.createdBy)).get()?.tz
    if (tz && isTimezone(tz)) return tz
  }
  return DEFAULT_TIMEZONE
}

// --- sync ----------------------------------------------------------------------------

export type SyncDeps = { fetch: Fetch; now: () => number; maxPages?: number; pageLimit?: number; timeoutMs?: number }
export const defaultSyncDeps: SyncDeps = { fetch: (...args) => fetch(...args), now: () => Date.now() }

export type SyncResult = {
  provider: AppProvider
  ok: boolean
  pages: number
  items: number
  skipped: number
  staged: number
  documents: number
  // Finansial transactions edited below the threshold, changed or deleted: their old document went.
  removed: number
  caughtUp: boolean
  error: string | null
}

// One sync per connector at a time, across the job loop and "Sync now".
// On globalThis for the same reason as the job queue's listeners.
const g = globalThis as { __recallAppSyncs?: Set<string> }
const running = (g.__recallAppSyncs ??= new Set())

type DocInput = { sourceId: string; title: string; content: string; documentDate: string | null; metadata: Record<string, unknown> }

function writeDoc(db: DB, row: AppConnector, doc: DocInput): boolean {
  const { duplicate } = createDocument(db, {
    workspaceId: row.workspaceId,
    container: row.container,
    sourceType: "text",
    sourceId: doc.sourceId,
    title: doc.title,
    content: doc.content,
    metadata: { ...doc.metadata, ...(doc.documentDate ? { documentDate: doc.documentDate } : {}) },
  })
  return !duplicate
}

// A Finansial transaction keeps one document under fin:txn:<id>, unlike rules
// and loans, which get one per version. So a later version of the item has to
// replace it: a delete, or an edit that takes it under the threshold, removes
// the document and the facts it wrote; an edit that changes what the document
// says replaces it; anything else (a touch that changes nothing in it) costs
// nothing. backfillFrom (first sync only) skips old transactions Recall has
// never seen.
function syncTransaction(
  db: DB,
  row: AppConnector,
  item: ChangeItem,
  settings: FinansialSettings,
  backfillFrom: string | null,
): { removed: boolean; written: boolean } {
  const existing = findBySourceId(db, row.workspaceId, row.container, transactionSourceId(item.id))
  const doc = finansialDocument(item, settings)
  if (!doc) return { removed: !!existing && deleteDocumentAndMemories(db, row.workspaceId, existing.id), written: false }
  let removed = false
  if (existing) {
    if (existing.metadata?.fingerprint === doc.fingerprint) return { removed: false, written: false }
    removed = deleteDocumentAndMemories(db, row.workspaceId, existing.id)
  } else {
    const date = finansialEventDate(item)
    if (backfillFrom && date && date < backfillFrom) return { removed: false, written: false }
  }
  const written = writeDoc(db, row, { ...doc, metadata: { source: "finansial", kind: item.kind, id: item.id, url: item.url, fingerprint: doc.fingerprint } })
  return { removed, written }
}

// Writes the digest of every Oslo day that is over. Only after a sync that
// reached the end of the feed, so a day can't close while its events are
// still unread.
function closeOsloDays(db: DB, row: AppConnector, timezone: string, now: number): number {
  let written = 0
  for (const day of closedDays(db, row, todayIn(timezone, now))) {
    db.transaction(() => {
      const events = dayEvents(db, row, day)
      if (events.length) {
        const { title, content } = osloDigest(day, events, timezone)
        if (writeDoc(db, row, { sourceId: osloSourceId(day), title, content, documentDate: day, metadata: { source: "oslo", kind: "day", day, events: events.length } }))
          written++
      }
      markDayWritten(db, row, day, now)
    })
  }
  return written
}

export async function syncAppConnector(db: DB, workspaceId: string, provider: AppProvider, deps: SyncDeps = defaultSyncDeps): Promise<SyncResult> {
  const row = getAppConnector(db, workspaceId, provider)
  if (!row) throw new AppConnectorError(`Set up ${APP_DEFAULTS[provider].name} first.`, "not_found")
  if (running.has(row.id)) throw new AppConnectorError(`${APP_DEFAULTS[provider].name} is already syncing.`, "busy")
  running.add(row.id)
  const result: SyncResult = { provider, ok: false, pages: 0, items: 0, skipped: 0, staged: 0, documents: 0, removed: 0, caughtUp: false, error: null }
  const startedAt = deps.now()
  try {
    if (!appSecretConfigured()) throw new AppConnectorError("CONNECTOR_SECRET isn't set on the server.", "not_configured")
    const apiKey = openKey(row)
    const timezone = connectorTimezone(db, row)
    const caughtUpBefore = !!row.caughtUpAt
    const backfillFrom = addDays(todayIn(timezone, startedAt), -BACKFILL_DAYS)
    const settings = finansialSettings(row.settings)
    const pulled = await pullChanges(
      deps.fetch,
      { baseUrl: row.baseUrl, apiKey, cursor: row.cursor, maxPages: deps.maxPages, limit: deps.pageLimit, timeoutMs: deps.timeoutMs },
      (page, cursorAfter) => {
        // A page's documents and the cursor after it are stored together.
        db.transaction(() => {
          if (provider === "oslo") {
            result.staged += stageOsloItems(db, row, page.items, { timezone, caughtUp: caughtUpBefore, backfillFrom, now: deps.now() })
          } else {
            for (const item of page.items) {
              if (item.kind === "transaction") {
                const r = syncTransaction(db, row, item, settings, caughtUpBefore ? null : backfillFrom)
                if (r.removed) result.removed++
                if (r.written) result.documents++
                continue
              }
              // Rules and loans: a new version is a new document, so source_id dedupe is enough.
              const doc = finansialDocument(item, settings)
              if (!doc) continue
              if (writeDoc(db, row, { ...doc, metadata: { source: "finansial", kind: item.kind, id: item.id, url: item.url } })) result.documents++
            }
          }
          db.update(appConnectors).set({ cursor: cursorAfter }).where(eq(appConnectors.id, row.id)).run()
        })
      },
    )
    result.pages = pulled.pages
    result.items = pulled.items
    result.skipped = pulled.skipped
    result.caughtUp = pulled.caughtUp
    const now = deps.now()
    if (provider === "oslo" && pulled.caughtUp) result.documents += closeOsloDays(db, row, timezone, now)
    db.update(appConnectors)
      .set({ lastSyncAt: now, lastSuccessAt: now, lastError: null, ...(pulled.caughtUp && !row.caughtUpAt ? { caughtUpAt: now } : {}) })
      .where(eq(appConnectors.id, row.id))
      .run()
    result.ok = true
  } catch (err) {
    result.error = syncErrorMessage(err)
    db.update(appConnectors).set({ lastSyncAt: deps.now(), lastError: result.error }).where(eq(appConnectors.id, row.id)).run()
  } finally {
    running.delete(row.id)
  }
  return result
}

// Short, safe text for last_error: never the key, never item content.
function syncErrorMessage(err: unknown): string {
  if (err instanceof FeedError || err instanceof AppConnectorError) return err.message
  if (err instanceof SealError) return "The stored API key can't be read (CONNECTOR_SECRET changed). Paste the key again."
  console.error("[recall:apps] sync failed:", err)
  return "Sync failed inside Recall. The server log has the details."
}

export type AppSyncRun = { connectors: number; ok: number; failed: number; documents: number; results: SyncResult[] }

// Syncs every enabled connector with a key, one after another. Never throws:
// a failure lands in that connector's last_error and the next one still runs.
export async function runAppSync(db: DB, deps: SyncDeps = defaultSyncDeps, log: (m: string) => void = (m) => console.log(`[recall:apps] ${m}`)): Promise<AppSyncRun> {
  const run: AppSyncRun = { connectors: 0, ok: 0, failed: 0, documents: 0, results: [] }
  const rows = db
    .select({ workspaceId: appConnectors.workspaceId, provider: appConnectors.provider })
    .from(appConnectors)
    .where(and(eq(appConnectors.enabled, true), isNotNull(appConnectors.apiKey)))
    .all()
  for (const r of rows) {
    run.connectors++
    try {
      const res = await syncAppConnector(db, r.workspaceId, r.provider, deps)
      run.results.push(res)
      run.documents += res.documents
      if (res.ok) run.ok++
      else {
        run.failed++
        log(`${r.provider} in ${r.workspaceId}: ${res.error}`)
      }
    } catch (err) {
      // Busy (a "Sync now" is running) or gone since the list was read.
      run.failed++
      log(`${r.provider} in ${r.workspaceId}: ${err instanceof Error ? err.message : String(err)}`)
    }
  }
  if (run.connectors) log(`synced ${run.ok} of ${run.connectors} connector(s), ${run.documents} new document(s)`)
  return run
}

// Makes sure exactly one app_sync job is queued. Called at boot and after each run.
export function scheduleAppSync(db: DB, now = Date.now()) {
  const queued = db
    .select({ id: jobs.id })
    .from(jobs)
    .where(and(eq(jobs.type, "app_sync"), eq(jobs.status, "queued")))
    .get()
  if (queued) return null
  return enqueue(db, SYSTEM_WORKSPACE, "app_sync", {}, nextHour(now))
}
