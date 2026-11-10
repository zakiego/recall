// The Oslo and Finansial pull connectors against a fake changes feed: paging,
// the cursor on failure, Oslo's day staging, dedupe through source_id,
// Finansial thresholds and money, rule versions, transaction edits and deletes, the hourly job and
// workspace isolation.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { and, eq } from "drizzle-orm"
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest"
import { openDb, setDb, type DB } from "../db"
import { appConnectorStaging, appConnectors, chunks, documents, jobs, memories } from "../db/schema"
import type { chatJson } from "../gateway/client"
import { JobRunner } from "../jobs/runner"
import { deleteDocumentAndMemories } from "../memories"
import { createHandlers } from "../pipeline"
import { createWorkspace } from "../users"
import { appConnectorView, getAppConnector, runAppSync, saveAppConnector, scheduleAppSync, syncAppConnector, type SyncDeps } from "./apps"
import { fetchPage, pullChanges, type ChangeItem } from "./changes-feed"
import { createFakeFeed, type FakeFeed } from "./fake-feed"
import { finansialDocument, finansialSettings, formatMoney, meetsThreshold } from "./finansial"
import { localDay, osloDigest } from "./oslo"

const ENV = { CONNECTOR_SECRET: "test-connector-secret-0123456789abcdefghijklmnop" }
const saved: Record<string, string | undefined> = {}
const OSLO_KEY = "oslo-key-SECRET-123"
const FIN_KEY = "fin-key-SECRET-456"

let db: DB
let dataDir: string
const ws = { a: "", b: "" }

beforeAll(() => {
  for (const k of [...Object.keys(ENV), "DATA_DIR", "MODELS_DIR"]) saved[k] = process.env[k]
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-apps-"))
  // The extract tests embed: keep the shared model cache instead of downloading into the temp dir.
  Object.assign(process.env, ENV, { DATA_DIR: dataDir, MODELS_DIR: process.env.MODELS_DIR ?? path.resolve("data/models") })
  db = openDb(":memory:")
  setDb(db)
  ws.a = createWorkspace(db, { name: "Alpha" }).id
  ws.b = createWorkspace(db, { name: "Beta" }).id
})

afterAll(() => {
  for (const [k, v] of Object.entries(saved)) {
    if (v === undefined) delete process.env[k]
    else process.env[k] = v
  }
  fs.rmSync(dataDir, { recursive: true, force: true })
})

beforeEach(() => {
  db.delete(appConnectors).run()
  db.delete(memories).run()
  db.delete(documents).run()
  db.delete(jobs).run()
})

// --- fixtures --------------------------------------------------------------------

let seq = 0
function task(id: string, updatedAt: string, data: Partial<Record<string, unknown>> = {}, deleted = false): ChangeItem {
  return {
    kind: "task",
    id,
    updatedAt,
    url: `https://todo.zakiego.com/tasks/${id}`,
    deleted,
    data: { title: `Task ${id}`, status: "done", projectId: "p1", projectName: "Recall", dueDate: null, completedAt: updatedAt, tags: [], notes: "", ...data },
  }
}
function project(id: string, updatedAt: string, data: Partial<Record<string, unknown>> = {}): ChangeItem {
  return { kind: "project", id, updatedAt, url: `https://todo.zakiego.com/projects/${id}`, deleted: false, data: { name: `Project ${id}`, status: "active", completedAt: null, ...data } }
}
function txn(id: string, amountMinor: number, data: Partial<Record<string, unknown>> = {}, updatedAt = `2026-11-09T0${seq++ % 10}:00:00.000Z`): ChangeItem {
  return {
    kind: "transaction",
    id,
    updatedAt,
    url: `https://finansial.zakiego.com/transactions/${id}`,
    deleted: false,
    data: { date: "2026-11-09", kind: "expense", amountMinor, currency: "IDR", minorUnitDigits: 0, account: "BCA", payee: "Tokopedia", category: "Electronics", note: "new monitor", tags: ["work"], ...data },
  }
}
function rule(id: string, updatedAt: string, data: Partial<Record<string, unknown>> = {}): ChangeItem {
  return {
    kind: "recurring_rule",
    id,
    updatedAt,
    url: `https://finansial.zakiego.com/recurring/${id}`,
    deleted: false,
    data: { name: "Rent", kind: "expense", amountMinor: 5_000_000, currency: "IDR", minorUnitDigits: 0, frequency: "monthly", nextDueDate: "2026-12-01", payee: "Pak Budi", category: "Housing", active: true, ...data },
  }
}

// Makassar is UTC+8. 10:00 local on Nov 10 is 02:00Z.
const at = (iso: string) => () => Date.parse(iso)
function deps(feed: FakeFeed, now: string, extra: Partial<SyncDeps> = {}): SyncDeps {
  return { fetch: feed.fetch, now: at(now), ...extra }
}

function setup(workspaceId: string, provider: "oslo" | "finansial", key: string, extra: { settings?: Record<string, unknown>; container?: string } = {}) {
  return saveAppConnector(db, {
    workspaceId,
    provider,
    userId: null,
    container: extra.container ?? "life",
    baseUrl: provider === "oslo" ? "https://todo.zakiego.com" : "https://finansial.zakiego.com",
    apiKey: key,
    enabled: true,
    settings: { timezone: "Asia/Makassar", ...extra.settings },
  })
}

const docs = (workspaceId: string) =>
  db.select().from(documents).where(eq(documents.workspaceId, workspaceId)).orderBy(documents.sourceId).all()
const staged = (connectorId: string) =>
  db.select().from(appConnectorStaging).where(eq(appConnectorStaging.connectorId, connectorId)).orderBy(appConnectorStaging.itemKey).all()

