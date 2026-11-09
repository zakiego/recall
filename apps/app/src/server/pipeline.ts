import fs from "node:fs"
import path from "node:path"
import { and, asc, desc, eq, isNull } from "drizzle-orm"
import { answerQuestion, defaultAskDeps, type AskDeps } from "./ask"
import { chunkText } from "./chunk"
import { bytesToText, fetchUrl, formatChat, MAX_TEXT_CHARS, type ChatMessage } from "./content"
import type { DB } from "./db"
import { chunks, documents, memories, profiles, type Document, type Memory } from "./db/schema"
import { embed } from "./embed"
import { googleFetch, resolveEmail } from "./connectors/gmail"
import type { Fetch } from "./connectors/google"
import { chatJson } from "./gateway/client"
import { EXTRACT_SYSTEM, extractPrompt, PROFILE_SYSTEM, profilePrompt } from "./gateway/prompts"
import { extractResult, profileResult, type ExtractResult } from "./gateway/schemas"
import { newId } from "./ids"
import { enqueue } from "./jobs/queue"
import type { Handlers } from "./jobs/runner"
import { jobAbortSignal, releaseJobAbort } from "./jobs/wait"
import { closeMemory, insertMemory, maskTitle, memoriesByIds, memoryText, setReviewNote, supersedeMemory, uploadsDir } from "./memories"
import { indexChunk, unindexChunks, vecMemories, type Scope } from "./search"
import { maskSecrets, sensitiveKinds } from "./sensitive"

// Up to this many words go to the model per call; longer documents are sent
// in consecutive windows so each call sees the memories the last one wrote.
const EXTRACT_WINDOW_WORDS = 3_000
const EXTRACT_MAX_WINDOWS = 10
const RELATED_MEMORIES = 20

// gmailFetch: what email ingest talks to Google with (tests pass the fixture fake).
type Deps = { chat: typeof chatJson; now: () => Date; gmailFetch?: Fetch }
const defaultDeps: Deps = { chat: chatJson, now: () => new Date() }

function setStatus(db: DB, id: string, status: Document["status"], error: string | null = null) {
  db.update(documents).set({ status, error }).where(eq(documents.id, id)).run()
}

// --- ingest -----------------------------------------------------------------

type ResolvedText = { title: string | null; text: string; metadata?: Record<string, unknown> }

async function resolveText(db: DB, doc: Document, gmailFetch?: Fetch): Promise<ResolvedText> {
  const meta = (doc.metadata ?? {}) as Record<string, unknown>
  if (doc.sourceType === "email") {
    // A retry after the text was stored doesn't go back to Gmail.
    if (doc.content?.trim()) return { title: doc.title, text: doc.content }
    return resolveEmail(db, doc, gmailFetch ?? (await googleFetch()))
  }
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

// Chat input keeps its raw messages in metadata; mask those too.
function maskedMetadata(metadata: Document["metadata"]) {
  const meta = { ...metadata }
  if (Array.isArray(meta.messages)) {
    meta.messages = (meta.messages as ChatMessage[]).map((m) => (typeof m.content === "string" ? { ...m, content: maskSecrets(m.content).text } : m))
  }
  return meta
}

export async function ingest(db: DB, documentId: string, gmailFetch?: Fetch) {
  const doc = db.select().from(documents).where(eq(documents.id, documentId)).get()
  if (!doc) return
  setStatus(db, doc.id, "processing")
  const resolved = await resolveText(db, doc, gmailFetch)
  // Keys, tokens and card numbers never reach the stored text, the index or the model.
  const { text, masked } = maskSecrets(resolved.text.slice(0, MAX_TEXT_CHARS).trim())
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
      db.insert(chunks).values({ id, documentId: doc.id, workspaceId: doc.workspaceId, container: doc.container, idx, text: piece }).run()
      indexChunk(db.$client, { id, workspaceId: doc.workspaceId, container: doc.container, text: piece }, vectors[idx]!)
    })
    db.update(documents)
      .set({ content: text, title: maskTitle(resolved.title ?? doc.title), metadata: { ...maskedMetadata(doc.metadata), ...resolved.metadata, chunks: pieces.length, masked } })
      .where(eq(documents.id, doc.id))
      .run()
  })
  // The upload is in the DB as text now.
  if (doc.sourceType === "file") fs.rmSync(path.join(uploadsDir(), doc.id), { force: true })
  enqueue(db, doc.workspaceId, "extract", { documentId: doc.id })
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
export function relatedMemories(db: DB, scope: Scope, vectors: Float32Array[], limit = RELATED_MEMORIES): Memory[] {
  const score = new Map<string, number>()
  for (const v of vectors) {
    for (const r of vecMemories(db.$client, scope, v, limit)) {
      score.set(r.id, (score.get(r.id) ?? 0) + 1 / (60 + r.rank))
    }
  }
  const ids = [...score.entries()].sort((a, b) => b[1] - a[1]).slice(0, limit).map(([id]) => id)
  const rows = memoriesByIds(db, scope.workspaceId, ids).filter((m) => m.validTo === null && m.container === scope.container)
  const byId = new Map(rows.map((m) => [m.id, m]))
  return ids.map((id) => byId.get(id)).filter((m): m is Memory => !!m)
}

