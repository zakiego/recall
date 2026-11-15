// Tenant isolation: two workspaces with the same container names and similar
// facts. Nothing in one may ever show up in the other, through any path.
import { eq } from "drizzle-orm"
import { beforeAll, describe, expect, it } from "vitest"
import { Route as ContainersRoute } from "@/server/routes-legacy/v1/containers"
import { Route as DocumentRoute } from "@/server/routes-legacy/v1/documents.$id"
import { Route as MemoryRoute } from "@/server/routes-legacy/v1/memories.$id"
import { Route as MemoriesRoute } from "@/server/routes-legacy/v1/memories"
import { Route as ProfileRoute } from "@/server/routes-legacy/v1/profile"
import { Route as SearchRoute } from "@/server/routes-legacy/v1/search"
import { Route as UpcomingRoute } from "@/server/routes-legacy/v1/upcoming"
import { Route as McpRoute } from "@/server/routes-legacy/mcp"
import { addContent, list, search, waitForDocument } from "./api"
import { answerQuestion, type AskDeps } from "./ask"
import { createApiKey, revokeApiKey, verifyApiKey } from "./auth"
import { openDb, setDb, type DB } from "./db"
import { documents, memories, profiles } from "./db/schema"
import { embedOne } from "./embed"
import { buildGraph } from "./graph"
import { newId } from "./ids"
import { enqueue } from "./jobs/queue"
import {
  addMemoryDirect,
  deleteDocument,
  editMemory,
  forgetMemory,
  getDocument,
  getMemory,
  getProfile,
  listContainers,
  listDocuments,
  listMemories,
  memoryChain,
  reviewCount,
  setReviewNote,
  versionCounts,
} from "./memories"
import { relatedMemories } from "./pipeline"
import { indexChunk } from "./search"

const A = "ws_alpha"
const B = "ws_beta"
const C = "shared" // the same container name in both workspaces

let db: DB
const ids = { a: [] as string[], b: [] as string[], docA: "", docB: "" }
const keys = { a: "", b: "" }

beforeAll(async () => {
  db = openDb(":memory:")
  setDb(db)
  for (const [ws, bucket, facts] of [
    [A, ids.a, ["Prefers Adidas running shoes", "Lives in Banjarbaru"]],
    [B, ids.b, ["Prefers Nike running shoes", "Lives in Jakarta"]],
  ] as const) {
    for (const fact of facts) bucket.push((await addMemoryDirect(db, { workspaceId: ws, container: C, fact, keywords: "sepatu shoes kota city", kind: "dynamic" })).id)
  }
  // A version chain in B, so chain and count lookups have something to leak.
  await editMemory(db, B, ids.b[0]!, { fact: "Switched to Puma running shoes" })

  for (const [ws, key, text] of [
    [A, "docA", "The alpha quarterly report covers revenue in Kalimantan."],
    [B, "docB", "The beta quarterly report covers revenue in Java."],
  ] as const) {
    const id = newId("doc")
    ids[key] = id
    db.insert(documents).values({ id, workspaceId: ws, container: C, sourceType: "text", title: `${key} notes`, content: text }).run()
    const chunkId = newId("chk")
    db.$client
      .prepare("INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES (?, ?, ?, ?, 0, ?)")
      .run(chunkId, id, ws, C, text)
    indexChunk(db.$client, { id: chunkId, workspaceId: ws, container: C, text }, await embedOne(text, "passage"))
  }
  db.insert(profiles).values({ workspaceId: A, container: C, staticText: "alpha profile", dynamicText: "" }).run()
  db.insert(profiles).values({ workspaceId: B, container: C, staticText: "beta profile", dynamicText: "" }).run()
  keys.a = createApiKey(db, { workspaceId: A, name: "a" }).key
  keys.b = createApiKey(db, { workspaceId: B, name: "b" }).key
})

const bText = () => ["Nike", "Puma", "Jakarta", "beta"]
const leaks = (value: unknown) => {
  const s = JSON.stringify(value)
  return bText().filter((w) => s.includes(w))
}