// --- the feed client ----------------------------------------------------------------

describe("changes feed client", () => {
  it("pages until hasMore is false and stores the cursor with each page", async () => {
    const feed = createFakeFeed({ key: OSLO_KEY, items: Array.from({ length: 250 }, (_, i) => task(`t${String(i).padStart(3, "0")}`, `2026-11-01T00:00:${String(i % 60).padStart(2, "0")}.${String(i).padStart(3, "0")}Z`)) })
    const cursors: (string | null)[] = []
    const res = await pullChanges(feed.fetch, { baseUrl: "https://todo.zakiego.com/", apiKey: OSLO_KEY, cursor: null }, (_p, c) => cursors.push(c))
    expect(res).toMatchObject({ pages: 3, items: 250, caughtUp: true })
    expect(feed.calls.map((c) => c.limit)).toEqual([100, 100, 100])
    expect(feed.calls[0]!.cursor).toBeNull()
    expect(feed.calls[1]!.cursor).toBe(cursors[0])
    expect(feed.calls[0]!.auth).toBe(`Bearer ${OSLO_KEY}`)
    expect(feed.calls[0]!.url).toBe("https://todo.zakiego.com/api/v1/changes?limit=100")
    // An empty page echoes the cursor and ends the run.
    const again = await pullChanges(feed.fetch, { baseUrl: "https://todo.zakiego.com", apiKey: OSLO_KEY, cursor: res.cursor }, () => {})
    expect(again).toMatchObject({ pages: 1, items: 0, cursor: res.cursor, caughtUp: true })
  })

  it("stops at the page cap and says it isn't caught up", async () => {
    const feed = createFakeFeed({ key: OSLO_KEY, items: Array.from({ length: 30 }, (_, i) => task(`t${String(i).padStart(2, "0")}`, `2026-11-01T00:00:${String(i).padStart(2, "0")}Z`)) })
    const res = await pullChanges(feed.fetch, { baseUrl: "https://x.test", apiKey: OSLO_KEY, cursor: null, maxPages: 2, limit: 10 }, () => {})
    expect(res).toMatchObject({ pages: 2, items: 20, caughtUp: false })
  })

  it("names the status on failure and never the key", async () => {
    const feed = createFakeFeed({ key: OSLO_KEY })
    await expect(fetchPage(feed.fetch, { baseUrl: "https://x.test", apiKey: "wrong-key-XYZ", cursor: null })).rejects.toMatchObject({ code: "auth", status: 401 })
    await expect(fetchPage(feed.fetch, { baseUrl: "https://x.test", apiKey: OSLO_KEY, cursor: "garbage" })).rejects.toMatchObject({ code: "bad_cursor" })
    feed.failNext(503)
    await expect(fetchPage(feed.fetch, { baseUrl: "https://x.test", apiKey: OSLO_KEY, cursor: null })).rejects.toThrow("the app answered HTTP 503: boom")
    feed.failNext("network")
    await expect(fetchPage(feed.fetch, { baseUrl: "https://x.test", apiKey: OSLO_KEY, cursor: null })).rejects.toMatchObject({ code: "network" })
  })

  it("rejects a response that doesn't match the contract and skips bad items", async () => {
    const bad = (async () => new Response(JSON.stringify({ rows: [] }), { status: 200 })) as unknown as typeof fetch
    await expect(fetchPage(bad, { baseUrl: "https://x.test", apiKey: "k", cursor: null })).rejects.toMatchObject({ code: "bad_response" })
    const mixed = (async () =>
      new Response(JSON.stringify({ items: [task("ok", "2026-11-01T00:00:00Z"), { kind: "task" }, { ...task("x", "not a date") }], nextCursor: "c1", hasMore: false }), {
        status: 200,
      })) as unknown as typeof fetch
    const page = await fetchPage(mixed, { baseUrl: "https://x.test", apiKey: "k", cursor: null })
    expect(page.items.map((i) => i.id)).toEqual(["ok"])
    expect(page.skipped).toBe(2)
  })

  it("times out a feed that hangs", async () => {
    const hang = ((_u: string, init?: RequestInit) =>
      new Promise((_r, reject) => init?.signal?.addEventListener("abort", () => reject(init.signal!.reason)))) as unknown as typeof fetch
    await expect(fetchPage(hang, { baseUrl: "https://x.test", apiKey: "k", cursor: null, timeoutMs: 20 })).rejects.toMatchObject({ code: "timeout" })
  })
})

// --- storage -----------------------------------------------------------------------