// `stale` counts updates skipped because their memory was closed after the
// refs were read (a concurrent edit, forget or extract).
// `cappedInvalidations` counts invalidations dropped over MAX_INVALIDATIONS.
export type ApplyStats = {
  added: number
  updated: number
  invalidated: number
  deduped: number
  conflicts: number
  redacted: number
  stale: number
  cappedInvalidations: number
}

export const MAX_INVALIDATIONS = 5

const isSensitive = (f: { fact: string; keywords: string }) => sensitiveKinds(`${f.fact}\n${f.keywords}`).length > 0

// An `add` whose vector (fact + keywords, e5 passage) is at least this close to a
// current memory in the container, or to another fact from the same result, is
// dropped as a duplicate. Measured on e5-small: restatements and paraphrases score
// 0.975 to 1.0, while distinct facts on the same topic reach 0.970 ("Skilled in
// TypeScript" vs "Skilled in Python"), so the gate sits just above that.
export const DEDUPE_SIMILARITY = 0.975
const DEDUPE_CANDIDATES = 5

function normalizeFact(fact: string) {
  return fact.toLowerCase().replace(/\s+/g, " ").replace(/[.!?\s]+$/, "").trim()
}

function numbersIn(fact: string) {
  return new Set((fact.match(/\d+/g) ?? []).map((n) => String(Number(n))))
}

// e5 barely moves when only a number changes (two signing dates score 0.995), so
// facts where each side has a number the other lacks are never duplicates.
function conflictingNumbers(a: string, b: string) {
  const x = numbersIn(a)
  const y = numbersIn(b)
  return [...x].some((n) => !y.has(n)) && [...y].some((n) => !x.has(n))
}

function cosine(a: Float32Array, b: Float32Array) {
  // embed() returns L2-normalized vectors, so the dot product is the cosine.
  let s = 0
  for (let i = 0; i < a.length; i++) s += a[i]! * b[i]!
  return s
}

function isDuplicate(fact: string, other: string, similarity: number) {
  if (normalizeFact(fact) === normalizeFact(other)) return true
  return similarity >= DEDUPE_SIMILARITY && !conflictingNumbers(fact, other)
}

const MAX_REVIEW_NOTE = 1_000

function reviewNote(other: string, note: string) {
  const quoted = other.length > 160 ? `${other.slice(0, 157)}...` : other
  return note ? `Conflicts with "${quoted}": ${note}` : `Conflicts with "${quoted}"`
}

// A memory can be flagged by more than one document; keep every reason.
function appendNote(existing: string | null, note: string) {
  return (existing ? `${existing}\n${note}` : note).slice(0, MAX_REVIEW_NOTE)
}

