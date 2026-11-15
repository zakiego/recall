// Runs a scripted ingest and extract session through the real TS pipeline
// (addContent, the job runner, ingest, extract, applyExtract, the profile job,
// the real gateway client against a fake gateway) and records, after every
// step, every request the gateway got and every row of the tables the
// pipeline writes. Random ids are replaced by prefix_N in order of first
// appearance; clock columns are left out. The Go test replays the same steps.
//
// Run from apps/app: npx tsx ../server/testdata/gen/pipeline-goldens.ts
import fs from "node:fs"
import path from "node:path"

process.env.GATEWAY_KEY = "zkg_test"
process.env.GATEWAY_URL = "http://gateway.test"
process.env.RECALL_EMBED = "inline"
process.env.MODELS_DIR = path.resolve("data/models")
process.env.DATA_DIR = fs.mkdtempSync(path.join((await import("node:os")).tmpdir(), "recall-pipeline-golden-"))
process.env.EXTRACT_MODEL = "claude-haiku-5-5"
process.env.FALLBACK_MODEL = "claude-sonnet-5-5"

const { openDb } = await import("../../../app/src/server/db/index.ts")
const { addContent } = await import("../../../app/src/server/api.ts")
const { JobRunner } = await import("../../../app/src/server/jobs/runner.ts")
const { createHandlers } = await import("../../../app/src/server/pipeline.ts")
const { deleteDocument, forgetMemory, memoryText, uploadsDir } = await import("../../../app/src/server/memories.ts")
const { addFile } = await import("../../../app/src/server/api.ts")

type Reply = { text: string } | { status: number; body: string; headers?: Record<string, string> }

const W = "ws_pipeline000000000"
const KEY = ["rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2"].join("")
const NOW = "2026-11-09T10:00:00Z"
const CREATED = Date.UTC(2026, 10, 8, 9, 0, 0)

function sse(text: string) {
  const parts = [JSON.stringify({ type: "start" }), JSON.stringify({ type: "text-start", id: "t0" })]
  // Split the text so the reader has to join deltas.
  for (let i = 0; i < text.length; i += 40) parts.push(JSON.stringify({ type: "text-delta", id: "t0", delta: text.slice(i, i + 40) }))
  parts.push(JSON.stringify({ type: "text-end", id: "t0" }), JSON.stringify({ type: "finish", finishReason: "stop" }), "[DONE]")
  return parts.map((p) => `data: ${p}\n\n`).join("")
}

const replies: Reply[] = []
let requests: string[] = []
globalThis.fetch = (async (input: unknown, init?: { body?: string }) => {
  const url = String(input)
  if (url !== "http://gateway.test/v1/chat") throw new Error(`network access refused: ${url}`)
  const body = String(init?.body)
  requests.push(body)
  const { system } = JSON.parse(body) as { system: string }
  if (system.includes("profile")) return new Response(sse(JSON.stringify({ static: "Static profile.", dynamic: "Dynamic profile." })), { status: 200 })
  const r = replies.shift() ?? { text: '{"add":[]}' }
  if ("text" in r) return new Response(sse(r.text), { status: 200, headers: { "content-type": "text/event-stream" } })
  return new Response(r.body, { status: r.status, headers: { "content-type": "application/json", ...r.headers } })
}) as typeof fetch

// 12 extract windows: only the first 10 go to the model.
const longerText = Array.from({ length: 36_000 }, (_, i) => `w${i % 97}`).join(" ")

const longText = Array.from(
  { length: 700 },
  (_, i) => `Note ${i + 1}: the ${["blue", "green", "red", "amber"][i % 4]} shelf holds box ${i * 7} of the archive.`,
).join(" ")

