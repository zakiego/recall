import fs from "node:fs"
import path from "node:path"
import { and, asc, desc, eq, isNull } from "drizzle-orm"
import { chunkText } from "./chunk"
import { bytesToText, fetchUrl, formatChat, MAX_TEXT_CHARS, type ChatMessage } from "./content"
import type { DB } from "./db"
import { chunks, documents, memories, profiles, type Document, type Memory } from "./db/schema"
import { embed } from "./embed"
import { chatJson } from "./gateway/client"
import { EXTRACT_SYSTEM, extractPrompt, PROFILE_SYSTEM, profilePrompt } from "./gateway/prompts"
import { extractResult, profileResult, type ExtractResult } from "./gateway/schemas"
import { newId } from "./ids"
import { enqueue } from "./jobs/queue"
import type { Handlers } from "./jobs/runner"
import { closeMemory, insertMemory, memoriesByIds, memoryText, supersedeMemory, uploadsDir } from "./memories"
import { indexChunk, unindexChunks, vecMemories } from "./search"

// Up to this many words go to the model per call; longer documents are sent
// in consecutive windows so each call sees the memories the last one wrote.
const EXTRACT_WINDOW_WORDS = 3_000
const EXTRACT_MAX_WINDOWS = 10
const RELATED_MEMORIES = 20

type Deps = { chat: typeof chatJson; now: () => Date }
const defaultDeps: Deps = { chat: chatJson, now: () => new Date() }

function setStatus(db: DB, id: string, status: Document["status"], error: string | null = null) {
  db.update(documents).set({ status, error }).where(eq(documents.id, id)).run()
}

// --- ingest -----------------------------------------------------------------

async function resolveText(doc: Document): Promise<{ title: string | null; text: string }> {
  const meta = (doc.metadata ?? {}) as Record<string, unknown>
  if (doc.sourceType === "url") {
    if (doc.content?.trim()) return { title: doc.title, text: doc.content }
    const r = await fetchUrl(doc.url!)
    return { title: doc.title ?? r.title, text: r.text }
  }
  if (doc.sourceType === "file") {
    if (doc.content?.trim()) return { title: doc.title, text: doc.content }
    const file = path.join(uploadsDir(), doc.id)
    const bytes = fs.readFileSync(file)
    const r = await bytesToText(bytes, (meta.mime as string) ?? null, (meta.filename as string) ?? doc.title)
    return { title: doc.title ?? r.title, text: r.text }
  }
  if (doc.sourceType === "chat" && Array.isArray(meta.messages)) {
    return { title: doc.title, text: formatChat(meta.messages as ChatMessage[]) }
  }
  return { title: doc.title, text: doc.content ?? "" }
}

export async function ingest(db: DB, documentId: string) {
  const doc = db.select().from(documents).where(eq(documents.id, documentId)).get()
  if (!doc) return
  setStatus(db, doc.id, "processing")
  const resolved = await resolveText(doc)
  const text = resolved.text.slice(0, MAX_TEXT_CHARS).trim()
  if (!text) throw new Error("no text could be extracted")
  const pieces = chunkText(text)
  const vectors = await embed(pieces, "passage")

  db.transaction(() => {
    // A retry must not duplicate chunks from a half-finished earlier attempt.
    const old = db.select({ id: chunks.id }).from(chunks).where(eq(chunks.documentId, doc.id)).all()
    unindexChunks(db.$client, old.map((c) => c.id))
    db.delete(chunks).where(eq(chunks.documentId, doc.id)).run()
    pieces.forEach((piece, idx) => {
      const id = newId("chk")
      db.insert(chunks).values({ id, documentId: doc.id, container: doc.container, idx, text: piece }).run()
      indexChunk(db.$client, { id, container: doc.container, text: piece }, vectors[idx]!)
    })
    db.update(documents)
      .set({ content: text, title: resolved.title ?? doc.title, metadata: { ...doc.metadata, chunks: pieces.length } })
      .where(eq(documents.id, doc.id))
      .run()
  })
  // The upload is in the DB as text now.
  if (doc.sourceType === "file") fs.rmSync(path.join(uploadsDir(), doc.id), { force: true })
  enqueue(db, "extract", { documentId: doc.id })
}

// --- extract ----------------------------------------------------------------

function windows(db: DB, documentId: string) {
  const rows = db.select().from(chunks).where(eq(chunks.documentId, documentId)).orderBy(asc(chunks.idx)).all()
  const out: { text: string; chunkIds: string[] }[] = []
  let cur: { text: string[]; chunkIds: string[]; words: number } = { text: [], chunkIds: [], words: 0 }
  for (const c of rows) {
    const n = c.text.split(/\s+/).length
    if (cur.words > 0 && cur.words + n > EXTRACT_WINDOW_WORDS) {
      out.push({ text: cur.text.join("\n\n"), chunkIds: cur.chunkIds })
      cur = { text: [], chunkIds: [], words: 0 }
    }
    cur.text.push(c.text)
    cur.chunkIds.push(c.id)
    cur.words += n
  }
  if (cur.words > 0) out.push({ text: cur.text.join("\n\n"), chunkIds: cur.chunkIds })
  return out
}

