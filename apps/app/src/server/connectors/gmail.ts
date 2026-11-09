// The Gmail connector: stored connections, search, import and the ingest step
// that reads a message. A connection belongs to one user in one workspace, and
// only that user's requests may use it. Mail content never goes to a log or an
// error message: errors name the step and the HTTP status only.
import { and, desc, eq, inArray, like, sql } from "drizzle-orm"
import { config } from "../config"
import type { DB } from "../db"
import { connectorAccounts, documents, user, type ConnectorAccount, type Document } from "../db/schema"
import { newId } from "../ids"
import { MAX_FILE_BYTES } from "../api"
import { enqueue } from "../jobs/queue"
import { createDocument, findBySourceId } from "../memories"
import { MIN_SECRET_LENGTH, seal, unseal } from "./crypto"
import { decodeEntities, header, parseMessage } from "./gmail-message"
import {
  GMAIL_SCOPE,
  GoogleError,
  getAttachment,
  getMessage,
  getMessageMetadata,
  listMessages,
  refreshAccessToken,
  revokeToken,
  type Fetch,
  type Tokens,
} from "./google"

export const MAX_SEARCH_RESULTS = 20
export const MAX_IMPORT = 50
export const MESSAGE_ID = /^[A-Za-z0-9_-]{6,64}$/
// The fake's secret: only ever used with RECALL_GMAIL_FAKE in a dev build.
const FAKE_SECRET = "recall-dev-only-fake-connector-secret-not-for-production"

export function connectorsConfigured() {
  const c = config()
  if (c.gmailFake) return true
  return !!c.googleClientId && !!c.googleClientSecret && c.connectorSecret.length >= MIN_SECRET_LENGTH
}

// Which env keys are missing, by name only, for the "not configured" state.
export function missingKeys() {
  const c = config()
  if (c.gmailFake) return []
  return [
    !c.googleClientId && "GOOGLE_CLIENT_ID",
    !c.googleClientSecret && "GOOGLE_CLIENT_SECRET",
    c.connectorSecret.length < MIN_SECRET_LENGTH && "CONNECTOR_SECRET",
  ].filter((k): k is string => !!k)
}

export function connectorSecret() {
  const c = config()
  return c.gmailFake && c.connectorSecret.length < MIN_SECRET_LENGTH ? FAKE_SECRET : c.connectorSecret
}

// The fetch that talks to Google: the real one, or the fixture fake in dev.
const g = globalThis as { __recallFakeGoogle?: Fetch }
export async function googleFetch(): Promise<Fetch> {
  if (!config().gmailFake) return fetch
  if (!g.__recallFakeGoogle) {
    const [{ createFakeGoogle }, { MESSAGES, WORST_MESSAGES }] = await Promise.all([import("./fake-google"), import("./fixtures/gmail")])
    // RECALL_GMAIL_FAKE_DATA=worst puts the worst-case fixtures first (break-ui).
    const worst = process.env.RECALL_GMAIL_FAKE_DATA === "worst"
    g.__recallFakeGoogle = createFakeGoogle({ messages: worst ? [...WORST_MESSAGES, ...MESSAGES] : MESSAGES }).fetch
  }
  return g.__recallFakeGoogle
}

export class ConnectorError extends Error {
  constructor(
    message: string,
    readonly code: "not_configured" | "not_connected" | "reauth" | "invalid",
  ) {
    super(message)
    this.name = "ConnectorError"
  }
}

// --- stored connections -----------------------------------------------------------

const tokenContext = (row: Pick<ConnectorAccount, "id" | "workspaceId" | "userId">) => `connector:${row.id}:${row.workspaceId}:${row.userId}`

function sealTokens(row: Pick<ConnectorAccount, "id" | "workspaceId" | "userId">, tokens: Tokens) {
  return seal(connectorSecret(), JSON.stringify(tokens), tokenContext(row))
}

function openTokens(row: ConnectorAccount): Tokens {
  return JSON.parse(unseal(connectorSecret(), row.tokens, tokenContext(row))) as Tokens
}

export function getConnection(db: DB, workspaceId: string, userId: string) {
  return db
    .select()
    .from(connectorAccounts)
    .where(and(eq(connectorAccounts.workspaceId, workspaceId), eq(connectorAccounts.userId, userId), eq(connectorAccounts.provider, "gmail")))
    .get()
}

