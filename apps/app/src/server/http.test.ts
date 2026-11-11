import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { createApiKey } from "./auth"
import { openDb, setDb, type DB } from "./db"
import { HttpError, withKey } from "./http"

let db: DB
let key: string
beforeEach(() => {
  db = openDb(":memory:")
  setDb(db)
  key = createApiKey(db, { workspaceId: "ws_http", name: "test", createdBy: null }).key
})
afterEach(() => {
  vi.restoreAllMocks()
  setDb(undefined)
})

const call = (handler: Parameters<typeof withKey>[0]) =>
  withKey(handler)({ request: new Request("http://localhost/v1/x", { headers: { authorization: `Bearer ${key}` } }), params: {} })

describe("withKey", () => {
  it("logs an unexpected error and answers with a generic 500", async () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {})
    const err = new Error("SQLITE_ERROR: no such table: secret_things at /data/recall.db")
    const res = await call(() => {
      throw err
    })
    expect(res.status).toBe(500)
    const body = await res.text()
    expect(JSON.parse(body)).toEqual({ error: { type: "internal", message: "internal error" } })
    expect(body).not.toContain("secret_things")
    expect(log).toHaveBeenCalledWith("[recall] api error:", err)
  })

  it("passes an HttpError through as written", async () => {
    const res = await call(() => {
      throw new HttpError(409, "conflict", "already there")
    })
    expect(res.status).toBe(409)
    expect(await res.json()).toEqual({ error: { type: "conflict", message: "already there" } })
  })

  it("sends an HttpError's headers, like Retry-After on a 429", async () => {
    const res = await call(() => {
      throw new HttpError(429, "rate_limited", "slow down", { "retry-after": "30" })
    })
    expect(res.status).toBe(429)
    expect(res.headers.get("retry-after")).toBe("30")
  })
})
