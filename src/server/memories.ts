import fs from "node:fs"
import path from "node:path"
import { and, count, desc, eq, inArray, isNotNull, isNull, lt, or, sql } from "drizzle-orm"
import { config } from "./config"
import type { DB } from "./db"
import { chunks, documents, memories, profiles, type Document, type Memory } from "./db/schema"
import { embedOne } from "./embed"
import { newId } from "./ids"
import { enqueue } from "./jobs/queue"
import { indexMemory, unindexChunks, unindexMemory } from "./search"

export const MAX_CONTAINER_LEN = 128

export function memoryText(m: { fact: string; keywords: string }) {
  return m.keywords ? `${m.fact}. ${m.keywords}` : m.fact
}

// --- documents --------------------------------------------------------------

export type NewDocument = {
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
      const existing = findBySourceId(db, input.container, input.sourceId)
      if (existing) return { doc: existing, duplicate: true }
    }
    const metadata = { ...input.metadata }
    if (input.file) Object.assign(metadata, { filename: input.file.name, mime: input.file.mime, size: input.file.bytes.byteLength })
    const doc = db
      .insert(documents)
      .values({
        id,
        container: input.container,
        sourceType: input.sourceType,
        sourceId: input.sourceId ?? null,
        title: input.title ?? input.file?.name ?? null,
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
    enqueue(db, "ingest", { documentId: id })
    return { doc, duplicate: false }
  })
}

export function findBySourceId(db: DB, container: string, sourceId: string) {
  return db
    .select()
    .from(documents)
    .where(and(eq(documents.container, container), eq(documents.sourceId, sourceId)))
    .get()
}

export function getDocument(db: DB, id: string) {
  return db.select().from(documents).where(eq(documents.id, id)).get()
}

export function listDocuments(db: DB, container: string, limit = 50) {
  return db
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
    .where(eq(documents.container, container))
    .orderBy(desc(documents.createdAt))
    .limit(limit)
    .all()
}

// Deletes the document and its chunks. Memories it produced stay, unlinked.
export function deleteDocument(db: DB, id: string) {
  const ids = db.select({ id: chunks.id }).from(chunks).where(eq(chunks.documentId, id)).all().map((c) => c.id)
  db.transaction(() => {
    unindexChunks(db.$client, ids)
    db.delete(documents).where(eq(documents.id, id)).run()
  })
  fs.rmSync(path.join(uploadsDir(), id), { force: true })
}

// --- memories ---------------------------------------------------------------

