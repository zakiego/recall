// A fake changes feed (the contract Oslo and Finansial serve), for tests. It
// pages through `items` sorted by (updatedAt, id), with an opaque cursor, a
// Bearer key check, and switches to fail the next request.
import type { ChangeItem, Fetch } from "./changes-feed"

export type FakeFeed = {
  fetch: Fetch
  items: ChangeItem[]
  calls: { url: string; cursor: string | null; limit: number; auth: string | null }[]
  // The next request answers with this status (or throws, for "network").
  failNext: (status: number | "network", times?: number) => void
  add: (...items: ChangeItem[]) => void
}

const encode = (offset: number) => Buffer.from(`o:${offset}`).toString("base64url")
function decode(cursor: string): number | null {
  const m = /^o:(\d+)$/.exec(Buffer.from(cursor, "base64url").toString())
  return m ? Number(m[1]) : null
}

export function createFakeFeed(opts: { key: string; items?: ChangeItem[] }): FakeFeed {
  const items: ChangeItem[] = [...(opts.items ?? [])]
  const calls: FakeFeed["calls"] = []
  let failures: { status: number | "network"; times: number } | null = null
  const sorted = () => [...items].sort((a, b) => a.updatedAt.localeCompare(b.updatedAt) || a.id.localeCompare(b.id))
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } })

  const fetchImpl = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url)
    const auth = new Headers(init?.headers).get("authorization")
    const cursor = url.searchParams.get("cursor")
    const limit = Number(url.searchParams.get("limit") ?? 100)
    calls.push({ url: url.href, cursor, limit, auth })
    if (failures) {
      const f = failures
      if (--f.times <= 0) failures = null
      if (f.status === "network") throw new TypeError("fetch failed")
      return json({ error: "boom" }, f.status)
    }
    if (url.pathname !== "/api/v1/changes") return json({ error: "not_found" }, 404)
    if (auth !== `Bearer ${opts.key}`) return json({ error: "unauthorized" }, 401)
    if (!Number.isInteger(limit) || limit < 1 || limit > 200) return json({ error: "bad_limit" }, 400)
    const offset = cursor ? decode(cursor) : 0
    if (offset === null) return json({ error: "bad_cursor" }, 400)
    const all = sorted()
    const page = all.slice(offset, offset + limit)
    const end = offset + page.length
    return json({ items: page, nextCursor: page.length ? encode(end) : cursor, hasMore: end < all.length })
  }) as Fetch

  return {
    fetch: fetchImpl,
    items,
    calls,
    failNext: (status, times = 1) => {
      failures = { status, times }
    },
    add: (...more) => items.push(...more),
  }
}
