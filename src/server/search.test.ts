import { beforeAll, describe, expect, it } from "vitest"
import { openDb, type DB } from "./db"
import { documents, memories } from "./db/schema"
import { embed, embedOne, EMBED_DIMS } from "./embed"
import { newId } from "./ids"
import { ftsQuery, hybridSearch, indexChunk, indexMemory, rrf, unindexMemory } from "./search"

describe("ftsQuery", () => {
  it("quotes terms and strips operators", () => {
    expect(ftsQuery('Adidas OR "shoes" NEAR(x)')).toBe('"adidas"* OR "or"* OR "shoes"* OR "near"*')
  })
  it("returns null when nothing is searchable", () => {
    expect(ftsQuery("!! ?")).toBeNull()
  })
})

describe("rrf", () => {
  it("rewards items found by both lists", () => {
    const fused = rrf(
      [
        { id: "a", rank: 1, raw: -3 },
        { id: "b", rank: 2, raw: -2 },
      ],
      [
        { id: "b", rank: 1, raw: 0.1 },
        { id: "c", rank: 2, raw: 0.2 },
      ],
    )
    expect(fused.map((f) => f.id)).toEqual(["b", "a", "c"])
    expect(fused[0]).toMatchObject({ ftsRank: 2, vecRank: 1, distance: 0.1 })
  })
})

describe("embed", () => {
  it("returns normalized 384-dim vectors", async () => {
    const [v] = await embed(["Zaki suka sepatu Adidas"], "passage")
    expect(v).toHaveLength(EMBED_DIMS)
    const norm = Math.sqrt(v!.reduce((s, x) => s + x * x, 0))
    expect(norm).toBeCloseTo(1, 3)
  })
})

describe("hybridSearch", () => {
  let db: DB
  const C = "test-user"
  const W = "ws_search"
  const ids: Record<string, string> = {}

  beforeAll(async () => {
    db = openDb(":memory:")
    const facts = {
      shoes: { fact: "Prefers Adidas running shoes", keywords: "sneakers sepatu lari footwear brand" },
      food: { fact: "Is allergic to peanuts", keywords: "alergi kacang food allergy" },
      city: { fact: "Lives in Banjarbaru, South Kalimantan", keywords: "tinggal kota domisili home city" },
    }
    const vectors = await embed(
      Object.values(facts).map((f) => `${f.fact}. ${f.keywords}`),
      "passage",
    )
    Object.entries(facts).forEach(([key, f], i) => {
      const id = newId("mem")
      ids[key] = id
      db.insert(memories).values({ id, workspaceId: W, container: C, ...f }).run()
      indexMemory(db.$client, { id, workspaceId: W, container: C, ...f }, vectors[i]!)
    })
    // A memory in another container must never leak.
    const other = newId("mem")
    db.insert(memories).values({ id: other, workspaceId: W, container: "someone-else", fact: "Prefers Nike shoes", keywords: "" }).run()
    indexMemory(db.$client, { id: other, workspaceId: W, container: "someone-else", fact: "Prefers Nike shoes", keywords: "" }, vectors[0]!)

    const docId = newId("doc")
    db.insert(documents).values({ id: docId, workspaceId: W, container: C, sourceType: "text", title: "Notes", content: "x" }).run()
    const chunkText = "The quarterly report covers revenue growth in Kalimantan."
    db.$client.prepare("INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES (?, ?, ?, ?, 0, ?)").run("chk_1", docId, W, C, chunkText)
    indexChunk(db.$client, { id: "chk_1", workspaceId: W, container: C, text: chunkText }, await embedOne(chunkText, "passage"))
  })

  async function search(q: string, include?: ("memories" | "chunks")[]) {
    return hybridSearch(db.$client, { workspaceId: W, container: C, q, vector: await embedOne(q, "query"), include })
  }

  it("finds by meaning across languages", async () => {
    const hits = await search("sepatu apa yang dia suka?")
    expect(hits[0]).toMatchObject({ type: "memory", id: ids.shoes })
  })

  it("finds by keyword", async () => {
    const hits = await search("peanuts")
    expect(hits[0]).toMatchObject({ id: ids.food })
    expect(hits[0]!.ftsRank).toBe(1)
  })

  it("stays inside the container", async () => {
    const hits = await search("Nike shoes")
    expect(hits.every((h) => h.type === "memory" && Object.values(ids).includes(h.id))).toBe(true)
  })

  it("skips memories removed from the index", async () => {
    unindexMemory(db.$client, ids.city!)
    const hits = await search("where does he live")
    expect(hits.find((h) => h.id === ids.city)).toBeUndefined()
  })

  it("includes chunks on request", async () => {
    const hits = await search("revenue report", ["memories", "chunks"])
    expect(hits[0]).toMatchObject({ type: "chunk", id: "chk_1", documentTitle: "Notes" })
  })

  it("is fast once the model is warm", async () => {
    const t = performance.now()
    await search("what shoes does he wear")
    expect(performance.now() - t).toBeLessThan(100)
  })
})