describe("search", () => {
  it("keyword and vector search never cross workspaces", async () => {
    for (const q of ["Nike running shoes", "Jakarta", "Puma", "sepatu", "where does she live", "beta quarterly report Java"]) {
      const res = await search(db, A, { container: C, q, limit: 50, include: ["memories", "chunks"] })
      expect(leaks(res.results), q).toEqual([])
      expect(res.results.every((r) => (r.type === "memory" ? ids.a.includes(r.id) : r.documentId === ids.docA))).toBe(true)
    }
  })

  it("finds the workspace's own rows", async () => {
    const res = await search(db, B, { container: C, q: "running shoes", include: ["memories"] })
    expect(res.results[0]?.text).toBe("Switched to Puma running shoes")
  })

  it("related memories for extraction stay in the workspace", async () => {
    const v = await embedOne("Prefers Nike running shoes", "passage")
    const rel = relatedMemories(db, { workspaceId: A, container: C }, [v])
    expect(rel.length).toBeGreaterThan(0)
    expect(rel.every((m) => m.workspaceId === A)).toBe(true)
  })
})

describe("reads by id and lists", () => {
  it("can't get, edit, forget or chain another workspace's memory", async () => {
    const bId = ids.b[1]!
    expect(getMemory(db, A, bId)).toBeUndefined()
    expect(memoryChain(db, A, bId)).toEqual([])
    expect(versionCounts(db, A, [bId])[bId]).toBe(0)
    expect(await editMemory(db, A, bId, { fact: "hijacked" })).toBeNull()
    expect(forgetMemory(db, A, bId)).toBeNull()
    expect(getMemory(db, B, bId)?.validTo).toBeNull()
  })

  it("lists, containers, documents, profiles and graph are scoped", () => {
    expect(leaks(listMemories(db, { workspaceId: A, container: C, history: true }))).toEqual([])
    expect(leaks(list(db, A, { container: C, history: true, review: false }))).toEqual([])
    expect(listContainers(db, A)).toEqual([expect.objectContaining({ container: C, memories: 2, documents: 1 })])
    expect(listDocuments(db, A, C).documents.map((d) => d.id)).toEqual([ids.docA])
    expect(getDocument(db, A, ids.docB)).toBeUndefined()
    expect(deleteDocument(db, A, ids.docB)).toBe(false)
    expect(getDocument(db, B, ids.docB)).toBeDefined()
    expect(getProfile(db, A, C).static).toBe("alpha profile")
    expect(leaks(getProfile(db, A, C))).toEqual([])
    expect(leaks(buildGraph(db, A, C))).toEqual([])
  })

  it("profile jobs carry their workspace", () => {
    enqueue(db, B, "profile", { container: "fresh" })
    const job = db.$client.prepare("SELECT workspace_id FROM jobs WHERE json_extract(payload, '$.container') = 'fresh'").get() as { workspace_id: string }
    expect(job.workspace_id).toBe(B)
  })
})

// --- REST and MCP, through the real route handlers -----------------------------

type Handler = (ctx: { request: Request; params: Record<string, string> }) => Promise<Response>
function handler(route: unknown, method: string): Handler {
  return (route as { options: { server: { handlers: Record<string, Handler> } } }).options.server.handlers[method]!
}
const req = (key: string, path: string, init: RequestInit = {}) =>
  new Request(`http://localhost${path}`, { ...init, headers: { authorization: `Bearer ${key}`, "content-type": "application/json", ...init.headers } })