describe("stored connectors", () => {
  it("seals the API key and never shows it", () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    expect(row.apiKey).not.toContain(OSLO_KEY)
    expect(row.apiKey).toMatch(/^v1\./)
    const view = appConnectorView(db, ws.a, "oslo")
    expect(view).toMatchObject({ hasKey: true, enabled: true, baseUrl: "https://todo.zakiego.com", container: "life" })
    expect(JSON.stringify(view)).not.toContain("v1.")
    // Saving without a key keeps it; a new address starts the feed over.
    db.update(appConnectors).set({ cursor: "c9", caughtUpAt: 1 }).where(eq(appConnectors.id, row.id)).run()
    const kept = saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl: "https://todo.zakiego.com/", enabled: false })
    expect(kept).toMatchObject({ apiKey: row.apiKey, cursor: "c9", enabled: false })
    const moved = saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl: "https://todo.zakiego.com/v2", enabled: true })
    expect(moved).toMatchObject({ cursor: null, caughtUpAt: null, apiKey: row.apiKey })
  })

  it("drops the stored key when the host changes, so it never goes to the new host", () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    const save = (baseUrl: string, more: Partial<Parameters<typeof saveAppConnector>[1]> = {}) =>
      saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl, enabled: false, ...more })
    expect(() => save("https://evil.example.com", { enabled: true })).toThrow(/paste the API key again/)
    expect(getAppConnector(db, ws.a, "oslo")).toMatchObject({ apiKey: row.apiKey, baseUrl: "https://todo.zakiego.com" })
    expect(save("https://evil.example.com")).toMatchObject({ apiKey: null, baseUrl: "https://evil.example.com" })
    // Moving back doesn't bring the old key back either.
    expect(save("https://todo.zakiego.com")).toMatchObject({ apiKey: null })
    const fresh = save("https://todo.zakiego.com:8443", { apiKey: OSLO_KEY, enabled: true })
    expect(fresh.apiKey).toMatch(/^v1\./)
  })

  it("defaults to the production addresses and the IDR threshold before setup", () => {
    expect(appConnectorView(db, ws.a, "oslo")).toMatchObject({ configured: false, baseUrl: "https://todo.zakiego.com", hasKey: false, enabled: false })
    expect(appConnectorView(db, ws.a, "finansial")).toMatchObject({ baseUrl: "https://finansial.zakiego.com", thresholds: { IDR: 1_000_000 } })
  })

  it("rejects an address that isn't https, except on this machine", () => {
    const save = (baseUrl: string) => saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl, enabled: false })
    expect(() => save("ftp://todo")).toThrow(/https/)
    expect(() => save("http://todo.zakiego.com")).toThrow(/https/)
    expect(() => save("http://192.168.1.5:3000")).toThrow(/https/)
    expect(save("http://localhost:3000").baseUrl).toBe("http://localhost:3000")
    expect(save("http://127.0.0.1:3000").baseUrl).toBe("http://127.0.0.1:3000")
    expect(() => saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl: "not a url", enabled: false })).toThrow(/address/)
  })
})

// --- sync and the cursor --------------------------------------------------------

describe("sync", () => {
  it("keeps the old cursor when the feed fails, and records a safe error", async () => {
    const row = setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("a", 2_000_000, {}, "2026-11-09T01:00:00Z")] })
    const ok = await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))
    expect(ok).toMatchObject({ ok: true, documents: 1 })
    const cursor = getAppConnector(db, ws.a, "finansial")!.cursor
    expect(cursor).toBeTruthy()

    feed.add(txn("b", 3_000_000, {}, "2026-11-09T02:00:00Z"))
    feed.failNext(500)
    const failed = await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T03:00:00Z"))
    expect(failed).toMatchObject({ ok: false, error: "the app answered HTTP 500: boom" })
    const after = getAppConnector(db, ws.a, "finansial")!
    expect(after).toMatchObject({ cursor, lastError: "the app answered HTTP 500: boom", lastSyncAt: Date.parse("2026-11-10T03:00:00Z"), lastSuccessAt: Date.parse("2026-11-10T02:00:00Z") })

    // The next run picks up from the same place and clears the error.
    const retry = await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T04:00:00Z"))
    expect(retry).toMatchObject({ ok: true, documents: 1 })
    expect(getAppConnector(db, ws.a, "finansial")!.lastError).toBeNull()
    expect(docs(ws.a).map((d) => d.sourceId)).toEqual(["fin:txn:a", "fin:txn:b"])
    expect(row.id).toBe(after.id)
  })

  it("keeps the pages before a failure mid-run", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: Array.from({ length: 5 }, (_, i) => txn(`t${i}`, 2_000_000, {}, `2026-11-09T0${i}:00:00Z`)) })
    // Page 1 answers, page 2 fails.
    let n = 0
    const flaky = (async (u: string, init?: RequestInit) => (++n === 2 ? new Response("{}", { status: 502 }) : feed.fetch(u, init))) as typeof fetch
    const res = await syncAppConnector(db, ws.a, "finansial", { fetch: flaky, now: at("2026-11-10T02:00:00Z"), pageLimit: 2 })
    expect(res).toMatchObject({ ok: false, documents: 2 })
    expect(getAppConnector(db, ws.a, "finansial")!.cursor).toBe(feed.calls[0] ? Buffer.from("o:2").toString("base64url") : null)
    const rest = await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T03:00:00Z", { pageLimit: 2 }))
    expect(rest).toMatchObject({ ok: true, documents: 3, caughtUp: true })
  })

  it("says so when the key is refused, without the key", async () => {
    setup(ws.a, "oslo", "a-wrong-key-SECRET")
    const feed = createFakeFeed({ key: OSLO_KEY })
    const res = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    expect(res.error).toBe("the app refused the API key (HTTP 401: unauthorized)")
    expect(getAppConnector(db, ws.a, "oslo")!.lastError).not.toContain("SECRET")
  })

  it("runs one sync per connector at a time", async () => {
    setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY })
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const slow = (async (u: string, init?: RequestInit) => {
      await gate
      return feed.fetch(u, init)
    }) as typeof fetch
    const first = syncAppConnector(db, ws.a, "oslo", { fetch: slow, now: at("2026-11-10T02:00:00Z") })
    await expect(syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))).rejects.toMatchObject({ code: "busy" })
    release()
    expect((await first).ok).toBe(true)
  })
})

// --- Oslo ----------------------------------------------------------------------------

