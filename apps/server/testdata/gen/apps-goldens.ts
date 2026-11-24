// Exports what the app connectors' pure functions make of crafted inputs:
// changes-feed URLs and page parsing, finance app documents, hashes and money,
// todo app local days and digests, and Date.parse validity (the feed contract
// rests on it).
//
// Run from apps/app: npx tsx ../server/testdata/gen/apps-goldens.ts
import fs from "node:fs"
import path from "node:path"

const { feedUrl, fetchPage } = await import("../../../app/src/server/connectors/changes-feed.ts")
const fin = await import("../../../app/src/server/connectors/finansial.ts")
const { localDay, osloDigest } = await import("../../../app/src/server/connectors/oslo.ts")

const urls = [
  ["https://todo.zakiego.com", null, 100],
  ["https://todo.zakiego.com/", "abc", 50],
  ["https://x.test/base//", "c u/r+sor=&", 1],
  ["http://localhost:3000/app", null, 200],
].map(([b, c, l]) => ({ base: b, cursor: c, limit: l, url: feedUrl(b as string, c as string | null, l as number) }))

const pages: unknown[] = []
const replies: { status: number; body: string }[] = [
  { status: 200, body: JSON.stringify({ items: [], nextCursor: null, hasMore: false }) },
  {
    status: 200,
    body: JSON.stringify({
      items: [
        { kind: "task", id: "t1", updatedAt: "2026-11-10T01:02:03Z", url: "https://x/t1", data: { a: 1 } },
        { kind: "task", id: "t2", updatedAt: "2026-11-10", deleted: true },
        { kind: "task", id: "t3", updatedAt: "not a date" },
        { kind: "", id: "t4", updatedAt: "2026-11-10T00:00:00Z" },
        { kind: "task", id: "t5", updatedAt: "2026-11-10T00:00:00Z", data: null },
        { kind: "task", id: "t6", updatedAt: "2026-11-10T00:00:00Z", data: [1] },
        { kind: "task", id: "t7", updatedAt: "Tue, 10 Nov 2026 08:00:00 GMT" },
        { kind: "task", id: "t8", updatedAt: "2026-11-10 08:00" },
        "junk",
      ],
      nextCursor: "n1",
      hasMore: true,
    }),
  },
  { status: 200, body: JSON.stringify({ items: [], hasMore: true }) },
  { status: 200, body: JSON.stringify({ items: "x", hasMore: true }) },
  { status: 200, body: "not json" },
  { status: 401, body: JSON.stringify({ error: "unauthorized" }) },
  { status: 403, body: JSON.stringify({ code: "forbidden" }) },
  { status: 400, body: JSON.stringify({ error: "bad cursor <script>" }) },
  { status: 500, body: JSON.stringify({ error: "x".repeat(81) }) },
  { status: 502, body: "gateway" },
  { status: 404, body: JSON.stringify({ error: "not_found", code: "nf" }) },
]
for (const r of replies) {
  const fetchImpl = (async () => new Response(r.body, { status: r.status })) as typeof fetch
  try {
    pages.push({ reply: r, ok: await fetchPage(fetchImpl, { baseUrl: "https://x.test", apiKey: "k", cursor: null }) })
  } catch (err) {
    const e = err as { name: string; message: string; status: number | null; code: string }
    pages.push({ reply: r, error: { name: e.name, message: e.message, status: e.status, code: e.code } })
  }
}

const settingsCases = [undefined, null, {}, { thresholds: null }, { thresholds: [] }, { thresholds: { IDR: 500000, USD: 100, usd: 5, EURO: 1, JPY: -1, GBP: "10", CHF: Infinity } }, { thresholds: {} }]
const settings = settingsCases.map((s) => ({ in: s === undefined ? "undefined" : s, out: fin.finansialSettings(s as never) }))

const money = [
  [150000000, "IDR", 2],
  [123450, "USD", 2],
  [-5, "USD", 2],
  [1234567, "", 0],
  [1, "BTC", 8],
  [999, "KWD", 3],
  [0, "IDR", 2],
  [12.7, "USD", 2],
].map(([a, c, d]) => ({ amount: a, currency: c, digits: d, out: fin.formatMoney(a as number, c as string, d as number) }))

