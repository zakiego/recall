// Runs a scripted session through the real TS domain code (memories, dated
// facts and upcoming, people, ask, the dates backfill, reminders with the
// clock moved across days and time zones) and records, after every step, the
// step's result, every gateway request and email, and every row of the tables
// those modules write. Every embedding request goes through
// embed-recorder.ts, so the Go test replays the TS vectors and runs the same
// on every platform. Random ids become prefix_N in order of first appearance;
// clock values written by the step become "<now>", and created_at of the rows
// a step wrote is pinned to a step clock so orders are stable.
//
// Run from apps/app: npx tsx ../server/testdata/gen/domains-goldens.ts
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "recall-domains-golden-"))
const embedLog = path.join(tmp, "embed.jsonl")
process.env.GATEWAY_KEY = "zkg_test"
process.env.GATEWAY_URL = "http://gateway.test"
process.env.RECALL_EMBED = "child"
process.env.RECALL_EMBED_CHILD_PATH = path.resolve(import.meta.dirname, "embed-recorder.ts")
process.env.EMBED_LOG = embedLog
process.env.MODELS_DIR = path.resolve("data/models")
process.env.DATA_DIR = tmp
process.env.EXTRACT_MODEL = "claude-haiku-5-5"
process.env.FALLBACK_MODEL = "claude-sonnet-5-5"

const { openDb } = await import("../../../app/src/server/db/index.ts")
const mem = await import("../../../app/src/server/memories.ts")
const ppl = await import("../../../app/src/server/people.ts")
const rem = await import("../../../app/src/server/reminders.ts")
const { answerQuestion } = await import("../../../app/src/server/ask.ts")
const { backfillBatch, countUndated } = await import("../../../app/src/server/backfill.ts")
const { chatJson, chatJsonWithModel } = await import("../../../app/src/server/gateway/client.ts")
const { insertUser, createWorkspace, addMember } = await import("../../../app/src/server/users.ts")
const { stopEmbedder } = await import("../../../app/src/server/embed.ts")

const W = "ws_domains0000000000"
const W2 = "ws_other00000000000"
// The step clock: rows a step writes get created_at = CREATED + step * 1000.
const CREATED = Date.UTC(2026, 10, 1, 0, 0, 0)
const ASK_NOW = "2026-11-12T03:00:00Z"
const START = Date.now()

function sse(text: string) {
  const parts = [JSON.stringify({ type: "start" }), JSON.stringify({ type: "text-start", id: "t0" })]
  for (let i = 0; i < text.length; i += 40) parts.push(JSON.stringify({ type: "text-delta", id: "t0", delta: text.slice(i, i + 40) }))
  parts.push(JSON.stringify({ type: "text-end", id: "t0" }), JSON.stringify({ type: "finish", finishReason: "stop" }), "[DONE]")
  return parts.map((p) => `data: ${p}\n\n`).join("")
}

const replies: string[] = []
let requests: string[] = []
globalThis.fetch = (async (input: unknown, init?: { body?: string }) => {
  const url = String(input)
  if (url !== "http://gateway.test/v1/chat") throw new Error(`network access refused: ${url}`)
  requests.push(String(init?.body))
  const text = replies.shift()
  if (text === undefined) throw new Error("no scripted reply left")
  return new Response(sse(text), { status: 200, headers: { "content-type": "text/event-stream" } })
}) as typeof fetch

let emails: unknown[] = []
let sendFails = false
const reminderDeps = (now: string) => ({
  configured: () => true,
  now: () => new Date(now),
  send: async (m: { to: string; subject: string; text: string; html: string; idempotencyKey?: string }) => {
    emails.push(m)
    if (sendFails) throw new Error("Resend 500: down")
    return { id: `re_${emails.length}` }
  },
})

const day = (date: string) => ({ date, precision: "day" })
const month = (date: string) => ({ date, precision: "month" })
const year = (date: string) => ({ date, precision: "year" })
const mention = (name: string, extra: Record<string, unknown> = {}) => ({ name, aliases: [], facts: [], ...extra })