// Writes one extract result. `refs` maps the numbers the model saw to rows.
// When the model names the same memory more than once, a clear "update" wins
// over a "conflict", and a "conflict" wins over "invalidate" (when unsure,
// keep the fact and ask).
// Facts that carry sensitive data (ids, card numbers, secrets) are dropped
// before anything is written, and counted in `redacted`. That happens after
// the rules above, so a dropped update or conflict still shields its memory
// from "invalidate" and leaves it as it was.
export async function applyExtract(db: DB, scope: Scope, documentId: string | null, refs: Memory[], result: ExtractResult): Promise<ApplyStats> {
  const byRef = (n: number) => refs[n - 1]
  const allUpdates = result.update.filter((u) => byRef(u.ref))
  const allConflicts = result.conflict.filter((c) => byRef(c.ref) && !allUpdates.some((u) => u.ref === c.ref))
  const allInvalidations = [...new Set(result.invalidate)].filter(
    (n) => byRef(n) && !allUpdates.some((u) => u.ref === n) && !allConflicts.some((c) => c.ref === n),
  )
  // Real content retires a fact or two. A window that tries to close many is
  // more likely an injected instruction than news, so only the first few count.
  const invalidations = allInvalidations.slice(0, MAX_INVALIDATIONS)
  const addFacts = result.add.filter((a) => !isSensitive(a))
  const safeUpdates = allUpdates.filter((u) => !isSensitive(u))
  const conflicts = allConflicts.filter((c) => !isSensitive(c))
  const redacted = result.add.length - addFacts.length + allUpdates.length - safeUpdates.length + allConflicts.length - conflicts.length
  // One update per memory: the first wins. A repeat would never be written, so
  // it must not reach the dedupe batch either, where it could swallow an add.
  const updates = safeUpdates.filter((u, i) => safeUpdates.findIndex((o) => o.ref === u.ref) === i)
  const vectors = await embed([...addFacts, ...updates, ...conflicts].map(memoryText), "passage")
  const stats: ApplyStats = {
    added: 0,
    updated: 0,
    invalidated: 0,
    deduped: 0,
    conflicts: 0,
    redacted,
    stale: 0,
    cappedInvalidations: allInvalidations.length - invalidations.length,
  }

  // Memories this result replaces or retires don't count as "already known".
  const leaving = new Set([...updates.map((u) => byRef(u.ref)!.id), ...invalidations.map((n) => byRef(n)!.id)])
  // Facts this result writes: update facts first, then each conflict and add that survives.
  type Written = { fact: string; vector: Float32Array; conflict?: true }
  const batch: Written[] = updates.map((u, i) => ({ fact: u.fact, vector: vectors[addFacts.length + i]! }))
  // True when a current memory in the container, or a fact already in the batch, says the same.
  const duplicate = (fact: string, vector: Float32Array, conflictWith?: string) => {
    // vecMemories is partitioned by workspace and container, so this never looks across either.
    const near = vecMemories(db.$client, scope, vector, DEDUPE_CANDIDATES).filter((r) => !leaving.has(r.id) && r.id !== conflictWith)
    const current = memoriesByIds(
      db,
      scope.workspaceId,
      near.map((r) => r.id),
    ).filter((m) => m.container === scope.container && m.validTo === null)
    const facts = new Map(current.map((m) => [m.id, m.fact]))
    const known = near.some((r) => facts.has(r.id) && isDuplicate(fact, facts.get(r.id)!, 1 - r.raw))
    return (
      known ||
      batch.some((b) =>
        // Two conflicts are on the same topic by design and e5 scores them as
        // near-identical, so between conflicts only the same text counts.
        conflictWith && b.conflict ? normalizeFact(fact) === normalizeFact(b.fact) : isDuplicate(fact, b.fact, cosine(vector, b.vector)),
      )
    )
  }
  // Conflicts go through the same check (minus the memory they disagree with,
  // which is on the same topic by definition), so a retried or replayed
  // document doesn't write the same conflict fact and review note twice.
  const keptConflicts: { conflict: ExtractResult["conflict"][number]; vector: Float32Array }[] = []
  conflicts.forEach((c, i) => {
    const vector = vectors[addFacts.length + updates.length + i]!
    if (duplicate(c.fact, vector, byRef(c.ref)!.id)) {
      stats.deduped++
      return
    }
    batch.push({ fact: c.fact, vector, conflict: true })
    keptConflicts.push({ conflict: c, vector })
  })
  const adds: { add: ExtractResult["add"][number]; vector: Float32Array }[] = []
  addFacts.forEach((a, i) => {
    const vector = vectors[i]!
    if (duplicate(a.fact, vector)) {
      stats.deduped++
      return
    }
    batch.push({ fact: a.fact, vector })
    adds.push({ add: a, vector })
  })

  db.transaction(() => {
    for (const { add, vector } of adds) {
      insertMemory(db, { ...scope, ...add, sourceDocumentId: documentId }, vector)
      stats.added++
    }
    updates.forEach((u, i) => {
      const old = byRef(u.ref)!
      if (supersedeMemory(db, old, { fact: u.fact, keywords: u.keywords, sourceDocumentId: documentId }, vectors[addFacts.length + i]!)) stats.updated++
      else stats.stale++
    })
    for (const n of invalidations) {
      if (closeMemory(db, byRef(n)!.id)) stats.invalidated++
    }
    for (const { conflict: c, vector } of keptConflicts) {
      // Read it fresh: an earlier conflict in this batch may have noted it already.
      const old = db
        .select()
        .from(memories)
        .where(and(eq(memories.id, byRef(c.ref)!.id), eq(memories.workspaceId, scope.workspaceId)))
        .get()
      if (!old || old.validTo !== null) continue
      const fact = { fact: c.fact, keywords: c.keywords, kind: old.kind, reviewNote: reviewNote(old.fact, c.note), conflictsWithId: old.id }
      const added = insertMemory(db, { ...scope, ...fact, sourceDocumentId: documentId }, vector)
      setReviewNote(db, scope.workspaceId, old.id, appendNote(old.reviewNote, reviewNote(c.fact, c.note)), added.id)
      stats.conflicts++
    }
  })
  return stats
}

