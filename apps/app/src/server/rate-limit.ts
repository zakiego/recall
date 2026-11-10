import { HttpError } from "./http"

// A fixed-window counter per key, in memory. Better Auth limits its own
// endpoints; this covers Recall's invite acceptance, test emails and ask.
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

// Every ask is a gateway call. Generous for an agent asking in bursts, but a
// leaked or looping key can't run up the model bill. Adding memories and search
// stay unlimited: imports send thousands of documents and search is local.
export const ASK_LIMIT = { max: 60, windowMs: 10 * 60_000 }
export const askLimiter = new RateLimiter(ASK_LIMIT.max, ASK_LIMIT.windowMs)

// Throws a 429 with Retry-After once `keyId` has used up its asks.
export function limitAsk(keyId: string, limiter = askLimiter) {
  const wait = limiter.take(keyId)
  if (wait > 0) throw new HttpError(429, "rate_limited", `Too many asks with this API key. Try again in ${wait} s.`, { "retry-after": String(wait) })
}
