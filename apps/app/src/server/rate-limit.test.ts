import { describe, expect, it } from "vitest"
import { HttpError } from "./http"
import { limitAsk, RateLimiter } from "./rate-limit"

describe("limitAsk", () => {
  it("lets a key ask up to the limit, then throws a 429 with Retry-After", () => {
    const limiter = new RateLimiter(2, 60_000)
    limitAsk("key_a", limiter)
    limitAsk("key_a", limiter)
    let err: unknown
    try {
      limitAsk("key_a", limiter)
    } catch (e) {
      err = e
    }
    expect(err).toBeInstanceOf(HttpError)
    expect(err).toMatchObject({ status: 429, type: "rate_limited", headers: { "retry-after": "60" } })
    // Another key has its own budget.
    expect(() => limitAsk("key_b", limiter)).not.toThrow()
  })

  it("opens again once the window passes", () => {
    const limiter = new RateLimiter(1, 1_000)
    expect(limiter.take("k", 0)).toBe(0)
    expect(limiter.take("k", 400)).toBe(1)
    expect(limiter.take("k", 1_000)).toBe(0)
  })
})
