import { eq } from "drizzle-orm"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { openDb, type DB } from "../db"
import { jobs } from "../db/schema"
import { GatewayError } from "../gateway/client"
import { BACKOFF_MS, MAX_ATTEMPTS, PRUNE_AFTER_MS, claimNext, complete, enqueue, fail, nextRunAt, pruneDone, recoverStale } from "./queue"
import { JobRunner, MAX_RUNNING, type Handlers } from "./runner"
import { JobWaitTimeout, waitForJob } from "./wait"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

const noop = async () => {}
const quiet = () => {}
const row = (id: number) => db.select().from(jobs).where(eq(jobs.id, id)).get()!

// A promise the test resolves by hand, to hold a handler mid-run.
function gate() {
  let open!: () => void
  const opened = new Promise<void>((r) => (open = r))
  return { open, opened }
}

async function until(check: () => boolean, ms = 2000) {
  const end = Date.now() + ms
  while (!check()) {
    if (Date.now() > end) throw new Error("timed out waiting")
    await new Promise((r) => setTimeout(r, 5))
  }
}

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

  it("hands the second slot only to asks", () => {
    const now = Date.now()
    enqueue(db, "ws_a", "extract", { documentId: "a" }, now - 1000)
    const ask = enqueue(db, "ws_a", "ask", { container: "c", question: "q", includeChunks: false }, now + 5000)
    expect(claimNext(db, now, { interactiveOnly: true })).toBeUndefined()
    expect(nextRunAt(db, { interactiveOnly: true })).toBe(now + 5000)
    expect(claimNext(db, now + 5000, { interactiveOnly: true })?.id).toBe(ask)
  })

  it("fails asks left over from a previous process instead of rerunning them", () => {
    enqueue(db, "ws_a", "ingest", { documentId: "a" })
    claimNext(db)
    const ask = enqueue(db, "ws_a", "ask", { container: "c", question: "q", includeChunks: false })!
    const queuedAsk = enqueue(db, "ws_a", "ask", { container: "c", question: "q2", includeChunks: false })!
    claimNext(db)
    expect(recoverStale(db).requeued).toBe(1)
    const status = (id: number) => db.select().from(jobs).where(eq(jobs.id, id)).get()?.status
    expect([status(ask), status(queuedAsk)]).toEqual(["failed", "failed"])
  })

  it("requeues jobs interrupted mid-run", () => {
    enqueue(db, "ws_a", "ingest", { documentId: "a" })
    claimNext(db)
    expect(recoverStale(db)).toEqual({ requeued: 1, gaveUp: [] })
    expect(claimNext(db)).toBeDefined()
  })

  it("fails a job that was on its last attempt when the process died, instead of looping", () => {
    const crashy = enqueue(db, "ws_a", "extract", { documentId: "big.pdf" })!
    const other = enqueue(db, "ws_a", "extract", { documentId: "small" })!
    // The crashy one dies with the process on every attempt.
    for (let i = 1; i < MAX_ATTEMPTS; i++) {
      expect(claimNext(db)?.id).toBe(crashy)
      expect(recoverStale(db).requeued).toBe(1)
    }
    claimNext(db)
    claimNext(db)
    const { requeued, gaveUp } = recoverStale(db, 5000)
    expect(requeued).toBe(1)
    expect(gaveUp.map((j) => j.id)).toEqual([crashy])
    expect(row(crashy)).toMatchObject({ status: "failed", attempts: MAX_ATTEMPTS, finishedAt: 5000, lastError: `the server stopped during attempt ${MAX_ATTEMPTS}, the last one (crash or restart)` })
    expect(row(other)).toMatchObject({ status: "queued", attempts: 1 })
  })

  it("records when each attempt started and finished, and keeps the error after a retry succeeds", () => {
    const id = enqueue(db, "ws_a", "extract", { documentId: "d" })!
    expect(row(id)).toMatchObject({ startedAt: null, finishedAt: null })
    const before = Date.now()
    const first = claimNext(db)!
    expect(row(id).startedAt).toBeGreaterThanOrEqual(before)
    fail(db, first, new Error("gateway hiccup"))
    expect(row(id).finishedAt).toBeGreaterThanOrEqual(row(id).startedAt!)

    claimNext(db, Date.now() + BACKOFF_MS[0]!)
    // A running retry has no finish time yet.
    expect(row(id).finishedAt).toBeNull()
    complete(db, id)
    expect(row(id)).toMatchObject({ status: "done", attempts: 2, lastError: "Error: gateway hiccup" })
    expect(row(id).finishedAt).toBeGreaterThanOrEqual(row(id).startedAt!)
  })

  it("prunes done jobs a week after they finished", () => {
    const now = Date.parse("2026-11-20T00:00:00Z")
    const job = (status: "done" | "failed" | "queued", finishedAt: number | null, createdAt = now - 30 * 86_400_000) => {
      const id = enqueue(db, "ws_a", "ingest", { documentId: `d${Math.random()}` })!
      db.update(jobs).set({ status, finishedAt, createdAt }).where(eq(jobs.id, id)).run()
      return id
    }
    job("done", now - PRUNE_AFTER_MS - 1)
    job("done", now - PRUNE_AFTER_MS)
    // Queued long ago, finished recently: age counts from the finish.
    const recent = job("done", now - PRUNE_AFTER_MS + 60_000)
    // Rows from before migration 0010 have no finish time and use created_at.
    job("done", null)
    const legacyNew = job("done", null, now - 86_400_000)
    const failed = job("failed", now - PRUNE_AFTER_MS - 1)
    const queued = job("queued", null)
    expect(pruneDone(db, now)).toBe(3)
    const left = db.select({ id: jobs.id }).from(jobs).all().map((r) => r.id)
    expect(left.sort()).toEqual([recent, legacyNew, failed, queued].sort())
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

  it("marks a job given up at boot through onGiveUp", () => {
    const onGiveUp = vi.fn()
    const id = enqueue(db, "ws_a", "extract", { documentId: "big.pdf" })!
    db.update(jobs).set({ status: "running", attempts: MAX_ATTEMPTS }).where(eq(jobs.id, id)).run()
    const runner = new JobRunner(db, handlers({ onGiveUp }), quiet)
    runner.start()
    void runner.stop()
    expect(onGiveUp).toHaveBeenCalledOnce()
    expect(onGiveUp.mock.calls[0]![0]).toMatchObject({ id, status: "failed" })
    expect(row(id).status).toBe("failed")
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

  describe("pruning", () => {
    afterEach(() => {
      vi.useRealTimers()
    })

    it("prunes old done jobs every hour, not only at boot", async () => {
      vi.useFakeTimers({ toFake: ["Date"] })
      const t0 = Date.parse("2026-11-20T00:00:00Z")
      vi.setSystemTime(t0)
      const runner = new JobRunner(db, handlers(), quiet)
      runner.start()
      const old = enqueue(db, "ws_a", "ingest", { documentId: "old" })!
      await until(() => row(old).status === "done")
      db.update(jobs).set({ finishedAt: t0 - PRUNE_AFTER_MS + 1000 }).where(eq(jobs.id, old)).run()

      // Still inside the hour: the boot prune already ran, nothing new is pruned.
      vi.setSystemTime(t0 + 30 * 60_000)
      const a = enqueue(db, "ws_a", "ingest", { documentId: "a" })!
      await until(() => row(a).status === "done")
      expect(db.select().from(jobs).where(eq(jobs.id, old)).get()).toBeDefined()

      vi.setSystemTime(t0 + 61 * 60_000)
      const b = enqueue(db, "ws_a", "ingest", { documentId: "b" })!
      await until(() => db.select().from(jobs).where(eq(jobs.id, old)).get() === undefined)
      await until(() => row(b).status === "done")
      await runner.stop()
    })
  })

  describe("slots", () => {
    it("answers an ask while an extraction runs, but never runs two background jobs", async () => {
      const slow = gate()
      const runningNow = new Set<string>()
      let maxBackground = 0
      const runner = new JobRunner(
        db,
        handlers({
          extract: async ({ documentId }) => {
            runningNow.add(documentId)
            maxBackground = Math.max(maxBackground, runningNow.size)
            if (documentId === "slow") await slow.opened
            runningNow.delete(documentId)
          },
          ask: async () => "answer",
        }),
        quiet,
      )
      const first = enqueue(db, "ws_a", "extract", { documentId: "slow" })!
      const second = enqueue(db, "ws_a", "extract", { documentId: "next" })!
      runner.start()
      await until(() => runningNow.has("slow"))

      const ask = enqueue(db, "ws_a", "ask", { container: "c", question: "q", includeChunks: false })!
      await expect(waitForJob(ask, { timeoutMs: 1000 })).resolves.toBe("answer")
      // The ask finished while the slow extraction still held its slot, and
      // the second extraction waited for it.
      expect(row(first).status).toBe("running")
      expect(row(second).status).toBe("queued")

      slow.open()
      await until(() => row(second).status === "done")
      await runner.stop()
      expect(maxBackground).toBe(1)
      expect(MAX_RUNNING).toBe(2)
    })

    it("waits for running jobs on stop", async () => {
      const slow = gate()
      let finished = false
      const runner = new JobRunner(
        db,
        handlers({
          extract: async () => {
            await slow.opened
            finished = true
          },
        }),
        quiet,
      )
      const id = enqueue(db, "ws_a", "extract", { documentId: "d" })!
      runner.start()
      await until(() => row(id).status === "running")
      const stopping = runner.stop()
      slow.open()
      await stopping
      expect(finished).toBe(true)
      expect(row(id).status).toBe("done")
    })
  })
})