describe("REST", () => {
  it("search, list, profile and containers only see the key's workspace", async () => {
    const s = await handler(SearchRoute, "POST")({ request: req(keys.a, "/v1/search", { method: "POST", body: JSON.stringify({ container: C, q: "Nike shoes Jakarta", include: ["memories", "chunks"] }) }), params: {} })
    expect(s.status).toBe(200)
    const body = await s.json()
    expect(body.results.length).toBeGreaterThan(0)
    expect(leaks(body.results)).toEqual([])

    const l = await handler(MemoriesRoute, "GET")({ request: req(keys.a, `/v1/memories?container=${C}&history=1`), params: {} })
    expect(leaks(await l.json())).toEqual([])
    const p = await handler(ProfileRoute, "GET")({ request: req(keys.a, `/v1/profile?container=${C}&q=Nike`), params: {} })
    expect(leaks(await p.json())).toEqual([])
    const c = await handler(ContainersRoute, "GET")({ request: req(keys.b, "/v1/containers"), params: {} })
    expect((await c.json()).containers).toEqual([expect.objectContaining({ container: C, memories: 2 })])
  })

  it("upcoming only lists the key's workspace, in every container", async () => {
    const due = new Date(Date.now() + 5 * 86_400_000).toISOString().slice(0, 10)
    db.update(memories).set({ dueDate: due, duePrecision: "day" }).where(eq(memories.id, ids.b[1]!)).run()
    const path = `/v1/upcoming?container=${C}&all=1&days=366&past_days=366`
    const a = await handler(UpcomingRoute, "GET")({ request: req(keys.a, path), params: {} })
    expect(a.status).toBe(200)
    expect((await a.json()).memories).toEqual([])
    const b = await handler(UpcomingRoute, "GET")({ request: req(keys.b, path), params: {} })
    expect((await b.json()).memories.map((m: { id: string; daysLeft: number }) => [m.id, m.daysLeft])).toEqual([[ids.b[1], 5]])
    db.update(memories).set({ dueDate: null, duePrecision: null }).where(eq(memories.id, ids.b[1]!)).run()
  })

  it("another workspace's memory and document ids are 404 for get, patch and delete", async () => {
    const bId = ids.b[1]!
    const get = await handler(MemoryRoute, "GET")({ request: req(keys.a, `/v1/memories/${bId}`), params: { id: bId } })
    expect(get.status).toBe(404)
    const patch = await handler(MemoryRoute, "PATCH")({ request: req(keys.a, `/v1/memories/${bId}`, { method: "PATCH", body: JSON.stringify({ fact: "x" }) }), params: { id: bId } })
    expect(patch.status).toBe(404)
    const del = await handler(MemoryRoute, "DELETE")({ request: req(keys.a, `/v1/memories/${bId}`, { method: "DELETE" }), params: { id: bId } })
    expect(del.status).toBe(404)
    const doc = await handler(DocumentRoute, "GET")({ request: req(keys.a, `/v1/documents/${ids.docB}`), params: { id: ids.docB } })
    expect(doc.status).toBe(404)
    const delDoc = await handler(DocumentRoute, "DELETE")({ request: req(keys.a, `/v1/documents/${ids.docB}`, { method: "DELETE" }), params: { id: ids.docB } })
    expect(delDoc.status).toBe(404)
    expect(getMemory(db, B, bId)?.validTo).toBeNull()
    expect(getDocument(db, B, ids.docB)).toBeDefined()
  })

  it("writes land in the key's workspace", async () => {
    const res = await handler(MemoriesRoute, "POST")({ request: req(keys.b, "/v1/memories", { method: "POST", body: JSON.stringify({ container: C, content: "beta only note" }) }), params: {} })
    expect(res.status).toBe(202)
    const { id } = await res.json()
    expect(getDocument(db, B, id)).toBeDefined()
    expect(getDocument(db, A, id)).toBeUndefined()
  })

  it("rejects missing and revoked keys", async () => {
    const r = await handler(ContainersRoute, "GET")({ request: req("rcl_nope", "/v1/containers"), params: {} })
    expect(r.status).toBe(401)
    const { key, row } = createApiKey(db, { workspaceId: A, name: "temp" })
    expect(verifyApiKey(db, key)?.workspaceId).toBe(A)
    // A workspace B admin can't revoke a workspace A key.
    expect(revokeApiKey(db, B, row.id)).toBe(false)
    expect(revokeApiKey(db, A, row.id)).toBe(true)
    const r2 = await handler(ContainersRoute, "GET")({ request: req(key, "/v1/containers"), params: {} })
    expect(r2.status).toBe(401)
  })
})