// Every step the Go test replays. Facts and people are named by text: the
// current memory with that fact, the person with that name.
const steps: Record<string, unknown>[] = [
  { op: "setup" },
  { op: "add", input: { container: "zaki", fact: "Thesis seminar on 20 November 2026", keywords: "thesis seminar skripsi", kind: "dynamic", due: day("2026-11-20") } },
  { op: "add", input: { container: "zaki", fact: "Hosting renewal due December 2026", keywords: "hosting renewal invoice", kind: "dynamic", due: month("2026-12") } },
  { op: "add", input: { container: "zaki", fact: "Passport expires in 2027", keywords: "passport paspor", kind: "static", due: year("2027") } },
  { op: "add", input: { container: "work", fact: "Vorneo contract ends 13 November 2026", keywords: "vorneo contract", kind: "dynamic", due: day("2026-11-13") } },
  { op: "add", input: { container: "zaki", fact: "Lives in Banjarbaru", keywords: "city home kota", kind: "static", event: year("2019") } },
  { op: "add", input: { container: "zaki", fact: "Works with Clara Simmons, a product designer at Vorneo", keywords: "clara designer vorneo", kind: "dynamic" } },
  { op: "add", input: { container: "zaki", fact: "Meets Clara every Tuesday to review mockups", keywords: "clara meeting", kind: "dynamic" } },
  { op: "add", input: { container: "zaki", fact: "Budi Santoso owns the Vorneo brief", keywords: "budi brief", kind: "dynamic", event: month("2026-10") } },
  { op: "add", input: { container: "zaki", fact: "Dentist appointment on 14 November 2026", keywords: "dentist dokter gigi", kind: "dynamic", due: day("2026-11-14") } },
  { op: "add", workspace: W2, input: { container: "zaki", fact: "Other workspace deadline on 21 November 2026", keywords: "deadline", kind: "dynamic", due: day("2026-11-21") } },
  { op: "list", opts: { container: "zaki" } },
  { op: "list", opts: { container: "zaki", kind: "static" } },
  { op: "list", opts: { container: "zaki", limit: 3 } },
  { op: "list", opts: { container: "zaki", limit: 3, page: 2 } },
  { op: "edit", fact: "Thesis seminar on 20 November 2026", patch: { fact: "Thesis seminar on 27 November 2026", due: day("2026-11-27") } },
  { op: "edit", fact: "Hosting renewal due December 2026", patch: { due: null } },
  { op: "edit", fact: "Lives in Banjarbaru", patch: {} },
  { op: "edit", fact: "Lives in Banjarbaru", patch: { fact: "  ", keywords: " home ", kind: "dynamic" } },
  { op: "sql", sql: "UPDATE memories SET review_note = 'Conflicts with the renewal' WHERE fact = 'Passport expires in 2027' AND valid_to IS NULL" },
  { op: "review", container: "zaki" },
  { op: "list", opts: { container: "zaki", review: true } },
  { op: "edit", fact: "Passport expires in 2027", patch: {} },
  { op: "review", container: "zaki" },
  { op: "chain", fact: "Thesis seminar on 27 November 2026" },
  { op: "versions", container: "zaki" },
  { op: "list", opts: { container: "zaki", history: true } },
  { op: "upcoming", range: { from: "2026-11-01", to: "2026-11-30" } },
  { op: "upcoming", range: { container: "zaki", from: "2026-11-14", to: "2027-12-31" }, limit: 2 },
  { op: "upcoming", range: { container: "work", from: "2026-12-01", to: "2026-12-31" } },
  // People.
  {
    op: "link",
    container: "zaki",
    mentions: [
      { mention: mention("Clara Simmons", { aliases: ["Clara"], role: "Product designer", organization: "Vorneo", email: "clara@vorneo.id" }), facts: ["Works with Clara Simmons, a product designer at Vorneo"] },
    ],
  },
  {
    op: "link",
    container: "zaki",
    mentions: [
      { mention: mention("Clara"), facts: ["Meets Clara every Tuesday to review mockups"] },
      { mention: mention("Budi Santoso", { email: "budi@vorneo.id", handle: "@budis" }), facts: ["Budi Santoso owns the Vorneo brief"] },
      { mention: mention("Vorneo Team", { email: "team@vorneo.id" }), facts: ["Budi Santoso owns the Vorneo brief"] },
      { mention: mention("Zaki"), facts: ["Lives in Banjarbaru"] },
    ],
  },
  { op: "link", container: "zaki", mentions: [{ mention: mention("Clarissa Simmons", { role: "Designer" }), facts: ["Meets Clara every Tuesday to review mockups"] }] },
  { op: "link", container: "zaki", mentions: [{ mention: mention("Pak Budi", { email: "BUDI@vorneo.id" }), facts: ["Budi Santoso owns the Vorneo brief"] }] },
  { op: "people", opts: { container: "zaki" } },
  { op: "people", opts: { container: "zaki", sort: "name" } },
  { op: "people", opts: { container: "zaki", q: "CLARA" } },
  { op: "people", opts: { container: "zaki", limit: 1, offset: 1 } },
  { op: "person", name: "Clara Simmons" },
  { op: "person", name: "Clara Simmons", history: true },
  { op: "forMemories", container: "zaki" },
  { op: "namedIn", container: "zaki", text: "What do I know about clara?" },
  { op: "namedIn", container: "zaki", text: "When does Budi Santoso deliver?" },
  { op: "updatePerson", name: "Budi Santoso", patch: { role: "Engineer", aliases: ["Pak Budi", " "], handles: ["@budis", "@budi"] } },
  { op: "suggest", container: "zaki" },
  { op: "forget", fact: "Meets Clara every Tuesday to review mockups" },
  { op: "person", name: "Clara Simmons", history: true },
  { op: "merge", keep: "Clara Simmons", drop: "Clarissa Simmons" },
  { op: "people", opts: { container: "zaki" } },
  // Ask.
  {
    op: "ask",
    input: { container: "zaki", question: "What do I know about Clara?", includeChunks: false },
    replies: [JSON.stringify({ answer: "Clara Simmons is a product designer at Vorneo [m1]. The thesis seminar is on 27 November [m9].", covered: true })],
  },
  { op: "ask", input: { container: "zaki", question: "Where do I live?", includeChunks: true }, replies: [JSON.stringify({ answer: "Not in memory.", covered: false })] },
  { op: "ask", input: { container: "empty", question: "Anything?", includeChunks: false } },
  { op: "deletePerson", name: "Budi Santoso" },
  // The dates backfill.
  { op: "undated", container: "" },
  {
    op: "backfill",
    payload: {},
    replies: [JSON.stringify({ dates: [{ ref: 1, event_date: "2026-08" }, { ref: 2, due_date: "2026-11-18" }, { ref: 3, event_date: "garbage" }] })],
  },
  { op: "undated", container: "zaki" },
  // Reminders: zaki is in W only, rina in W2 only.
  { op: "settings", user: "usr_zaki" },
  { op: "saveSettings", user: "usr_zaki", input: { enabled: true, email: " Zaki@Example.com ", leadDays: [30, 7, 1, 1, 400, 0, 2.5], timezone: "Asia/Makassar" } },
  { op: "saveSettings", user: "usr_rina", input: { enabled: true, email: "rina@example.com", leadDays: [7], timezone: "UTC" } },
  { op: "remind", now: "2026-11-12T23:30:00Z" },
  { op: "remind", now: "2026-11-13T00:30:00Z" },
  { op: "remind", now: "2026-11-13T05:00:00Z" },
  { op: "remind", now: "2026-11-14T00:00:00Z" },
  { op: "sendFails", value: true },
  { op: "remind", now: "2026-11-19T23:59:00Z" },
  { op: "sendFails", value: false },
  { op: "remind", now: "2026-11-20T01:00:00Z" },
  { op: "saveSettings", user: "usr_zaki", input: { enabled: true, email: "zaki@example.com", leadDays: [1], timezone: "America/Los_Angeles" } },
  { op: "remind", now: "2026-11-26T15:59:00Z" },
  { op: "remind", now: "2026-11-26T16:00:00Z" },
  { op: "edit", fact: "Thesis seminar on 27 November 2026", patch: { due: day("2026-11-28") } },
  { op: "remind", now: "2026-11-27T16:30:00Z" },
  { op: "sendTest", user: "usr_zaki", now: "2026-11-12T00:00:00Z" },
  { op: "saveSettings", user: "usr_zaki", input: { enabled: false, email: null, leadDays: [], timezone: "Pacific/Chatham" } },
  { op: "remind", now: "2026-12-01T00:00:00Z" },
]

