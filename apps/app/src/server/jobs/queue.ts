import { and, asc, desc, eq, gte, inArray, lte, sql } from "drizzle-orm"
import type { DB } from "../db"
import { jobs, type Job } from "../db/schema"

export type JobPayloads = {
  ingest: { documentId: string }
  extract: { documentId: string }
  profile: { container: string }
  ask: { container: string; question: string; includeChunks: boolean }
  // Hourly; sends the day's reminder emails (reminders.ts).
  reminders: Record<string, never>
  // One batch of the one-off date backfill (backfill.ts). The totals carry over between batches.
  backfill_dates: { container?: string; cursor?: { createdAt: number; id: string }; checked?: number; dated?: number }
  // Links a container's current facts to people (run from the CLI, see people.ts).
  people_backfill: { container: string }
  // Hourly; pulls every enabled app connector's changes feed (connectors/apps.ts).
  app_sync: Record<string, never>
}
export type JobType = keyof JobPayloads

// Retry delays after the 1st, 2nd and 3rd failure. The 4th failure is final.
export const BACKOFF_MS = [30_000, 120_000, 600_000]
export const MAX_ATTEMPTS = BACKOFF_MS.length + 1

// Someone is waiting on an ask, so it runs before background work and is
// never retried: a retry 30 s later would answer a request that has gone.
export const INTERACTIVE: readonly JobType[] = ["ask"]
const maxAttempts = (type: JobType) => (INTERACTIVE.includes(type) ? 1 : MAX_ATTEMPTS)
const isInteractive = sql`${jobs.type} IN (${sql.join(INTERACTIVE.map((t) => sql`${t}`), sql`, `)})`
const interactiveFirst = desc(isInteractive)

// Which queued jobs a free slot may take (see runner.ts).
export type ClaimFilter = { interactiveOnly?: boolean }
const due = (now: number, filter: ClaimFilter) => and(eq(jobs.status, "queued"), lte(jobs.runAfter, now), filter.interactiveOnly ? isInteractive : undefined)

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
// On globalThis for the same reason as the enqueue listeners above.
const gf = globalThis as { __recallFinishedListeners?: Set<(job: Job) => void> }
const finishedListeners = (gf.__recallFinishedListeners ??= new Set())
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

// Every job belongs to the workspace its document or container is in.
export function enqueue<T extends JobType>(db: DB, workspaceId: string, type: T, payload: JobPayloads[T], runAfter = Date.now()): number | null {
  // One pending profile rebuild (or people backfill) per container is enough.
  if (type === "profile" || type === "people_backfill") {
    const existing = db
      .select({ id: jobs.id })
      .from(jobs)
      .where(
        and(
          eq(jobs.type, type),
          eq(jobs.status, "queued"),
          eq(jobs.workspaceId, workspaceId),
          sql`json_extract(${jobs.payload}, '$.container') = ${(payload as JobPayloads["profile"]).container}`,
        ),
      )
      .get()
    if (existing) return null
  }
  const row = db.insert(jobs).values({ workspaceId, type, payload, runAfter }).returning({ id: jobs.id }).get()
  for (const fn of listeners) fn()
  return row.id
}

// Takes the next due job and marks it running, atomically. Interactive jobs
// (asks) jump ahead; otherwise oldest first.
export function claimNext(db: DB, now = Date.now(), filter: ClaimFilter = {}): Job | undefined {
  return db.transaction((tx) => {
    const next = tx
      .select()
      .from(jobs)
      .where(due(now, filter))
      .orderBy(interactiveFirst, asc(jobs.runAfter), asc(jobs.id))
      .limit(1)
      .get()
    if (!next) return undefined
    return tx
      .update(jobs)
      .set({ status: "running", attempts: next.attempts + 1, startedAt: Date.now(), finishedAt: null })
      .where(eq(jobs.id, next.id))
      .returning()
      .get()
  })
}

export function nextRunAt(db: DB, filter: ClaimFilter = {}): number | undefined {
  const row = db
    .select({ at: jobs.runAfter })
    .from(jobs)
    .where(and(eq(jobs.status, "queued"), filter.interactiveOnly ? isInteractive : undefined))
    .orderBy(asc(jobs.runAfter))
    .limit(1)
    .get()
  return row?.at
}

// Leaves last_error alone: a job that failed once and then succeeded keeps
// the error that cost it a retry.
export function complete(db: DB, id: number) {
  db.update(jobs).set({ status: "done", finishedAt: Date.now() }).where(eq(jobs.id, id)).run()
}

// Returns true when the job will be retried, false when it's given up.
export function fail(db: DB, job: Job, error: unknown, now = Date.now(), minDelayMs = 0): boolean {
  const message = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
  if (job.attempts >= maxAttempts(job.type)) {
    db.update(jobs).set({ status: "failed", lastError: message, finishedAt: Date.now() }).where(eq(jobs.id, job.id)).run()
    return false
  }
  const delay = Math.max(BACKOFF_MS[job.attempts - 1] ?? BACKOFF_MS.at(-1)!, minDelayMs)
  db.update(jobs)
    .set({ status: "queued", lastError: message, runAfter: now + delay, finishedAt: Date.now() })
    .where(eq(jobs.id, job.id))
    .run()
  return true
}

// Drops a queued job nobody waits for anymore. Returns false when it already
// started (it then finishes and its result is ignored).
export function cancelQueued(db: DB, id: number, reason: string) {
  return db
    .update(jobs)
    .set({ status: "failed", lastError: reason, finishedAt: Date.now() })
    .where(and(eq(jobs.id, id), eq(jobs.status, "queued")))
    .run().changes > 0
}

// A job left "running" means the process died mid-job. Run it again, except
// interactive ones (whoever asked lost the connection with the old process)
// and ones already on their last attempt: a job that takes the process down
// with it (out of memory on a huge PDF) would otherwise run on every boot and
// never fail. Returns the given-up jobs so the runner can mark their documents.
export function recoverStale(db: DB, now = Date.now()): { requeued: number; gaveUp: Job[] } {
  db.update(jobs)
    .set({ status: "failed", lastError: "server restarted", finishedAt: now })
    .where(and(inArray(jobs.type, [...INTERACTIVE]), inArray(jobs.status, ["queued", "running"])))
    .run()
  const gaveUp = db
    .update(jobs)
    .set({ status: "failed", lastError: sql`'the server stopped during attempt ' || ${jobs.attempts} || ', the last one (crash or restart)'`, finishedAt: now })
    .where(and(eq(jobs.status, "running"), gte(jobs.attempts, MAX_ATTEMPTS)))
    .returning()
    .all()
  const requeued = db.update(jobs).set({ status: "queued" }).where(eq(jobs.status, "running")).run().changes
  return { requeued, gaveUp }
}

export const PRUNE_AFTER_MS = 7 * 86_400_000

// Done jobs older than a week are noise. Age counts from when the job
// finished; rows from before migration 0010 fall back to when it was queued.
export function pruneDone(db: DB, now = Date.now()) {
  return db
    .delete(jobs)
    .where(and(eq(jobs.status, "done"), lte(sql`coalesce(${jobs.finishedAt}, ${jobs.createdAt})`, now - PRUNE_AFTER_MS)))
    .run().changes
}