function chunkVector(db: DB, id: string): Float32Array | null {
  const row = db.$client.prepare("SELECT embedding FROM chunks_vec WHERE chunk_id = ?").get(id) as { embedding: Buffer } | undefined
  if (!row) return null
  return new Float32Array(row.embedding.buffer.slice(row.embedding.byteOffset, row.embedding.byteOffset + row.embedding.byteLength))
}

// The current memories closest to any chunk of the window, fused by RRF.
export function relatedMemories(db: DB, container: string, vectors: Float32Array[], limit = RELATED_MEMORIES): Memory[] {
  const score = new Map<string, number>()
  for (const v of vectors) {
    for (const r of vecMemories(db.$client, container, v, limit)) {
      score.set(r.id, (score.get(r.id) ?? 0) + 1 / (60 + r.rank))
    }
  }
  const ids = [...score.entries()].sort((a, b) => b[1] - a[1]).slice(0, limit).map(([id]) => id)
  const rows = memoriesByIds(db, ids).filter((m) => m.validTo === null && m.container === container)
  const byId = new Map(rows.map((m) => [m.id, m]))
  return ids.map((id) => byId.get(id)).filter((m): m is Memory => !!m)
}

export type ApplyStats = { added: number; updated: number; invalidated: number }

// Writes one extract result. `refs` maps the numbers the model saw to rows.
export async function applyExtract(db: DB, container: string, documentId: string | null, refs: Memory[], result: ExtractResult): Promise<ApplyStats> {
  const byRef = (n: number) => refs[n - 1]
  const updates = result.update.filter((u) => byRef(u.ref))
  const invalidations = [...new Set(result.invalidate)].filter((n) => byRef(n) && !updates.some((u) => u.ref === n))
  const vectors = await embed([...result.add, ...updates].map(memoryText), "passage")
  const stats: ApplyStats = { added: 0, updated: 0, invalidated: 0 }
  db.transaction(() => {
    result.add.forEach((a, i) => {
      insertMemory(db, { container, ...a, sourceDocumentId: documentId }, vectors[i]!)
      stats.added++
    })
    const seen = new Set<string>()
    updates.forEach((u, i) => {
      const old = byRef(u.ref)!
      if (seen.has(old.id)) return
      seen.add(old.id)
      supersedeMemory(db, old, { fact: u.fact, keywords: u.keywords, sourceDocumentId: documentId }, vectors[result.add.length + i]!)
      stats.updated++
    })
    for (const n of invalidations) {
      if (closeMemory(db, byRef(n)!.id)) stats.invalidated++
    }
  })
  return stats
}

export async function extract(db: DB, documentId: string, deps: Deps = defaultDeps) {
  const doc = db.select().from(documents).where(eq(documents.id, documentId)).get()
  if (!doc) return
  const all = windows(db, doc.id)
  const total: ApplyStats = { added: 0, updated: 0, invalidated: 0 }
  for (const w of all.slice(0, EXTRACT_MAX_WINDOWS)) {
    const vectors = w.chunkIds.map((id) => chunkVector(db, id)).filter((v): v is Float32Array => !!v)
    const refs = relatedMemories(db, doc.container, vectors)
    const result = await deps.chat({
      system: EXTRACT_SYSTEM,
      prompt: extractPrompt({ content: w.text, existing: refs.map((m, i) => ({ ref: i + 1, fact: m.fact, kind: m.kind })), now: deps.now() }),
      schema: extractResult,
    })
    const s = await applyExtract(db, doc.container, doc.id, refs, result)
    total.added += s.added
    total.updated += s.updated
    total.invalidated += s.invalidated
  }
  db.update(documents)
    .set({
      status: "done",
      error: null,
      metadata: { ...doc.metadata, extract: { ...total, windows: all.length, truncated: all.length > EXTRACT_MAX_WINDOWS } },
    })
    .where(eq(documents.id, doc.id))
    .run()
  if (total.added + total.updated + total.invalidated > 0) enqueue(db, "profile", { container: doc.container })
}

// --- profile ----------------------------------------------------------------

const PROFILE_MAX_FACTS = 200

export async function buildProfile(db: DB, container: string, deps: Deps = defaultDeps) {
  const current = db
    .select({ fact: memories.fact, kind: memories.kind })
    .from(memories)
    .where(and(eq(memories.container, container), isNull(memories.validTo)))
    .orderBy(asc(memories.kind), desc(memories.createdAt))
    .limit(PROFILE_MAX_FACTS)
    .all()
  let result = { static: "", dynamic: "" }
  if (current.length > 0) {
    result = await deps.chat({ system: PROFILE_SYSTEM, prompt: profilePrompt(current), schema: profileResult })
  }
  const now = Date.now()
  db.insert(profiles)
    .values({ container, staticText: result.static, dynamicText: result.dynamic, updatedAt: now })
    .onConflictDoUpdate({ target: profiles.container, set: { staticText: result.static, dynamicText: result.dynamic, updatedAt: now } })
    .run()
}

// --- wiring -----------------------------------------------------------------

export function createHandlers(db: DB, deps: Deps = defaultDeps): Handlers {
  return {
    ingest: ({ documentId }) => ingest(db, documentId),
    extract: ({ documentId }) => extract(db, documentId, deps),
    profile: ({ container }) => buildProfile(db, container, deps),
    onGiveUp: (job, error) => {
      const documentId = (job.payload as { documentId?: string }).documentId
      if (documentId) setStatus(db, documentId, "failed", error instanceof Error ? error.message : String(error))
    },
  }
}