// Creates or replaces the user's connection. Returns the refresh token it
// replaced, so the caller can revoke it at Google.
export function saveConnection(
  db: DB,
  input: { workspaceId: string; userId: string; email: string | null; scope: string; tokens: Tokens; now?: number },
): { row: ConnectorAccount; replacedRefreshToken: string | null } {
  const now = input.now ?? Date.now()
  return db.transaction(() => {
    const old = getConnection(db, input.workspaceId, input.userId)
    let replaced: string | null = null
    if (old) {
      try {
        const prev = openTokens(old).refreshToken
        if (prev !== input.tokens.refreshToken) replaced = prev
      } catch {
        /* unreadable after a secret change: nothing to revoke */
      }
      db.delete(connectorAccounts).where(eq(connectorAccounts.id, old.id)).run()
    }
    const id = newId("con")
    const ids = { id, workspaceId: input.workspaceId, userId: input.userId }
    const row = db
      .insert(connectorAccounts)
      .values({
        ...ids,
        provider: "gmail",
        accountEmail: input.email,
        scope: input.scope || GMAIL_SCOPE,
        tokens: sealTokens(ids, input.tokens),
        status: "active",
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
    return { row, replacedRefreshToken: replaced }
  })
}

// Who has connected Gmail in the workspace. No tokens, no mail, and the Google
// address only for the caller's own row.
export function listConnections(db: DB, workspaceId: string, viewerId: string) {
  return db
    .select({
      id: connectorAccounts.id,
      userId: connectorAccounts.userId,
      username: user.displayUsername,
      fallbackName: user.name,
      accountEmail: connectorAccounts.accountEmail,
      status: connectorAccounts.status,
      createdAt: connectorAccounts.createdAt,
      lastUsedAt: connectorAccounts.lastUsedAt,
    })
    .from(connectorAccounts)
    .innerJoin(user, eq(user.id, connectorAccounts.userId))
    .where(and(eq(connectorAccounts.workspaceId, workspaceId), eq(connectorAccounts.provider, "gmail")))
    .orderBy(desc(connectorAccounts.createdAt))
    .all()
    .map(({ fallbackName, accountEmail, ...r }) => ({
      ...r,
      username: r.username || fallbackName,
      mine: r.userId === viewerId,
      accountEmail: r.userId === viewerId ? accountEmail : null,
    }))
}

function requireConnection(db: DB, workspaceId: string, userId: string) {
  if (!connectorsConfigured()) throw new ConnectorError("Gmail isn't set up on this server.", "not_configured")
  const row = getConnection(db, workspaceId, userId)
  if (!row) throw new ConnectorError("Connect Gmail first.", "not_connected")
  if (row.status === "reauth") throw new ConnectorError("Gmail access was revoked or expired. Connect again.", "reauth")
  return row
}

// A valid access token for the row, refreshed (and re-sealed) when it is
// about to expire. A refused refresh marks the row for reconnecting.
export async function accessToken(db: DB, row: ConnectorAccount, fetchImpl: Fetch, now = Date.now(), force = false): Promise<string> {
  const tokens = openTokens(row)
  if (!force && tokens.expiresAt - 60_000 > now) return tokens.accessToken
  try {
    const fresh = await refreshAccessToken(fetchImpl, tokens.refreshToken, now)
    db.update(connectorAccounts).set({ tokens: sealTokens(row, fresh), updatedAt: now }).where(eq(connectorAccounts.id, row.id)).run()
    return fresh.accessToken
  } catch (err) {
    if (err instanceof GoogleError && err.code === "reauth") {
      db.update(connectorAccounts).set({ status: "reauth", updatedAt: now }).where(eq(connectorAccounts.id, row.id)).run()
      throw new ConnectorError("Gmail access was revoked or expired. Connect again.", "reauth")
    }
    throw err
  }
}

// Runs a Gmail call with the row's token. A 401 from Gmail gets one forced
// refresh; a second 401 means the grant is gone.
async function withToken<T>(db: DB, row: ConnectorAccount, fetchImpl: Fetch, fn: (token: string) => Promise<T>): Promise<T> {
  const token = await accessToken(db, row, fetchImpl)
  try {
    return await fn(token)
  } catch (err) {
    if (!(err instanceof GoogleError) || err.code !== "reauth") throw err
    const current = db.select().from(connectorAccounts).where(eq(connectorAccounts.id, row.id)).get() ?? row
    return fn(await accessToken(db, current, fetchImpl, Date.now(), true))
  } finally {
    db.update(connectorAccounts).set({ lastUsedAt: Date.now() }).where(eq(connectorAccounts.id, row.id)).run()
  }
}

// Deletes the user's connection here and revokes it at Google. `revoked` is
// false when Google couldn't confirm; the local row is gone either way.
export async function disconnect(db: DB, workspaceId: string, userId: string, fetchImpl: Fetch): Promise<{ removed: boolean; revoked: boolean }> {
  const row = getConnection(db, workspaceId, userId)
  if (!row) return { removed: false, revoked: true }
  let token: string | null = null
  try {
    token = openTokens(row).refreshToken
  } catch {
    /* unreadable: delete anyway */
  }
  db.delete(connectorAccounts).where(eq(connectorAccounts.id, row.id)).run()
  const revoked = token ? await revokeToken(fetchImpl, token) : false
  return { removed: true, revoked }
}

// --- search ----------------------------------------------------------------------------

// imported: already a document in this container. importFailed: that document
// failed, and importing again retries it.
export type GmailResult = {
  id: string
  threadId: string | null
  from: string
  subject: string
  snippet: string
  date: number | null
  imported: boolean
  importFailed: boolean
}

export async function searchGmail(
  db: DB,
  input: { workspaceId: string; userId: string; container: string; q: string; pageToken?: string },
  fetchImpl: Fetch,
): Promise<{ messages: GmailResult[]; nextPageToken: string | null; estimate: number }> {
  const row = requireConnection(db, input.workspaceId, input.userId)
  return withToken(db, row, fetchImpl, async (token) => {
    const list = await listMessages(fetchImpl, token, { q: input.q, pageToken: input.pageToken, maxResults: MAX_SEARCH_RESULTS })
    const ids = (list.messages ?? []).map((m) => m.id)
    const metas = await mapLimit(ids, 8, (id) => getMessageMetadata(fetchImpl, token, id))
    const imported = importedIds(db, input.workspaceId, input.container, ids)
    return {
      messages: metas.map((m) => ({
        id: m.id,
        threadId: m.threadId ?? null,
        from: header(m.payload, "From") ?? "",
        subject: header(m.payload, "Subject") ?? "",
        snippet: decodeEntities(m.snippet ?? ""),
        date: m.internalDate ? Number(m.internalDate) : null,
        imported: imported.has(m.id),
        importFailed: imported.get(m.id) === "failed",
      })),
      nextPageToken: list.nextPageToken ?? null,
      estimate: list.resultSizeEstimate ?? ids.length,
    }
  })
}

export const emailSourceId = (messageId: string) => `gmail:${messageId}`
// Gmail's attachmentId is long (often 300+ characters) and changes between
// fetches of the same message, so the stable part id identifies an attachment.
export const attachmentSourceId = (messageId: string, partId: string) => `gmail:${messageId}:${partId}`

function importedIds(db: DB, workspaceId: string, container: string, ids: string[]) {
  if (!ids.length) return new Map<string, Document["status"]>()
  const rows = db
    .select({ sourceId: documents.sourceId, status: documents.status })
    .from(documents)
    .where(
      and(
        eq(documents.workspaceId, workspaceId),
        eq(documents.container, container),
        // Only real imports count; a text add that borrowed a gmail: source_id doesn't.
        eq(documents.sourceType, "email"),
        inArray(documents.sourceId, ids.map(emailSourceId)),
      ),
    )
    .all()
  return new Map(rows.map((r) => [r.sourceId!.slice("gmail:".length), r.status]))
}

async function mapLimit<T, R>(items: T[], limit: number, fn: (item: T) => Promise<R>): Promise<R[]> {
  const out: R[] = new Array(items.length)
  let next = 0
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, async () => {
      while (next < items.length) {
        const i = next++
        out[i] = await fn(items[i]!)
      }
    }),
  )
  return out
}

