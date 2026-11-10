import { eq, isNull } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { addContent, patchMemory } from "./api"
import { openDb, type DB } from "./db"
import { chunks, documents, jobs, memories, profiles } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { addMemoryDirect, deleteDocument, editMemory, forgetMemory, insertMemory, memoryChain, supersedeMemory, versionCounts } from "./memories"
import { applyExtract, buildProfile, createHandlers, DEDUPE_SIMILARITY, documentDate, emailDate, factAsOf, MAX_INVALIDATIONS, PROFILE_DYNAMIC_FACTS, PROFILE_STATIC_FACTS } from "./pipeline"
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

  it("masks a key in the document title, on create and on ingest", async () => {
    const { chat } = scripted([{ add: [] }, { add: [] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    const apiKey = ["rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2"].join("")
    // The title comes from the first line of the content.
    const fromContent = addContent(db, W, { container: "c", content: `key ${apiKey}\nfor staging` })
    const doc = (id: string) => db.select().from(documents).all().find((d) => d.id === id)!
    expect(doc(fromContent.id).title).toBe("key [redacted:secret]")
    const explicit = addContent(db, W, { container: "c", content: "body", title: `deploy ${apiKey}` })
    expect(doc(explicit.id).title).toBe("deploy [redacted:secret]")
    // A row written before titles were masked is cleaned up when it's ingested.
    db.update(documents).set({ title: `old ${apiKey}` }).run()
    await drain(runner)
    expect(db.select().from(documents).all().map((d) => d.title)).toEqual(["old [redacted:secret]", "old [redacted:secret]"])
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

  describe("settling a conflict", () => {
    // Two current facts flagged against each other, the way extraction leaves them.
    async function conflictPair() {
      const { chat } = scripted([
        { add: [{ fact: "Works at Bubungan Digital", keywords: "job kerja", kind: "dynamic" }] },
        { conflict: [{ ref: 1, fact: "Works at Speechify", keywords: "job kerja", note: "two sources disagree" }] },
      ])
      const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
      addContent(db, W, { container: "c", content: "I work at Bubungan Digital" })
      await drain(runner)
      addContent(db, W, { container: "c", content: "I work at Speechify" })
      await drain(runner)
      const current = db.select().from(memories).where(isNull(memories.validTo)).all()
      return { a: current.find((m) => m.fact.includes("Bubungan"))!, b: current.find((m) => m.fact.includes("Speechify"))! }
    }
    const flags = () =>
      db
        .select()
        .from(memories)
        .where(isNull(memories.validTo))
        .all()
        .map((m) => [m.fact, m.reviewNote !== null, m.conflictsWithId !== null])
        .sort()

    it("links the two sides", async () => {
      const { a, b } = await conflictPair()
      expect(a.conflictsWithId).toBe(b.id)
      expect(b.conflictsWithId).toBe(a.id)
    })

    it("clears both when one is marked resolved", async () => {
      const { a } = await conflictPair()
      await patchMemory(db, W, a.id, { resolve: true })
      expect(flags()).toEqual([
        ["Works at Bubungan Digital", false, false],
        ["Works at Speechify", false, false],
      ])
    })

    it("clears the survivor when the other side is deleted", async () => {
      const { a } = await conflictPair()
      forgetMemory(db, W, a.id)
      expect(flags()).toEqual([["Works at Speechify", false, false]])
    })

    it("keeps a fact flagged while another conflict on it is still open", async () => {
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
      const byFact = (f: string) => db.select().from(memories).where(isNull(memories.validTo)).all().find((m) => m.fact === f)!
      const martapura = byFact("Lives in Martapura")
      // The original links to the newest conflict, Banjarmasin. Deleting that
      // one must leave the original flagged against Martapura.
      forgetMemory(db, W, byFact("Lives in Banjarmasin").id)
      expect(byFact("Lives in Banjarbaru")).toMatchObject({ conflictsWithId: martapura.id })
      expect(byFact("Lives in Banjarbaru").reviewNote).not.toBeNull()
      // Resolving the original settles everything that pointed at it.
      await patchMemory(db, W, byFact("Lives in Banjarbaru").id, { resolve: true })
      expect(flags()).toEqual([
        ["Lives in Banjarbaru", false, false],
        ["Lives in Martapura", false, false],
      ])
    })

    it("clears both when one side is edited", async () => {
      const { b } = await conflictPair()
      await editMemory(db, W, b.id, { fact: "Works at Speechify as a contractor" })
      expect(flags()).toEqual([
        ["Works at Bubungan Digital", false, false],
        ["Works at Speechify as a contractor", false, false],
      ])
    })
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

  it("writes nothing for a document deleted while its extract waits on the model", async () => {
    let id = ""
    const chat = vi.fn(async (req: { system: string; schema: { parse: (x: unknown) => unknown } }) => {
      if (req.system.includes("profile")) return req.schema.parse({ static: "", dynamic: "" })
      deleteDocument(db, W, id)
      return req.schema.parse({ add: [{ fact: "Bought a monitor", keywords: "monitor", kind: "dynamic" }] })
    }) as unknown as typeof chatJson
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    id = addContent(db, W, { container: "c", content: "I bought a monitor." }).id
    await drain(runner)
    expect(chat).toHaveBeenCalledOnce()
    expect(db.select().from(documents).all()).toHaveLength(0)
    expect(db.select().from(memories).all()).toHaveLength(0)
    expect(db.select().from(jobs).all().every((j) => j.status === "done")).toBe(true)
  })

  it("doesn't store chunks or queue an extract for a document deleted before its ingest ran", async () => {
    const { chat } = scripted([])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    const { id } = addContent(db, W, { container: "c", content: "I bought a monitor." })
    deleteDocument(db, W, id)
    await drain(runner)
    expect(db.select().from(chunks).all()).toHaveLength(0)
    expect(db.select().from(jobs).all().map((j) => [j.type, j.status])).toEqual([["ingest", "done"]])
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
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 0, deduped: 2, conflicts: 0, redacted: 0, stale: 0, cappedInvalidations: 0 })
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
    expect(stats).toEqual({ added: 1, updated: 1, invalidated: 0, deduped: 2, conflicts: 0, redacted: 0, stale: 0, cappedInvalidations: 0 })
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
    expect(stats).toEqual({ added: 2, updated: 0, invalidated: 1, deduped: 0, conflicts: 0, redacted: 0, stale: 0, cappedInvalidations: 0 })
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
    expect(stats).toEqual({ added: 0, updated: 0, invalidated: 0, deduped: 1, conflicts: 1, redacted: 0, stale: 0, cappedInvalidations: 0 })
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

describe("stale refs", () => {
  it("skips an update whose memory was forgotten after the refs were read", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CITY] })
    const refs = db.select().from(memories).all()
    // The user forgets the fact while the extract call is out at the gateway.
    forgetMemory(db, W, refs[0]!.id)
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      ...empty,
      update: [{ ref: 1, fact: "Lives in Martapura", keywords: CITY.keywords }],
    })
    expect(stats).toMatchObject({ updated: 0, stale: 1 })
    expect(currentFacts("zaki")).toEqual([])
    expect(db.select().from(memories).all()).toHaveLength(1)
  })

  it("never gives one memory two current successors", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CITY] })
    const refs = db.select().from(memories).all()
    const update = (fact: string) => applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, { ...empty, update: [{ ref: 1, fact, keywords: CITY.keywords }] })
    // Two extracts read the same refs, then both write.
    const [a, b] = await Promise.all([update("Lives in Martapura"), update("Lives in Banjarmasin")])
    expect(a.updated + b.updated).toBe(1)
    expect(a.stale + b.stale).toBe(1)
    expect(currentFacts("zaki")).toHaveLength(1)
    expect(db.select().from(memories).where(eq(memories.supersedesId, refs[0]!.id)).all()).toHaveLength(1)
  })

  it("editMemory returns null when the fact is forgotten mid-edit", async () => {
    const m = await addMemoryDirect(db, { workspaceId: W, container: "zaki", ...CITY })
    const editing = editMemory(db, W, m.id, { fact: "Lives in Martapura" })
    forgetMemory(db, W, m.id)
    expect(await editing).toBeNull()
    expect(currentFacts("zaki")).toEqual([])
  })
})