export async function extract(db: DB, documentId: string, deps: Deps = defaultDeps) {
  const doc = db.select().from(documents).where(eq(documents.id, documentId)).get()
  if (!doc) return
  const all = windows(db, doc.id)
  const total: ApplyStats = { added: 0, updated: 0, invalidated: 0, deduped: 0, conflicts: 0, redacted: 0, stale: 0, cappedInvalidations: 0 }
  for (const w of all.slice(0, EXTRACT_MAX_WINDOWS)) {
    const vectors = w.chunkIds.map((id) => chunkVector(db, id)).filter((v): v is Float32Array => !!v)
    const scope = { workspaceId: doc.workspaceId, container: doc.container }
    const refs = relatedMemories(db, scope, vectors)
    const result = await deps.chat({
      system: EXTRACT_SYSTEM,
      prompt: extractPrompt({ content: w.text, existing: refs.map((m, i) => ({ ref: i + 1, fact: m.fact, kind: m.kind })), now: deps.now() }),
      schema: extractResult,
    })
    const s = await applyExtract(db, scope, doc.id, refs, result)
    total.added += s.added
    total.updated += s.updated
    total.invalidated += s.invalidated
    total.deduped += s.deduped
    total.conflicts += s.conflicts
    total.redacted += s.redacted
    total.stale += s.stale
    total.cappedInvalidations += s.cappedInvalidations
  }
  db.update(documents)
    .set({
      status: "done",
      error: null,
      metadata: { ...doc.metadata, extract: { ...total, windows: all.length, truncated: all.length > EXTRACT_MAX_WINDOWS } },
    })
    .where(eq(documents.id, doc.id))
    .run()
  if (total.added + total.updated + total.invalidated + total.conflicts > 0) enqueue(db, doc.workspaceId, "profile", { container: doc.container })
}

// --- profile ----------------------------------------------------------------

const PROFILE_MAX_FACTS = 200

export async function buildProfile(db: DB, workspaceId: string, container: string, deps: Deps = defaultDeps) {
  const current = db
    .select({ fact: memories.fact, kind: memories.kind })
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), eq(memories.container, container), isNull(memories.validTo)))
    .orderBy(asc(memories.kind), desc(memories.createdAt))
    .limit(PROFILE_MAX_FACTS)
    .all()
  let result = { static: "", dynamic: "" }
  if (current.length > 0) {
    result = await deps.chat({ system: PROFILE_SYSTEM, prompt: profilePrompt(current), schema: profileResult })
  }
  const now = Date.now()
  db.insert(profiles)
    .values({ workspaceId, container, staticText: result.static, dynamicText: result.dynamic, updatedAt: now })
    .onConflictDoUpdate({ target: [profiles.workspaceId, profiles.container], set: { staticText: result.static, dynamicText: result.dynamic, updatedAt: now } })
    .run()
}

// --- wiring -----------------------------------------------------------------

export function createHandlers(db: DB, deps: Deps = defaultDeps, askDeps: AskDeps = defaultAskDeps): Handlers {
  return {
    ask: async (payload, job) => {
      // The request waiting on this ask can call it off (see ask()).
      const signal = jobAbortSignal(job.id)
      try {
        return await answerQuestion(db, job.workspaceId, payload, askDeps, signal)
      } finally {
        releaseJobAbort(job.id)
      }
    },
    ingest: ({ documentId }) => ingest(db, documentId, deps.gmailFetch),
    extract: ({ documentId }) => extract(db, documentId, deps),
    profile: ({ container }, job) => buildProfile(db, job.workspaceId, container, deps),
    onGiveUp: (job, error) => {
      const documentId = (job.payload as { documentId?: string }).documentId
      if (documentId) setStatus(db, documentId, "failed", error instanceof Error ? error.message : String(error))
    },
  }
}
