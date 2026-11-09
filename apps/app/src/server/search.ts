import type { Sqlite } from "./db"
import type { DatePrecision } from "./db/schema"

// Raw SQL over the FTS5 and vec0 virtual tables. Drizzle can't model them.

const RRF_K = 60

export function vecParam(v: Float32Array) {
  return Buffer.from(v.buffer, v.byteOffset, v.byteLength)
}

// --- writes -----------------------------------------------------------------

export function indexChunk(db: Sqlite, c: { id: string; workspaceId: string; container: string; text: string }, vector: Float32Array) {
  db.prepare("INSERT INTO chunks_fts (text, chunk_id, workspace_id, container) VALUES (?, ?, ?, ?)").run(c.text, c.id, c.workspaceId, c.container)
  db.prepare("INSERT INTO chunks_vec (chunk_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)").run(
    c.id,
    c.workspaceId,
    c.container,
    vecParam(vector),
  )
}

export function unindexChunks(db: Sqlite, ids: string[]) {
  const fts = db.prepare("DELETE FROM chunks_fts WHERE chunk_id = ?")
  const vec = db.prepare("DELETE FROM chunks_vec WHERE chunk_id = ?")
  for (const id of ids) {
    fts.run(id)
    vec.run(id)
  }
}

export function indexMemory(
  db: Sqlite,
  m: { id: string; workspaceId: string; container: string; fact: string; keywords: string },
  vector: Float32Array,
) {
  unindexMemory(db, m.id)
  db.prepare("INSERT INTO memories_fts (fact, keywords, memory_id, workspace_id, container) VALUES (?, ?, ?, ?, ?)").run(
    m.fact,
    m.keywords,
    m.id,
    m.workspaceId,
    m.container,
  )
  db.prepare("INSERT INTO memories_vec (memory_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)").run(
    m.id,
    m.workspaceId,
    m.container,
    vecParam(vector),
  )
}

export function unindexMemory(db: Sqlite, id: string) {
  db.prepare("DELETE FROM memories_fts WHERE memory_id = ?").run(id)
  db.prepare("DELETE FROM memories_vec WHERE memory_id = ?").run(id)
}

// --- reads ------------------------------------------------------------------

// Turns free text into an FTS5 query: every word quoted (so punctuation and
// operators in user input can't break the syntax), OR-ed, prefix-matched.
export function ftsQuery(q: string): string | null {
  const terms = q
    .toLowerCase()
    .normalize("NFKC")
    .split(/[^\p{L}\p{N}]+/u)
    .filter((t) => t.length > 1 || /\p{N}/u.test(t))
    .slice(0, 32)
  if (terms.length === 0) return null
  return [...new Set(terms)].map((t) => `"${t}"*`).join(" OR ")
}

type Ranked = { id: string; rank: number; raw: number }

// Every read is scoped to one container inside one workspace.
export type Scope = { workspaceId: string; container: string }

function ftsMemories(db: Sqlite, s: Scope, match: string, limit: number): Ranked[] {
  const rows = db
    .prepare(
      `SELECT memory_id AS id, bm25(memories_fts, 1.0, 0.5) AS raw
       FROM memories_fts WHERE memories_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`,
    )
    .all(match, s.workspaceId, s.container, limit) as { id: string; raw: number }[]
  return rows.map((r, i) => ({ ...r, rank: i + 1 }))
}

function ftsChunks(db: Sqlite, s: Scope, match: string, limit: number): Ranked[] {
  const rows = db
    .prepare(
      `SELECT chunk_id AS id, bm25(chunks_fts) AS raw
       FROM chunks_fts WHERE chunks_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`,
    )
    .all(match, s.workspaceId, s.container, limit) as { id: string; raw: number }[]
  return rows.map((r, i) => ({ ...r, rank: i + 1 }))
}

export function vecMemories(db: Sqlite, s: Scope, v: Float32Array, k: number): Ranked[] {
  const rows = db
    .prepare(
      `SELECT memory_id AS id, distance AS raw FROM memories_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`,
    )
    .all(vecParam(v), k, s.workspaceId, s.container) as { id: string; raw: number }[]
  return rows.map((r, i) => ({ ...r, rank: i + 1 }))
}

function vecChunks(db: Sqlite, s: Scope, v: Float32Array, k: number): Ranked[] {
  const rows = db
    .prepare(
      `SELECT chunk_id AS id, distance AS raw FROM chunks_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`,
    )
    .all(vecParam(v), k, s.workspaceId, s.container) as { id: string; raw: number }[]
  return rows.map((r, i) => ({ ...r, rank: i + 1 }))
}

