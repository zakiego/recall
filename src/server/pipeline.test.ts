import { isNull } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { addContent } from "./api"
import { openDb, type DB } from "./db"
import { documents, jobs, memories, profiles } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { memoryChain } from "./memories"
import { applyExtract, createHandlers, DEDUPE_SIMILARITY } from "./pipeline"
import { hybridSearch } from "./search"
import { embed, embedOne } from "./embed"
import { memoryText } from "./memories"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

// Plays the model: answers each extract call from a script and records prompts.
function scripted(replies: unknown[]) {
  const prompts: string[] = []
  const chat = vi.fn(async (req: { system: string; prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
    prompts.push(req.prompt)
    if (req.system.includes("profile")) return req.schema.parse({ static: "Static summary.", dynamic: "Dynamic summary." })
    return req.schema.parse(replies.shift() ?? { add: [] })
  })
  return { chat: chat as unknown as typeof chatJson, prompts }
}

async function drain(runner: JobRunner) {
  for (let i = 0; i < 50; i++) if (!(await runner.runOnce())) return
}

describe("pipeline", () => {
  it("ingests, extracts and supersedes contradicting facts", async () => {
    const { chat, prompts } = scripted([
      { add: [{ fact: "Loves Adidas shoes", keywords: "sneakers sepatu", kind: "dynamic" }] },
      { add: [{ fact: "Lives in Banjarbaru", keywords: "city kota", kind: "static" }] },
      { update: [{ ref: 1, fact: "Switched to Puma shoes", keywords: "sneakers sepatu puma" }] },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    const C = "zaki"

    addContent(db, { container: C, content: "I love my Adidas shoes." })
    await drain(runner)
    addContent(db, { container: C, content: "I live in Banjarbaru." })
    await drain(runner)
    addContent(db, { container: C, content: "I switched to Puma, done with Adidas." })
    await drain(runner)

    // The third extract saw the Adidas memory as ref 1.
    expect(prompts.at(-2) ?? "").toMatch(/1\. \[dynamic\] Loves Adidas shoes/)

    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current.map((m) => m.fact).sort()).toEqual(["Lives in Banjarbaru", "Switched to Puma shoes"])

    const puma = current.find((m) => m.fact.includes("Puma"))!
    const chain = memoryChain(db, puma.id)
    expect(chain.map((m) => m.fact)).toEqual(["Loves Adidas shoes", "Switched to Puma shoes"])
    expect(chain[0]!.validTo).not.toBeNull()

    // Search only returns the current version.
    const hits = hybridSearch(db.$client, { container: C, q: "Adidas shoes", vector: await embedOne("Adidas shoes", "query") })
    expect(hits.map((h) => h.text)).not.toContain("Loves Adidas shoes")
    expect(hits[0]?.text).toBe("Switched to Puma shoes")

    expect(db.select().from(documents).all().every((d) => d.status === "done")).toBe(true)
    expect(db.select().from(profiles).get()).toMatchObject({ container: C, staticText: "Static summary." })
    expect(db.select().from(jobs).all().every((j) => j.status === "done")).toBe(true)
  })

  it("invalidates, ignores unknown refs, and handles chat input", async () => {
    const { chat } = scripted([
      { add: [{ fact: "Works at Speechify", keywords: "job kerja", kind: "dynamic" }] },
      { invalidate: [1, 7], update: [{ ref: 9, fact: "ghost", keywords: "" }] },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, { container: "c", messages: [{ role: "user", content: "I work at Speechify" }] })
    await drain(runner)
    addContent(db, { container: "c", messages: [{ role: "user", content: "I quit my job" }] })
    await drain(runner)
    expect(db.select().from(memories).where(isNull(memories.validTo)).all()).toHaveLength(0)
    expect(db.select().from(memories).all()).toHaveLength(1)
  })

  it("marks the document failed after the last retry", async () => {
    const chat = vi.fn(async () => {
      throw new Error("gateway down")
    }) as unknown as typeof chatJson
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    const { id } = addContent(db, { container: "c", content: "something" })
    let now = Date.now()
    for (let i = 0; i < 6; i++) {
      await runner.runOnce(now)
      now += 3_600_000
    }
    const doc = db.select().from(documents).all().find((d) => d.id === id)!
    expect(doc.status).toBe("failed")
    expect(doc.error).toBe("gateway down")
  })
})

const fact = (f: string, keywords: string, kind: "static" | "dynamic" = "dynamic") => ({ fact: f, keywords, kind })
const CONTRACTOR = fact("Works as an independent contractor", "contractor freelance job kerja kontraktor")
const SIGNED = fact("Signed the Unifrutti contractor agreement on 2026-10-01", "contract agreement signed date kontrak perjanjian tanda tangan tanggal unifrutti")
const CITY = fact("Lives in Banjarbaru", "city home residence kota rumah tempat tinggal banjarbaru", "static")
const empty = { add: [], update: [], invalidate: [] }

function currentFacts(container: string) {
  return db
    .select()
    .from(memories)
    .where(isNull(memories.validTo))
    .all()
    .filter((m) => m.container === container)
    .map((m) => m.fact)
    .sort()
}

describe("dedupe", () => {
  // Cosine similarity of memoryText() vectors, measured with e5-small (q8).
  // Duplicates must clear DEDUPE_SIMILARITY and distinct facts must stay under it.
  it("keeps the threshold between measured duplicates and distinct facts", async () => {
    const pairs: [string, typeof CONTRACTOR, typeof CONTRACTOR, boolean][] = [
      ["restated signing date (0.986)", SIGNED, fact("Contract with Unifrutti was signed on October 1, 2026", "contract signing date timeline kontrak tanggal tanda tangan unifrutti"), true],
      ["paraphrase (0.980)", CITY, fact("Resides in Banjarbaru", CITY.keywords), true],
      ["punctuation and case (0.992)", CONTRACTOR, fact("works as an independent contractor.", CONTRACTOR.keywords), true],
      ["same topic, other value (0.970)", fact("Skilled in TypeScript", "programming language skill typescript bahasa pemrograman keahlian"), fact("Skilled in Python", "programming language skill python bahasa pemrograman keahlian"), false],
      ["same topic, other brand (0.962)", fact("Prefers Puma running shoes", "shoes sneakers running footwear sepatu lari puma"), fact("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas"), false],
      ["contract terms (0.884)", fact("Paid USD 50 per hour under the Unifrutti contract", "rate pay hourly salary tarif gaji per jam unifrutti"), fact("Unifrutti contract runs for 12 months", "contract term duration kontrak durasi bulan unifrutti"), false],
    ]
    for (const [label, a, b, duplicate] of pairs) {
      const [x, y] = await embed([memoryText(a), memoryText(b)], "passage")
      const sim = x!.reduce((s, v, i) => s + v * y![i]!, 0)
      expect(sim >= DEDUPE_SIMILARITY, `${label}: ${sim.toFixed(4)}`).toBe(duplicate)
    }
  })

  it("skips exact and near duplicates of existing memories and keeps distinct facts", async () => {
    await applyExtract(db, "zaki", null, [], { ...empty, add: [CONTRACTOR, SIGNED] })
    const stats = await applyExtract(db, "zaki", null, [], {
      ...empty,
      add: [
        // Exact fact, different keywords (0.975 by vector, caught by the text match).
        fact("Works as an independent contractor", "employment status independent pekerjaan lepas mandiri"),
        fact("Contract with Unifrutti was signed on October 1, 2026", "contract signing date timeline kontrak tanggal tanda tangan unifrutti"),
        CITY,
        fact("Skilled in TypeScript", "programming language skill typescript bahasa pemrograman keahlian"),
      ],
    })
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 0, deduped: 2 })
    expect(currentFacts("zaki")).toEqual([
      "Lives in Banjarbaru",
      "Signed the Unifrutti contractor agreement on 2026-10-01",
      "Skilled in TypeScript",
      "Works as an independent contractor",
    ])
  })

  it("skips duplicates inside one batch and duplicates of an update", async () => {
    await applyExtract(db, "zaki", null, [], { ...empty, add: [fact("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas")] })
    const refs = db.select().from(memories).all()
    const stats = await applyExtract(db, "zaki", null, refs, {
      add: [CITY, fact("Resides in Banjarbaru", CITY.keywords), fact("Prefers Puma running shoes", "shoes sneakers running footwear sepatu lari puma")],
      update: [{ ref: 1, fact: "Prefers Puma running shoes", keywords: "shoes sneakers running footwear sepatu lari puma" }],
      invalidate: [],
    })
    expect(stats).toEqual({ added: 1, updated: 1, invalidated: 0, deduped: 2 })
    expect(currentFacts("zaki")).toEqual(["Lives in Banjarbaru", "Prefers Puma running shoes"])
  })

  it("keeps facts that only differ by a number, and an add matching a memory being retired", async () => {
    await applyExtract(db, "zaki", null, [], { ...empty, add: [SIGNED, CONTRACTOR] })
    const refs = db.select().from(memories).all()
    const contractorRef = refs.findIndex((m) => m.fact === CONTRACTOR.fact) + 1
    const stats = await applyExtract(db, "zaki", null, refs, {
      add: [fact("Signed the Unifrutti contractor agreement on 2026-11-15", SIGNED.keywords), CONTRACTOR],
      update: [],
      invalidate: [contractorRef],
    })
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 1, deduped: 0 })
    expect(currentFacts("zaki")).toHaveLength(3)
  })

  it("never dedupes across containers", async () => {
    await applyExtract(db, "a", null, [], { ...empty, add: [CONTRACTOR] })
    const stats = await applyExtract(db, "b", null, [], { ...empty, add: [CONTRACTOR] })
    expect(stats.deduped).toBe(0)
    expect(currentFacts("a")).toEqual([CONTRACTOR.fact])
    expect(currentFacts("b")).toEqual([CONTRACTOR.fact])
  })

  it("records deduped in the document's extract stats", async () => {
    const { chat } = scripted([
      { add: [CONTRACTOR] },
      { add: [fact("works as an independent contractor.", CONTRACTOR.keywords), fact("Works as a contractor for Unifrutti", "contractor unifrutti client job kerja kontraktor klien")] },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    addContent(db, { container: "zaki", content: "I work as an independent contractor." })
    await drain(runner)
    const { id } = addContent(db, { container: "zaki", content: "Independent contractor agreement with Unifrutti." })
    await drain(runner)
    const doc = db.select().from(documents).all().find((d) => d.id === id)!
    expect((doc.metadata as { extract: unknown }).extract).toMatchObject({ added: 1, updated: 0, invalidated: 0, deduped: 1 })
  })
})