const dumps: [string, string][] = [
  ["memories", "SELECT id, workspace_id, container, fact, keywords, kind, supersedes_id, valid_to IS NOT NULL, review_note, conflicts_with_id, event_date, event_precision, due_date, due_precision, created_at FROM memories ORDER BY rowid"],
  ["memories_fts", "SELECT memory_id, fact, keywords, workspace_id, container FROM memories_fts ORDER BY rowid"],
  ["memories_vec", "SELECT v.memory_id, v.workspace_id, v.container FROM memories_vec v JOIN memories m ON m.id = v.memory_id ORDER BY m.rowid"],
  ["people", "SELECT id, workspace_id, container, name, aliases, role, organization, emails, handles, summary, manual, created_at FROM people ORDER BY rowid"],
  ["person_memories", "SELECT person_id, memory_id, workspace_id, created_at FROM person_memories ORDER BY rowid"],
  ["people_scans", "SELECT memory_id, workspace_id FROM people_scans ORDER BY rowid"],
  ["jobs", "SELECT id, workspace_id, type, payload, status, attempts, last_error FROM jobs ORDER BY id"],
  ["reminder_settings", "SELECT user_id, enabled, email, lead_days, timezone FROM reminder_settings ORDER BY user_id"],
  ["reminders_sent", "SELECT id, user_id, workspace_id, memory_id, due_date, lead_days, status, sent_at IS NOT NULL FROM reminders_sent ORDER BY id"],
]
// Tables whose created_at a step pins to the step clock.
const pinned = ["memories", "people", "person_memories"]