// Every step the Go test replays. "add" stores a document (then pins its
// created_at), "sql" runs a statement, "drain" runs jobs until none is due
// at Date.now() + advanceMs (replies queue up until a drain uses them), "delete" removes the nth added document,
// "forget" closes the current memory with that fact.
const steps: Record<string, unknown>[] = [
  {
    op: "add",
    input: { container: "zaki", content: `Staging key {{KEY}}, card 4111 1111 1111 1111.\nI moved to Banjarbaru last month and my thesis seminar is on 20 November.` },
    replies: [
      {
        text: JSON.stringify({
          add: [
            { fact: "Lives in Banjarbaru", keywords: "city home kota rumah banjarbaru", kind: "static", event_date: "2026-10" },
            { fact: "Thesis seminar on 20 November 2026", keywords: "thesis seminar skripsi semhas", kind: "dynamic", due_date: "2026-11-20" },
            { fact: "Passport number is C1234567", keywords: "passport paspor", kind: "static" },
          ],
        }),
      },
    ],
  },
  { op: "drain" },
  {
    op: "add",
    input: { container: "zaki", source_id: "claude-code:s1", messages: [{ role: "user", content: "The seminar moved to 27 November." }, { role: "assistant", content: "Noted, 27 November." }] },
    replies: [
      {
        text:
          "Here is the JSON:\n```json\n" +
          JSON.stringify({
            update: [{ ref: 2, fact: "Thesis seminar on 27 November 2026", keywords: "thesis seminar skripsi semhas", due_date: "2026-11-27" }],
            conflict: [{ ref: 1, fact: "Lives in Martapura", keywords: "city home kota rumah martapura", note: "The session says Martapura" }],
            add: [{ fact: "lives in banjarbaru.", keywords: "city", kind: "static" }, { fact: "Prefers pnpm over npm", keywords: "package manager tools", kind: "static" }],
            invalidate: [1, 2, 9],
          }) +
          "\n```",
      },
    ],
  },
  { op: "drain" },
  { op: "add", input: { container: "zaki", source_id: "claude-code:s1", content: "duplicate source id" }, replies: [] },
  { op: "drain" },
  {
    op: "add",
    input: { container: "work", content: "Kickoff notes for the Vorneo contract.", title: "Explicit title", metadata: { from: "test", n: 1 }, document_date: "2021-03-04" },
    replies: [{ text: "not json at all" }, { text: '{"add": "wrong shape"}' }],
  },
  { op: "drain" },
  { op: "drain", advanceMs: 3_600_000, replies: [{ text: JSON.stringify({ add: [{ fact: "Vorneo contract kicked off", keywords: "vorneo contract kontrak", kind: "dynamic", event_date: "2021-03-04", due_date: null }] }) }] },
  { op: "add", input: { container: "work", content: "Gateway outage test." }, replies: [{ status: 502, body: JSON.stringify({ error: { type: "upstream_error", message: "upstream failed" } }), headers: { "retry-after": "7" } }] },
  { op: "drain" },
  { op: "drain", advanceMs: 3_600_000, replies: [{ text: '{"add":[]}' }] },
  { op: "add", input: { container: "work", content: longText }, replies: [{ text: JSON.stringify({ add: [{ fact: "Archive has a blue shelf", keywords: "archive shelf", kind: "static" }] }) }, { text: JSON.stringify({ add: [{ fact: "Archive has an amber shelf", keywords: "archive shelf amber", kind: "static" }] }) }] },
  { op: "drain" },
  { op: "add", input: { container: "work", content: "placeholder" }, replies: [{ text: JSON.stringify({ add: [{ fact: "Hosting invoice is unpaid", keywords: "invoice tagihan hosting", kind: "dynamic" }] }) }] },
  { op: "sql", sql: "UPDATE documents SET source_type = 'email', content = ? WHERE content = 'placeholder'", args: ["Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPlease pay the hosting invoice by Friday."] },
  { op: "drain" },
  { op: "forget", fact: "Prefers pnpm over npm" },
  { op: "delete", nth: 0 },
  { op: "drain" },
  // Uploads: the file is read, stored as text and removed; the model falls back.
  {
    op: "addFile",
    input: { container: "work", name: "notes.txt", mime: "text/plain", text: "Owns a grey cat named Abu.\nIt likes fish." },
    replies: [{ status: 403, body: JSON.stringify({ error: { type: "model_not_allowed", message: "no" } }) }, { text: JSON.stringify({ add: [{ fact: "Owns a grey cat named Abu", keywords: "pet kucing cat", kind: "static" }] }) }],
  },
  { op: "uploads" },
  { op: "drain" },
  { op: "uploads" },
  { op: "addFile", input: { container: "work", name: "page.html", mime: "text/html", text: "<html><head><title>Page title</title></head><body><p>HTML upload body text about the cat Abu.</p></body></html>" } },
  { op: "drain" },
  { op: "addFile", input: { container: "work", name: "blob.bin", mime: "", text: "zz" } },
  { op: "drain", advanceMs: 3_600_000 },
  { op: "uploads" },
  // Updates that keep, set and clear dates.
  { op: "add", input: { container: "work", content: "The Vorneo contract got a deadline of 1 January 2022." }, replies: [{ text: JSON.stringify({ update: [{ ref: 1, fact: "Vorneo contract runs until 2022-01-01", keywords: "vorneo contract kontrak", due_date: "2022-01-01" }] }) }] },
  { op: "drain" },
  { op: "add", input: { container: "work", content: "The Vorneo contract start date was wrong." }, replies: [{ text: JSON.stringify({ update: [{ ref: 1, fact: "Vorneo contract runs until 2022-01-01, start unknown", keywords: "vorneo contract kontrak", event_date: null }] }) }] },
  { op: "drain" },
  { op: "add", input: { container: "long", content: longerText } },
  { op: "drain" },
]