export type Fused = { id: string; score: number; ftsRank?: number; vecRank?: number; distance?: number }

// Reciprocal rank fusion: each list adds 1 / (k + rank).
export function rrf(fts: Ranked[], vec: Ranked[]): Fused[] {
  const map = new Map<string, Fused>()
  for (const r of fts) {
    map.set(r.id, { id: r.id, score: 1 / (RRF_K + r.rank), ftsRank: r.rank })
  }
  for (const r of vec) {
    const cur = map.get(r.id) ?? { id: r.id, score: 0 }
    cur.score += 1 / (RRF_K + r.rank)
    cur.vecRank = r.rank
    cur.distance = r.raw
    map.set(r.id, cur)
  }
  return [...map.values()].sort((a, b) => b.score - a.score)
}

export type SearchHit =
  | {
      type: "memory"
      id: string
      text: string
      kind: "static" | "dynamic"
      keywords: string
      sourceDocumentId: string | null
      createdAt: number
      eventDate: string | null
      eventPrecision: DatePrecision | null
      dueDate: string | null
      duePrecision: DatePrecision | null
      score: number
      ftsRank?: number
      vecRank?: number
      similarity?: number
    }
  | {
      type: "chunk"
      id: string
      text: string
      documentId: string
      documentTitle: string | null
      idx: number
      score: number
      ftsRank?: number
      vecRank?: number
      similarity?: number
    }

export type SearchInput = {
  workspaceId: string
  container: string
  q: string
  vector: Float32Array
  limit?: number
  include?: ("memories" | "chunks")[]
}

export function hybridSearch(db: Sqlite, input: SearchInput): SearchHit[] {
  const limit = Math.min(Math.max(input.limit ?? 10, 1), 100)
  const include = input.include?.length ? input.include : ["memories"]
  const pool = Math.max(limit * 3, 30)
  const match = ftsQuery(input.q)
  const hits: SearchHit[] = []

  if (include.includes("memories")) {
    const fused = rrf(match ? ftsMemories(db, input, match, pool) : [], vecMemories(db, input, input.vector, pool))
    // The row fetch checks the scope again, so a stray index entry can't leak a row.
    const get = db.prepare(
      `SELECT id, fact, kind, keywords, source_document_id AS sourceDocumentId, created_at AS createdAt,
              event_date AS eventDate, event_precision AS eventPrecision, due_date AS dueDate, due_precision AS duePrecision
       FROM memories WHERE id = ? AND workspace_id = ? AND container = ? AND valid_to IS NULL`,
    )
    for (const f of fused) {
      const m = get.get(f.id, input.workspaceId, input.container) as
        | {
            id: string
            fact: string
            kind: "static" | "dynamic"
            keywords: string
            sourceDocumentId: string | null
            createdAt: number
            eventDate: string | null
            eventPrecision: DatePrecision | null
            dueDate: string | null
            duePrecision: DatePrecision | null
          }
        | undefined
      if (!m) continue
      hits.push({
        type: "memory",
        id: m.id,
        text: m.fact,
        kind: m.kind,
        keywords: m.keywords,
        sourceDocumentId: m.sourceDocumentId,
        createdAt: m.createdAt,
        eventDate: m.eventDate,
        eventPrecision: m.eventPrecision,
        dueDate: m.dueDate,
        duePrecision: m.duePrecision,
        score: f.score,
        ftsRank: f.ftsRank,
        vecRank: f.vecRank,
        similarity: f.distance === undefined ? undefined : 1 - f.distance,
      })
    }
  }

  if (include.includes("chunks")) {
    const fused = rrf(match ? ftsChunks(db, input, match, pool) : [], vecChunks(db, input, input.vector, pool))
    const get = db.prepare(
      `SELECT c.id, c.text, c.idx, c.document_id AS documentId, d.title AS documentTitle
       FROM chunks c JOIN documents d ON d.id = c.document_id
       WHERE c.id = ? AND c.workspace_id = ? AND c.container = ? AND d.workspace_id = c.workspace_id`,
    )
    for (const f of fused) {
      const c = get.get(f.id, input.workspaceId, input.container) as { id: string; text: string; idx: number; documentId: string; documentTitle: string | null } | undefined
      if (!c) continue
      hits.push({
        type: "chunk",
        ...c,
        score: f.score,
        ftsRank: f.ftsRank,
        vecRank: f.vecRank,
        similarity: f.distance === undefined ? undefined : 1 - f.distance,
      })
    }
  }

  return hits.sort((a, b) => b.score - a.score).slice(0, limit)
}
