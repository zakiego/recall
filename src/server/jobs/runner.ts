import type { DB } from "../db"
import type { Job } from "../db/schema"
import { GatewayError } from "../gateway/client"
import { claimNext, complete, fail, nextRunAt, notifyJobFinished, onEnqueue, pruneDone, recoverStale, type JobPayloads, type JobType } from "./queue"
import { settleJob } from "./wait"

// A handler's return value goes to whoever waits on the job (see wait.ts).
export type Handlers = {
  [T in JobType]: (payload: JobPayloads[T], job: Job) => Promise<unknown>
} & {
  // Called once a job has used up its retries.
  onGiveUp?: (job: Job, error: unknown) => void
}

const IDLE_POLL_MS = 5_000

// Runs one job at a time, forever. The gateway has a few slots shared with
// every other project, so recall never holds more than one.
export class JobRunner {
  private running = false
  private wake: (() => void) | undefined
  private stopped: Promise<void> | undefined
  private unsubscribe: (() => void) | undefined

  constructor(
    private db: DB,
    private handlers: Handlers,
    private log: (msg: string) => void = (m) => console.log(`[recall:jobs] ${m}`),
  ) {}

  start() {
    if (this.running) return
    this.running = true
    const recovered = recoverStale(this.db)
    if (recovered) this.log(`requeued ${recovered} interrupted job(s)`)
    pruneDone(this.db)
    this.unsubscribe = onEnqueue(() => this.wake?.())
    this.stopped = this.loop()
  }

  async stop() {
    this.running = false
    this.unsubscribe?.()
    this.wake?.()
    await this.stopped
  }

  // Runs the next due job, if any. Exposed for tests.
  async runOnce(now = Date.now()): Promise<Job | undefined> {
    const job = claimNext(this.db, now)
    if (!job) return undefined
    const started = Date.now()
    try {
      const handler = this.handlers[job.type] as (p: unknown, j: Job) => Promise<unknown>
      const value = await handler(job.payload, job)
      complete(this.db, job.id)
      this.log(`${job.type} #${job.id} done in ${Date.now() - started} ms`)
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
    return job
  }

  private async loop() {
    while (this.running) {
      try {
        if (await this.runOnce()) continue
      } catch (err) {
        this.log(`loop error: ${err instanceof Error ? err.message : String(err)}`)
      }
      const next = nextRunAt(this.db)
      const wait = next === undefined ? IDLE_POLL_MS : Math.min(Math.max(next - Date.now(), 50), IDLE_POLL_MS)
      await new Promise<void>((resolve) => {
        const t = setTimeout(resolve, wait)
        this.wake = () => {
          clearTimeout(t)
          resolve()
        }
      })
      this.wake = undefined
    }
  }
}