describe("Oslo day digests", () => {
  it("buckets by local day in the time zone", () => {
    // 17:30Z on Nov 9 is 01:30 on Nov 10 in Makassar.
    expect(localDay("2026-11-09T17:30:00Z", "Asia/Makassar")).toBe("2026-11-10")
    expect(localDay("2026-11-09T15:59:00Z", "Asia/Makassar")).toBe("2026-11-09")
    expect(localDay("2026-11-09T17:30:00Z", "UTC")).toBe("2026-11-09")
  })

  it("writes a day once it's over and stages today until it closes", async () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({
      key: OSLO_KEY,
      items: [
        task("y1", "2026-11-09T03:00:00Z", { title: "Ship the Gmail connector" }),
        // 23:30 local on Nov 9.
        task("y2", "2026-11-09T15:30:00Z", { title: "Write the release notes", projectName: "Docs" }),
        task("open", "2026-11-09T04:00:00Z", { status: "open", completedAt: null }),
        task("cancel", "2026-11-09T04:30:00Z", { status: "cancelled", completedAt: "2026-11-09T04:30:00Z" }),
        task("gone", "2026-11-09T05:00:00Z", {}, true),
        // 01:00 local on Nov 10: today.
        task("t1", "2026-11-09T17:00:00Z", { title: "Plan the week" }),
      ],
    })
    // 10:00 local on Nov 10.
    const first = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    expect(first).toMatchObject({ ok: true, caughtUp: true, documents: 1 })
    const [day9] = docs(ws.a)
    expect(day9).toMatchObject({ sourceId: "oslo:day:2026-11-09", sourceType: "text", container: "life" })
    expect(day9!.metadata).toMatchObject({ source: "oslo", day: "2026-11-09", documentDate: "2026-11-09", events: 2 })
    expect(day9!.content).toContain("Tasks completed (2):")
    expect(day9!.content).toContain("- Ship the Gmail connector (project: Recall), at 11:00. https://todo.zakiego.com/tasks/y1")
    expect(day9!.content).toContain("- Write the release notes (project: Docs), at 23:30. https://todo.zakiego.com/tasks/y2")
    expect(day9!.content).not.toMatch(/open|cancel|gone/)
    expect(day9!.title).toBe("Oslo on Nov 9, 2026: 2 tasks done")
    // Today's task waits, past the cursor.
    expect(staged(row.id).map((s) => [s.day, s.itemKey])).toEqual([["2026-11-10", "task:t1:2026-11-10"]])

    // The same task done again today, a new project, and a project finished.
    feed.add(
      task("t1", "2026-11-10T03:00:00Z", { title: "Plan the week (v2)", completedAt: "2026-11-09T17:00:00Z" }),
      project("p9", "2026-11-10T04:00:00Z", { name: "Garden" }),
      task("t2", "2026-11-10T05:00:00Z", { title: "Water plants" }),
    )
    const second = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T06:00:00Z"))
    expect(second).toMatchObject({ ok: true, documents: 0 })

    // Next morning the day closes.
    feed.add(project("p9", "2026-11-10T10:00:00Z", { name: "Garden", status: "done", completedAt: "2026-11-10T10:00:00Z" }))
    const third = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T17:00:00Z"))
    expect(third).toMatchObject({ ok: true, documents: 1 })
    const day10 = docs(ws.a).find((d) => d.sourceId === "oslo:day:2026-11-10")!
    expect(day10.content!.match(/Plan the week/g)).toHaveLength(1)
    expect(day10.content).toContain("Plan the week (v2)")
    expect(day10.content).toContain("Projects created (1):\n- Garden, at 12:00. https://todo.zakiego.com/projects/p9")
    expect(day10.content).toContain("Projects completed (1):\n- Garden, at 18:00.")
    expect(day10.title).toBe("Oslo on Nov 10, 2026: 2 tasks done, 1 project started, 1 project finished")
    // Only the "already reported" marker for the project is left.
    expect(staged(row.id).map((s) => s.itemKey)).toEqual(["project-created:p9"])
  })

  it("drops a task reopened before its day closes", async () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY, items: [task("r1", "2026-11-10T01:00:00Z")] })
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    expect(staged(row.id)).toHaveLength(1)
    feed.add(task("r1", "2026-11-10T03:00:00Z", { status: "open", completedAt: null }))
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T04:00:00Z"))
    expect(staged(row.id)).toHaveLength(0)
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-11T04:00:00Z"))
    expect(docs(ws.a)).toHaveLength(0)
  })

  it("doesn't close a day while the feed has more to read", async () => {
    setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY, items: Array.from({ length: 6 }, (_, i) => task(`m${i}`, `2026-11-09T0${i}:00:00Z`)) })
    const capped = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z", { maxPages: 1, pageLimit: 2 }))
    expect(capped).toMatchObject({ ok: true, caughtUp: false, documents: 0 })
    const done = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T03:00:00Z"))
    expect(done).toMatchObject({ caughtUp: true, documents: 1 })
    expect(docs(ws.a)[0]!.metadata).toMatchObject({ events: 6 })
  })

  it("remembers projects that existed before the first sync without calling them new", async () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY, items: [project("old", "2026-11-09T01:00:00Z")] })
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    feed.add(project("old", "2026-11-10T03:00:00Z", { name: "Renamed" }))
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-11T02:00:00Z"))
    expect(docs(ws.a)).toHaveLength(0)
    expect(staged(row.id).map((s) => [s.itemKey, !!s.writtenAt])).toEqual([["project-created:old", true]])
  })

  it("never rewrites a day, even when its items come back", async () => {
    const row = setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY, items: [task("d1", "2026-11-09T03:00:00Z")] })
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    // Read the whole feed again from the start.
    db.update(appConnectors).set({ cursor: null }).where(eq(appConnectors.id, row.id)).run()
    const again = await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T03:00:00Z"))
    expect(again).toMatchObject({ ok: true, staged: 0, documents: 0 })
    expect(docs(ws.a)).toHaveLength(1)
    expect(staged(row.id)).toHaveLength(0)
  })

  it("only reaches back 30 days on the first sync", async () => {
    setup(ws.a, "oslo", OSLO_KEY)
    const feed = createFakeFeed({ key: OSLO_KEY, items: [task("ancient", "2026-08-01T03:00:00Z"), task("recent", "2026-11-01T03:00:00Z")] })
    await syncAppConnector(db, ws.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
    expect(docs(ws.a).map((d) => d.sourceId)).toEqual(["oslo:day:2026-11-01"])
  })

  it("renders a digest", () => {
    const d = osloDigest(
      "2026-11-10",
      [
        { type: "task", id: "b", title: "Second", url: "https://t/b", at: "2026-11-10T05:00:00Z" },
        { type: "task", id: "a", title: "First", url: "", at: "2026-11-10T01:00:00Z" },
      ],
      "Asia/Makassar",
    )
    expect(d.content).toBe("Activity in Oslo, the todo app, on Tuesday, Nov 10, 2026 (Asia/Makassar time).\n\nTasks completed (2):\n- First, at 09:00.\n- Second, at 13:00. https://t/b")
  })
})

