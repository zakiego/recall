import fs from "node:fs"
import path from "node:path"
import { eq } from "drizzle-orm"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ask, NOTHING_STORED, resolveCitations, type AskDeps } from "./ask"
import { openDb, type DB } from "./db"
import { jobs } from "./db/schema"
import { chatJsonWithModel, GatewayError } from "./gateway/client"
import { HttpError } from "./http"
import { JobRunner } from "./jobs/runner"
import { addMemoryDirect } from "./memories"
import { createHandlers } from "./pipeline"

const fixture = (name: string) => fs.readFileSync(path.join(import.meta.dirname, "gateway", "fixtures", name), "utf8")
const sse = (text: string) => new Response(text, { headers: { "content-type": "text/event-stream" } })

const env = { ...process.env }
let db: DB
let runner: JobRunner | undefined

beforeEach(() => {
  db = openDb(":memory:")
  process.env.GATEWAY_KEY = "zkg_test"
  process.env.GATEWAY_URL = "http://gateway.test"
  process.env.EXTRACT_MODEL = "claude-haiku-5-5"
  delete process.env.RECALL_JOBS
})
afterEach(async () => {
  await runner?.stop()
  runner = undefined
  process.env = { ...env }
})

function start(chat: AskDeps["chat"]) {
  runner = new JobRunner(db, createHandlers(db, undefined, { chat, now: () => new Date("2026-11-09") }), () => {})
  runner.start()
}