// --- import ----------------------------------------------------------------------------

export type ImportResult = { queued: number; duplicates: number; documents: { id: string; messageId: string; duplicate: boolean }[] }

// Queues one email document per message. Nothing is fetched here: the ingest
// job reads the message (see resolveEmail), so the request returns at once.
// A message already imported into the container is a no-op, unless that
// import failed: then it is queued again, with the current connection.
export function importGmail(
  db: DB,
  input: { workspaceId: string; userId: string; container: string; messages: { id: string; subject?: string | null }[] },
): ImportResult {
  const row = requireConnection(db, input.workspaceId, input.userId)
  if (input.messages.length > MAX_IMPORT) throw new ConnectorError(`Import up to ${MAX_IMPORT} messages at a time.`, "invalid")
  const result: ImportResult = { queued: 0, duplicates: 0, documents: [] }
  for (const m of input.messages) {
    if (!MESSAGE_ID.test(m.id)) throw new ConnectorError("That isn't a Gmail message id.", "invalid")
    const { doc, duplicate } = createDocument(db, {
      workspaceId: input.workspaceId,
      container: input.container,
      sourceType: "email",
      sourceId: emailSourceId(m.id),
      // A placeholder until ingest reads the real subject.
      title: m.subject?.slice(0, 300) || null,
      metadata: { connector: "gmail", messageId: m.id, connectionId: row.id, userId: input.userId, account: row.accountEmail },
    })
    if (duplicate && doc.status === "failed" && doc.sourceType === "email") {
      db.update(documents)
        .set({ status: "queued", error: null, metadata: { ...doc.metadata, connectionId: row.id, userId: input.userId, account: row.accountEmail } })
        .where(eq(documents.id, doc.id))
        .run()
      enqueue(db, input.workspaceId, "ingest", { documentId: doc.id })
      result.documents.push({ id: doc.id, messageId: m.id, duplicate: false })
      result.queued += 1
      continue
    }
    result.documents.push({ id: doc.id, messageId: m.id, duplicate })
    if (duplicate) result.duplicates += 1
    else result.queued += 1
  }
  return result
}