describe("patchMemory race", () => {
  it("answers 409 when the memory is forgotten while the edit is embedded", async () => {
    const m = await addMemoryDirect(db, { workspaceId: W, container: "zaki", ...CITY })
    const patching = patchMemory(db, W, m.id, { fact: "Lives in Martapura" }).catch((e) => e)
    forgetMemory(db, W, m.id)
    expect(await patching).toMatchObject({ status: 409, type: "not_current" })
  })
})

describe("invalidation cap", () => {
  it("closes at most MAX_INVALIDATIONS memories per window and counts the rest", async () => {
    const facts = ["Owns a cat", "Drinks oat milk", "Runs on Sundays", "Plays chess", "Reads sci-fi", "Uses a Kindle", "Grows chillies", "Bikes to work"]
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: facts.map((f) => fact(f, "")) })
    const refs = db.select().from(memories).all()
    expect(refs).toHaveLength(8)
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, { ...empty, invalidate: [1, 2, 3, 4, 5, 6, 7, 8] })
    expect(stats).toMatchObject({ invalidated: MAX_INVALIDATIONS, cappedInvalidations: 8 - MAX_INVALIDATIONS })
    expect(currentFacts("zaki")).toHaveLength(8 - MAX_INVALIDATIONS)
  })
})

describe("replays and repeated refs", () => {
  const CONFLICT = { ref: 1, fact: "Lives in Martapura", keywords: CITY.keywords, note: "Letter says Martapura" }

  it("doesn't write the same conflict twice when a document is replayed", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CITY] })
    const refs = db.select().from(memories).all()
    const first = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, { ...empty, conflict: [CONFLICT] })
    expect(first).toMatchObject({ conflicts: 1, deduped: 0 })
    // A retry of the same window sees the same refs and gets the same answer.
    const again = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, { ...empty, conflict: [CONFLICT] })
    expect(again).toMatchObject({ conflicts: 0, deduped: 1 })
    expect(currentFacts("zaki")).toEqual(["Lives in Banjarbaru", "Lives in Martapura"])
    const original = db.select().from(memories).where(eq(memories.fact, CITY.fact)).get()!
    expect(original.reviewNote!.split("\n")).toHaveLength(1)
  })

  it("treats a conflict with the same text as its memory as a no-op", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [CITY] })
    const refs = db.select().from(memories).all()
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      ...empty,
      conflict: [{ ref: 1, fact: "lives in  Banjarbaru.", keywords: CITY.keywords, note: "Same thing" }],
    })
    expect(stats).toMatchObject({ conflicts: 0, deduped: 1 })
    expect(currentFacts("zaki")).toEqual([CITY.fact])
    expect(db.select().from(memories).all().map((m) => [m.reviewNote, m.conflictsWithId])).toEqual([[null, null]])
  })

  it("ignores a repeated update ref, so an add it matches is kept", async () => {
    await applyExtract(db, { workspaceId: W, container: "zaki" }, null, [], { ...empty, add: [fact("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas")] })
    const refs = db.select().from(memories).all()
    const NIKE = fact("Prefers Nike basketball shoes", "shoes sneakers basketball footwear sepatu basket nike")
    const stats = await applyExtract(db, { workspaceId: W, container: "zaki" }, null, refs, {
      ...empty,
      add: [NIKE],
      update: [
        { ref: 1, fact: "Prefers Puma running shoes", keywords: "shoes sneakers running footwear sepatu lari puma" },
        // Never written (the first update for ref 1 wins), so it must not dedupe the add.
        { ref: 1, fact: NIKE.fact, keywords: NIKE.keywords },
      ],
    })
    expect(stats).toMatchObject({ added: 1, updated: 1, deduped: 0 })
    expect(currentFacts("zaki")).toEqual(["Prefers Nike basketball shoes", "Prefers Puma running shoes"])
  })
})