describe("MCP", () => {
  let nextId = 1
  async function call(key: string, name: string, args: Record<string, unknown>) {
    const res = await handler(McpRoute, "POST")({
      request: req(key, "/mcp", {
        method: "POST",
        headers: { accept: "application/json, text/event-stream", "mcp-protocol-version": "2025-06-18" },
        body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method: "tools/call", params: { name, arguments: args } }),
      }),
      params: {},
    })
    expect(res.status).toBe(200)
    const body = (await res.json()) as { result: { content: { text: string }[] } }
    return JSON.parse(body.result.content[0]!.text)
  }

  it("search, profile and list only return the key's workspace", async () => {
    const hits = await call(keys.a, "search_memories", { query: "Nike Puma shoes Jakarta", container: C, include_chunks: true })
    expect(hits.length).toBeGreaterThan(0)
    expect(leaks(hits)).toEqual([])
    expect(leaks(await call(keys.a, "get_profile", { container: C, query: "shoes" }))).toEqual([])
    const listed = await call(keys.a, "list_memories", { container: C, history: true })
    expect(leaks(listed)).toEqual([])
    expect(listed.containers).toEqual([C])
  })

  it("add_memory writes into the key's workspace", async () => {
    const out = await call(keys.a, "add_memory", { content: "alpha only note", container: "fresh-a" })
    expect(getDocument(db, A, out.id)).toBeDefined()
    expect(getDocument(db, B, out.id)).toBeUndefined()
  })

  it("refuses a missing key", async () => {
    const res = await handler(McpRoute, "POST")({ request: req("", "/mcp", { method: "POST", body: "{}" }), params: {} })
    expect(res.status).toBe(401)
  })
})

describe("source ids, review flags and waits", () => {
  it("dedupes a source_id per workspace, never across", () => {
    const a = addContent(db, A, { container: C, content: "alpha mail", source_id: "gmail:1" })
    const b = addContent(db, B, { container: C, content: "beta mail", source_id: "gmail:1" })
    expect(a.duplicate).toBe(false)
    expect(b.duplicate).toBe(false)
    expect(a.id).not.toBe(b.id)
    expect(addContent(db, A, { container: C, content: "again", source_id: "gmail:1" })).toMatchObject({ id: a.id, duplicate: true })
  })

  it("can't flag, resolve or count another workspace's review notes", () => {
    const bId = ids.b[1]!
    expect(setReviewNote(db, A, bId, "hijack")).toBeUndefined()
    expect(setReviewNote(db, B, bId, "check this")).toMatchObject({ id: bId, reviewNote: "check this" })
    expect(reviewCount(db, A, C)).toBe(0)
    expect(reviewCount(db, B, C)).toBe(1)
    expect(setReviewNote(db, A, bId, null)).toBeUndefined()
    expect(getMemory(db, B, bId)?.reviewNote).toBe("check this")
  })

  it("a wait never reads another workspace's document", async () => {
    db.$client.prepare("UPDATE documents SET status = 'done' WHERE id = ?").run(ids.docB)
    expect(await waitForDocument(db, A, ids.docB, 10)).toBeNull()
    expect(await waitForDocument(db, B, ids.docB, 10)).toMatchObject({ id: ids.docB })
  })
})

describe("ask", () => {
  it("only hands the model facts and excerpts from the asking workspace", async () => {
    let prompt = ""
    const chat = (async (req: { prompt: string }) => {
      prompt = req.prompt
      return { data: { answer: "From what's stored [m1].", covered: true }, model: "test" }
    }) as unknown as AskDeps["chat"]
    const res = await answerQuestion(db, A, { container: C, question: "Which running shoes, which city, and what does the quarterly report say?", includeChunks: true }, { chat, now: () => new Date() })
    expect(prompt.length).toBeGreaterThan(0)
    expect(leaks(prompt)).toEqual([])
    expect(res.citations.every((c) => (c.type === "memory" ? ids.a.includes(c.id) : true))).toBe(true)
  })
})
