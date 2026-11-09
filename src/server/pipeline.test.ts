import { isNull } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { addContent } from "./api"
import { openDb, type DB } from "./db"
import { chunks, documents, jobs, memories, profiles } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { memoryChain } from "./memories"
import { applyExtract, createHandlers, DEDUPE_SIMILARITY } from "./pipeline"
import { hybridSearch } from "./search"
import { embed, embedOne } from "./embed"
import { memoryText } from "./memories"

const W = "ws_test"
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

    addContent(db, W, { container: C, content: "I love my Adidas shoes." })
    await drain(runner)
    addContent(db, W, { container: C, content: "I live in Banjarbaru." })
    await drain(runner)
    addContent(db, W, { container: C, content: "I switched to Puma, done with Adidas." })
    await drain(runner)

    // The third extract saw the Adidas memory as ref 1.
    expect(prompts.at(-2) ?? "").toMatch(/1\. \[dynamic\] Loves Adidas shoes/)

    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current.map((m) => m.fact).sort()).toEqual(["Lives in Banjarbaru", "Switched to Puma shoes"])

    const puma = current.find((m) => m.fact.includes("Puma"))!
    const chain = memoryChain(db, W, puma.id)
    expect(chain.map((m) => m.fact)).toEqual(["Loves Adidas shoes", "Switched to Puma shoes"])
    expect(chain[0]!.validTo).not.toBeNull()

    // Search only returns the current version.
    const hits = hybridSearch(db.$client, { workspaceId: W, container: C, q: "Adidas shoes", vector: await embedOne("Adidas shoes", "query") })
    expect(hits.map((h) => h.text)).not.toContain("Loves Adidas shoes")
    expect(hits[0]?.text).toBe("Switched to Puma shoes")

    expect(db.select().from(documents).all().every((d) => d.status === "done")).toBe(true)
    expect(db.select().from(profiles).get()).toMatchObject({ workspaceId: W, container: C, staticText: "Static summary." })
    expect(db.select().from(memories).all().every((m) => m.workspaceId === W)).toBe(true)
    expect(db.select().from(jobs).all().every((j) => j.workspaceId === W)).toBe(true)
    expect(db.select().from(jobs).all().every((j) => j.status === "done")).toBe(true)
  })

  it("invalidates, ignores unknown refs, and handles chat input", async () => {
    const { chat } = scripted([
      { add: [{ fact: "Works at Speechify", keywords: "job kerja", kind: "dynamic" }] },
      { invalidate: [1, 7], update: [{ ref: 9, fact: "ghost", keywords: "" }] },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, W, { container: "c", messages: [{ role: "user", content: "I work at Speechify" }] })
    await drain(runner)
    addContent(db, W, { container: "c", messages: [{ role: "user", content: "I quit my job" }] })
    await drain(runner)
    expect(db.select().from(memories).where(isNull(memories.validTo)).all()).toHaveLength(0)
    expect(db.select().from(memories).all()).toHaveLength(1)
  })

  it("drops sensitive facts before writing and counts them", async () => {
    const { chat } = scripted([
      {
        add: [
          { fact: "Applied for a backend role at Acme", keywords: "job lamaran kerja", kind: "dynamic" },
          { fact: "Passport number is C1234567", keywords: "paspor passport", kind: "static" },
          { fact: "Lives at Jl. Mawar No. 5, Banjarbaru", keywords: "alamat rumah", kind: "static" },
        ],
      },
      {
        update: [
          { ref: 1, fact: "NIK is 3171011508900001", keywords: "ktp nik" },
          { ref: 2, fact: "Pays with card 4111 1111 1111 1111", keywords: "kartu kredit" },
        ],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    const first = addContent(db, W, { container: "c", content: "Job email: application to Acme, passport C1234567 attached." })
    await drain(runner)
    const second = addContent(db, W, { container: "c", content: "Form: NIK 3171011508900001, card 4111 1111 1111 1111." })
    await drain(runner)

    const facts = db.select().from(memories).all().map((m) => m.fact).sort()
    // The street address is the prompt's job; the code guard only blocks patterns it can check.
    expect(facts).toEqual(["Applied for a backend role at Acme", "Lives at Jl. Mawar No. 5, Banjarbaru"])
    expect(db.select().from(memories).where(isNull(memories.validTo)).all()).toHaveLength(2)

    const stats = (id: string) => (db.select().from(documents).all().find((d) => d.id === id)!.metadata as { extract: unknown }).extract
    expect(stats(first.id)).toMatchObject({ added: 2, updated: 0, redacted: 1 })
    expect(stats(second.id)).toMatchObject({ added: 0, updated: 0, redacted: 2 })
  })

  it("drops a sensitive conflict and leaves the memory unflagged", async () => {
    const { chat } = scripted([
      { add: [{ fact: "Passport is held by the employer", keywords: "paspor passport", kind: "dynamic" }] },
      {
        conflict: [{ ref: 1, fact: "Passport number is C1234567", keywords: "paspor", note: "unclear" }],
        invalidate: [1],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, W, { container: "c", content: "HR keeps my passport." })
    await drain(runner)
    const second = addContent(db, W, { container: "c", content: "Passport C1234567 attached." })
    await drain(runner)

    // The dropped conflict still outranks "invalidate", so the memory stays as it was.
    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current.map((m) => [m.fact, m.reviewNote])).toEqual([["Passport is held by the employer", null]])
    const doc = db.select().from(documents).all().find((d) => d.id === second.id)!
    expect((doc.metadata as { extract: unknown }).extract).toMatchObject({ conflicts: 0, invalidated: 0, redacted: 1 })
  })

  it("masks keys and card numbers in stored text, chunks and the prompt", async () => {
    const { chat, prompts } = scripted([{ add: [] }, { add: [] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    const apiKey = ["rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2"].join("")
    const plain = addContent(db, W, { container: "c", content: `Staging key ${apiKey}, card 4111 1111 1111 1111. Call me at 0812-3456-7890.` })
    const chatDoc = addContent(db, W, { container: "c", messages: [{ role: "user", content: `my key is ${apiKey}` }] })
    await drain(runner)

    const stored = db.select().from(chunks).all().map((c) => c.text).join("\n")
    expect(stored).not.toContain(apiKey)
    expect(stored).not.toContain("4111 1111 1111 1111")
    expect(stored).toContain("Staging key [redacted:secret], card [redacted:card]. Call me at 0812-3456-7890.")
    expect(prompts.join("\n")).not.toContain(apiKey)

    const doc = (id: string) => db.select().from(documents).all().find((d) => d.id === id)!
    expect(doc(plain.id).content).not.toContain(apiKey)
    expect(doc(plain.id).metadata).toMatchObject({ masked: 2 })
    expect(JSON.stringify(doc(chatDoc.id).metadata)).not.toContain(apiKey)

    // Indexed text is masked too: the key finds nothing.
    const hits = hybridSearch(db.$client, { workspaceId: W, container: "c", q: apiKey, vector: await embedOne(apiKey, "query"), include: ["chunks"] })
    expect(hits.map((h) => h.text).join("\n")).not.toContain(apiKey)
  })

  it("keeps both sides of an unclear conflict and flags them for review", async () => {
    const { chat, prompts } = scripted([
      { add: [{ fact: "Salary is paid weekly after timesheet approval", keywords: "salary pay gaji", kind: "dynamic" }] },
      {
        conflict: [
          {
            ref: 1,
            fact: "Salary is paid on the last Friday of the month",
            keywords: "salary pay gaji",
            note: "Email says monthly on the last Friday; the contract says weekly after timesheet approval",
          },
          { ref: 5, fact: "ghost", keywords: "", note: "unknown ref" },
        ],
        // A conflict wins over an invalidate of the same memory.
        invalidate: [1],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, W, { container: "c", content: "Contract: salary is paid weekly after timesheet approval." })
    await drain(runner)
    const { id: emailId } = addContent(db, W, { container: "c", content: "Email: salary goes out on the last Friday of the month." })
    await drain(runner)

    expect(prompts.at(-2) ?? "").toMatch(/1\. \[dynamic\] Salary is paid weekly/)

    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current).toHaveLength(2)
    const weekly = current.find((m) => m.fact.includes("weekly"))!
    const monthly = current.find((m) => m.fact.includes("Friday"))!
    expect(monthly).toMatchObject({ kind: "dynamic", sourceDocumentId: emailId, supersedesId: null })
    expect(monthly.reviewNote).toBe(
      'Conflicts with "Salary is paid weekly after timesheet approval": Email says monthly on the last Friday; the contract says weekly after timesheet approval',
    )
    expect(weekly.reviewNote).toMatch(/^Conflicts with "Salary is paid on the last Friday of the month": Email says/)

    const email = db.select().from(documents).all().find((d) => d.id === emailId)!
    expect(email.metadata).toMatchObject({ extract: { added: 0, updated: 0, invalidated: 0, conflicts: 1 } })
  })

  it("lets a clear update win over a conflict on the same memory", async () => {
    const { chat } = scripted([
      { add: [{ fact: "Works at Speechify", keywords: "job kerja", kind: "dynamic" }] },
      {
        update: [{ ref: 1, fact: "Works at Anthropic", keywords: "job kerja" }],
        conflict: [{ ref: 1, fact: "Works at Google", keywords: "job kerja", note: "unclear" }],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, W, { container: "c", content: "I work at Speechify" })
    await drain(runner)
    addContent(db, W, { container: "c", content: "I moved to Anthropic" })
    await drain(runner)
    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current.map((m) => [m.fact, m.reviewNote])).toEqual([["Works at Anthropic", null]])
  })

  it("appends a second conflict note instead of overwriting the first", async () => {
    const { chat } = scripted([
      { add: [{ fact: "Lives in Banjarbaru", keywords: "city kota", kind: "static" }] },
      {
        conflict: [
          { ref: 1, fact: "Lives in Martapura", keywords: "city kota", note: "A says Martapura" },
          { ref: 1, fact: "Lives in Banjarmasin", keywords: "city kota", note: "B says Banjarmasin" },
        ],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    addContent(db, W, { container: "c", content: "I live in Banjarbaru" })
    await drain(runner)
    addContent(db, W, { container: "c", content: "Two letters disagree about where I live" })
    await drain(runner)
    const current = db.select().from(memories).where(isNull(memories.validTo)).all()
    expect(current).toHaveLength(3)
    expect(current.every((m) => m.kind === "static" && m.reviewNote)).toBe(true)
    const original = current.find((m) => m.fact === "Lives in Banjarbaru")!
    expect(original.reviewNote!.split("\n")).toEqual([
      'Conflicts with "Lives in Martapura": A says Martapura',
      'Conflicts with "Lives in Banjarmasin": B says Banjarmasin',
    ])
  })

  it("marks the document failed after the last retry", async () => {
    const chat = vi.fn(async () => {
      throw new Error("gateway down")
    }) as unknown as typeof chatJson
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    const { id } = addContent(db, W, { container: "c", content: "something" })
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
const empty = { add: [], update: [], invalidate: [], conflict: [] }

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
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CONTRACTOR, SIGNED] })
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], {
      ...empty,
      add: [
        // Exact fact, different keywords (0.975 by vector, caught by the text match).
        fact("Works as an independent contractor", "employment status independent pekerjaan lepas mandiri"),
        fact("Contract with Unifrutti was signed on October 1, 2026", "contract signing date timeline kontrak tanggal tanda tangan unifrutti"),
        CITY,
        fact("Skilled in TypeScript", "programming language skill typescript bahasa pemrograman keahlian"),
      ],
    })
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 0, deduped: 2, conflicts: 0, redacted: 0 })
    expect(currentFacts("zaki")).toEqual([
      "Lives in Banjarbaru",
      "Signed the Unifrutti contractor agreement on 2026-10-01",
      "Skilled in TypeScript",
      "Works as an independent contractor",
    ])
  })

  it("skips duplicates inside one batch and duplicates of an update", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [fact("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas")] })
    const refs = db.select().from(memories).all()
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      add: [CITY, fact("Resides in Banjarbaru", CITY.keywords), fact("Prefers Puma running shoes", "shoes sneakers running footwear sepatu lari puma")],
      update: [{ ref: 1, fact: "Prefers Puma running shoes", keywords: "shoes sneakers running footwear sepatu lari puma" }],
      invalidate: [],
      conflict: [],
    })
    expect(stats).toEqual({ added: 1, updated: 1, invalidated: 0, deduped: 2, conflicts: 0, redacted: 0 })
    expect(currentFacts("zaki")).toEqual(["Lives in Banjarbaru", "Prefers Puma running shoes"])
  })

  it("keeps facts that only differ by a number, and an add matching a memory being retired", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [SIGNED, CONTRACTOR] })
    const refs = db.select().from(memories).all()
    const contractorRef = refs.findIndex((m) => m.fact === CONTRACTOR.fact) + 1
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      add: [fact("Signed the Unifrutti contractor agreement on 2026-11-15", SIGNED.keywords), CONTRACTOR],
      update: [],
      invalidate: [contractorRef],
      conflict: [],
    })
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 1, deduped: 0, conflicts: 0, redacted: 0 })
    expect(currentFacts("zaki")).toHaveLength(3)
  })

  it("skips an add that restates a conflict fact but keeps the conflict", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CITY] })
    const refs = db.select().from(memories).all()
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      ...empty,
      add: [fact("Lives in Martapura", CITY.keywords)],
      conflict: [{ ref: 1, fact: "Lives in Martapura", keywords: CITY.keywords, note: "Letter says Martapura" }],
    })
    expect(stats).toEqual({ added: 0, updated: 0, invalidated: 0, deduped: 1, conflicts: 1, redacted: 0 })
    expect(currentFacts("zaki")).toEqual(["Lives in Banjarbaru", "Lives in Martapura"])
    expect(db.select().from(memories).all().every((m) => m.reviewNote)).toBe(true)
  })

  it("never dedupes across containers", async () => {
    await applyExtract(db, { workspaceId: W, container: "a" }, null, [], { ...empty, add: [CONTRACTOR] })
    const stats = await applyExtract(db, { workspaceId: W, container: "b" }, null, [], { ...empty, add: [CONTRACTOR] })
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
    addContent(db, W, { container: "zaki", content: "I work as an independent contractor." })
    await drain(runner)
    const { id } = addContent(db, W, { container: "zaki", content: "Independent contractor agreement with Unifrutti." })
    await drain(runner)
    const doc = db.select().from(documents).all().find((d) => d.id === id)!
    expect((doc.metadata as { extract: unknown }).extract).toMatchObject({ added: 1, updated: 0, invalidated: 0, deduped: 1 })
  })
})
