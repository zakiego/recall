import fs from "node:fs"
import path from "node:path"
import { and, asc, count, desc, eq, gte, inArray, isNotNull, isNull, lt, lte, or, sql } from "drizzle-orm"
import { dated, type Dated } from "../lib/dates"
import { config } from "./config"
import type { DB } from "./db"
import { chunks, documents, memories, profiles, type Document, type Memory } from "./db/schema"
import { embedOne } from "./embed"
import { newId } from "./ids"
import { enqueue } from "./jobs/queue"
import { indexMemory, unindexChunks, unindexMemory } from "./search"
import { maskSecrets } from "./sensitive"

export const MAX_CONTAINER_LEN = 128

export function memoryText(m: { fact: string; keywords: string }) {
  return m.keywords ? `${m.fact}. ${m.keywords}` : m.fact
}

// --- documents --------------------------------------------------------------

export type NewDocument = {
  workspaceId: string
  container: string
  sourceType: Document["sourceType"]
  content?: string
  url?: string
  title?: string | null
  metadata?: Record<string, unknown>
  file?: { name: string; mime: string; bytes: Uint8Array }
  sourceId?: string
}

export function uploadsDir() {
  return path.join(config().dataDir, "uploads")
}

// Creates the document and queues its ingest. With a sourceId that already
// exists in the container, returns that document instead and queues nothing.
// The lookup and insert run in one sync transaction, so two requests for the
// same sourceId can't both insert.
export function createDocument(db: DB, input: NewDocument): { doc: Document; duplicate: boolean } {
  const id = newId("doc")
  return db.transaction(() => {
    if (input.sourceId) {
      const existing = findBySourceId(db, input.workspaceId, input.container, input.sourceId)
      if (existing) return { doc: existing, duplicate: true }
    }
    const metadata = { ...input.metadata }
    if (input.file) Object.assign(metadata, { filename: input.file.name, mime: input.file.mime, size: input.file.bytes.byteLength })
    const doc = db
      .insert(documents)
      .values({
        id,
        workspaceId: input.workspaceId,
        container: input.container,
        sourceType: input.sourceType,
        sourceId: input.sourceId ?? null,
        // Titles are often the content's first line, so a key there must not survive.
        title: maskTitle(input.title ?? input.file?.name ?? null),
        url: input.url ?? null,
        content: input.content ?? null,
        metadata,
      })
      .returning()
      .get()
    if (input.file) {
      fs.mkdirSync(uploadsDir(), { recursive: true })
      fs.writeFileSync(path.join(uploadsDir(), id), input.file.bytes)
    }
    enqueue(db, input.workspaceId, "ingest", { documentId: id })
    return { doc, duplicate: false }
  })
}

export function maskTitle(title: string | null | undefined) {
  return title ? maskSecrets(title).text : null
}

export function findBySourceId(db: DB, workspaceId: string, container: string, sourceId: string) {
  return db
    .select()
    .from(documents)
    .where(and(eq(documents.workspaceId, workspaceId), eq(documents.container, container), eq(documents.sourceId, sourceId)))
    .get()
}

export function getDocument(db: DB, workspaceId: string, id: string) {
  return db
    .select()
    .from(documents)
    .where(and(eq(documents.id, id), eq(documents.workspaceId, workspaceId)))
    .get()
}

// Newest first, a page at a time. The cursor is the last row's (createdAt, id),
// the same keyset listMemories uses, so a page never repeats or skips a row
// when documents arrive in between.
export function listDocuments(db: DB, workspaceId: string, container: string, opts: { cursor?: string | null; limit?: number } = {}) {
  const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200)
  const cur = decodeCursor(opts.cursor)
  const rows = db
    .select({
      id: documents.id,
      container: documents.container,
      sourceType: documents.sourceType,
      title: documents.title,
      url: documents.url,
      status: documents.status,
      error: documents.error,
      metadata: documents.metadata,
      createdAt: documents.createdAt,
      preview: sql<string>`substr(coalesce(${documents.content}, ''), 1, 280)`,
      memoryCount: sql<number>`(SELECT count(*) FROM memories m WHERE m.source_document_id = "documents"."id" AND m.valid_to IS NULL)`,
    })
    .from(documents)
    .where(
      and(
        eq(documents.workspaceId, workspaceId),
        eq(documents.container, container),
        cur
          ? or(lt(documents.createdAt, cur.createdAt), and(eq(documents.createdAt, cur.createdAt), lt(documents.id, cur.id)))
          : undefined,
      ),
    )
    .orderBy(desc(documents.createdAt), desc(documents.id))
    .limit(limit + 1)
    .all()
  const page = rows.slice(0, limit)
  return { documents: page, nextCursor: rows.length > limit ? encodeCursor(page.at(-1)!) : null }
}

