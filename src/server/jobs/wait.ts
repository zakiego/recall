// Lets a request wait for a job the in-process runner will run. The runner
// calls settleJob when a job finishes or gives up; waitForJob resolves with
// the handler's return value. In-memory only: a waiter in another process,
// or one registered after the job settled, never hears back, so register
// right after enqueue (same tick) and always pass a timeout.

export type JobOutcome = { ok: true; value: unknown } | { ok: false; error: unknown }

// On globalThis because the Nitro plugin that runs jobs and the routes that
// wait can load separate copies of this module (they do under vite dev).
const g = globalThis as { __recallJobWaiters?: Map<number, Set<(o: JobOutcome) => void>> }
const waiters = (g.__recallJobWaiters ??= new Map())

export function settleJob(id: number, outcome: JobOutcome) {
  const set = waiters.get(id)
  if (!set) return
  waiters.delete(id)
  for (const fn of set) fn(outcome)
}

export class JobWaitTimeout extends Error {
  constructor(readonly jobId: number, readonly timeoutMs: number) {
    super(`job #${jobId} did not finish within ${Math.round(timeoutMs / 1000)} s`)
    this.name = "JobWaitTimeout"
  }
}

export function waitForJob<T = unknown>(id: number, opts: { timeoutMs: number; signal?: AbortSignal }): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const set = waiters.get(id) ?? new Set()
    waiters.set(id, set)
    const done = () => {
      clearTimeout(timer)
      opts.signal?.removeEventListener("abort", onAbort)
      set.delete(listener)
      if (set.size === 0 && waiters.get(id) === set) waiters.delete(id)
    }
    const listener = (o: JobOutcome) => {
      done()
      if (o.ok) resolve(o.value as T)
      else reject(o.error)
    }
    const onAbort = () => {
      done()
      reject(opts.signal?.reason ?? new Error("aborted"))
    }
    const timer = setTimeout(() => {
      done()
      reject(new JobWaitTimeout(id, opts.timeoutMs))
    }, opts.timeoutMs)
    set.add(listener)
    if (opts.signal?.aborted) onAbort()
    else opts.signal?.addEventListener("abort", onAbort, { once: true })
  })
}

// Running jobs a waiter can still call off. When the request behind an ask
// times out or goes away, the job's gateway call is aborted so the single job
// slot isn't held for minutes answering nobody. Shared through globalThis for
// the same reason as the waiters above.
const ga = globalThis as { __recallJobAborts?: Map<number, AbortController> }
const aborts = (ga.__recallJobAborts ??= new Map())

// Called by the handler when the job starts. Pair with releaseJobAbort.
export function jobAbortSignal(id: number): AbortSignal {
  const controller = new AbortController()
  aborts.set(id, controller)
  return controller.signal
}

export function releaseJobAbort(id: number) {
  aborts.delete(id)
}

// Returns false when the job isn't running in this process.
export function abortJob(id: number, reason: string) {
  const controller = aborts.get(id)
  if (!controller) return false
  aborts.delete(id)
  controller.abort(new Error(reason))
  return true
}