// --- Finansial ------------------------------------------------------------------------

describe("Finansial", () => {
  it("formats money with the minor unit digits", () => {
    expect(formatMoney(1_500_000, "IDR", 0)).toBe("IDR 1,500,000")
    expect(formatMoney(150_000_000, "IDR", 2)).toBe("IDR 1,500,000.00")
    expect(formatMoney(12_345, "USD", 2)).toBe("USD 123.45")
    expect(formatMoney(5, "USD", 2)).toBe("USD 0.05")
    expect(formatMoney(1_234_567, "KWD", 3)).toBe("KWD 1,234.567")
    expect(formatMoney(999, "JPY", 0)).toBe("JPY 999")
  })

  it("applies the per-currency threshold in major units", () => {
    const s = finansialSettings(undefined)
    expect(s.thresholds).toEqual({ IDR: 1_000_000 })
    expect(meetsThreshold(1_000_000, "IDR", 0, s)).toBe(true)
    expect(meetsThreshold(999_999, "IDR", 0, s)).toBe(false)
    expect(meetsThreshold(100_000_000, "IDR", 2, s)).toBe(true)
    expect(meetsThreshold(99_999_999, "IDR", 2, s)).toBe(false)
    expect(meetsThreshold(10_000_000, "USD", 2, s)).toBe(false)
    const usd = finansialSettings({ thresholds: { USD: 100, bad: 5, EUR: -1 } })
    expect(usd.thresholds).toEqual({ USD: 100 })
    expect(meetsThreshold(10_000, "USD", 2, usd)).toBe(true)
    expect(meetsThreshold(9_999, "USD", 2, usd)).toBe(false)
  })

  it("writes plain-English notes for notable items", () => {
    const s = finansialSettings(undefined)
    const big = finansialDocument(txn("big", 1_500_000), s)!
    expect(big).toMatchObject({ sourceId: "fin:txn:big", title: "Expense of IDR 1,500,000 to Tokopedia on 2026-11-09", documentDate: "2026-11-09" })
    expect(big.content).toBe(
      "Finansial transaction on 2026-11-09: an expense of IDR 1,500,000 paid to Tokopedia from the account BCA.\nCategory: Electronics.\nNote: new monitor\nTags: work.\nLink: https://finansial.zakiego.com/transactions/big",
    )
    expect(finansialDocument(txn("small", 999_999), s)).toBeNull()
    expect(finansialDocument(txn("usd", 1_000_000_00, { currency: "USD", minorUnitDigits: 2 }), s)).toBeNull()
    expect(finansialDocument({ ...txn("del", 9_000_000), deleted: true }, s)).toBeNull()
    expect(finansialDocument(txn("in", 2_000_000, { kind: "income", payee: "Socium" }), s)!.content).toContain("an income of IDR 2,000,000 from Socium into the account BCA")

    const r = finansialDocument(rule("r1", "2026-11-10T01:00:00Z"), s)!
    expect(r.sourceId).toMatch(/^fin:rule:r1:[0-9a-f]{16}$/)
    expect(r.title).toBe("Recurring expense: Rent, IDR 5,000,000 monthly")
    expect(r.content).toContain('Finansial recurring rule "Rent", as of 2026-11-10: a monthly expense of IDR 5,000,000 to Pak Budi.')
    expect(r.content).not.toContain("Next due")
    expect(r.content).not.toContain("2026-12-01")
    expect(r.content).toContain("It is active.")
    // Key order in the feed doesn't change the version.
    const shuffled = rule("r1", "2026-11-10T01:00:00Z")
    shuffled.data = Object.fromEntries(Object.entries(shuffled.data).reverse())
    expect(finansialDocument(shuffled, s)!.sourceId).toBe(r.sourceId)
    expect(finansialDocument({ ...rule("r1", "2026-11-11T01:00:00Z"), deleted: true }, s)!.sourceId).toBe("fin:rule:r1:deleted")

    const loanItem = (updatedAt: string, data: Record<string, unknown> = {}, deleted = false): ChangeItem => ({
      kind: "loan",
      id: "l1",
      updatedAt,
      url: "",
      deleted,
      data: { name: "Car", principalMinor: 150_000_000, currency: "IDR", minorUnitDigits: 0, lender: "BCA Finance", status: "active", ...data },
    })
    const loan = finansialDocument(loanItem("2026-11-10T01:00:00Z"), s)!
    expect(loan.sourceId).toMatch(/^fin:loan:l1:[0-9a-f]{16}$/)
    expect(loan.title).toBe("Loan: Car, IDR 150,000,000")
    expect(finansialDocument(loanItem("2026-11-12T01:00:00Z", { paidMinor: 5_000_000 }), s)!.sourceId).toBe(loan.sourceId)
    expect(finansialDocument(loanItem("2026-11-12T01:00:00Z", { status: "paid_off" }), s)!.sourceId).not.toBe(loan.sourceId)
    expect(finansialDocument(loanItem("2026-11-12T01:00:00Z", {}, true), s)!.sourceId).toBe("fin:loan:l1:deleted")
    expect(finansialDocument({ kind: "account", id: "a", updatedAt: "2026-11-10T00:00:00Z", url: "", deleted: false, data: {} }, s)).toBeNull()
  })

  it("ingests every real version of a rule and dedupes the rest by source_id", async () => {
    const row = setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({
      key: FIN_KEY,
      items: [rule("r1", "2026-11-09T01:00:00Z"), txn("t1", 2_500_000, {}, "2026-11-09T02:00:00Z"), txn("t2", 10_000, {}, "2026-11-09T03:00:00Z")],
    })
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))).toMatchObject({ ok: true, documents: 2 })
    const v1 = rule("r1", "2026-11-09T01:00:00Z")
    const v2 = rule("r1", "2026-11-10T01:00:00Z", { amountMinor: 5_500_000 })
    const gone: ChangeItem = { ...rule("r1", "2026-11-10T03:00:00Z"), deleted: true, data: { name: "Rent" } }
    feed.add(v2, gone)
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T04:00:00Z"))).toMatchObject({ ok: true, documents: 2 })
    const all = docs(ws.a)
    const s = finansialSettings(undefined)
    const id = (item: ChangeItem) => finansialDocument(item, s)!.sourceId
    expect(all.map((d) => d.sourceId).sort()).toEqual([id(v1), id(v2), "fin:rule:r1:deleted", "fin:txn:t1"].sort())
    const bySource = (sourceId: string) => all.find((d) => d.sourceId === sourceId)!
    expect(bySource(id(v2)).content).toContain("IDR 5,500,000")
    expect(bySource("fin:rule:r1:deleted").content).toContain("was deleted on 2026-11-10")
    expect(bySource("fin:txn:t1").metadata).toMatchObject({ source: "finansial", kind: "transaction", id: "t1", url: "https://finansial.zakiego.com/transactions/t1", documentDate: "2026-11-09" })

    // Reading the whole feed again adds nothing.
    db.update(appConnectors).set({ cursor: null }).where(eq(appConnectors.id, row.id)).run()
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T05:00:00Z"))).toMatchObject({ ok: true, documents: 0 })
    expect(docs(ws.a)).toHaveLength(4)
  })

  it("keeps a rule that only posts (next due date moves on) as one document", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [rule("r1", "2026-09-01T01:00:00Z", { nextDueDate: "2026-10-01" })] })
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))).toMatchObject({ ok: true, documents: 1 })
    // Three postings: Finansial bumps updated_at and the next due date each time.
    for (const [updatedAt, next] of [
      ["2026-10-01T01:00:00Z", "2026-11-01"],
      ["2026-11-01T01:00:00Z", "2026-12-01"],
      ["2026-11-10T03:00:00Z", "2027-01-01"],
    ]) {
      feed.add(rule("r1", updatedAt!, { nextDueDate: next }))
      expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T04:00:00Z"))).toMatchObject({ ok: true, documents: 0 })
    }
    expect(docs(ws.a)).toHaveLength(1)
    expect(db.select().from(jobs).where(eq(jobs.type, "ingest")).all()).toHaveLength(1)

    // A real change (the amount) is a new version.
    feed.add(rule("r1", "2026-11-10T05:00:00Z", { nextDueDate: "2027-01-01", amountMinor: 6_000_000 }))
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T06:00:00Z"))).toMatchObject({ ok: true, documents: 1 })
    expect(docs(ws.a).map((d) => d.content).filter((c) => c!.includes("IDR 6,000,000"))).toHaveLength(1)
    expect(docs(ws.a)).toHaveLength(2)

    // Deleting it writes one "removed" document, and a second delete event adds nothing.
    const gone: ChangeItem = { ...rule("r1", "2026-11-10T07:00:00Z"), deleted: true, data: { name: "Rent" } }
    feed.add(gone, { ...gone, updatedAt: "2026-11-10T08:00:00Z" })
    expect(await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T09:00:00Z"))).toMatchObject({ ok: true, documents: 1 })
    const removed = docs(ws.a).find((d) => d.sourceId === "fin:rule:r1:deleted")!
    expect(removed.title).toBe("Recurring rule removed: Rent")
    expect(docs(ws.a)).toHaveLength(3)
  })

  it("uses thresholds from settings", async () => {
    setup(ws.a, "finansial", FIN_KEY, { settings: { thresholds: { USD: 50 } } })
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("usd", 6_000, { currency: "USD", minorUnitDigits: 2 }, "2026-11-09T01:00:00Z"), txn("idr", 9_000_000, {}, "2026-11-09T02:00:00Z")] })
    await syncAppConnector(db, ws.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))
    const [d] = docs(ws.a)
    expect(docs(ws.a)).toHaveLength(1)
    expect(d!.title).toBe("Expense of USD 60.00 to Tokopedia on 2026-11-09")
  })
})