export function countDocuments(db: DB, workspaceId: string, container: string) {
  const row = db
    .select({ n: count() })
    .from(documents)
    .where(and(eq(documents.workspaceId, workspaceId), eq(documents.container, container)))
    .get()
  return row?.n ?? 0
}

// Deletes the document and its chunks. Memories it produced stay, unlinked.
export function deleteDocument(db: DB, workspaceId: string, id: string): boolean {
  if (!getDocument(db, workspaceId, id)) return false
  const ids = db.select({ id: chunks.id }).from(chunks).where(eq(chunks.documentId, id)).all().map((c) => c.id)
  db.transaction(() => {
    unindexChunks(db.$client, ids)
    db.delete(documents).where(eq(documents.id, id)).run()
  })
  fs.rmSync(path.join(uploadsDir(), id), { force: true })
  return true
}

// For a source that took its content back (an edited or deleted record): forgets
// the current memories the document wrote, the same way forgetting each one in
// the dashboard does, then deletes the document through deleteDocument. Older
// facts those memories superseded stay closed, and a later fact that already
// superseded one of them (from another document) stays current.
export function deleteDocumentAndMemories(db: DB, workspaceId: string, id: string): boolean {
  if (!getDocument(db, workspaceId, id)) return false
  const current = db
    .select({ id: memories.id })
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), eq(memories.sourceDocumentId, id), isNull(memories.validTo)))
    .all()
  db.transaction(() => {
    for (const m of current) forgetMemory(db, workspaceId, m.id)
    deleteDocument(db, workspaceId, id)
  })
  return true
}

// --- memories ---------------------------------------------------------------

export type MemoryView = Pick<
  Memory,
    | "id"
  | "workspaceId"
  | "container"
  | "fact"
  | "keywords"
  | "kind"
  | "sourceDocumentId"
  | "supersedesId"
  | "validFrom"
  | "validTo"
  | "createdAt"
  | "reviewNote"
  | "eventDate"
  | "eventPrecision"
  | "dueDate"
  | "duePrecision"
>

function encodeCursor(m: { createdAt: number; id: string }) {
  return Buffer.from(`${m.createdAt}:${m.id}`).toString("base64url")
}

function decodeCursor(c: string | null | undefined) {
  if (!c) return null
  const [at, id] = Buffer.from(c, "base64url").toString().split(":")
  const n = Number(at)
  return Number.isFinite(n) && id ? { createdAt: n, id } : null
}

export function listMemories(
  db: DB,
  opts: { workspaceId: string; container: string; cursor?: string | null; history?: boolean; limit?: number; kind?: "static" | "dynamic"; review?: boolean },
) {
  const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200)
  const cur = decodeCursor(opts.cursor)
  const rows = db
    .select()
    .from(memories)
    .where(
      and(
        eq(memories.workspaceId, opts.workspaceId),
        eq(memories.container, opts.container),
        // Only current facts can need review, so `review` ignores `history`.
        opts.history && !opts.review ? undefined : isNull(memories.validTo),
        opts.review ? isNotNull(memories.reviewNote) : undefined,
        opts.kind ? eq(memories.kind, opts.kind) : undefined,
        cur
          ? or(lt(memories.createdAt, cur.createdAt), and(eq(memories.createdAt, cur.createdAt), lt(memories.id, cur.id)))
          : undefined,
      ),
    )
    .orderBy(desc(memories.createdAt), desc(memories.id))
    .limit(limit + 1)
    .all()
  const page = rows.slice(0, limit)
  return { memories: page, nextCursor: rows.length > limit ? encodeCursor(page.at(-1)!) : null }
}

// Current facts flagged for review in a container.
export function reviewCount(db: DB, workspaceId: string, container: string) {
  const row = db
    .select({ n: count() })
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), eq(memories.container, container), isNull(memories.validTo), isNotNull(memories.reviewNote)))
    .get()
  return row?.n ?? 0
}

export function getMemory(db: DB, workspaceId: string, id: string) {
  return db
    .select()
    .from(memories)
    .where(and(eq(memories.id, id), eq(memories.workspaceId, workspaceId)))
    .get()
}