const dumps: [string, string][] = [
  ["documents", "SELECT id, workspace_id, container, source_type, title, url, content, status, error, metadata, source_id FROM documents ORDER BY rowid"],
  ["chunks", "SELECT id, document_id, workspace_id, container, idx, text FROM chunks ORDER BY rowid"],
  ["memories", "SELECT id, workspace_id, container, fact, keywords, kind, source_document_id, supersedes_id, valid_to IS NOT NULL, review_note, conflicts_with_id, event_date, event_precision, due_date, due_precision FROM memories ORDER BY rowid"],
  ["jobs", "SELECT id, workspace_id, type, payload, status, attempts, last_error, started_at IS NOT NULL, finished_at IS NOT NULL FROM jobs ORDER BY id"],
  ["profiles", "SELECT workspace_id, container, static_text, dynamic_text FROM profiles ORDER BY container"],
  ["memories_fts", "SELECT memory_id, fact, keywords, workspace_id, container FROM memories_fts ORDER BY rowid"],
  ["chunks_fts", "SELECT chunk_id, text, workspace_id, container FROM chunks_fts ORDER BY rowid"],
  ["memories_vec", "SELECT v.memory_id, v.workspace_id, v.container FROM memories_vec v JOIN memories m ON m.id = v.memory_id ORDER BY m.rowid"],
  ["chunks_vec", "SELECT v.chunk_id, v.workspace_id, v.container FROM chunks_vec v JOIN chunks c ON c.id = v.chunk_id ORDER BY c.rowid"],
]