describe("Finansial transaction edits and deletes", () => {
  // Plays the model: one fact per transaction, naming the amount it read.
  // onExtract runs before it answers, while the job waits on the "network".
  function factChat(onExtract?: () => void) {
    return (async (req: { system: string; prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
      if (req.system.includes("profile")) return req.schema.parse({ static: "", dynamic: "" })
      onExtract?.()
      const money = /IDR [\d,]+/.exec(req.prompt)?.[0] ?? "an amount"
      return req.schema.parse({ add: [{ fact: `Spent ${money} at Tokopedia on 2026-11-09`, keywords: "expense belanja", kind: "dynamic" }] })
    }) as unknown as typeof chatJson
  }
  async function drain(onExtract?: () => void) {
    const runner = new JobRunner(db, createHandlers(db, { chat: factChat(onExtract), now: () => new Date("2026-11-10") }), () => {})
    for (let i = 0; i < 50; i++) if (!(await runner.runOnce())) return
  }
  const current = () =>
    db
      .select()
      .from(memories)
      .where(eq(memories.workspaceId, ws.a))
      .all()
      .filter((m) => m.validTo === null)
      .map((m) => m.fact)
  const sync = (feed: FakeFeed, now = "2026-11-10T04:00:00Z") => syncAppConnector(db, ws.a, "finansial", deps(feed, now))
  const failed = () => db.select().from(jobs).all().filter((j) => j.status !== "done")

  async function ingested(feed: FakeFeed) {
    expect(await sync(feed, "2026-11-10T02:00:00Z")).toMatchObject({ ok: true, documents: 1, removed: 0 })
    await drain()
    expect(current()).toEqual(["Spent IDR 5,000,000 at Tokopedia on 2026-11-09"])
    return docs(ws.a)[0]!
  }

  it("replaces the document and its facts when the amount is edited", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    const old = await ingested(feed)
    expect(old.metadata).toMatchObject({ fingerprint: expect.stringMatching(/^[0-9a-f]{64}$/) })

    feed.add(txn("t1", 4_500_000, {}, "2026-11-10T03:00:00Z"))
    expect(await sync(feed)).toMatchObject({ ok: true, documents: 1, removed: 1 })
    const [doc] = docs(ws.a)
    expect(docs(ws.a)).toHaveLength(1)
    expect(doc).toMatchObject({ sourceId: "fin:txn:t1", title: "Expense of IDR 4,500,000 to Tokopedia on 2026-11-09" })
    expect(doc!.id).not.toBe(old.id)
    expect(doc!.metadata!.fingerprint).not.toBe(old.metadata!.fingerprint)
    // The old fact is forgotten right away and the old chunks are gone.
    expect(current()).toEqual([])
    expect(db.select().from(chunks).where(eq(chunks.documentId, old.id)).all()).toHaveLength(0)

    await drain()
    expect(current()).toEqual(["Spent IDR 4,500,000 at Tokopedia on 2026-11-09"])
    expect(failed()).toEqual([])
  })

  it("does nothing when an edit doesn't change what the document says", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    const old = await ingested(feed)
    const jobsBefore = db.select().from(jobs).all().length

    // A newer updated_at, tags in another order and a field Recall doesn't use.
    feed.add(txn("t1", 5_000_000, { tags: ["work"], reconciled: true }, "2026-11-10T03:00:00Z"))
    expect(await sync(feed)).toMatchObject({ ok: true, documents: 0, removed: 0 })
    expect(docs(ws.a).map((d) => d.id)).toEqual([old.id])
    expect(db.select().from(jobs).all()).toHaveLength(jobsBefore)
    expect(current()).toEqual(["Spent IDR 5,000,000 at Tokopedia on 2026-11-09"])
  })

  it("removes the document and its facts when the transaction is deleted", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    await ingested(feed)
    feed.add({ ...txn("t1", 0, {}, "2026-11-10T03:00:00Z"), deleted: true, data: {} })
    expect(await sync(feed)).toMatchObject({ ok: true, documents: 0, removed: 1 })
    expect(docs(ws.a)).toHaveLength(0)
    expect(current()).toEqual([])
    // The forgotten fact keeps its history, now without a document.
    expect(db.select().from(memories).all()).toMatchObject([{ fact: "Spent IDR 5,000,000 at Tokopedia on 2026-11-09", validTo: expect.any(Number), sourceDocumentId: null }])
  })

  it("removes the document when an edit takes it under the threshold", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    await ingested(feed)
    feed.add(txn("t1", 500_000, {}, "2026-11-10T03:00:00Z"))
    expect(await sync(feed)).toMatchObject({ ok: true, documents: 0, removed: 1 })
    expect(docs(ws.a)).toHaveLength(0)
    expect(current()).toEqual([])
  })

  it("ignores a deleted transaction it never ingested", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [{ ...txn("t9", 9_000_000, {}, "2026-11-09T01:00:00Z"), deleted: true }, txn("small", 10_000, {}, "2026-11-09T02:00:00Z")] })
    expect(await sync(feed)).toMatchObject({ ok: true, documents: 0, removed: 0 })
    expect(docs(ws.a)).toHaveLength(0)
    expect(db.select().from(jobs).all()).toHaveLength(0)
  })

  it("doesn't let a queued ingest bring back a document deleted before it ran", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    await sync(feed, "2026-11-10T02:00:00Z")
    feed.add({ ...txn("t1", 0, {}, "2026-11-10T03:00:00Z"), deleted: true, data: {} })
    expect(await sync(feed)).toMatchObject({ removed: 1 })
    await drain()
    expect(docs(ws.a)).toHaveLength(0)
    expect(db.select().from(chunks).all()).toHaveLength(0)
    expect(current()).toEqual([])
    expect(failed()).toEqual([])
  })

  it("doesn't let an extract in flight write facts for a document deleted meanwhile", async () => {
    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 5_000_000, {}, "2026-11-09T01:00:00Z")] })
    await sync(feed, "2026-11-10T02:00:00Z")
    const doc = docs(ws.a)[0]!
    // The delete lands while the model is answering.
    await drain(() => deleteDocumentAndMemories(db, ws.a, doc.id))
    expect(docs(ws.a)).toHaveLength(0)
    expect(db.select().from(memories).all()).toHaveLength(0)
    expect(failed()).toEqual([])
  })
})

