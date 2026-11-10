import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest"
import { handleAdd, MAX_MULTIPART_BYTES } from "./add"
import { openDb, type DB } from "./db"
import { documents, jobs } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { JobRunner } from "./jobs/runner"
import { MAX_JSON_BYTES } from "./http"
import { createHandlers } from "./pipeline"

const W = "ws_test"
let db: DB
let dataDir: string
beforeAll(() => {
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-add-"))
  process.env.DATA_DIR = dataDir
})
afterAll(() => {
  delete process.env.DATA_DIR
  fs.rmSync(dataDir, { recursive: true, force: true })
})
let runner: JobRunner | undefined
beforeEach(() => {
  db = openDb(":memory:")
})
afterEach(async () => {
  await runner?.stop()
  runner = undefined
})

// Starts the real job loop with a model that answers extract calls from a script.
function startRunner(replies: unknown[]) {
  const chat = vi.fn(async (req: { system: string; schema: { parse: (x: unknown) => unknown } }) => {
    if (req.system.includes("profile")) return req.schema.parse({ static: "", dynamic: "" })
    return req.schema.parse(replies.shift() ?? { add: [] })
  }) as unknown as typeof chatJson
  runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-09") }), () => {})
  runner.start()
}

function post(body: unknown, query = "") {
  return handleAdd(
    db, W,
    new Request(`http://localhost/v1/memories${query}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) }),
  )
}

function postFile(fields: Record<string, string>, query = "") {
  const form = new FormData()
  form.set("file", new File(["Owns a grey cat named Abu."], "notes.txt", { type: "text/plain" }))
  for (const [k, v] of Object.entries(fields)) form.set(k, v)
  return handleAdd(db, W, new Request(`http://localhost/v1/memories${query}`, { method: "POST", body: form }))
}

// An endless body that counts how much of it was read.
function endless(contentType: string, headers: Record<string, string> = {}) {
  const chunk = new Uint8Array(1024 * 1024).fill(32)
  const state = { pulled: 0 }
  const body = new ReadableStream<Uint8Array>({
    pull(c) {
      state.pulled += chunk.byteLength
      c.enqueue(chunk)
    },
  })
  const request = new Request("http://localhost/v1/memories", {
    method: "POST",
    headers: { "content-type": contentType, ...headers },
    body,
    duplex: "half",
  } as RequestInit)
  return { request, state }
}

describe("body size", () => {
  it("refuses a declared length over the cap before reading", async () => {
    for (const [type, max] of [["application/json", MAX_JSON_BYTES], ["multipart/form-data; boundary=x", MAX_MULTIPART_BYTES]] as const) {
      const { request, state } = endless(type, { "content-length": String(max + 1) })
      await expect(handleAdd(db, W, request)).rejects.toMatchObject({ status: 413, type: "too_large" })
      expect(state.pulled).toBeLessThanOrEqual(2 * 1024 * 1024)
    }
  })

  it("stops reading a streamed body once it passes the cap", async () => {
    for (const [type, max] of [["application/json", MAX_JSON_BYTES], ["multipart/form-data; boundary=x", MAX_MULTIPART_BYTES]] as const) {
      const { request, state } = endless(type)
      await expect(handleAdd(db, W, request)).rejects.toMatchObject({ status: 413, type: "too_large" })
      expect(state.pulled).toBeLessThan(max + 4 * 1024 * 1024)
    }
  })
})

describe("source_id", () => {
  it("returns the existing document for a repeated source_id", async () => {
    const first = await post({ container: "zaki", content: "Flight to Jakarta on Friday", source_id: "gmail:1a1ab7843db48820" })
    expect(first.status).toBe(202)
    const a = await first.json()
    expect(a).toMatchObject({ container: "zaki", status: "queued", sourceId: "gmail:1a1ab7843db48820", duplicate: false })

    const second = await post({ container: "zaki", content: "Flight to Jakarta on Friday (re-sent)", source_id: "gmail:1a1ab7843db48820" })
    expect(second.status).toBe(200)
    const b = await second.json()
    expect(b).toMatchObject({ id: a.id, duplicate: true, sourceId: "gmail:1a1ab7843db48820" })

    expect(db.select().from(documents).all()).toHaveLength(1)
    expect(db.select().from(jobs).all()).toHaveLength(1)
  })

  it("allows the same source_id in another container", async () => {
    const a = await (await post({ container: "zaki", content: "x", source_id: "gmail:abc" })).json()
    const res = await post({ container: "eva", content: "x", source_id: "gmail:abc" })
    expect(res.status).toBe(202)
    const b = await res.json()
    expect(b.duplicate).toBe(false)
    expect(b.id).not.toBe(a.id)
    expect(db.select().from(documents).all()).toHaveLength(2)
  })

  it("never treats documents without a source_id as duplicates", async () => {
    await post({ container: "zaki", content: "same text" })
    const res = await post({ container: "zaki", content: "same text" })
    expect(res.status).toBe(202)
    expect(db.select().from(documents).all()).toHaveLength(2)
  })

  it("rejects a source_id over 200 characters", async () => {
    // withKey turns HttpError into the JSON error response.
    await expect(post({ container: "zaki", content: "x", source_id: "s".repeat(201) })).rejects.toMatchObject({ status: 400 })
  })

  it("dedupes multipart uploads by the source_id form field", async () => {
    const first = await postFile({ container: "zaki", source_id: "drive:notes" })
    expect(first.status).toBe(202)
    const a = await first.json()
    const second = await postFile({ container: "zaki", source_id: "drive:notes" })
    expect(second.status).toBe(200)
    expect(await second.json()).toMatchObject({ id: a.id, duplicate: true })
    expect(fs.readdirSync(path.join(dataDir, "uploads"))).toEqual([a.id])
  })
})

describe("wait", () => {
  it("returns the extracted facts once the document is done", async () => {
    startRunner([
      { add: [{ fact: "Loves Adidas shoes", keywords: "sneakers sepatu", kind: "dynamic" }] },
      { update: [{ ref: 1, fact: "Switched to Puma shoes", keywords: "sneakers sepatu puma" }] },
    ])
    const first = await post({ container: "zaki", content: "I love my Adidas shoes.", wait: true })
    expect(first.status).toBe(200)
    const a = await first.json()
    expect(a).toMatchObject({ container: "zaki", status: "done", settled: true, error: null, duplicate: false, updated: [], invalidated: 0 })
    expect(a.added.map((m: { fact: string }) => m.fact)).toEqual(["Loves Adidas shoes"])

    const second = await post({ container: "zaki", content: "Switched to Puma.", source_id: "chat:42" }, "?wait=1")
    expect(second.status).toBe(200)
    const b = await second.json()
    expect(b.added).toEqual([])
    expect(b.updated).toMatchObject([{ fact: "Switched to Puma shoes", kind: "dynamic", supersedesId: a.added[0].id }])

    // A repeated source_id with wait returns the finished original.
    const again = await post({ container: "zaki", content: "Switched to Puma.", source_id: "chat:42", wait: true })
    expect(again.status).toBe(200)
    expect(await again.json()).toMatchObject({ id: b.id, duplicate: true, settled: true, status: "done", updated: b.updated })
  })

  it("includes the review note on a fact that conflicts with an older one", async () => {
    startRunner([
      { add: [{ fact: "Salary is paid weekly", keywords: "salary gaji", kind: "dynamic" }] },
      { conflict: [{ ref: 1, fact: "Salary is paid on the last Friday of the month", keywords: "salary gaji", note: "Contract says weekly" }] },
    ])
    await post({ container: "zaki", content: "Contract: salary is paid weekly.", wait: true })
    const res = await post({ container: "zaki", content: "Email: salary goes out on the last Friday.", wait: true })
    const out = await res.json()
    expect(out.added).toMatchObject([{ fact: "Salary is paid on the last Friday of the month", supersedesId: null }])
    expect(out.added[0].reviewNote).toBeTruthy()
  })

  it("waits on a multipart upload with the wait form field", async () => {
    startRunner([{ add: [{ fact: "Owns a grey cat named Abu", keywords: "pet kucing", kind: "static" }] }])
    const res = await postFile({ container: "zaki", wait: "1", wait_timeout: "30" })
    expect(res.status).toBe(200)
    const out = await res.json()
    expect(out).toMatchObject({ status: "done", settled: true })
    expect(out.added.map((m: { fact: string }) => m.fact)).toEqual(["Owns a grey cat named Abu"])
  })

  it("returns 202 with the id when the wait times out", async () => {
    // No job loop running, so the document stays queued.
    const started = Date.now()
    const res = await post({ container: "zaki", content: "Something to remember", wait: true, wait_timeout: 0.2 })
    expect(Date.now() - started).toBeGreaterThanOrEqual(190)
    expect(res.status).toBe(202)
    const out = await res.json()
    expect(out).toMatchObject({ status: "queued", settled: false, duplicate: false })
    expect(out.id).toMatch(/^doc_/)
    expect(out.added).toBeUndefined()
  })

  it("stops waiting when the client goes away", async () => {
    const ctrl = new AbortController()
    const pending = handleAdd(
      db, W,
      new Request("http://localhost/v1/memories?wait=1", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ container: "zaki", content: "x" }),
        signal: ctrl.signal,
      }),
    )
    setTimeout(() => ctrl.abort(), 50)
    const res = await pending
    expect(res.status).toBe(202)
  })

  it("rejects a wait_timeout over 120 seconds", async () => {
    await expect(post({ container: "zaki", content: "x" }, "?wait=1&wait_timeout=121")).rejects.toMatchObject({ status: 400 })
    await expect(post({ container: "zaki", content: "x", wait: true, wait_timeout: 121 })).rejects.toMatchObject({ status: 400 })
  })
})