const db = openDb(":memory:")
const runner = new JobRunner(db, createHandlers(db, { chat: (await import("../../../app/src/server/gateway/client.ts")).chatJson, now: () => new Date(NOW) }), () => {})
const added: string[] = []
const last = new Map<string, unknown[][]>()
const vectors: Record<string, string> = {}
const out: { step: Record<string, unknown>; result?: unknown; requests: string[]; rows: [string, unknown[][] | null][] }[] = []
for (const step of steps) {
  requests = []
  replies.push(...((step.replies as Reply[]) ?? []))
  let result: unknown
  switch (step.op) {
    case "addFile": {
      const f = step.input as { container: string; name: string; mime: string; text: string }
      const bytes = new TextEncoder().encode(f.text)
      const r = addFile(db, W, { container: f.container, file: new File([bytes], f.name, { type: f.mime }), bytes })
      db.$client.prepare("UPDATE documents SET created_at = ? WHERE id = ?").run(CREATED, r.id)
      added.push(r.id)
      result = r
      break
    }
    case "uploads":
      result = fs.existsSync(uploadsDir()) ? fs.readdirSync(uploadsDir()).sort() : []
      break
    case "add": {
      // {{KEY}} keeps a secret-shaped string out of the golden file.
      const input = JSON.parse(JSON.stringify(step.input).replaceAll("{{KEY}}", KEY))
      const r = addContent(db, W, input as Parameters<typeof addContent>[2])
      db.$client.prepare("UPDATE documents SET created_at = ? WHERE id = ?").run(CREATED, r.id)
      added.push(r.id)
      result = r
      break
    }
    case "sql":
      db.$client.prepare(step.sql as string).run(...((step.args as unknown[]) ?? []))
      break
    case "drain":
      for (let i = 0; i < 50; i++) if (!(await runner.runOnce(Date.now() + ((step.advanceMs as number) ?? 0)))) break
      break
    case "delete":
      result = deleteDocument(db, W, added[step.nth as number]!)
      break
    case "forget": {
      const m = db.$client.prepare("SELECT id FROM memories WHERE fact = ? AND valid_to IS NULL").get(step.fact) as { id: string }
      result = forgetMemory(db, W, m.id)?.validTo !== null
      break
    }
  }
  if (step.op === "drain" && replies.length) throw new Error(`step ${JSON.stringify(step)} left ${replies.length} replies`)
  // A table equal to the previous step's dump is written as null.
  const rows = dumps.map(([name, sql]) => {
    const now = db.$client.prepare(sql).raw().all() as unknown[][]
    const same = JSON.stringify(now) === JSON.stringify(last.get(name))
    last.set(name, now)
    return [name, same ? null : now] as [string, unknown[][] | null]
  })
  out.push({ step, result, requests, rows })
  // Every vector the step stored, by the text that was embedded.
  for (const [text, emb] of db.$client.prepare("SELECT c.text, v.embedding FROM chunks_vec v JOIN chunks c ON c.id = v.chunk_id").raw().all() as [string, Buffer][])
    if (!(text in vectors)) vectors[text] = Buffer.from(emb).toString("base64")
  for (const [fact, keywords, emb] of db.$client.prepare("SELECT m.fact, m.keywords, v.embedding FROM memories_vec v JOIN memories m ON m.id = v.memory_id").raw().all() as [string, string, Buffer][]) {
    const text = memoryText({ fact, keywords })
    if (!(text in vectors)) vectors[text] = Buffer.from(emb).toString("base64")
  }
}

// Ids by first appearance, depth first.
const seen = new Map<string, string>()
const counts = new Map<string, number>()
const idPattern = /\b(mem|doc|chk|per)_[0-9a-z]{16}\b/g
function norm(v: unknown): unknown {
  if (typeof v === "string")
    return v.replaceAll(KEY, "{{KEY}}").replace(idPattern, (id, prefix: string) => {
      if (!seen.has(id)) {
        const n = (counts.get(prefix) ?? 0) + 1
        counts.set(prefix, n)
        seen.set(id, `${prefix}_${n}`)
      }
      return seen.get(id)!
    })
  if (Array.isArray(v)) return v.map(norm)
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, norm(x)]))
  return v
}

const file = path.resolve(import.meta.dirname, "../pipeline.json")
fs.writeFileSync(file, JSON.stringify({ workspaceId: W, now: NOW, createdAt: CREATED, dumps, steps: norm(out), vectors: norm(vectors) }, null, 1) + "\n")
console.log(`wrote ${file}: ${out.length} steps`)
