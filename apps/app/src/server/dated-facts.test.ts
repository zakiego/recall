// Dated facts: extraction with a scripted model, relative dates, versions,
// edits, upcoming, search, Ask and MCP outputs, and the one-off backfill.
import { eq, isNull } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { addContent, addInput, patchInput, patchMemory, search, upcoming, upcomingInput } from "./api"
import { answerQuestion, type AskDeps } from "./ask"
import { backfillBatch, countUndated } from "./backfill"
import { runCli } from "./admin-cli"
import { openDb, type DB } from "./db"
import { documents, jobs, memories } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { extractPrompt } from "./gateway/prompts"
import { extractResult } from "./gateway/schemas"
import { JobRunner } from "./jobs/runner"
import { handleMcp } from "./mcp"
import { addMemoryDirect, editMemory, getMemory, memoryChain } from "./memories"
import { createHandlers, documentDate } from "./pipeline"
import { createWorkspace } from "./users"

const W = "ws_test"
const C = "zaki"
let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

// Plays the model: answers each call from a script and records what it was sent.
function scripted(replies: unknown[]) {
  const prompts: string[] = []
  const systems: string[] = []
  const chat = vi.fn(async (req: { system: string; prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
    prompts.push(req.prompt)
    systems.push(req.system)
    if (req.system.includes("short profile")) return req.schema.parse({ static: "", dynamic: "" })
    return req.schema.parse(replies.shift() ?? { add: [] })
  })
  return { chat: chat as unknown as typeof chatJson, prompts, systems, calls: chat }
}

async function drain(runner: JobRunner) {
  for (let i = 0; i < 50; i++) if (!(await runner.runOnce())) return
}

const current = () => db.select().from(memories).where(isNull(memories.validTo)).all()

describe("extract schema", () => {
  it("parses dates by precision and drops what it can't read", () => {
    const r = extractResult.parse({
      add: [
        { fact: "a", keywords: "", kind: "dynamic", event_date: "2026-11-05", due_date: "2027-12" },
        { fact: "b", keywords: "", kind: "dynamic", due_date: "next friday", event_date: 20261105 },
        { fact: "c", keywords: "", kind: "dynamic", due_date: null },
      ],
      update: [{ ref: 1, fact: "d", keywords: "", due_date: "2026-02-30" }],
    })
    expect(r.add[0]).toMatchObject({ event_date: { date: "2026-11-05", precision: "day" }, due_date: { date: "2027-12-01", precision: "month" } })
    expect(r.add[1]!.due_date).toBeUndefined()
    expect(r.add[1]!.event_date).toBeUndefined()
    expect(r.add[2]!.due_date).toBeNull()
    expect(r.update[0]!.due_date).toBeUndefined()
  })
})

describe("extraction", () => {
  it("stores the dates the model returns, with precision", async () => {
    const { chat } = scripted([
      {
        add: [
          { fact: "Contract with Vorneo runs until the end of 2027", keywords: "contract kontrak", kind: "dynamic", event_date: "2026-11-05", due_date: "2027-12-31" },
          { fact: "Sister's wedding is in March 2027", keywords: "wedding nikah", kind: "dynamic", due_date: "2027-03" },
          { fact: "Prefers Puma running shoes", keywords: "shoes sepatu", kind: "dynamic" },
        ],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    addContent(db, W, { container: C, content: "Signed with Vorneo. Wedding in March. Puma shoes." })
    await drain(runner)
    const byFact = Object.fromEntries(current().map((m) => [m.fact.split(" ")[0], m]))
    expect(byFact.Contract).toMatchObject({ eventDate: "2026-11-05", eventPrecision: "day", dueDate: "2027-12-31", duePrecision: "day" })
    expect(byFact["Sister's"]).toMatchObject({ eventDate: null, dueDate: "2027-03-01", duePrecision: "month" })
    expect(byFact.Prefers).toMatchObject({ eventDate: null, eventPrecision: null, dueDate: null, duePrecision: null })
  })

  it("an update keeps the old dates unless it sends new ones or null", async () => {
    const { chat } = scripted([
      {
        add: [
          { fact: "Office lease with Ruang Kerja ends 2027-06-30", keywords: "office lease sewa kantor", kind: "dynamic", event_date: "2025-07-01", due_date: "2027-06-30" },
        ],
      },
      { update: [{ ref: 1, fact: "Office lease with Ruang Kerja ends mid 2027, rent paid quarterly", keywords: "office lease sewa kantor quarterly" }] },
      { update: [{ ref: 1, fact: "Office lease with Ruang Kerja extended to the end of 2028", keywords: "office lease sewa kantor", due_date: "2028-12-31" }] },
      { update: [{ ref: 1, fact: "Office lease with Ruang Kerja is now month to month", keywords: "office lease sewa kantor", due_date: null }] },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    for (const text of ["Lease signed.", "Rent is quarterly now.", "We extended the lease to end of 2028.", "Lease is month to month now."]) {
      addContent(db, W, { container: C, content: `Office lease with Ruang Kerja. ${text}` })
      await drain(runner)
    }
    const [now] = current()
    const chain = memoryChain(db, W, now!.id)
    expect(chain.map((m) => [m.eventDate, m.dueDate])).toEqual([
      ["2025-07-01", "2027-06-30"],
      ["2025-07-01", "2027-06-30"], // left out: carried over
      ["2025-07-01", "2028-12-31"], // new due date, event date carried over
      ["2025-07-01", null], // null clears it
    ])
  })

  it("a conflict fact gets its own dates", async () => {
    const m = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Car insurance renews on 2026-12-01", keywords: "car insurance asuransi", kind: "dynamic", due: { date: "2026-12-01", precision: "day" } })
    const { chat } = scripted([{ conflict: [{ ref: 1, fact: "Car insurance renews on 2026-12-15", keywords: "car insurance asuransi", note: "Dates differ", due_date: "2026-12-15" }] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
    addContent(db, W, { container: C, content: "Car insurance renews 15 December." })
    await drain(runner)
    const rows = current()
    expect(rows.find((r) => r.id === m.id)?.dueDate).toBe("2026-12-01")
    expect(rows.find((r) => r.id !== m.id)).toMatchObject({ dueDate: "2026-12-15", duePrecision: "day" })
  })
})

describe("relative dates resolve against the document date", () => {
  it("sends the caller's document_date, not today, and the extract system explains it", async () => {
    const { chat, prompts, systems } = scripted([{ add: [{ fact: "Dentist appointment", keywords: "dentist dokter gigi", kind: "dynamic", due_date: "2026-11-06" }] }])
    // Extraction runs on 2026-11-20 for an email written on Tuesday 2026-11-03.
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-20T03:00:00Z") }), () => {})
    addContent(db, W, { container: C, content: "Dentist appointment next Friday.", document_date: "2026-11-03" })
    await drain(runner)
    const prompt = prompts.find((p) => p.includes("Dentist"))!
    expect(prompt).toContain("Today is 2026-11-20.")
    expect(prompt).toContain("DOCUMENT DATE: 2026-11-03")
    expect(systems[0]).toMatch(/Resolve relative dates .* against the DOCUMENT DATE, never against today's date/)
    expect(current()[0]).toMatchObject({ dueDate: "2026-11-06" })
  })

  it("falls back to metadata.date, then the day the document was added, never extraction time", () => {
    const createdAt = Date.UTC(2026, 9, 1, 12)
    expect(documentDate({ metadata: { documentDate: "2026-11-03" }, createdAt })).toBe("2026-11-03")
    expect(documentDate({ metadata: { date: "2026-10-20T08:00:00Z" }, createdAt })).toBe("2026-10-20")
    // A month is not a day to count from.
    expect(documentDate({ metadata: { date: "2026-10" }, createdAt })).toBe("2026-10-01")
    expect(documentDate({ metadata: { date: "Tue, 3 Nov 2026" }, createdAt })).toBe("2026-10-01")
    expect(documentDate({ metadata: null, createdAt })).toBe("2026-10-01")
  })

  it("a retried extract reads the same document date", async () => {
    const { chat, prompts } = scripted([{ add: [] }, { add: [] }])
    let today = new Date("2026-11-05")
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => today }), () => {})
    const res = addContent(db, W, { container: C, content: "Payment due in two weeks." })
    db.update(documents).set({ createdAt: Date.UTC(2026, 10, 1) }).where(eq(documents.id, res.id)).run()
    await drain(runner)
    today = new Date("2026-11-30")
    db.insert(jobs).values({ workspaceId: W, type: "extract", payload: { documentId: res.id } }).run()
    await drain(runner)
    const dated = prompts.filter((p) => p.includes("Payment"))
    expect(dated).toHaveLength(2)
    for (const p of dated) expect(p).toContain("DOCUMENT DATE: 2026-11-01")
  })

  it("only takes a day as document_date, and the prompt defaults to today without one", () => {
    expect(addInput.safeParse({ content: "x", document_date: "2026-11" }).success).toBe(false)
    expect(addInput.safeParse({ content: "x", document_date: "yesterday" }).success).toBe(false)
    expect(addInput.safeParse({ content: "x", document_date: "2026-11-03T10:00:00Z" }).success).toBe(true)
    expect(extractPrompt({ content: "x", existing: [], now: new Date("2026-11-20") })).toContain("DOCUMENT DATE: 2026-11-20")
  })
})

describe("edits", () => {
  it("a date change is a new version; the same dates change nothing", async () => {
    const m = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Domain renews yearly", keywords: "domain", kind: "dynamic" })
    const v2 = await editMemory(db, W, m.id, { due: { date: "2026-12-02", precision: "day" } })
    expect(v2!.id).not.toBe(m.id)
    expect(v2).toMatchObject({ fact: "Domain renews yearly", dueDate: "2026-12-02", supersedesId: m.id })
    const same = await editMemory(db, W, v2!.id, { due: { date: "2026-12-02", precision: "day" } })
    expect(same!.id).toBe(v2!.id)
    // Editing the text alone keeps the dates.
    const v3 = await editMemory(db, W, v2!.id, { fact: "Domain zakiego.com renews yearly" })
    expect(v3).toMatchObject({ dueDate: "2026-12-02", duePrecision: "day" })
  })

  it("PATCH takes short dates and null, and rejects bad ones", async () => {
    const m = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Contract with Vorneo", keywords: "contract", kind: "dynamic" })
    const { memory } = await patchMemory(db, W, m.id, patchInput.parse({ event_date: "2026-01", due_date: "2027-12-31" }))
    expect(memory).toMatchObject({ eventDate: "2026-01-01", eventPrecision: "month", dueDate: "2027-12-31", duePrecision: "day" })
    const cleared = await patchMemory(db, W, memory.id, patchInput.parse({ event_date: null }))
    expect(cleared.memory).toMatchObject({ eventDate: null, eventPrecision: null, dueDate: "2027-12-31" })
    expect(patchInput.safeParse({ due_date: "end of 2027" }).success).toBe(false)
    expect(patchInput.safeParse({ due_date: "2027-02-30" }).success).toBe(false)
  })
})

describe("upcoming, search, Ask and MCP", () => {
  async function seed() {
    const d = (date: string) => ({ date, precision: "day" as const })
    await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Contract with Vorneo ends", keywords: "contract kontrak vorneo", kind: "dynamic", event: d("2026-01-02"), due: d("2027-12-31") })
    await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Car insurance renewal", keywords: "car insurance asuransi", kind: "dynamic", due: d("2026-11-16") })
    await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Gym membership expired", keywords: "gym", kind: "dynamic", due: d("2026-11-07") })
    await addMemoryDirect(db, { workspaceId: W, container: "work", fact: "Tax report due", keywords: "tax pajak", kind: "dynamic", due: d("2026-11-11") })
    const old = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Hosting ends", keywords: "hosting", kind: "dynamic", due: d("2026-11-20") })
    await editMemory(db, W, old.id, { due: d("2027-02-01") })
    await addMemoryDirect(db, { workspaceId: "ws_other", container: C, fact: "Other workspace lease ends", keywords: "lease", kind: "dynamic", due: d("2026-11-12") })
  }
  const NOW = Date.UTC(2026, 10, 10, 6)

  it("lists current facts due in the window, soonest first, scoped to the workspace", async () => {
    await seed()
    const q = (over: Record<string, unknown>) => upcoming(db, W, upcomingInput.parse({ container: C, ...over }), NOW)
    const month = q({ days: "30" })
    expect(month.today).toBe("2026-11-10")
    expect(month.memories.map((m) => [m.fact, m.daysLeft])).toEqual([["Car insurance renewal", 6]])
    expect(q({ days: "30", all: "1" }).memories.map((m) => m.fact)).toEqual(["Tax report due", "Car insurance renewal"])
    expect(q({ days: "30", past_days: "7" }).memories.map((m) => [m.fact, m.daysLeft])).toEqual([
      ["Gym membership expired", -3],
      ["Car insurance renewal", 6],
    ])
    // The superseded hosting version (due 2026-11-20) is gone; the current one is due 2027-02-01.
    // The contract (2027-12-31) is past the 366-day window.
    expect(q({ days: "366" }).memories.map((m) => m.fact)).toEqual(["Car insurance renewal", "Hosting ends"])
    expect(JSON.stringify(q({ days: "366", all: "true", past_days: "366" }))).not.toContain("Other workspace")
    expect(upcomingInput.safeParse({ tz: "Mars/Olympus" }).success).toBe(false)
  })

  it("search hits carry the dates, and Ask is told them", async () => {
    await seed()
    const res = await search(db, W, { container: C, q: "when does my contract end", limit: 3 })
    const hit = res.results.find((r) => r.type === "memory" && r.text.startsWith("Contract"))
    expect(hit).toMatchObject({ eventDate: "2026-01-02", dueDate: "2027-12-31", duePrecision: "day" })

    const prompts: string[] = []
    const chat = vi.fn(async (req: { prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
      prompts.push(req.prompt)
      return { data: req.schema.parse({ answer: "It ends on 2027-12-31 [m1].", covered: true }), model: "m" }
    })
    const out = await answerQuestion(db, W, { container: C, question: "When does my contract end?", includeChunks: false }, { chat: chat as unknown as AskDeps["chat"], now: () => new Date("2026-11-10") })
    expect(prompts[0]).toMatch(/\[m\d+\] \(dynamic, [\d-]+, happened 2026-01-02, due 2027-12-31\) Contract with Vorneo ends/)
    // Due on the 7th, asked on the 10th.
    expect(prompts[0]).toMatch(/\(dynamic, [\d-]+, due 2026-11-07, passed\) Gym membership expired/)
    expect(out.citations[0]?.text).toBe("Contract with Vorneo ends")
  })

  it("MCP: upcoming, list_memories and search_memories show short dates", async () => {
    await seed()
    let id = 1
    const call = async (name: string, args: Record<string, unknown>) => {
      const res = await handleMcp(
        db,
        W,
        new Request("http://localhost/mcp", {
          method: "POST",
          headers: { "content-type": "application/json", accept: "application/json, text/event-stream", "mcp-protocol-version": "2025-06-18" },
          body: JSON.stringify({ jsonrpc: "2.0", id: id++, method: "tools/call", params: { name, arguments: args } }),
        }),
      )
      const body = (await res.json()) as { result: { content: { text: string }[]; isError?: boolean } }
      return { isError: body.result.isError, value: body.result.isError ? body.result.content[0]!.text : JSON.parse(body.result.content[0]!.text) }
    }
    const up = await call("upcoming", { container: C, days: 366 })
    expect(up.value.memories[0]).toMatchObject({ fact: "Car insurance renewal", due_date: "2026-11-16" })
    expect(typeof up.value.memories[0].days_left).toBe("number")
    expect((await call("upcoming", { timezone: "Nope/Zone" })).isError).toBe(true)
    const listed = await call("list_memories", { container: C })
    expect(listed.value.memories.find((m: { fact: string }) => m.fact.startsWith("Contract"))).toMatchObject({ event_date: "2026-01-02", due_date: "2027-12-31" })
    expect(listed.value.memories.find((m: { fact: string }) => m.fact === "Gym membership expired")).not.toHaveProperty("event_date")
    const hits = await call("search_memories", { query: "contract Vorneo", container: C })
    expect(hits.value[0]).toMatchObject({ due_date: "2027-12-31" })
  })
})

describe("backfill", () => {
  it("dates undated facts in batches, in place, and skips facts that changed", async () => {
    const a = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Contract with Vorneo runs until the end of 2027", keywords: "contract", kind: "dynamic" })
    const b = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Prefers Puma running shoes", keywords: "shoes", kind: "dynamic" })
    const c = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Moved to Banjarbaru in 2019", keywords: "city", kind: "static" })
    const dated = await addMemoryDirect(db, { workspaceId: W, container: C, fact: "Already dated", keywords: "", kind: "dynamic", due: { date: "2027-01-01", precision: "year" } })
    await addMemoryDirect(db, { workspaceId: "ws_other", container: C, fact: "Other workspace contract ends 2027", keywords: "", kind: "dynamic" })
    expect(countUndated(db, W)).toBe(3)

    const { chat, prompts } = scripted([
      {
        dates: [
          { ref: 1, due_date: "2027-12-31" },
          { ref: 3, event_date: "2019" },
          { ref: 2, due_date: "not a date" },
          { ref: 9, due_date: "2030-01-01" },
        ],
      },
    ])
    // The fact the model calls ref 3 is edited while the call is out.
    const deps = {
      chat: (async (req: Parameters<typeof chatJson>[0]) => {
        const out = await chat(req)
        db.update(memories).set({ eventDate: "2020-01-01", eventPrecision: "year" }).where(eq(memories.id, c.id)).run()
        return out
      }) as typeof chatJson,
    }
    const res = await backfillBatch(db, W, {}, deps)
    expect(res).toEqual({ checked: 3, dated: 1, next: false })
    expect(prompts[0]).not.toContain("Already dated")
    expect(prompts[0]).not.toContain("Other workspace")
    expect(prompts[0]).toMatch(/1\. \(stored [\d-]+\) Contract with Vorneo/)
    expect(getMemory(db, W, a.id)).toMatchObject({ dueDate: "2027-12-31", duePrecision: "day", validTo: null })
    expect(memoryChain(db, W, a.id)).toHaveLength(1) // in place, not a new version
    expect(getMemory(db, W, b.id)).toMatchObject({ eventDate: null, dueDate: null })
    expect(getMemory(db, W, c.id)).toMatchObject({ eventDate: "2020-01-01" }) // the concurrent write won
    expect(getMemory(db, W, dated.id)).toMatchObject({ dueDate: "2027-01-01" })
  })

  it("queues the next batch with a cursor, and the CLI queues one job per workspace", async () => {
    for (let i = 0; i < 41; i++) {
      db.insert(memories).values({ id: `mem_${String(i).padStart(3, "0")}`, workspaceId: W, container: C, fact: `Fact ${i}`, createdAt: 1_000 + i }).run()
    }
    const { chat, prompts } = scripted([{ dates: [] }, { dates: [{ ref: 1, due_date: "2027-05" }] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    createWorkspace(db, { name: "Test", id: W, slug: "test" })
    createWorkspace(db, { name: "Empty", id: "ws_empty", slug: "empty" })
    const out: string[] = []
    const io = { db: () => db, out: (l: string) => out.push(l), err: (l: string) => out.push(`ERR ${l}`), readPassword: async () => "" }
    expect(await runCli(["backfill-dates", "--json"], io)).toBe(0)
    expect(JSON.parse(out[0]!)).toEqual([
      { workspace: "test", undated: 41, job: expect.any(Number) },
      { workspace: "empty", undated: 0, job: null },
    ])
    await drain(runner)
    expect(prompts).toHaveLength(2)
    expect(prompts[1]).toContain("1. (stored 1970-01-01) Fact 40")
    expect(getMemory(db, W, "mem_040")).toMatchObject({ dueDate: "2027-05-01", duePrecision: "month" })
    const done = db.select().from(jobs).where(eq(jobs.type, "backfill_dates")).all()
    expect(done.map((j) => j.status)).toEqual(["done", "done"])
    expect(done[1]!.payload).toMatchObject({ checked: 40, dated: 0, cursor: { id: "mem_039" } })
  })
})
