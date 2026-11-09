import { beforeAll, describe, expect, it } from "vitest"
import { openDb, type DB } from "./db"
import { documents } from "./db/schema"
import { handleMcp } from "./mcp"
import { addMemoryDirect } from "./memories"

let db: DB
beforeAll(async () => {
  db = openDb(":memory:")
  process.env.RECALL_DEFAULT_CONTAINER = "me"
  await addMemoryDirect(db, { container: "me", fact: "Prefers Nike Pegasus running shoes", keywords: "shoes sepatu", kind: "dynamic" })
})

let nextId = 1
async function rpc(method: string, params?: unknown) {
  const res = await handleMcp(
    db,
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
  it("initializes and lists the five tools", async () => {
    const init = await rpc("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "test", version: "1" } })
    expect(init.result.serverInfo.name).toBe("recall")
    const tools = await rpc("tools/list")
    expect(tools.result.tools.map((t: { name: string }) => t.name).sort()).toEqual(["add_memory", "ask_memory", "get_profile", "list_memories", "search_memories"])
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
})