// A chain is a linked list through supersedes_id, so this caps the walk and
// the recursive counts at something no real fact reaches.
const MAX_CHAIN = 100_000

// The version chain a memory belongs to, oldest first. `limit` caps how far it
// walks each way; versionCounts() on the first row says how many came before it.
export function memoryChain(db: DB, workspaceId: string, id: string, limit = 500): Memory[] {
  const start = getMemory(db, workspaceId, id)
  if (!start) return []
  const chain = [start]
  let back = start
  for (let i = 0; i < limit && back.supersedesId; i++) {
    const prev = getMemory(db, workspaceId, back.supersedesId)
    if (!prev) break
    chain.unshift(prev)
    back = prev
  }
  let fwd = start
  for (let i = 0; i < limit; i++) {
    const next = db
      .select()
      .from(memories)
      .where(and(eq(memories.supersedesId, fwd.id), eq(memories.workspaceId, workspaceId)))
      .get()
    if (!next) break
    chain.push(next)
    fwd = next
  }
  return chain
}

// Whether a newer version replaced `id`. A chain cut off by the walk limit
// ends on a closed row that isn't deleted, and this tells the two apart.
export function hasNewerVersion(db: DB, workspaceId: string, id: string) {
  return !!db
    .select({ id: memories.id })
    .from(memories)
    .where(and(eq(memories.supersedesId, id), eq(memories.workspaceId, workspaceId)))
    .get()
}

// How many older versions each memory has, for the "history" badge in lists.
export function versionCounts(db: DB, workspaceId: string, ids: string[]): Record<string, number> {
  const stmt = db.$client.prepare(
    `WITH RECURSIVE chain(id, prev, n) AS (
       SELECT id, supersedes_id, 0 FROM memories WHERE id = ? AND workspace_id = ?
       UNION ALL SELECT m.id, m.supersedes_id, chain.n + 1 FROM memories m JOIN chain ON m.id = chain.prev
       WHERE chain.n < ${MAX_CHAIN} AND m.workspace_id = ?
     ) SELECT max(n) AS n FROM chain`,
  )
  const out: Record<string, number> = {}
  for (const id of ids) {
    const row = stmt.get(id, workspaceId, workspaceId) as { n: number | null }
    out[id] = row.n ?? 0
  }
  return out
}

type NewFact = { fact: string; keywords: string; kind: "static" | "dynamic" }

// Dates on a write. undefined keeps what is there (on a new version, the old
// version's date); null clears it.
export type DateFields = { event?: Dated | null; due?: Dated | null }

function dateColumns(d: DateFields) {
  return {
    eventDate: d.event?.date ?? null,
    eventPrecision: d.event?.precision ?? null,
    dueDate: d.due?.date ?? null,
    duePrecision: d.due?.precision ?? null,
  }
}

export const eventOf = (m: Pick<Memory, "eventDate" | "eventPrecision">) => dated(m.eventDate, m.eventPrecision)
export const dueOf = (m: Pick<Memory, "dueDate" | "duePrecision">) => dated(m.dueDate, m.duePrecision)
const sameDate = (a: Dated | null, b: Dated | null) => a?.date === b?.date && a?.precision === b?.precision

// Writes rows and index entries. Vectors are computed by the caller first,
// because embedding is async and this runs inside a sync transaction.
export function insertMemory(
  db: DB,
  input: NewFact & DateFields & {
    workspaceId: string
    container: string
    sourceDocumentId?: string | null
    supersedesId?: string | null
    reviewNote?: string | null
    conflictsWithId?: string | null
  },
  vector: Float32Array,
): Memory {
  const row = db
    .insert(memories)
    .values({
      id: newId("mem"),
      workspaceId: input.workspaceId,
      container: input.container,
      fact: input.fact,
      keywords: input.keywords,
      kind: input.kind,
      sourceDocumentId: input.sourceDocumentId ?? null,
      supersedesId: input.supersedesId ?? null,
      reviewNote: input.reviewNote ?? null,
      conflictsWithId: input.conflictsWithId ?? null,
      ...dateColumns(input),
    })
    .returning()
    .get()
  indexMemory(db.$client, row, vector)
  return row
}

export function closeMemory(db: DB, id: string, at = Date.now()) {
  const changed = db
    .update(memories)
    .set({ validTo: at })
    .where(and(eq(memories.id, id), isNull(memories.validTo)))
    .run().changes
  unindexMemory(db.$client, id)
  return changed > 0
}