// --- the job and isolation --------------------------------------------------------

describe("hourly job", () => {
  it("syncs every enabled connector and one failure doesn't block the other", async () => {
    setup(ws.a, "oslo", OSLO_KEY)
    setup(ws.a, "finansial", FIN_KEY)
    const oslo = createFakeFeed({ key: OSLO_KEY })
    const fin = createFakeFeed({ key: FIN_KEY, items: [txn("t1", 2_000_000, {}, "2026-11-09T01:00:00Z")] })
    oslo.failNext(503)
    const route = (async (u: string, init?: RequestInit) => (u.startsWith("https://todo") ? oslo.fetch(u, init) : fin.fetch(u, init))) as typeof fetch
    const run = await runAppSync(db, { fetch: route, now: at("2026-11-10T02:00:00Z") }, () => {})
    expect(run).toMatchObject({ connectors: 2, ok: 1, failed: 1, documents: 1 })
    expect(getAppConnector(db, ws.a, "oslo")!.lastError).toContain("503")
    expect(getAppConnector(db, ws.a, "finansial")!.lastError).toBeNull()
  })

  it("skips connectors that are off or have no key", async () => {
    saveAppConnector(db, { workspaceId: ws.a, provider: "oslo", userId: null, container: "life", baseUrl: "https://todo.zakiego.com", apiKey: OSLO_KEY, enabled: false })
    saveAppConnector(db, { workspaceId: ws.b, provider: "oslo", userId: null, container: "life", baseUrl: "https://todo.zakiego.com", enabled: true })
    const feed = createFakeFeed({ key: OSLO_KEY })
    expect(await runAppSync(db, deps(feed, "2026-11-10T02:00:00Z"), () => {})).toMatchObject({ connectors: 0 })
    expect(feed.calls).toHaveLength(0)
  })

  it("queues exactly one hourly job, and the job queues the next", async () => {
    const now = Date.parse("2026-11-10T02:20:00Z")
    expect(scheduleAppSync(db, now)).toBeTypeOf("number")
    expect(scheduleAppSync(db, now)).toBeNull()
    const queued = db.select().from(jobs).where(eq(jobs.type, "app_sync")).all()
    expect(queued).toHaveLength(1)
    expect(queued[0]!.runAfter).toBe(Date.parse("2026-11-10T03:00:00Z"))

    setup(ws.a, "finansial", FIN_KEY)
    const feed = createFakeFeed({ key: FIN_KEY, items: [txn("j1", 2_000_000, {}, "2026-11-09T01:00:00Z")] })
    const handlers = createHandlers(db, { chat: (async () => ({})) as never, now: () => new Date(), appSync: deps(feed, "2026-11-10T03:00:00Z") })
    const runner = new JobRunner(db, handlers, () => {})
    const job = await runner.runOnce(Date.parse("2026-11-10T03:00:01Z"))
    expect(job?.type).toBe("app_sync")
    expect(db.select().from(jobs).where(and(eq(jobs.type, "app_sync"), eq(jobs.status, "done"))).all()).toHaveLength(1)
    expect(db.select().from(jobs).where(and(eq(jobs.type, "app_sync"), eq(jobs.status, "queued"))).all()).toHaveLength(1)
    expect(docs(ws.a).map((d) => d.sourceId)).toEqual(["fin:txn:j1"])
  })
})

