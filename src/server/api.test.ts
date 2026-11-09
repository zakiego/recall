import { afterAll, beforeAll, describe, expect, it } from "vitest"
import { Route as MemoryRoute } from "@/routes/v1/memories.$id"
import { Route as MemoriesRoute } from "@/routes/v1/memories"
import { createApiKey } from "./auth"
import { openDb, setDb, type DB } from "./db"
import { embedOne } from "./embed"
import { getMemory, insertMemory, memoryChain } from "./memories"

type Handler = (ctx: { request: Request; params: Record<string, string> }) => Promise<Response>
const handlers = (route: unknown) => (route as { options: { server: { handlers: Record<string, Handler> } } }).options.server.handlers

const W = "ws_test"
let db: DB
let key: string
let flagged: string
let other: string

beforeAll(async () => {
  db = openDb(":memory:")
  setDb(db)
  key = createApiKey(db, { workspaceId: W, name: "test" }).key
  const vector = await embedOne("salary", "passage")
  other = insertMemory(db, { workspaceId: W, container: "c", fact: "Lives in Banjarbaru", keywords: "", kind: "static" }, vector).id
  flagged = insertMemory(
    db,
    { workspaceId: W, container: "c", fact: "Salary is paid weekly", keywords: "gaji", kind: "dynamic", reviewNote: 'Conflicts with "Salary is paid monthly"' },
    vector,
  ).id
  // A superseded flagged row must never show up in the review list.
  insertMemory(db, { workspaceId: W, container: "c", fact: "Old flagged", keywords: "", kind: "dynamic", reviewNote: "stale" }, vector)
  db.$client.prepare("UPDATE memories SET valid_to = 1 WHERE fact = 'Old flagged'").run()
})

afterAll(() => setDb(undefined))

function call(route: unknown, method: string, path: string, params: Record<string, string> = {}, body?: unknown) {
  const request = new Request(`http://localhost${path}`, {
    method,
    headers: { authorization: `Bearer ${key}`, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  return handlers(route)[method]!({ request, params })
}

describe("review flags over /v1/memories", () => {
  it("lists only flagged current facts with review=1", async () => {
    const all = await (await call(MemoriesRoute, "GET", "/v1/memories?container=c")).json()
    expect(all.memories).toHaveLength(2)
    const res = await call(MemoriesRoute, "GET", "/v1/memories?container=c&review=1&history=1")
    expect(res.status).toBe(200)
    const body = await res.json()
    expect(body.memories.map((m: { id: string; reviewNote: string }) => [m.id, m.reviewNote])).toEqual([[flagged, 'Conflicts with "Salary is paid monthly"']])
  })

  it("resolves a flag without writing a new version", async () => {
    const res = await call(MemoryRoute, "PATCH", `/v1/memories/${flagged}`, { id: flagged }, { resolve: true })
    expect(res.status).toBe(200)
    const body = await res.json()
    expect(body.memory).toMatchObject({ id: flagged, reviewNote: null, validTo: null })
    expect(memoryChain(db, W, flagged)).toHaveLength(1)
    const list = await (await call(MemoriesRoute, "GET", "/v1/memories?container=c&review=true")).json()
    expect(list.memories).toEqual([])
  })

  it("clears the flag when the fact is edited", async () => {
    db.$client.prepare("UPDATE memories SET review_note = 'check' WHERE id = ?").run(other)
    const res = await call(MemoryRoute, "PATCH", `/v1/memories/${other}`, { id: other }, { fact: "Lives in Martapura" })
    const body = await res.json()
    expect(body.memory).toMatchObject({ fact: "Lives in Martapura", reviewNote: null, supersedesId: other })
    expect(getMemory(db, W, other)!.validTo).not.toBeNull()
  })

  it("rejects an empty patch", async () => {
    const res = await call(MemoryRoute, "PATCH", `/v1/memories/${flagged}`, { id: flagged }, { resolve: false })
    expect(res.status).toBe(400)
  })

  it("refuses to resolve a superseded version", async () => {
    const res = await call(MemoryRoute, "PATCH", `/v1/memories/${other}`, { id: other }, { resolve: true })
    expect(res.status).toBe(409)
  })
})
