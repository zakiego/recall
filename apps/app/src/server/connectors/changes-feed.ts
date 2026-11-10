// Client for the changes feed that Oslo and Finansial both serve:
//
//   GET {baseUrl}/api/v1/changes?cursor=<opaque>&limit=<1..200>
//   Authorization: Bearer <key>
//   -> { items: ChangeItem[], nextCursor: string | null, hasMore: boolean }
//
// Items ascend by (updatedAt, id). Recall pulls; the apps never push. Errors
// name the HTTP status and the app's short error code only, never the key or
// item content.
import { z } from "zod"

export type Fetch = typeof fetch

export const PAGE_LIMIT = 100
// Pages per sync run. A big backfill finishes over several hourly runs.
export const MAX_PAGES_PER_RUN = 20
export const FEED_TIMEOUT_MS = 20_000

export const changeItem = z.object({
  kind: z.string().min(1).max(64),
  id: z.string().min(1).max(200),
  updatedAt: z.string().min(1).max(64),
  url: z.string().max(2000).default(""),
  deleted: z.boolean().default(false),
  data: z.record(z.string(), z.unknown()).nullish().transform((d) => d ?? {}),
})
export type ChangeItem = z.infer<typeof changeItem>

const page = z.object({
  items: z.array(z.unknown()),
  nextCursor: z.string().nullable().optional(),
  hasMore: z.boolean(),
})

export type FeedPage = { items: ChangeItem[]; skipped: number; nextCursor: string | null; hasMore: boolean }

export class FeedError extends Error {
  constructor(
    message: string,
    readonly status: number | null,
    readonly code: "auth" | "bad_cursor" | "upstream" | "bad_response" | "timeout" | "network",
  ) {
    super(message)
    this.name = "FeedError"
  }
}

export function feedUrl(baseUrl: string, cursor: string | null, limit = PAGE_LIMIT) {
  const url = new URL("/api/v1/changes", baseUrl.replace(/\/+$/, "") + "/")
  if (cursor) url.searchParams.set("cursor", cursor)
  url.searchParams.set("limit", String(limit))
  return url.toString()
}

// The app's own error code, if it sent a short one, for the "last error" line.
async function upstreamReason(res: Response) {
  try {
    const body = (await res.json()) as { error?: unknown; code?: unknown }
    const reason = typeof body.error === "string" ? body.error : typeof body.code === "string" ? body.code : ""
    return reason && reason.length <= 80 && /^[\w .,:'-]+$/.test(reason) ? `: ${reason}` : ""
  } catch {
    return ""
  }
}

export async function fetchPage(
  fetchImpl: Fetch,
  opts: { baseUrl: string; apiKey: string; cursor: string | null; limit?: number; timeoutMs?: number },
): Promise<FeedPage> {
  let res: Response
  try {
    res = await fetchImpl(feedUrl(opts.baseUrl, opts.cursor, opts.limit), {
      headers: { authorization: `Bearer ${opts.apiKey}`, accept: "application/json" },
      signal: AbortSignal.timeout(opts.timeoutMs ?? FEED_TIMEOUT_MS),
    })
  } catch (err) {
    if (err instanceof Error && (err.name === "TimeoutError" || err.name === "AbortError")) {
      throw new FeedError(`the feed didn't answer within ${Math.round((opts.timeoutMs ?? FEED_TIMEOUT_MS) / 1000)} s`, null, "timeout")
    }
    throw new FeedError("couldn't reach the app", null, "network")
  }
  if (!res.ok) {
    const reason = await upstreamReason(res)
    if (res.status === 401 || res.status === 403) throw new FeedError(`the app refused the API key (HTTP ${res.status}${reason})`, res.status, "auth")
    if (res.status === 400) throw new FeedError(`the app rejected the cursor (HTTP 400${reason})`, 400, "bad_cursor")
    throw new FeedError(`the app answered HTTP ${res.status}${reason}`, res.status, "upstream")
  }
  let body: unknown
  try {
    body = await res.json()
  } catch {
    throw new FeedError("the feed didn't return JSON", res.status, "bad_response")
  }
  const parsed = page.safeParse(body)
  if (!parsed.success) throw new FeedError("the feed's response doesn't match the changes contract", res.status, "bad_response")
  const items: ChangeItem[] = []
  let skipped = 0
  for (const raw of parsed.data.items) {
    const item = changeItem.safeParse(raw)
    if (item.success && !Number.isNaN(Date.parse(item.data.updatedAt))) items.push(item.data)
    else skipped++
  }
  return { items, skipped, nextCursor: parsed.data.nextCursor ?? null, hasMore: parsed.data.hasMore }
}

export type PullResult = { pages: number; items: number; skipped: number; cursor: string | null; caughtUp: boolean }

// Reads pages from `cursor` until hasMore is false or the page cap is hit.
// `onPage` stores a page's items and the cursor after it, together, so a
// failure on a later page keeps the earlier pages and the cursor they reached.
// A FeedError stops the loop and propagates; the cursor stays where the last
// stored page left it.
export async function pullChanges(
  fetchImpl: Fetch,
  opts: { baseUrl: string; apiKey: string; cursor: string | null; maxPages?: number; limit?: number; timeoutMs?: number },
  onPage: (page: FeedPage, cursorAfter: string | null) => void,
): Promise<PullResult> {
  const maxPages = opts.maxPages ?? MAX_PAGES_PER_RUN
  let cursor = opts.cursor
  const out: PullResult = { pages: 0, items: 0, skipped: 0, cursor, caughtUp: false }
  while (out.pages < maxPages) {
    const p = await fetchPage(fetchImpl, { ...opts, cursor })
    // An empty page echoes the cursor; a missing nextCursor keeps the one we have.
    const next = p.nextCursor ?? cursor
    onPage(p, next)
    out.pages++
    out.items += p.items.length
    out.skipped += p.skipped
    const moved = next !== cursor
    cursor = next
    out.cursor = cursor
    if (!p.hasMore) {
      out.caughtUp = true
      break
    }
    // A feed that says "more" but doesn't move would loop forever.
    if (!moved) break
  }
  return out
}