// Returns null when `old` is no longer current (forgotten or superseded since
// the caller read it). Nothing is written then, so a stale read can't bring a
// forgotten fact back or give one memory two current successors.
export function supersedeMemory(
  db: DB,
  old: Memory,
  next: Omit<NewFact, "kind"> & DateFields & { kind?: NewFact["kind"]; sourceDocumentId?: string | null },
  vector: Float32Array,
): Memory | null {
  return db.transaction(() => {
    const now = Date.now()
    if (!closeMemory(db, old.id, now)) return null
    const row = insertMemory(
      db,
      {
        workspaceId: old.workspaceId,
        container: old.container,
        fact: next.fact,
        keywords: next.keywords,
        kind: next.kind ?? old.kind,
        sourceDocumentId: next.sourceDocumentId ?? old.sourceDocumentId,
        supersedesId: old.id,
        event: next.event === undefined ? eventOf(old) : next.event,
        due: next.due === undefined ? dueOf(old) : next.due,
      },
      vector,
    )
    // The new version mentions the same people as the one it replaces.
    db.$client
      .prepare("INSERT OR IGNORE INTO person_memories (person_id, memory_id, workspace_id) SELECT person_id, ?, workspace_id FROM person_memories WHERE memory_id = ? AND workspace_id = ?")
      .run(row.id, old.id, old.workspaceId)
    return row
  })
}

// Sets or clears the review note on a current memory. Not a new version.
// `conflictsWithId` names the other side when the note is set.
export function setReviewNote(db: DB, workspaceId: string, id: string, note: string | null, conflictsWithId: string | null = null) {
  return db
    .update(memories)
    .set({ reviewNote: note, conflictsWithId: note === null ? null : conflictsWithId })
    .where(and(eq(memories.id, id), eq(memories.workspaceId, workspaceId), isNull(memories.validTo)))
    .returning()
    .get()
}

// A person settled the conflict `id` is part of: clear its note, and the note
// on every current fact whose conflict points at it. Returns the row, or
// undefined when `id` isn't current.
export function resolveConflict(db: DB, workspaceId: string, id: string) {
  return db.transaction(() => {
    const row = setReviewNote(db, workspaceId, id, null)
    clearConflictsWith(db, workspaceId, id)
    return row
  })
}

// Settles the pair on the other side: current facts whose conflict is with
// `id`. Called when `id` is resolved, edited or deleted. A fact can be in
// more than one conflict (its link points at the newest), so one that another
// open conflict still points at stays flagged and is relinked to it.
function clearConflictsWith(db: DB, workspaceId: string, id: string) {
  const current = (cond: ReturnType<typeof eq>) => and(eq(memories.workspaceId, workspaceId), isNull(memories.validTo), cond)
  const others = db.select({ id: memories.id }).from(memories).where(current(eq(memories.conflictsWithId, id))).all()
  for (const other of others) {
    const open = db
      .select({ id: memories.id })
      .from(memories)
      .where(and(current(eq(memories.conflictsWithId, other.id)), sql`${memories.id} != ${id}`))
      .get()
    db.update(memories)
      .set(open ? { conflictsWithId: open.id } : { reviewNote: null, conflictsWithId: null })
      .where(eq(memories.id, other.id))
      .run()
  }
}

// A manual edit is a new version, so the history keeps what it replaced. The
// new version starts without a review note: editing a flagged fact resolves it,
// on both sides.
export async function editMemory(
  db: DB,
  workspaceId: string,
  id: string,
  patch: { fact?: string; keywords?: string; kind?: "static" | "dynamic" } & DateFields,
) {
  const old = getMemory(db, workspaceId, id)
  if (!old || old.validTo !== null) return null
  const next = {
    fact: patch.fact?.trim() || old.fact,
    keywords: patch.keywords?.trim() ?? old.keywords,
    kind: patch.kind ?? old.kind,
    event: patch.event === undefined ? eventOf(old) : patch.event,
    due: patch.due === undefined ? dueOf(old) : patch.due,
  }
  if (
    next.fact === old.fact &&
    next.keywords === old.keywords &&
    next.kind === old.kind &&
    sameDate(next.event, eventOf(old)) &&
    sameDate(next.due, dueOf(old))
  ) {
    return old.reviewNote === null ? old : (resolveConflict(db, workspaceId, old.id) ?? old)
  }
  const vector = await embedOne(memoryText(next), "passage")
  // null when the fact changed while the vector was computed.
  const row = db.transaction(() => {
    const written = supersedeMemory(db, old, next, vector)
    if (written) clearConflictsWith(db, workspaceId, old.id)
    return written
  })
  if (row) enqueue(db, workspaceId, "profile", { container: old.container })
  return row
}