export type MemoryView = Pick<
  Memory,
  "id" | "container" | "fact" | "keywords" | "kind" | "sourceDocumentId" | "supersedesId" | "validFrom" | "validTo" | "createdAt" | "reviewNote"
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
  opts: { container: string; cursor?: string | null; history?: boolean; limit?: number; kind?: "static" | "dynamic"; review?: boolean },
) {
  const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200)
  const cur = decodeCursor(opts.cursor)
  const rows = db
    .select()
    .from(memories)
    .where(
      and(
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
export function reviewCount(db: DB, container: string) {
  const row = db
    .select({ n: count() })
    .from(memories)
    .where(and(eq(memories.container, container), isNull(memories.validTo), isNotNull(memories.reviewNote)))
    .get()
  return row?.n ?? 0
}

export function getMemory(db: DB, id: string) {
  return db.select().from(memories).where(eq(memories.id, id)).get()
}

// The whole version chain a memory belongs to, oldest first.
export function memoryChain(db: DB, id: string): Memory[] {
  const start = getMemory(db, id)
  if (!start) return []
  const chain = [start]
  let back = start
  for (let i = 0; i < 100 && back.supersedesId; i++) {
    const prev = getMemory(db, back.supersedesId)
    if (!prev) break
    chain.unshift(prev)
    back = prev
  }
  let fwd = start
  for (let i = 0; i < 100; i++) {
    const next = db.select().from(memories).where(eq(memories.supersedesId, fwd.id)).get()
    if (!next) break
    chain.push(next)
    fwd = next
  }
  return chain
}

// How many older versions each memory has, for the "history" badge in lists.
export function versionCounts(db: DB, ids: string[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (const id of ids) {
    const row = db.$client
      .prepare(
        `WITH RECURSIVE chain(id, prev, n) AS (
           SELECT id, supersedes_id, 0 FROM memories WHERE id = ?
           UNION ALL SELECT m.id, m.supersedes_id, chain.n + 1 FROM memories m JOIN chain ON m.id = chain.prev WHERE chain.n < 100
         ) SELECT max(n) AS n FROM chain`,
      )
      .get(id) as { n: number | null }
    out[id] = row.n ?? 0
  }
  return out
}

type NewFact = { fact: string; keywords: string; kind: "static" | "dynamic" }

// Writes rows and index entries. Vectors are computed by the caller first,
// because embedding is async and this runs inside a sync transaction.
export function insertMemory(
  db: DB,
  input: NewFact & { container: string; sourceDocumentId?: string | null; supersedesId?: string | null; reviewNote?: string | null },
  vector: Float32Array,
): Memory {
  const row = db
    .insert(memories)
    .values({
      id: newId("mem"),
      container: input.container,
      fact: input.fact,
      keywords: input.keywords,
      kind: input.kind,
      sourceDocumentId: input.sourceDocumentId ?? null,
      supersedesId: input.supersedesId ?? null,
      reviewNote: input.reviewNote ?? null,
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

export function supersedeMemory(db: DB, old: Memory, next: Omit<NewFact, "kind"> & { kind?: NewFact["kind"]; sourceDocumentId?: string | null }, vector: Float32Array) {
  return db.transaction(() => {
    const now = Date.now()
    closeMemory(db, old.id, now)
    return insertMemory(
      db,
      {
        container: old.container,
        fact: next.fact,
        keywords: next.keywords,
        kind: next.kind ?? old.kind,
        sourceDocumentId: next.sourceDocumentId ?? old.sourceDocumentId,
        supersedesId: old.id,
      },
      vector,
    )
  })
}

// Sets or clears the review note on a current memory. Not a new version.
export function setReviewNote(db: DB, id: string, note: string | null) {
  return db
    .update(memories)
    .set({ reviewNote: note })
    .where(and(eq(memories.id, id), isNull(memories.validTo)))
    .returning()
    .get()
}

// A manual edit is a new version, so the history keeps what it replaced. The
// new version starts without a review note: editing a flagged fact resolves it.
export async function editMemory(db: DB, id: string, patch: { fact?: string; keywords?: string; kind?: "static" | "dynamic" }) {
  const old = getMemory(db, id)
  if (!old || old.validTo !== null) return null
  const next = { fact: patch.fact?.trim() || old.fact, keywords: patch.keywords?.trim() ?? old.keywords, kind: patch.kind ?? old.kind }
  if (next.fact === old.fact && next.keywords === old.keywords && next.kind === old.kind) {
    return old.reviewNote === null ? old : (setReviewNote(db, old.id, null) ?? old)
  }
  const vector = await embedOne(memoryText(next), "passage")
  const row = supersedeMemory(db, old, next, vector)
  enqueue(db, "profile", { container: old.container })
  return row
}

export async function addMemoryDirect(db: DB, input: NewFact & { container: string }) {
  const vector = await embedOne(memoryText(input), "passage")
  const row = db.transaction(() => insertMemory(db, input, vector))
  enqueue(db, "profile", { container: input.container })
  return row
}

export function forgetMemory(db: DB, id: string) {
  const m = getMemory(db, id)
  if (!m) return null
  const closed = db.transaction(() => closeMemory(db, id))
  if (closed) enqueue(db, "profile", { container: m.container })
  return getMemory(db, id)!
}

// --- containers and profile -------------------------------------------------

export function listContainers(db: DB) {
  return db.$client
    .prepare(
      `SELECT container,
              sum(CASE WHEN kind = 'mem' THEN 1 ELSE 0 END) AS memories,
              sum(CASE WHEN kind = 'doc' THEN 1 ELSE 0 END) AS documents,
              max(at) AS lastActivity
       FROM (
         SELECT container, 'mem' AS kind, created_at AS at FROM memories WHERE valid_to IS NULL
         UNION ALL SELECT container, 'doc', created_at FROM documents
       ) GROUP BY container ORDER BY lastActivity DESC`,
    )
    .all() as { container: string; memories: number; documents: number; lastActivity: number }[]
}

export function getProfile(db: DB, container: string) {
  const profile = db.select().from(profiles).where(eq(profiles.container, container)).get()
  const current = (kind: "static" | "dynamic", limit: number) =>
    db
      .select()
      .from(memories)
      .where(and(eq(memories.container, container), eq(memories.kind, kind), isNull(memories.validTo)))
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
export function memoriesFromDocument(db: DB, documentId: string) {
  return db.select().from(memories).where(eq(memories.sourceDocumentId, documentId)).orderBy(memories.createdAt, memories.id).all()
}

export function memoriesByIds(db: DB, ids: string[]) {
  if (ids.length === 0) return []
  return db.select().from(memories).where(inArray(memories.id, ids)).all()
}