// Plays the model with a fixed reply and keeps the prompts it was sent.
function scripted(reply: unknown, model = "claude-haiku-5-5") {
  const prompts: string[] = []
  const chat = vi.fn(async (req: { prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
    prompts.push(req.prompt)
    return { data: req.schema.parse(reply), model }
  })
  return { chat: chat as unknown as AskDeps["chat"], prompts, calls: chat }
}

async function seed() {
  await addMemoryDirect(db, { container: "me", fact: "Wears Puma running shoes", keywords: "shoes sneakers sepatu lari", kind: "dynamic" })
  await addMemoryDirect(db, { container: "me", fact: "Lives in Banjarbaru", keywords: "city home kota tinggal", kind: "static" })
  await addMemoryDirect(db, { container: "other", fact: "Wears Nike shoes", keywords: "shoes sepatu", kind: "dynamic" })
}

// The fact the prompt showed under a ref, e.g. "m2" -> "Lives in Banjarbaru".
function factFor(prompt: string, ref: string) {
  return prompt.match(new RegExp(`^\\[${ref}\\] \\([a-z]+, [\\d-]+\\) (.+)$`, "m"))?.[1]
}

describe("resolveCitations", () => {
  const sources = new Map([
    ["m1", { type: "memory" as const, id: "mem_a", text: "Wears Puma" }],
    ["m2", { type: "memory" as const, id: "mem_b", text: "Lives in Banjarbaru" }],
    ["c1", { type: "chunk" as const, id: "chk_a", text: "excerpt", documentId: "doc_a", documentTitle: "Notes" }],
  ])

  it("numbers refs by first use and maps them to real ids", () => {
    const out = resolveCitations("Lives in Banjarbaru [m2]. Wears Puma [m1][c1], still [M2].", sources)
    expect(out.answer).toBe("Lives in Banjarbaru [1]. Wears Puma [2][3], still [1].")
    expect(out.citations.map((c) => [c.ref, c.id])).toEqual([
      [1, "mem_b"],
      [2, "mem_a"],
      [3, "chk_a"],
    ])
  })

  it("drops refs the model made up, from the text and the list", () => {
    const out = resolveCitations("Wears Puma [m1, m7]. Owns a cat [m9].", sources)
    expect(out.answer).toBe("Wears Puma [1]. Owns a cat.")
    expect(out.citations).toHaveLength(1)
  })

  it("leaves brackets that aren't refs alone", () => {
    expect(resolveCitations("He said [sic] it [m1].", sources).answer).toBe("He said [sic] it [1].")
  })
})

describe("ask", () => {
  it("answers through the job queue from a recorded gateway stream", async () => {
    await seed()
    const fetchMock = vi.fn(async () => sse(fixture("ask-ok.sse")))
    start((req) => chatJsonWithModel(req, fetchMock as typeof fetch))

    const res = await ask(db, { container: "me", question: "What shoes do they wear and where do they live?" })

    const body = JSON.parse((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body as string)
    const prompt: string = body.messages[0].parts[0].text
    expect(prompt).not.toContain("Nike")
    expect(prompt).not.toContain("mem_")
    expect(res.answer).toBe("They wear Puma running shoes [1] and live in Banjarbaru [2].")
    expect(res.covered).toBe(true)
    expect(res.model).toBe("claude-haiku-5-5")
    expect(res.citations).toHaveLength(2)
    // Each footnote is the row the prompt numbered m1 / m2, not a model-made id.
    const rows = db.$client.prepare("SELECT id, fact FROM memories WHERE container = 'me'").all() as { id: string; fact: string }[]
    for (const [i, ref] of ["m1", "m2"].entries()) {
      const fact = factFor(prompt, ref)
      expect(res.citations[i]).toMatchObject({ ref: i + 1, type: "memory", text: fact, id: rows.find((r) => r.fact === fact)!.id })
    }
    expect(db.select().from(jobs).where(eq(jobs.type, "ask")).get()?.status).toBe("done")
  })

  it("passes through a 'not in memory' answer with no citations", async () => {
    await seed()
    const { chat } = scripted({ answer: "Nothing in memory says where they work.", covered: false })
    start(chat)
    const res = await ask(db, { container: "me", question: "Where do they work?" })
    expect(res).toMatchObject({ answer: "Nothing in memory says where they work.", covered: false, citations: [] })
  })

  it("doesn't call the gateway for an empty container", async () => {
    const { chat, calls } = scripted({ answer: "x" })
    start(chat)
    const res = await ask(db, { container: "empty", question: "anything?" })
    expect(res).toMatchObject({ answer: NOTHING_STORED, covered: false, citations: [], model: null })
    expect(calls).not.toHaveBeenCalled()
  })

  it("times out with 504 and cancels the queued job", async () => {
    // No runner: the job never runs.
    const err = await ask(db, { container: "me", question: "anything?" }, { timeoutMs: 50 }).catch((e) => e)
    expect(err).toBeInstanceOf(HttpError)
    expect(err).toMatchObject({ status: 504, type: "timeout" })
    expect(err.message).toMatch(/No answer within 0 s/)
    const job = db.select().from(jobs).where(eq(jobs.type, "ask")).get()!
    expect(job.status).toBe("failed")
  })

  it("turns a gateway failure into a 502 and doesn't retry", async () => {
    await seed()
    const chat = vi.fn(async () => {
      throw new GatewayError("all slots busy", 429, "overloaded", 10_000)
    })
    start(chat as unknown as AskDeps["chat"])
    const err = await ask(db, { container: "me", question: "shoes?" }).catch((e) => e)
    expect(err).toMatchObject({ status: 502, type: "gateway_error" })
    expect(chat).toHaveBeenCalledOnce()
    expect(db.select().from(jobs).where(eq(jobs.type, "ask")).get()?.status).toBe("failed")
  })

  it("says so plainly when the gateway can't be reached", async () => {
    await seed()
    start((async () => {
      throw new TypeError("fetch failed")
    }) as unknown as AskDeps["chat"])
    await expect(ask(db, { container: "me", question: "shoes?" })).rejects.toMatchObject({ status: 502, type: "gateway_unreachable" })
  })

  it("refuses with 503 when the job loop is off", async () => {
    process.env.RECALL_JOBS = "off"
    await expect(ask(db, { container: "me", question: "x" })).rejects.toMatchObject({ status: 503 })
  })
})