export async function addMemoryDirect(db: DB, input: NewFact & DateFields & { workspaceId: string; container: string }) {
  const vector = await embedOne(memoryText(input), "passage")
  const row = db.transaction(() => insertMemory(db, input, vector))
  enqueue(db, input.workspaceId, "profile", { container: input.container })
  return row
}

export function forgetMemory(db: DB, workspaceId: string, id: string) {
  const m = getMemory(db, workspaceId, id)
  if (!m) return null
  // A deleted fact can't conflict with anything, so the other side is settled too.
  const closed = db.transaction(() => {
    const done = closeMemory(db, id)
    if (done) clearConflictsWith(db, workspaceId, id)
    return done
  })
  if (closed) enqueue(db, workspaceId, "profile", { container: m.container })
  return getMemory(db, workspaceId, id)!
}

// Current facts due between two calendar dates (inclusive), soonest first.
// Without a container, every container in the workspace.
type UpcomingRange = { workspaceId: string; container?: string; from: string; to: string }

function upcomingWhere(opts: UpcomingRange) {
  return and(
    eq(memories.workspaceId, opts.workspaceId),
    opts.container ? eq(memories.container, opts.container) : undefined,
    isNull(memories.validTo),
    isNotNull(memories.dueDate),
    gte(memories.dueDate, opts.from),
    lte(memories.dueDate, opts.to),
  )
}

export function listUpcoming(db: DB, opts: UpcomingRange & { limit?: number }) {
  return db
    .select()
    .from(memories)
    .where(upcomingWhere(opts))
    .orderBy(asc(memories.dueDate), asc(memories.createdAt))
    .limit(Math.min(Math.max(opts.limit ?? 200, 1), 500))
    .all()
}

// How many facts listUpcoming would return without its limit.
export function countUpcoming(db: DB, opts: UpcomingRange) {
  return db.select({ n: count() }).from(memories).where(upcomingWhere(opts)).get()?.n ?? 0
}

// --- containers and profile -------------------------------------------------

export function listContainers(db: DB, workspaceId: string) {
  return db.$client
    .prepare(
      `SELECT container,
              sum(CASE WHEN kind = 'mem' THEN 1 ELSE 0 END) AS memories,
              sum(CASE WHEN kind = 'doc' THEN 1 ELSE 0 END) AS documents,
              max(at) AS lastActivity
       FROM (
         SELECT container, 'mem' AS kind, created_at AS at FROM memories WHERE workspace_id = ? AND valid_to IS NULL
         UNION ALL SELECT container, 'doc', created_at FROM documents WHERE workspace_id = ?
       ) GROUP BY container ORDER BY lastActivity DESC`,
    )
    .all(workspaceId, workspaceId) as { container: string; memories: number; documents: number; lastActivity: number }[]
}

export function getProfile(db: DB, workspaceId: string, container: string) {
  const profile = db
    .select()
    .from(profiles)
    .where(and(eq(profiles.workspaceId, workspaceId), eq(profiles.container, container)))
    .get()
  const current = (kind: "static" | "dynamic", limit: number) =>
    db
      .select()
      .from(memories)
      .where(and(eq(memories.workspaceId, workspaceId), eq(memories.container, container), eq(memories.kind, kind), isNull(memories.validTo)))
      .orderBy(desc(memories.createdAt))
      .limit(limit)
      .all()
  return {
    container,
    static: profile?.staticText ?? "",
    dynamic: profile?.dynamicText ?? "",
    updatedAt: profile?.updatedAt ?? null,
    staticMemories: current("static", 25),
    dynamicMemories: current("dynamic", 25),
  }
}

// Memories a document wrote, oldest first. A row with supersedesId replaced an
// older fact; one without is new.
export function memoriesFromDocument(db: DB, workspaceId: string, documentId: string) {
  return db
    .select()
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), eq(memories.sourceDocumentId, documentId)))
    .orderBy(memories.createdAt, memories.id)
    .all()
}

export function memoriesByIds(db: DB, workspaceId: string, ids: string[]) {
  if (ids.length === 0) return []
  return db
    .select()
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), inArray(memories.id, ids)))
    .all()
}
