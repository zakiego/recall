import { isNull } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { addContent } from "./api"
import { openDb, type DB } from "./db"
import { documents, jobs, memories, profiles } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { memoryChain } from "./memories"
import { createHandlers } from "./pipeline"
import { hybridSearch } from "./search"
import { embedOne } from "./embed"

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
