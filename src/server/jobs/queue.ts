import { and, asc, desc, eq, inArray, lte, sql } from "drizzle-orm"
import type { DB } from "../db"
import { jobs, type Job } from "../db/schema"

export type JobPayloads = {
  ingest: { documentId: string }
  extract: { documentId: string }
  profile: { container: string }
  ask: { container: string; question: string; includeChunks: boolean }
}
export type JobType = keyof JobPayloads

// Retry delays after the 1st, 2nd and 3rd failure. The 4th failure is final.
export const BACKOFF_MS = [30_000, 120_000, 600_000]
export const MAX_ATTEMPTS = BACKOFF_MS.length + 1

// Someone is waiting on an ask, so it runs before background work and is
// never retried: a retry 30 s later would answer a request that has gone.
export const INTERACTIVE: readonly JobType[] = ["ask"]
const maxAttempts = (type: JobType) => (INTERACTIVE.includes(type) ? 1 : MAX_ATTEMPTS)
const interactiveFirst = desc(sql`${jobs.type} IN (${sql.join(INTERACTIVE.map((t) => sql`${t}`), sql`, `)})`)

// Shared through globalThis: the job loop and the code that enqueues can hold
// separate copies of this module (vite dev does), and the wake-up must cross.
const g = globalThis as { __recallEnqueueListeners?: Set<() => void> }
const listeners = (g.__recallEnqueueListeners ??= new Set())
export function onEnqueue(fn: () => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// Fired after every job attempt ends (done, retrying or given up), once its
// status and any document status are written. Waiters re-read the DB.
const finishedListeners = new Set<(job: Job) => void>()
export function onJobFinished(fn: (job: Job) => void) {
  finishedListeners.add(fn)
  return () => {
    finishedListeners.delete(fn)
  }
}
export function notifyJobFinished(job: Job) {
  for (const fn of finishedListeners) {
    try {
      fn(job)
    } catch (err) {
      console.error("[recall:jobs] job listener failed:", err)
    }
  }
}

export function enqueue<T extends JobType>(db: DB, type: T, payload: JobPayloads[T], runAfter = Date.now()): number | null {
  // One pending profile rebuild per container is enough.
  if (type === "profile") {
    const existing = db
      .select({ id: jobs.id })
      .from(jobs)
      .where(
        and(
          eq(jobs.type, "profile"),
          eq(jobs.status, "queued"),
          sql`json_extract(${jobs.payload}, '$.container') = ${(payload as JobPayloads["profile"]).container}`,
        ),
      )
      .get()
    if (existing) return null
  }
  const row = db.insert(jobs).values({ type, payload, runAfter }).returning({ id: jobs.id }).get()
  for (const fn of listeners) fn()
  return row.id
}

// Takes the next due job and marks it running, atomically. Interactive jobs
// (asks) jump ahead; otherwise oldest first.
export function claimNext(db: DB, now = Date.now()): Job | undefined {
  return db.transaction((tx) => {
    const next = tx
      .select()
      .from(jobs)
      .where(and(eq(jobs.status, "queued"), lte(jobs.runAfter, now)))
      .orderBy(interactiveFirst, asc(jobs.runAfter), asc(jobs.id))
      .limit(1)
      .get()
    if (!next) return undefined
    return tx
      .update(jobs)
      .set({ status: "running", attempts: next.attempts + 1 })
      .where(eq(jobs.id, next.id))
      .returning()
      .get()
  })
}

export function nextRunAt(db: DB): number | undefined {
  const row = db
    .select({ at: jobs.runAfter })
    .from(jobs)
    .where(eq(jobs.status, "queued"))
    .orderBy(asc(jobs.runAfter))
    .limit(1)
    .get()
  return row?.at
}

export function complete(db: DB, id: number) {
  db.update(jobs).set({ status: "done", lastError: null }).where(eq(jobs.id, id)).run()
}

// Returns true when the job will be retried, false when it's given up.
export function fail(db: DB, job: Job, error: unknown, now = Date.now(), minDelayMs = 0): boolean {
  const message = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
  if (job.attempts >= maxAttempts(job.type)) {
    db.update(jobs).set({ status: "failed", lastError: message }).where(eq(jobs.id, job.id)).run()
    return false
  }
  const delay = Math.max(BACKOFF_MS[job.attempts - 1] ?? BACKOFF_MS.at(-1)!, minDelayMs)
  db.update(jobs)
    .set({ status: "queued", lastError: message, runAfter: now + delay })
    .where(eq(jobs.id, job.id))
    .run()
  return true
}

// Drops a queued job nobody waits for anymore. Returns false when it already
// started (it then finishes and its result is ignored).
export function cancelQueued(db: DB, id: number, reason: string) {
  return db
    .update(jobs)
    .set({ status: "failed", lastError: reason })
    .where(and(eq(jobs.id, id), eq(jobs.status, "queued")))
    .run().changes > 0
}

// A job left "running" means the process died mid-job. Run it again, except
// interactive ones: whoever asked lost the connection with the old process.
export function recoverStale(db: DB) {
  db.update(jobs)
    .set({ status: "failed", lastError: "server restarted" })
    .where(and(inArray(jobs.type, [...INTERACTIVE]), inArray(jobs.status, ["queued", "running"])))
    .run()
  return db.update(jobs).set({ status: "queued" }).where(eq(jobs.status, "running")).run().changes
}

// Done jobs older than a week are noise.
export function pruneDone(db: DB, now = Date.now()) {
  db.delete(jobs)
    .where(and(eq(jobs.status, "done"), lte(jobs.createdAt, now - 7 * 86_400_000)))
    .run()
}
