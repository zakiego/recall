import { eq } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { openDb, type DB } from "../db"
import { jobs } from "../db/schema"
import { GatewayError } from "../gateway/client"
import { BACKOFF_MS, claimNext, enqueue, recoverStale } from "./queue"
import { JobRunner, type Handlers } from "./runner"
import { JobWaitTimeout, waitForJob } from "./wait"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

const noop = async () => {}
const quiet = () => {}

function handlers(over: Partial<Handlers> = {}): Handlers {
  return { ingest: noop, extract: noop, profile: noop, ask: noop, reminders: noop, backfill_dates: noop, people_backfill: noop, app_sync: noop, ...over }
}

describe("queue", () => {
  it("claims due jobs oldest first and skips future ones", () => {
    const now = Date.now()
    enqueue(db, "ws_a", "ingest", { documentId: "a" }, now + 60_000)
    const b = enqueue(db, "ws_a", "ingest", { documentId: "b" }, now - 10)
    const job = claimNext(db, now)
    expect(job).toMatchObject({ id: b, status: "running", attempts: 1 })
    expect(claimNext(db, now)).toBeUndefined()
  })

  it("dedupes queued profile jobs per container", () => {
    expect(enqueue(db, "ws_a", "profile", { container: "x" })).not.toBeNull()
    expect(enqueue(db, "ws_a", "profile", { container: "x" })).toBeNull()
    expect(enqueue(db, "ws_a", "profile", { container: "y" })).not.toBeNull()
    // Same container name in another workspace is a different profile.
    expect(enqueue(db, "ws_b", "profile", { container: "x" })).not.toBeNull()
  })

  it("runs asks before older background jobs", () => {
    const now = Date.now()
    const ingest = enqueue(db, "ws_a", "ingest", { documentId: "a" }, now - 3000)
    const extract = enqueue(db, "ws_a", "extract", { documentId: "b" }, now - 2000)
    const profile = enqueue(db, "ws_a", "profile", { container: "c" }, now - 1000)
    const ask1 = enqueue(db, "ws_a", "ask", { container: "c", question: "first?", includeChunks: false }, now)
    const ask2 = enqueue(db, "ws_a", "ask", { container: "c", question: "second?", includeChunks: false }, now)
    const order = [1, 2, 3, 4, 5].map(() => claimNext(db, now)?.id)
    expect(order).toEqual([ask1, ask2, ingest, extract, profile])
  })

  it("fails asks left over from a previous process instead of rerunning them", () => {
    enqueue(db, "ws_a", "ingest", { documentId: "a" })
    claimNext(db)
    const ask = enqueue(db, "ws_a", "ask", { container: "c", question: "q", includeChunks: false })!
    const queuedAsk = enqueue(db, "ws_a", "ask", { container: "c", question: "q2", includeChunks: false })!
    claimNext(db)
    expect(recoverStale(db)).toBe(1)
    const status = (id: number) => db.select().from(jobs).where(eq(jobs.id, id)).get()?.status
    expect([status(ask), status(queuedAsk)]).toEqual(["failed", "failed"])
  })

  it("requeues jobs interrupted mid-run", () => {
    enqueue(db, "ws_a", "ingest", { documentId: "a" })
    claimNext(db)
    expect(recoverStale(db)).toBe(1)
    expect(claimNext(db)).toBeDefined()
  })
})

describe("JobRunner", () => {
  it("passes the payload and marks the job done", async () => {
    const ingest = vi.fn(noop)
    const runner = new JobRunner(db, handlers({ ingest }), quiet)
    const id = enqueue(db, "ws_a", "ingest", { documentId: "doc_1" })!
    await runner.runOnce()
    expect(ingest).toHaveBeenCalledWith({ documentId: "doc_1" }, expect.objectContaining({ id }))
    expect(db.select().from(jobs).where(eq(jobs.id, id)).get()?.status).toBe("done")
  })

  it("backs off 30 s, 2 min, 10 min, then gives up", async () => {
    const onGiveUp = vi.fn()
    const runner = new JobRunner(db, handlers({ extract: async () => Promise.reject(new Error("boom")), onGiveUp }), quiet)
    const id = enqueue(db, "ws_a", "extract", { documentId: "d" })!
    const delays: number[] = []
    let now = Date.now()
    for (let i = 0; i < 4; i++) {
      const before = Date.now()
      await runner.runOnce(now)
      const row = db.select().from(jobs).where(eq(jobs.id, id)).get()!
      if (i < 3) {
        expect(row.status).toBe("queued")
        delays.push(Math.round((row.runAfter - before) / 1000) * 1000)
        now = row.runAfter
      } else {
        expect(row.status).toBe("failed")
        expect(row.lastError).toBe("Error: boom")
      }
    }
    expect(delays).toEqual(BACKOFF_MS)
    expect(onGiveUp).toHaveBeenCalledOnce()
  })

  it("honors a longer retry-after from the gateway", async () => {
    const runner = new JobRunner(
      db,
      handlers({ profile: async () => Promise.reject(new GatewayError("busy", 429, "overloaded", 300_000)) }),
      quiet,
    )
    const id = enqueue(db, "ws_a", "profile", { container: "c" })!
    const before = Date.now()
    await runner.runOnce()
    const row = db.select().from(jobs).where(eq(jobs.id, id)).get()!
    expect(row.runAfter - before).toBeGreaterThanOrEqual(300_000)
  })

  it("hands the handler's result to a waiter", async () => {
    const runner = new JobRunner(db, handlers({ ask: async (p) => ({ echo: p.question }) }), quiet)
    const id = enqueue(db, "ws_a", "ask", { container: "c", question: "hi", includeChunks: false })!
    const waiting = waitForJob(id, { timeoutMs: 1000 })
    await runner.runOnce()
    await expect(waiting).resolves.toEqual({ echo: "hi" })
  })

  it("rejects a waiter when the job gives up, without retrying asks", async () => {
    const ask = vi.fn(async () => Promise.reject(new Error("nope")))
    const runner = new JobRunner(db, handlers({ ask }), quiet)
    const id = enqueue(db, "ws_a", "ask", { container: "c", question: "hi", includeChunks: false })!
    const waiting = waitForJob(id, { timeoutMs: 1000 })
    await runner.runOnce()
    await expect(waiting).rejects.toThrow("nope")
    expect(db.select().from(jobs).where(eq(jobs.id, id)).get()?.status).toBe("failed")
    expect(await runner.runOnce(Date.now() + 3_600_000)).toBeUndefined()
  })

  it("times a waiter out", async () => {
    const id = enqueue(db, "ws_a", "ask", { container: "c", question: "hi", includeChunks: false })!
    await expect(waitForJob(id, { timeoutMs: 20 })).rejects.toBeInstanceOf(JobWaitTimeout)
  })

  it("runs jobs enqueued while the loop is idle", async () => {
    const done = new Promise<void>((resolve) => {
      const runner = new JobRunner(
        db,
        handlers({
          ingest: async () => {
            resolve()
            void runner.stop()
          },
        }),
        quiet,
      )
      runner.start()
      setTimeout(() => enqueue(db, "ws_a", "ingest", { documentId: "late" }), 20)
    })
    await expect(done).resolves.toBeUndefined()
  })
})