const db = openDb(":memory:")
const current = (fact: string, ws = W) => {
  const row = db.$client.prepare("SELECT id FROM memories WHERE fact = ? AND workspace_id = ? AND valid_to IS NULL").get(fact, ws) as { id: string } | undefined
  if (!row) throw new Error(`no current memory ${JSON.stringify(fact)}`)
  return row.id
}
const person = (name: string) => (db.$client.prepare("SELECT id FROM people WHERE name = ? AND workspace_id = ?").get(name, W) as { id: string }).id
const cursors: Record<string, string | null> = {}

const last = new Map<string, unknown[][]>()
const out: { step: Record<string, unknown>; result?: unknown; requests: string[]; emails: unknown[]; rows: [string, unknown[][] | null][] }[] = []
for (const [i, step] of steps.entries()) {
  requests = []
  emails = []
  replies.push(...((step.replies as string[]) ?? []))
  const ws = (step.workspace as string) ?? W
  let result: unknown
  switch (step.op) {
    case "setup": {
      createWorkspace(db, { name: "Zaki", id: W, slug: "zaki" })
      createWorkspace(db, { name: "Other", id: W2, slug: "other" })
      for (const [id, username, w] of [["usr_zaki", "zaki", W], ["usr_rina", "rina", W2]] as const) {
        const u = insertUser(db, { username, passwordHash: "x", name: username === "zaki" ? "Zaki Munziri" : "Rina" })
        // Fixed ids, so the reminder idempotency keys are the same in Go.
        db.$client.pragma("foreign_keys = OFF")
        db.$client.prepare("UPDATE user SET id = ? WHERE id = ?").run(id, u.id)
        db.$client.prepare("UPDATE account SET user_id = ?, account_id = ? WHERE user_id = ?").run(id, id, u.id)
        db.$client.pragma("foreign_keys = ON")
        addMember(db, { workspaceId: w, userId: id, role: "owner" })
      }
      break
    }
    case "add":
      result = await mem.addMemoryDirect(db, { workspaceId: ws, ...(step.input as { container: string; fact: string; keywords: string; kind: "static" | "dynamic" }) })
      break
    case "list": {
      const o = step.opts as { container: string; page?: number; limit?: number; kind?: "static"; history?: boolean; review?: boolean }
      const key = JSON.stringify({ ...o, page: undefined })
      const r = mem.listMemories(db, { workspaceId: W, ...o, cursor: o.page ? cursors[key] : null })
      cursors[key] = r.nextCursor
      result = r
      break
    }
    case "edit":
      result = await mem.editMemory(db, W, current(step.fact as string), step.patch as never)
      break
    case "forget":
      result = mem.forgetMemory(db, W, current(step.fact as string))
      break
    case "sql":
      db.$client.prepare(step.sql as string).run()
      break
    case "review":
      result = mem.reviewCount(db, W, step.container as string)
      break
    case "chain":
      result = mem.memoryChain(db, W, current(step.fact as string))
      break
    case "versions": {
      const ids = (db.$client.prepare("SELECT id FROM memories WHERE workspace_id = ? AND container = ? AND valid_to IS NULL ORDER BY rowid").all(W, step.container) as { id: string }[]).map((r) => r.id)
      result = mem.versionCounts(db, W, ids)
      break
    }
    case "upcoming": {
      const range = { workspaceId: W, ...(step.range as { from: string; to: string }) }
      result = { list: mem.listUpcoming(db, { ...range, limit: step.limit as number | undefined }), count: mem.countUpcoming(db, range) }
      break
    }
    case "link": {
      const list = (step.mentions as { mention: never; facts: string[] }[]).map((m) => ({ mention: m.mention, memoryIds: m.facts.map((f) => current(f)) }))
      result = await ppl.linkMentions(db, { workspaceId: W, container: step.container as string }, list)
      break
    }
    case "people":
      result = ppl.listPeople(db, { workspaceId: W, ...(step.opts as { container: string }) })
      break
    case "person":
      result = ppl.getPerson(db, W, person(step.name as string), { history: step.history as boolean | undefined })
      break
    case "forMemories": {
      const ids = (db.$client.prepare("SELECT id FROM memories WHERE workspace_id = ? AND container = ? ORDER BY rowid").all(W, step.container) as { id: string }[]).map((r) => r.id)
      result = ppl.peopleForMemories(db, W, ids)
      break
    }
    case "namedIn":
      result = ppl.peopleNamedIn(db, { workspaceId: W, container: step.container as string }, step.text as string)
      break
    case "updatePerson":
      result = ppl.updatePerson(db, W, person(step.name as string), step.patch as never)
      break
    case "suggest":
      result = await ppl.suggestPeopleMerges(db, { workspaceId: W, container: step.container as string })
      break
    case "merge":
      result = ppl.mergePeople(db, W, person(step.keep as string), person(step.drop as string))
      break
    case "deletePerson":
      result = ppl.deletePerson(db, W, person(step.name as string))
      break
    case "ask":
      result = await answerQuestion(db, W, step.input as never, { chat: chatJsonWithModel, now: () => new Date(ASK_NOW) })
      break
    case "undated":
      result = countUndated(db, W, (step.container as string) || undefined)
      break
    case "backfill":
      result = await backfillBatch(db, W, step.payload as never, { chat: chatJson })
      break
    case "settings":
      result = rem.getReminderSettings(db, step.user as string)
      break
    case "saveSettings":
      result = rem.saveReminderSettings(db, step.user as string, step.input as never)
      break
    case "sendFails":
      sendFails = step.value as boolean
      break
    case "remind": {
      const logs: string[] = []
      result = { run: await rem.runReminders(db, reminderDeps(step.now as string), (m) => logs.push(m)), logs }
      break
    }
    case "sendTest":
      result = await rem.sendTestReminder(db, step.user as string, reminderDeps(step.now as string))
      break
    default:
      throw new Error(`unknown op ${String(step.op)}`)
  }
  if (replies.length) throw new Error(`step ${i} left ${replies.length} replies`)
  // Rows written in this step move to the step clock.
  for (const t of pinned) db.$client.prepare(`UPDATE ${t} SET created_at = ? WHERE created_at >= ?`).run(CREATED + i * 1000, START)
  const rows = dumps.map(([name, sql]) => {
    const now = db.$client.prepare(sql).raw().all() as unknown[][]
    const same = JSON.stringify(now) === JSON.stringify(last.get(name))
    last.set(name, now)
    return [name, same ? null : now] as [string, unknown[][] | null]
  })
  out.push({ step, result, requests, emails, rows })
}
stopEmbedder()