// The ingest step for an email document: reads the message with the stored
// connection, returns its text, and queues each PDF attachment as its own file
// document. Runs inside the job loop.
export async function resolveEmail(db: DB, doc: Document, fetchImpl: Fetch): Promise<{ title: string | null; text: string; metadata: Record<string, unknown> }> {
  const meta = (doc.metadata ?? {}) as { messageId?: string; connectionId?: string; userId?: string; account?: string | null }
  if (!meta.messageId || !meta.connectionId) throw new Error("email document has no Gmail message reference")
  // A reconnect replaces the row, so fall back to the same user's current
  // connection in this workspace, and only while it's the same Google account.
  // Never anyone else's, and never another mailbox of theirs.
  const current = meta.userId ? getConnection(db, doc.workspaceId, meta.userId) : undefined
  const row =
    db.select().from(connectorAccounts).where(eq(connectorAccounts.id, meta.connectionId)).get() ??
    (current && current.accountEmail && current.accountEmail === meta.account ? current : undefined)
  // The connection must still exist and belong to this document's workspace.
  if (!row || row.workspaceId !== doc.workspaceId) throw new Error("the Gmail connection used for this import was removed")
  if (row.status === "reauth") throw new Error("Gmail access was revoked or expired. Connect again, then retry")
  if (!connectorsConfigured()) throw new Error("Gmail isn't set up on this server")
  const messageId = meta.messageId
  return withToken(db, row, fetchImpl, async (token) => {
    const parsed = parseMessage(await getMessage(fetchImpl, token, messageId))
    let attachments = 0
    let skipped = 0
    for (const pdf of parsed.pdfs) {
      if (pdf.size > MAX_FILE_BYTES) {
        skipped += 1
        continue
      }
      const sourceId = attachmentSourceId(messageId, pdf.partId)
      if (findBySourceId(db, doc.workspaceId, doc.container, sourceId)) {
        attachments += 1
        continue
      }
      const bytes = pdf.attachmentId ? await getAttachment(fetchImpl, token, messageId, pdf.attachmentId) : Buffer.from(pdf.inlineData ?? "", "base64url")
      if (bytes.byteLength > MAX_FILE_BYTES) {
        skipped += 1
        continue
      }
      createDocument(db, {
        workspaceId: doc.workspaceId,
        container: doc.container,
        sourceType: "file",
        sourceId,
        title: pdf.filename,
        metadata: { connector: "gmail", messageId, emailDocumentId: doc.id },
        file: { name: pdf.filename, mime: "application/pdf", bytes },
      })
      attachments += 1
    }
    return {
      title: parsed.subject || "(no subject)",
      text: parsed.text,
      metadata: { attachments, ...(skipped ? { attachmentsSkipped: skipped } : {}) },
    }
  })
}

// Retrying an email whose text isn't stored yet reads Gmail again with the
// importer's grant, so only the importer may do it. Anything else (an email
// that failed after its text was stored, an attachment) re-ingests locally.
export function mayRetry(doc: Pick<Document, "sourceType" | "content" | "metadata">, userId: string) {
  if (doc.sourceType !== "email" || doc.content?.trim()) return true
  return (doc.metadata as { userId?: string } | null)?.userId === userId
}

// Recent Gmail imports (emails and their attachments) in a container, newest first.
export function listGmailImports(db: DB, workspaceId: string, container: string, viewerId: string, limit = 30) {
  return db
    .select({
      id: documents.id,
      sourceType: documents.sourceType,
      sourceId: documents.sourceId,
      title: documents.title,
      status: documents.status,
      error: documents.error,
      createdAt: documents.createdAt,
      content: sql<string | null>`substr(${documents.content}, 1, 1)`,
      metadata: documents.metadata,
    })
    .from(documents)
    .where(
      and(
        eq(documents.workspaceId, workspaceId),
        eq(documents.container, container),
        inArray(documents.sourceType, ["email", "file"]),
        like(documents.sourceId, "gmail:%"),
      ),
    )
    .orderBy(desc(documents.createdAt))
    .limit(limit)
    .all()
    .map(({ content, metadata, ...d }) => ({ ...d, canRetry: mayRetry({ sourceType: d.sourceType, content, metadata }, viewerId) }))
}