describe("document dates", () => {
  it("reads an email's own Date header, in the sender's calendar", () => {
    expect(emailDate("Subject: Hi\nFrom: a@b.c\nDate: Tue, 3 May 2016 23:30:00 -0700\n\nBody")).toBe("2016-05-03")
    expect(emailDate("Date: 7 Jan 2021 08:00:00 +0700")).toBe("2021-01-07")
    expect(emailDate("Subject: no date\n\nBody")).toBeNull()
    expect(emailDate("Date: 31 Feb 2021")).toBeNull()
  })

  it("uses the email's date over the import day, but a caller's date still wins", () => {
    const createdAt = Date.UTC(2026, 10, 10)
    const content = "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPlease pay."
    expect(documentDate({ sourceType: "email", content, metadata: {}, createdAt })).toBe("2021-08-01")
    expect(documentDate({ sourceType: "email", content, metadata: { documentDate: "2021-07-30" }, createdAt })).toBe("2021-07-30")
    // Only emails: a pasted note quoting a Date line is still dated by when it was added.
    expect(documentDate({ sourceType: "text", content, metadata: {}, createdAt })).toBe("2026-11-10")
  })

  it("tells the model how old an old email is", async () => {
    const { chat, prompts } = scripted([{ add: [] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-10") }), () => {})
    const { id } = addContent(db, W, { container: "c", content: "Please pay the hosting invoice by Friday." })
    // Email ingest only goes to Gmail when the text isn't stored yet, so store it as Gmail would give it.
    const email = "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPlease pay the hosting invoice by Friday."
    db.update(documents).set({ sourceType: "email", content: email }).where(eq(documents.id, id)).run()
    await drain(runner)
    expect(prompts[0]).toMatch(/DOCUMENT DATE: 2021-08-01 /)
    expect(prompts[0]).toMatch(/This document is about 5 years old: follow the "Old content" rules\./)
  })
})

describe("coding sessions", () => {
  it("marks a Claude Code session as one, and only that", async () => {
    const { chat, prompts } = scripted([{ add: [] }, { add: [] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-10") }), () => {})
    addContent(db, W, { container: "c", source_id: "claude-code:abc123", messages: [{ role: "user", content: "Open a draft PR for the r4 branch." }] })
    await drain(runner)
    addContent(db, W, { container: "c", messages: [{ role: "user", content: "I prefer pnpm over npm." }] })
    await drain(runner)
    const extracts = prompts.filter((p) => p.includes("NEW CONTENT"))
    expect(extracts[0]).toMatch(/SOURCE: a coding-agent session\. Follow the "Coding sessions" rules\./)
    expect(extracts[1]).not.toMatch(/SOURCE:/)
  })
})

describe("profile", () => {
  const vector = new Float32Array(384).fill(0.05)
  const day = (iso: string) => ({ date: iso, precision: "day" as const })

  function profilePrompts() {
    const prompts: string[] = []
    const chat = vi.fn(async (req: { prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
      prompts.push(req.prompt)
      return req.schema.parse({ static: "s", dynamic: "d" })
    })
    return { deps: { chat: chat as unknown as typeof chatJson, now: () => new Date("2026-11-10T12:00:00Z") }, prompts }
  }

  it("dates a fact by when it was true, capped at today", () => {
    expect(factAsOf({ event: null, due: null, document: "2016-05-03" }, "2026-11-10")).toBe("2016-05-03")
    expect(factAsOf({ event: "2016-01-01", due: null, document: "2026-11-02" }, "2026-11-10")).toBe("2026-11-02")
    expect(factAsOf({ event: null, due: "2021-08-01", document: "2021-07-20" }, "2026-11-10")).toBe("2021-08-01")
    expect(factAsOf({ event: "2026-11-05", due: "2028-11-05", document: "2026-11-05" }, "2026-11-10")).toBe("2026-11-10")
  })

  it("gives static facts their own budget, however many dynamic facts there are", async () => {
    const insert = (fact: string, kind: "static" | "dynamic") => insertMemory(db, { workspaceId: W, container: "c", fact, keywords: "", kind }, vector)
    for (let i = 0; i < PROFILE_DYNAMIC_FACTS + 30; i++) insert(`Dynamic ${i}`, "dynamic")
    for (let i = 0; i < PROFILE_STATIC_FACTS + 5; i++) insert(`Static ${i}`, "static")
    const { deps, prompts } = profilePrompts()
    await buildProfile(db, W, "c", deps)
    expect(prompts[0]!.match(/^- \[static\]/gm)).toHaveLength(PROFILE_STATIC_FACTS)
    expect(prompts[0]!.match(/^- \[dynamic, as of /gm)).toHaveLength(PROFILE_DYNAMIC_FACTS)
    expect(prompts[0]).toMatch(/^Today is 2026-11-10\./)
  })

  it("ranks dynamic facts by when they were true, not by when they were stored", async () => {
    const doc = (id: string, values: Partial<typeof documents.$inferInsert>) =>
      db.insert(documents).values({ id, workspaceId: W, container: "c", sourceType: "text", status: "done", createdAt: Date.UTC(2026, 10, 9), ...values }).run()
    doc("doc_old_email", { sourceType: "email", content: "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPay by Friday." })
    doc("doc_dated", { metadata: { documentDate: "2026-10-01" } })
    const insert = (fact: string, extra: Record<string, unknown>, createdAt: number) => {
      const row = insertMemory(db, { workspaceId: W, container: "c", fact, keywords: "", kind: "dynamic", ...extra }, vector)
      db.update(memories).set({ createdAt }).where(eq(memories.id, row.id)).run()
    }
    // Stored last, but from a 2021 email.
    insert("Hosting invoice unpaid", { sourceDocumentId: "doc_old_email" }, Date.UTC(2026, 10, 10))
    insert("Training for a marathon", { sourceDocumentId: "doc_dated" }, Date.UTC(2026, 10, 1))
    insert("Contract with Vorneo runs to 2028", { event: day("2026-11-05"), due: day("2028-11-05") }, Date.UTC(2026, 9, 1))
    insert("Added by hand", {}, Date.UTC(2026, 10, 3))
    const { deps, prompts } = profilePrompts()
    await buildProfile(db, W, "c", deps)
    expect(prompts[0]!.split("\n").filter((l) => l.startsWith("- "))).toEqual([
      "- [dynamic, as of 2026-11-10] Contract with Vorneo runs to 2028",
      "- [dynamic, as of 2026-11-03] Added by hand",
      "- [dynamic, as of 2026-10-01] Training for a marathon",
      "- [dynamic, as of 2021-08-01] Hosting invoice unpaid",
    ])
  })
})

describe("long version chains", () => {
  it("counts and walks past 100 versions", () => {
    const vector = new Float32Array(384).fill(0.05)
    let row = insertMemory(db, { workspaceId: W, container: "c", fact: "Working on day 1", keywords: "", kind: "dynamic" }, vector)
    for (let i = 2; i <= 130; i++) row = supersedeMemory(db, row, { fact: `Working on day ${i}`, keywords: "" }, vector)!
    expect(versionCounts(db, W, [row.id])[row.id]).toBe(129)
    const chain = memoryChain(db, W, row.id)
    expect(chain).toHaveLength(130)
    expect(chain[0]!.fact).toBe("Working on day 1")
    // A shorter walk still knows where it starts.
    const window = memoryChain(db, W, row.id, 10)
    expect(window[0]!.fact).toBe("Working on day 120")
    expect(versionCounts(db, W, [window[0]!.id])[window[0]!.id]).toBe(119)
  })
})