describe("workspace isolation", () => {
  it("keeps each workspace's connector, key, cursor and documents apart", async () => {
    const a = setup(ws.a, "oslo", OSLO_KEY)
    const b = setup(ws.b, "oslo", "beta-key-SECRET")
    const feedA = createFakeFeed({ key: OSLO_KEY, items: [task("a1", "2026-11-09T03:00:00Z", { title: "Alpha work" })] })
    const feedB = createFakeFeed({ key: "beta-key-SECRET", items: [task("b1", "2026-11-09T03:00:00Z", { title: "Beta work" })] })
    await syncAppConnector(db, ws.a, "oslo", deps(feedA, "2026-11-10T02:00:00Z"))
    await syncAppConnector(db, ws.b, "oslo", deps(feedB, "2026-11-10T02:00:00Z"))
    expect(feedA.calls[0]!.auth).toBe(`Bearer ${OSLO_KEY}`)
    expect(feedB.calls[0]!.auth).toBe("Bearer beta-key-SECRET")
    expect(docs(ws.a).map((d) => d.content)).toEqual([expect.stringContaining("Alpha work")])
    expect(docs(ws.b).map((d) => d.content)).toEqual([expect.stringContaining("Beta work")])
    expect(docs(ws.a)[0]!.content).not.toContain("Beta")
    // The same day in two workspaces is two documents with the same source_id.
    expect(docs(ws.a)[0]!.sourceId).toBe(docs(ws.b)[0]!.sourceId)
    expect(appConnectorView(db, ws.b, "oslo").lastSyncAt).not.toBeNull()

    // A sealed key copied into the other workspace's row doesn't open.
    db.update(appConnectors).set({ apiKey: a.apiKey }).where(eq(appConnectors.id, b.id)).run()
    const res = await syncAppConnector(db, ws.b, "oslo", deps(feedA, "2026-11-10T03:00:00Z"))
    expect(res.error).toMatch(/can't be read/)
    expect(feedA.calls).toHaveLength(1)
  })
})