const hashes = [
  { a: 1, b: "x" },
  { b: "x", a: 1 },
  { tags: ["b", "a"], n: null, f: 1.5, neg: -0, big: 1e21, s: "é " },
].map((v) => ({ in: v, out: fin.contentHash(v), short: fin.contentHash(v, 16) }))

const t = (data: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({ kind: "transaction", id: "txn_1", updatedAt: "2026-11-09T10:00:00Z", url: "https://fin/txn_1", deleted: false, data, ...extra })
const items = [
  t({ amountMinor: 150000000, currency: "idr", minorUnitDigits: 2, kind: "expense", payee: " Toko Buku ", account: "BCA", category: "Books", note: "Gift", tags: ["b", " a ", 3, ""], date: "2026-11-08T23:00:00+08:00" }),
  t({ amountMinor: "200000000", currency: "IDR", kind: "income", payee: "Client", account: "Mandiri" }),
  t({ amountMinor: 100000000, currency: "IDR", kind: "transfer", account: "BCA", payee: "Savings", minorUnitDigits: "2" }),
  t({ amountMinor: 99999999, currency: "IDR" }),
  t({ amountMinor: 100000000, currency: "USD" }),
  t({ amountMinor: 1.5, currency: "IDR" }),
  t({ amountMinor: -300000000, currency: "IDR", minorUnitDigits: 9 }),
  t({ amountMinor: 300000000, currency: "IDR", date: "08/11/2026" }, { url: "" }),
  t({ amountMinor: 300000000, currency: "IDR" }, { deleted: true }),
  { kind: "recurring_rule", id: "r1", updatedAt: "2026-11-01T00:00:00Z", url: "https://fin/r1", deleted: false, data: { name: "Rent", kind: "expense", amountMinor: 500000000, currency: "IDR", frequency: "monthly", payee: "Landlord", category: "Housing", active: true } },
  { kind: "recurring_rule", id: "r2", updatedAt: "2026-11-01T00:00:00Z", url: "", deleted: false, data: { kind: "income", status: "paused", payee: "Employer" } },
  { kind: "recurring_rule", id: "r3", updatedAt: "2026-11-01T00:00:00Z", url: "", deleted: false, data: { name: "Gym", active: false, amountMinor: 10, currency: "" } },
  { kind: "recurring_rule", id: "r4", updatedAt: "2026-11-01T00:00:00Z", url: "x", deleted: true, data: { name: "Old" } },
  { kind: "recurring_rule", id: "r5", updatedAt: "2026-11-01T00:00:00Z", url: "", deleted: false, data: { name: "Ended", status: "ended" } },
  { kind: "loan", id: "l1", updatedAt: "2026-10-01T00:00:00Z", url: "https://fin/l1", deleted: false, data: { name: "Car", principalMinor: 12000000000, currency: "IDR", lender: "Bank", status: "active" } },
  { kind: "loan", id: "l2", updatedAt: "2026-10-01T00:00:00Z", url: "", deleted: false, data: {} },
  { kind: "loan", id: "l3", updatedAt: "2026-10-01T00:00:00Z", url: "", deleted: true, data: {} },
  { kind: "account", id: "a1", updatedAt: "2026-10-01T00:00:00Z", url: "", deleted: false, data: {} },
]
const settingsForDocs = [fin.finansialSettings(undefined), fin.finansialSettings({ thresholds: { IDR: 0.5, USD: 1 } })]
const docs = items.map((item) => ({
  item,
  docs: settingsForDocs.map((s) => fin.finansialDocument(item as never, s)),
  eventDate: fin.finansialEventDate(item as never),
  ruleVersion: item.kind === "recurring_rule" ? fin.ruleVersion(item as never) : null,
  loanVersion: item.kind === "loan" ? fin.loanVersion(item as never) : null,
}))

const zones = ["Asia/Makassar", "UTC", "America/Los_Angeles", "Asia/Kolkata", "Pacific/Chatham", "Nope/Zone"]
const instants = ["2026-11-09T15:59:59Z", "2026-11-09T16:00:00Z", "2026-03-08T10:30:00Z", "2026-11-10", "2026-11-10T00:00:00+08:00", "garbage", "2026-02-29T00:00:00Z"]
const days = zones.flatMap((z) => instants.map((i) => ({ zone: z, at: i, day: localDay(i, z) })))

const events = [
  { type: "task", id: "t2", title: "Write report", url: "https://todo/t2", at: "2026-11-09T02:15:00Z", project: "Thesis" },
  { type: "task", id: "t1", title: "Buy milk", url: "", at: "2026-11-09T02:15:00Z" },
  { type: "project_created", id: "p1", title: "Garden", url: "https://todo/p1", at: "2026-11-09T05:00:00Z" },
  { type: "project_completed", id: "p2", title: "Move house", url: "https://todo/p2", at: "2026-11-09T09:59:00Z" },
]
const digests = [
  { day: "2026-11-09", events, zone: "Asia/Makassar" },
  { day: "2026-11-09", events: events.slice(0, 1), zone: "UTC" },
  { day: "2026-11-09", events: events.slice(2, 3), zone: "America/Los_Angeles" },
  { day: "2024-02-29", events: [events[1], events[1]], zone: "Asia/Kolkata" },
].map((c) => ({ ...c, out: osloDigest(c.day, c.events as never, c.zone) }))

const dateStrings = [
  "2026-11-10", "2026-11", "2026", "2026-11-10T08:00", "2026-11-10T08:00:00", "2026-11-10T08:00:00.123", "2026-11-10T08:00:00Z", "2026-11-10T08:00:00.5Z",
  "2026-11-10T08:00:00+07:00", "2026-11-10T08:00:00-0130", "2026-11-10 08:00", "2026-11-10 08:00:00Z", "Tue, 10 Nov 2026 08:00:00 GMT", "10 Nov 2026",
  "Nov 10, 2026", "2026/11/10", "11/10/2026", "not a date", "", " 2026-11-10 ", "2026-13-01", "2026-02-30", "+002026-11-10T00:00:00Z", "2026-11-10T24:00:00Z",
  "2026-11-10T08:00:00z", "1970-01-01T00:00:00Z", "0", "12", "2026-11-10T08:00:00.123456Z",
  // Forms past the feed contract, from the M4 review.
  "2026-11-10Z", "2026-11-10 08:00:00.5", "2026-11-10T08:00:00.5", "2026-11-10T8:00", "2026-1-5", "2026/1/5", "1/5/2026",
  "Jan 5 2026", "5 Jan 2026 10:00 GMT+0700", "Tue Nov 10 2026 08:00:00 GMT+0700 (Western Indonesia Time)", "2026-11-10T08:00:00+07",
  "2026-11-10T08:00:00+0700", "2026-11-10T08:00:00 +07:00", "2026-11-10T08:00:00.000000Z", "2026-11-10T08:00:60Z",
  "2026-11-10T23:59:59.999Z", "-000001-01-01T00:00:00Z", "+275760-09-13T00:00:00Z", "+275760-09-13T00:00:00.001Z",
  "Mon, 10 Nov 2026 08:00:00 +0000", "10 November 2026", "November 10, 2026 8:00 PM", "2026-11-10T08:00:00.123+07:00",
  "  2026-11-10", "2026-11-10\n", "20261110", "2026.11.10", "11.10.2026", "Thu, 01 Jan 1970 00:00:00 GMT", "12/31/1969 23:59:59",
  "2026-W45", "2026-11-10T08:00:00.Z", "2026-11-10T08Z", "2026-11-10t08:00:00Z", "2026-11-10T08:00:00ZZ", "2026-02-29", "2024-02-29",
  "2026-11-10T08:00:00-00:00", "2026-11-10T08:00:00+24:00", "Nov 10 2026 08:00 UTC", "10-Nov-2026", "2026-11-10 08:00 PM",
].map((s) => ({ s, t: Date.parse(s) }))

const out = path.resolve(import.meta.dirname, "../apps.json")
fs.writeFileSync(out, JSON.stringify({ tz: process.env.TZ ?? Intl.DateTimeFormat().resolvedOptions().timeZone, urls, pages, settings, money, hashes, docs, days, digests, dateStrings }, null, 1) + "\n")
console.log(`wrote ${out}`)
