import { eq } from "drizzle-orm"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { openDb, type DB } from "../db"
import { jobs } from "../db/schema"
import { GatewayError } from "../gateway/client"
import { BACKOFF_MS, claimNext, enqueue, recoverStale } from "./queue"
import { JobRunner, type Handlers } from "./runner"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

const noop = async () => {}
const quiet = () => {}

function handlers(over: Partial<Handlers> = {}): Handlers {
  return { ingest: noop, extract: noop, profile: noop, ...over }
}

describe("queue", () => {
  it("claims due jobs oldest first and skips future ones", () => {
    const now = Date.now()
    enqueue(db, "ingest", { documentId: "a" }, now + 60_000)
    const b = enqueue(db, "ingest", { documentId: "b" }, now - 10)
    const job = claimNext(db, now)
    expect(job).toMatchObject({ id: b, status: "running", attempts: 1 })
    expect(claimNext(db, now)).toBeUndefined()
  })

  it("dedupes queued profile jobs per container", () => {
    expect(enqueue(db, "profile", { container: "x" })).not.toBeNull()
    expect(enqueue(db, "profile", { container: "x" })).toBeNull()
    expect(enqueue(db, "profile", { container: "y" })).not.toBeNull()
  })

  it("requeues jobs interrupted mid-run", () => {
    enqueue(db, "ingest", { documentId: "a" })
    claimNext(db)
    expect(recoverStale(db)).toBe(1)
    expect(claimNext(db)).toBeDefined()
  })
})

describe("JobRunner", () => {
  it("passes the payload and marks the job done", async () => {
    const ingest = vi.fn(noop)
    const runner = new JobRunner(db, handlers({ ingest }), quiet)
    const id = enqueue(db, "ingest", { documentId: "doc_1" })!
    await runner.runOnce()
    expect(ingest).toHaveBeenCalledWith({ documentId: "doc_1" }, expect.objectContaining({ id }))
    expect(db.select().from(jobs).where(eq(jobs.id, id)).get()?.status).toBe("done")
  })

  it("backs off 30 s, 2 min, 10 min, then gives up", async () => {
    const onGiveUp = vi.fn()
    const runner = new JobRunner(db, handlers({ extract: async () => Promise.reject(new Error("boom")), onGiveUp }), quiet)
    const id = enqueue(db, "extract", { documentId: "d" })!
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
    const id = enqueue(db, "profile", { container: "c" })!
    const before = Date.now()
    await runner.runOnce()
    const row = db.select().from(jobs).where(eq(jobs.id, id)).get()!
    expect(row.runAfter - before).toBeGreaterThanOrEqual(300_000)
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
      setTimeout(() => enqueue(db, "ingest", { documentId: "late" }), 20)
    })
    await expect(done).resolves.toBeUndefined()
  })
})
