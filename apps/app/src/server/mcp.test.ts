import { beforeAll, describe, expect, it, vi } from "vitest"
import { openDb, type DB } from "./db"
import { documents } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { createHandlers } from "./pipeline"
import { handleMcp } from "./mcp"
import { addMemoryDirect } from "./memories"
import { linkMentions } from "./people"

let db: DB
beforeAll(async () => {
  db = openDb(":memory:")
  process.env.RECALL_DEFAULT_CONTAINER = "me"
  await addMemoryDirect(db, { workspaceId: "ws_mcp", container: "me", fact: "Prefers Nike Pegasus running shoes", keywords: "shoes sepatu", kind: "dynamic" })
})

let nextId = 1
async function rpc(method: string, params?: unknown) {
  const res = await handleMcp(
    db,
    "ws_mcp",
    new Request("http://localhost/mcp", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream", "mcp-protocol-version": "2025-06-18" },
      body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }),
    }),
  )
  expect(res.status).toBe(200)
  return (await res.json()) as { result?: any; error?: any }
}

describe("mcp", () => {
  it("initializes and lists the eight tools", async () => {
    const init = await rpc("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "test", version: "1" } })
    expect(init.result.serverInfo.name).toBe("recall")
    const tools = await rpc("tools/list")
    expect(tools.result.tools.map((t: { name: string }) => t.name).sort()).toEqual([
      "add_memory",
      "ask_memory",
      "get_person",
      "get_profile",
      "list_memories",
      "list_people",
      "search_memories",
      "upcoming",
    ])
  })

  it("lists people and gets one by name, with the people on search hits", async () => {
    const m = await addMemoryDirect(db, { workspaceId: "ws_mcp", container: "team", fact: "Clara Simmons designs the Vorneo listing page", keywords: "", kind: "dynamic" })
    await linkMentions(db, { workspaceId: "ws_mcp", container: "team" }, [
      { mention: { name: "Clara Simmons", aliases: ["Clara"], role: "Designer", organization: undefined, handle: undefined, summary: undefined, facts: [] }, memoryIds: [m.id] },
    ])
    const list = JSON.parse((await rpc("tools/call", { name: "list_people", arguments: { container: "team" } })).result.content[0].text)
    expect(list.people).toEqual([expect.objectContaining({ name: "Clara Simmons", role: "Designer", facts: 1 })])
    const one = JSON.parse((await rpc("tools/call", { name: "get_person", arguments: { name: "clara", container: "team" } })).result.content[0].text)
    expect(one).toMatchObject({ name: "Clara Simmons", facts: [expect.objectContaining({ fact: "Clara Simmons designs the Vorneo listing page", current: true })] })
    expect((await rpc("tools/call", { name: "get_person", arguments: { name: "Nobody", container: "team" } })).result.isError).toBe(true)
    // Another workspace's id is "no such person".
    expect((await rpc("tools/call", { name: "get_person", arguments: { id: "per_missing" } })).result.isError).toBe(true)
    const hits = JSON.parse((await rpc("tools/call", { name: "search_memories", arguments: { query: "Vorneo listing designer", container: "team" } })).result.content[0].text)
    expect(hits.find((h: { id: string }) => h.id === m.id).people).toEqual(["Clara Simmons"])
  })

  it("searches the default container", async () => {
    const res = await rpc("tools/call", { name: "search_memories", arguments: { query: "what shoes" } })
    const hits = JSON.parse(res.result.content[0].text)
    expect(hits[0].text).toBe("Prefers Nike Pegasus running shoes")
  })

  it("adds a memory as a queued document", async () => {
    const res = await rpc("tools/call", { name: "add_memory", arguments: { content: "Drinks dark roast coffee", container: "other" } })
    const out = JSON.parse(res.result.content[0].text)
    expect(out).toMatchObject({ container: "other", status: "queued" })
    expect(db.select().from(documents).all().map((d) => d.id)).toContain(out.id)
  })

  it("rejects an empty add", async () => {
    const res = await rpc("tools/call", { name: "add_memory", arguments: {} })
    expect(res.result.isError).toBe(true)
  })

  it("returns the profile and lists memories", async () => {
    const p = JSON.parse((await rpc("tools/call", { name: "get_profile", arguments: {} })).result.content[0].text)
    expect(p.dynamicFacts).toContain("Prefers Nike Pegasus running shoes")
    const l = JSON.parse((await rpc("tools/call", { name: "list_memories", arguments: {} })).result.content[0].text)
    expect(l.memories).toHaveLength(1)
    expect(l.containers).toContain("me")
  })

  it("returns the existing document for a repeated source_id", async () => {
    const call = () => rpc("tools/call", { name: "add_memory", arguments: { content: "Dentist on Monday", source_id: "gmail:dup", container: "mcp-dup" } })
    const a = JSON.parse((await call()).result.content[0].text)
    const b = JSON.parse((await call()).result.content[0].text)
    expect(a.duplicate).toBe(false)
    expect(b).toMatchObject({ id: a.id, duplicate: true })
    expect(db.select().from(documents).all().filter((d) => d.container === "mcp-dup")).toHaveLength(1)
  })

  it("with wait, returns the extracted facts", async () => {
    const chat = vi.fn(async (req: { system: string; schema: { parse: (x: unknown) => unknown } }) => {
      if (req.system.includes("profile")) return req.schema.parse({ static: "", dynamic: "" })
      return req.schema.parse({ add: [{ fact: "Drinks oat milk lattes", keywords: "coffee kopi", kind: "dynamic" }] })
    }) as unknown as typeof chatJson
    // Older tests left queued documents; the loop runs those first.
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    runner.start()
    try {
      const res = await rpc("tools/call", { name: "add_memory", arguments: { content: "I drink oat milk lattes", container: "mcp-wait", wait: true } })
      const out = JSON.parse(res.result.content[0].text)
      expect(out).toMatchObject({ container: "mcp-wait", status: "done", added: [{ fact: "Drinks oat milk lattes", kind: "dynamic" }], updated: [] })
    } finally {
      await runner.stop()
    }
  })

  it("lists only flagged facts with needs_review", async () => {
    const call = async (args: object) => JSON.parse((await rpc("tools/call", { name: "list_memories", arguments: args })).result.content[0].text)
    expect((await call({ needs_review: true })).memories).toEqual([])
    db.$client.prepare("UPDATE memories SET review_note = 'Conflicts with \"Prefers Adidas\"' WHERE container = 'me'").run()
    const flagged = await call({ needs_review: true })
    expect(flagged.memories).toHaveLength(1)
    expect(flagged.memories[0]).toMatchObject({ fact: "Prefers Nike Pegasus running shoes", reviewNote: 'Conflicts with "Prefers Adidas"' })
  })
})
