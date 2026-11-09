// A fixed-window counter per key, in memory. Better Auth limits its own
// endpoints; this covers Recall's invite acceptance.
export class RateLimiter {
  private hits = new Map<string, { start: number; n: number }>()

  constructor(
    private max: number,
    private windowMs: number,
  ) {}

  // Returns seconds to wait, or 0 when the call is allowed.
  take(key: string, now = Date.now()): number {
    const cur = this.hits.get(key)
    if (!cur || cur.start <= now - this.windowMs) {
      this.hits.set(key, { start: now, n: 1 })
      if (this.hits.size > 10_000) this.prune(now)
      return 0
    }
    if (cur.n >= this.max) return Math.ceil((cur.start + this.windowMs - now) / 1000)
    cur.n++
    return 0
  }

  private prune(now: number) {
    for (const [k, v] of this.hits) if (v.start <= now - this.windowMs) this.hits.delete(k)
  }
}