// Ids by first appearance, depth first; clock values from the run become
// "<now>"; a list cursor is shown decoded.
const seen = new Map<string, string>()
const counts = new Map<string, number>()
const idPattern = /\b(mem|per)_[0-9a-z]{16}\b/g
const clockKeys = new Set(["createdAt", "updatedAt", "validFrom", "validTo", "lastMentionedAt", "sentAt"])
function normStr(s: string) {
  return s.replace(idPattern, (id, prefix: string) => {
    if (!seen.has(id)) {
      const n = (counts.get(prefix) ?? 0) + 1
      counts.set(prefix, n)
      seen.set(id, `${prefix}_${n}`)
    }
    return seen.get(id)!
  })
}
function norm(v: unknown, key = ""): unknown {
  if (key === "nextCursor" && typeof v === "string") return `cursor(${normStr(Buffer.from(v, "base64url").toString())})`
  if (clockKeys.has(key) && typeof v === "number" && v >= START) return "<now>"
  if (typeof v === "string") return normStr(v)
  if (Array.isArray(v)) return v.map((x) => norm(x))
  // Keys too (versionCounts and peopleForMemories are keyed by memory id),
  // each before its value: the order ids appear in JSON.stringify's text.
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [normStr(k), norm(x, k)]))
  return v
}

// Every embedding the run asked for, by kind and text.
const vectors: Record<string, string> = {}
for (const line of fs.readFileSync(embedLog, "utf8").split("\n").filter(Boolean)) {
  const r = JSON.parse(line) as { kind: string; texts: string[]; vectors: string[] }
  r.texts.forEach((t, k) => (vectors[`${r.kind}:${t}`] ??= r.vectors[k]!))
}

const file = path.resolve(import.meta.dirname, "../domains.json")
fs.writeFileSync(file, JSON.stringify({ workspaceId: W, created: CREATED, askNow: ASK_NOW, dumps, pinned, steps: norm(out), vectors }, null, 1) + "\n")
fs.rmSync(tmp, { recursive: true, force: true })
console.log(`wrote ${file}: ${out.length} steps, ${Object.keys(vectors).length} vectors`)
