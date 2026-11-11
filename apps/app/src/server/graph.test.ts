// The Graph page's data: the newest documents with their own facts, facts with
// no source on one synthetic node, and older documents' facts left out.
import { beforeEach, describe, expect, it } from "vitest"
import { openDb, type DB } from "./db"
import { buildGraph, GRAPH_MAX_DOCUMENTS } from "./graph"

const W = "ws_graph"
const C = "zaki"
const T0 = Date.UTC(2026, 0, 1)
let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

function doc(id: string, createdAt: number, container = C) {
  db.$client
    .prepare("INSERT INTO documents (id, workspace_id, container, source_type, title, status, created_at) VALUES (?, ?, ?, 'email', ?, 'done', ?)")
    .run(id, W, container, `Title ${id}`, createdAt)
}

function mem(id: string, opts: { doc?: string | null; at: number; supersedes?: string; validTo?: number; container?: string }) {
  db.$client
    .prepare(
      "INSERT INTO memories (id, workspace_id, container, fact, source_document_id, supersedes_id, valid_from, valid_to, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .run(id, W, opts.container ?? C, `fact ${id}`, opts.doc ?? null, opts.supersedes ?? null, opts.at, opts.validTo ?? null, opts.at)
}

// n documents, newest first: doc_0 is the newest. Each has one fact.
function seed(n: number) {
  for (let i = 0; i < n; i++) {
    doc(`doc_${i}`, T0 - i * 1000)
    mem(`mem_${i}`, { doc: `doc_${i}`, at: T0 - i * 1000 + 1 })
  }
}

const nodeFor = (g: ReturnType<typeof buildGraph>, id: string) => g.documents.find((d) => d.id === id)
const noSource = (g: ReturnType<typeof buildGraph>) => nodeFor(g, `manual:${C}`)

describe("buildGraph", () => {
  it("draws every document and its facts when under the cap", () => {
    seed(3)
    mem("mem_hand", { at: T0 - 5000 })
    const g = buildGraph(db, W, C)
    expect(g.capped).toBe(false)
    expect(g.shownDocuments).toBe(3)
    expect(g.totalDocuments).toBe(3)
    expect(g.hiddenFacts).toBe(0)
    expect(nodeFor(g, "doc_0")?.memories.map((m) => m.id)).toEqual(["mem_0"])
    expect(noSource(g)?.memories.map((m) => m.id)).toEqual(["mem_hand"])
  })

  it("leaves facts from documents past the cap out instead of calling them sourceless", () => {
    const n = GRAPH_MAX_DOCUMENTS + 50
    seed(n)
    mem("mem_hand_1", { at: T0 - 10 })
    mem("mem_hand_2", { at: T0 - 20 })
    const g = buildGraph(db, W, C)
    expect(g.capped).toBe(true)
    expect(g.shownDocuments).toBe(GRAPH_MAX_DOCUMENTS)
    expect(g.totalDocuments).toBe(n)
    expect(g.hiddenFacts).toBe(50)
    // The synthetic node is real orphans only, and its dates are theirs.
    const orphan = noSource(g)!
    expect(orphan.title).toBe("No source")
    expect(orphan.memories.map((m) => m.id).sort()).toEqual(["mem_hand_1", "mem_hand_2"])
    expect(orphan.createdAt).toBe(new Date(T0 - 20).toISOString())
    expect(orphan.updatedAt).toBe(new Date(T0 - 10).toISOString())
    // The newest documents are drawn, the oldest are not.
    expect(nodeFor(g, "doc_0")).toBeDefined()
    expect(nodeFor(g, `doc_${GRAPH_MAX_DOCUMENTS - 1}`)?.memories).toHaveLength(1)
    expect(nodeFor(g, `doc_${GRAPH_MAX_DOCUMENTS}`)).toBeUndefined()
    const drawn = g.documents.flatMap((d) => d.memories.map((m) => m.id))
    expect(drawn).toHaveLength(GRAPH_MAX_DOCUMENTS + 2)
    expect(drawn).not.toContain(`mem_${n - 1}`)
  })

  it("adds no synthetic node when every fact has a source", () => {
    seed(GRAPH_MAX_DOCUMENTS + 1)
    const g = buildGraph(db, W, C)
    expect(noSource(g)).toBeUndefined()
    expect(g.documents).toHaveLength(GRAPH_MAX_DOCUMENTS)
  })

  it("keeps version numbers and latest flags right across the window edge", () => {
    seed(GRAPH_MAX_DOCUMENTS + 2)
    const old = `doc_${GRAPH_MAX_DOCUMENTS + 1}`
    // v1 from an old document, v2 from a drawn one, and a drawn v1 replaced by a fact from an old document.
    mem("v1", { doc: old, at: T0 - 9_000_000, validTo: T0 })
    mem("v2", { doc: "doc_0", at: T0, supersedes: "v1" })
    mem("w1", { doc: "doc_1", at: T0 - 5000, validTo: T0 })
    mem("w2", { doc: old, at: T0, supersedes: "w1" })
    const g = buildGraph(db, W, C)
    const v2 = nodeFor(g, "doc_0")!.memories.find((m) => m.id === "v2")!
    expect(v2).toMatchObject({ version: 2, rootMemoryId: "v1", parentMemoryId: "v1", isLatest: true })
    const w1 = nodeFor(g, "doc_1")!.memories.find((m) => m.id === "w1")!
    expect(w1).toMatchObject({ isLatest: false, isForgotten: false, nextVersionId: "w2" })
    expect(g.hiddenFacts).toBe(2 + 2)
  })

  it("only reads its own workspace and container", () => {
    seed(2)
    doc("doc_other", T0, "other")
    mem("mem_other", { doc: "doc_other", at: T0, container: "other" })
    mem("mem_other_hand", { at: T0, container: "other" })
    const g = buildGraph(db, W, C)
    expect(g.totalDocuments).toBe(2)
    expect(g.hiddenFacts).toBe(0)
    expect(noSource(g)).toBeUndefined()
    expect(buildGraph(db, "ws_else", C).documents).toEqual([])
  })
})
