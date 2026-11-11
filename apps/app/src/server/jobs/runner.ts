import type { DB } from "../db"
import type { Job } from "../db/schema"
import { GatewayError } from "../gateway/client"
import { memoryLine } from "../mem"
import { INTERACTIVE, claimNext, complete, fail, nextRunAt, notifyJobFinished, onEnqueue, pruneDone, recoverStale, type JobPayloads, type JobType } from "./queue"
import { settleJob } from "./wait"

// A handler's return value goes to whoever waits on the job (see wait.ts).
export type Handlers = {
  [T in JobType]: (payload: JobPayloads[T], job: Job) => Promise<unknown>
} & {
  // Called once a job has used up its retries.
  onGiveUp?: (job: Job, error: unknown) => void
}

const IDLE_POLL_MS = 5_000
const PRUNE_EVERY_MS = 3_600_000

// The gateway has 4 slots shared with every other project, so recall holds at
// most 2: one background job (ingest, extract, profile...) and one more that
// only an ask may take. An ask then never waits behind a long extraction, and
// two background jobs never run at once.
export const MAX_RUNNING = 2

const interactive = (job: Job) => INTERACTIVE.includes(job.type)

// Runs jobs forever, at most MAX_RUNNING at once (see above).
export class JobRunner {
  private running = false
  private stopped: Promise<void> | undefined
  private unsubscribe: (() => void) | undefined
  private active = new Map<number, { job: Job; done: Promise<void> }>()
  // Set by poke() while the loop isn't sleeping, so a wake-up between two
  // sleeps isn't lost.
  private poked = false
  private wake: (() => void) | undefined
  private lastPruneAt = 0

  constructor(
    private db: DB,
    private handlers: Handlers,
    private log: (msg: string) => void = (m) => console.log(`[recall:jobs] ${m}`),
  ) {}

  start() {
    if (this.running) return
    this.running = true
    const { requeued, gaveUp } = recoverStale(this.db)
    if (requeued) this.log(`requeued ${requeued} interrupted job(s)`)
    for (const job of gaveUp) {
      this.log(`${job.type} #${job.id} gave up: ${job.lastError}`)
      this.handlers.onGiveUp?.(job, new Error(job.lastError ?? "interrupted"))
      notifyJobFinished(job)
    }
    this.prune()
    this.unsubscribe = onEnqueue(() => this.poke())
    this.stopped = this.loop()
  }

  async stop() {
    this.running = false
    this.unsubscribe?.()
    this.poke()
    await this.stopped
  }

  // Runs the next due job, if any, and waits for it. Exposed for tests.
  async runOnce(now = Date.now()): Promise<Job | undefined> {
    const job = claimNext(this.db, now)
    if (!job) return undefined
    await this.execute(job)
    return job
  }

  private async execute(job: Job) {
    const started = Date.now()
    try {
      const handler = this.handlers[job.type] as (p: unknown, j: Job) => Promise<unknown>
      const value = await handler(job.payload, job)
      complete(this.db, job.id)
      this.log(`${job.type} #${job.id} done in ${Date.now() - started} ms`)
      // Ingest is the job that embeds, so it's where memory peaks.
      if (job.type === "ingest") this.log(`memory after ingest #${job.id}: ${memoryLine()}`)
      settleJob(job.id, { ok: true, value })
    } catch (err) {
      const retryAfter = err instanceof GatewayError ? err.retryAfterMs : undefined
      const retrying = fail(this.db, job, err, Date.now(), retryAfter)
      this.log(`${job.type} #${job.id} attempt ${job.attempts} failed${retrying ? ", will retry" : ", giving up"}: ${err instanceof Error ? err.message : String(err)}`)
      if (!retrying) {
        this.handlers.onGiveUp?.(job, err)
        settleJob(job.id, { ok: false, error: err })
      }
    }
    notifyJobFinished(job)
  }

  // Starts due jobs until the slots are full or nothing is due. Once a
  // background job holds a slot, the free one only takes asks.
  private fill() {
    while (this.running && this.active.size < MAX_RUNNING) {
      const interactiveOnly = this.backgroundBusy()
      const job = claimNext(this.db, Date.now(), { interactiveOnly })
      if (!job) return
      const done = this.execute(job)
        .catch((err) => this.log(`${job.type} #${job.id} bookkeeping failed: ${err instanceof Error ? err.message : String(err)}`))
        .finally(() => {
          this.active.delete(job.id)
          this.poke()
        })
      this.active.set(job.id, { job, done })
    }
  }

  private backgroundBusy() {
    for (const { job } of this.active.values()) if (!interactive(job)) return true
    return false
  }

  // Done jobs older than a week, checked hourly so a long-running process
  // doesn't keep them until the next restart.
  private prune(now = Date.now()) {
    if (now - this.lastPruneAt < PRUNE_EVERY_MS) return
    this.lastPruneAt = now
    const pruned = pruneDone(this.db, now)
    if (pruned) this.log(`pruned ${pruned} old done job(s)`)
  }

  private poke() {
    this.poked = true
    this.wake?.()
  }

  private sleep(ms: number) {
    if (this.poked) {
      this.poked = false
      return Promise.resolve()
    }
    return new Promise<void>((resolve) => {
      const done = () => {
        clearTimeout(t)
        this.wake = undefined
        this.poked = false
        resolve()
      }
      const t = setTimeout(done, ms)
      this.wake = done
    })
  }

  private nextWaitMs() {
    // Full: a finishing job pokes the loop. The poll is only a safety net.
    if (this.active.size >= MAX_RUNNING) return IDLE_POLL_MS
    const next = nextRunAt(this.db, { interactiveOnly: this.backgroundBusy() })
    return next === undefined ? IDLE_POLL_MS : Math.min(Math.max(next - Date.now(), 50), IDLE_POLL_MS)
  }

  private async loop() {
    while (this.running) {
      let wait = IDLE_POLL_MS
      try {
        this.prune()
        this.fill()
        wait = this.nextWaitMs()
      } catch (err) {
        this.log(`loop error: ${err instanceof Error ? err.message : String(err)}`)
      }
      await this.sleep(wait)
    }
    await Promise.all([...this.active.values()].map((a) => a.done))
  }
}
