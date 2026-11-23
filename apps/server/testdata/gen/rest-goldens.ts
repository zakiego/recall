// Calls the real TS route handlers (src/routes/v1, /health, /mcp's 401, the
// protected-resource metadata) with Request objects on a copy of
// testdata/search/seeded.db and records every response: status, the headers
// the handler set, and the body. The Go test sends the same requests over
// real HTTP to `recall serve`'s handler on its own copy.
//
// The clock is fixed (Date.now), the job loop is off, embeddings go through
// embed-recorder.ts so Go replays them, and random ids become prefix_N in
// order of first appearance. A step's path and body can name {ref} values
// from setup SQL and {cursor:N}, the nextCursor of step N's response.
//
// Run from apps/app: npx tsx ../server/testdata/gen/rest-goldens.ts
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { createHash } from "node:crypto"

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "recall-rest-golden-"))
const embedLog = path.join(tmp, "embed.jsonl")
const NOW = Date.UTC(2026, 10, 12, 3, 0, 0)
Date.now = () => NOW
process.env.GATEWAY_KEY = "zkg_test"
process.env.GATEWAY_URL = "http://gateway.test"
process.env.RECALL_EMBED = "child"
process.env.RECALL_EMBED_CHILD_PATH = path.resolve(import.meta.dirname, "embed-recorder.ts")
process.env.EMBED_LOG = embedLog
process.env.MODELS_DIR = path.resolve("data/models")
process.env.DATA_DIR = tmp
process.env.RECALL_JOBS = "off"
process.env.RECALL_URL = "https://recall.test"

globalThis.fetch = (async (input: unknown) => {
  throw new Error(`network access refused: ${String(input)}`)
}) as typeof fetch

const dbFile = path.join(tmp, "recall.db")
fs.copyFileSync(path.resolve(import.meta.dirname, "../search/seeded.db"), dbFile)
process.env.RECALL_DB_PATH = dbFile

const { getDb } = await import("../../../app/src/server/db/index.ts")
const { stopEmbedder } = await import("../../../app/src/server/embed.ts")
const routes = {
  "/health": (await import("../../../app/src/server/routes-legacy/health.ts")).Route,
  "/mcp": (await import("../../../app/src/server/routes-legacy/mcp.ts")).Route,
  "/.well-known/oauth-protected-resource/$": (await import("../../../app/src/server/routes-legacy/well-known/oauth-protected-resource.$.ts")).Route,
  "/v1/ask": (await import("../../../app/src/server/routes-legacy/v1/ask.ts")).Route,
  "/v1/containers": (await import("../../../app/src/server/routes-legacy/v1/containers.ts")).Route,
  "/v1/documents/$id": (await import("../../../app/src/server/routes-legacy/v1/documents.$id.ts")).Route,
  "/v1/memories": (await import("../../../app/src/server/routes-legacy/v1/memories.ts")).Route,
  "/v1/memories/$id": (await import("../../../app/src/server/routes-legacy/v1/memories.$id.ts")).Route,
  "/v1/people": (await import("../../../app/src/server/routes-legacy/v1/people.ts")).Route,
  "/v1/people/$id": (await import("../../../app/src/server/routes-legacy/v1/people.$id.ts")).Route,
  "/v1/profile": (await import("../../../app/src/server/routes-legacy/v1/profile.ts")).Route,
  "/v1/search": (await import("../../../app/src/server/routes-legacy/v1/search.ts")).Route,
  "/v1/upcoming": (await import("../../../app/src/server/routes-legacy/v1/upcoming.ts")).Route,
  "/v1/connectors/gmail/import": (await import("../../../app/src/server/routes-legacy/v1/connectors/gmail/import.ts")).Route,
}

// Which route a path hits, with its params, as the router matches it.
function match(p: string): { route: string; params: Record<string, string> } {
  for (const route of Object.keys(routes)) {
    const re = new RegExp(`^${route.replace(/[.]/g, "\\.").replace(/\$id/, "(?<id>[^/]+)").replace(/\/\$$/, "(?:/(?<_splat>.*))?")}$`)
    const m = re.exec(p)
    if (m) return { route, params: Object.fromEntries(Object.entries(m.groups ?? {}).filter(([, v]) => v !== undefined)) as Record<string, string> }
  }
  throw new Error(`no route for ${p}`)
}

const db = getDb()
const W1 = "ws_alpha00000000000"
const W2 = "ws_beta000000000000"
// Fixed keys, so both builds insert the same rows.
const keyOf = (n: number) => `rcl_${String(n).repeat(32).slice(0, 32)}`
const keys = { alpha: keyOf(1), beta: keyOf(2), revoked: keyOf(3), owned: keyOf(4) }
const setup = [
  `INSERT INTO organization (id, name, slug, created_at) VALUES ('${W1}', 'Alpha', 'alpha', ${NOW}), ('${W2}', 'Beta', 'beta', ${NOW})`,
  `INSERT INTO user (id, name, email, email_verified, username, created_at, updated_at) VALUES ('usr_owner000000000000', 'Owner', 'owner@users.recall.invalid', 0, 'owner', ${NOW}, ${NOW})`,
  `INSERT INTO member (id, organization_id, user_id, role, created_at) VALUES ('mbr_owner000000000000', '${W1}', 'usr_owner000000000000', 'owner', ${NOW})`,
  ...Object.entries(keys).map(([name, key], i) => {
    const ws = name === "beta" ? W2 : W1
    const revoked = name === "revoked" ? NOW : "NULL"
    const by = name === "owned" ? "'usr_owner000000000000'" : "NULL"
    const hash = createHash("sha256").update(key).digest("hex")
    const id = `key_${(name + "0".repeat(16)).slice(0, 16)}`
    return `INSERT INTO api_keys (id, workspace_id, name, key_hash, prefix, created_at, revoked_at, created_by) VALUES ('${id}', '${ws}', '${name}', '${hash}', '${key.slice(0, 10)}', ${NOW - 1000 * (i + 1)}, ${revoked}, ${by})`
  }),
  // People, linked to the first two current facts in alpha/default.
  `INSERT INTO people (id, workspace_id, container, name, aliases, role, organization, emails, handles, summary, manual, created_at, updated_at) VALUES
    ('per_clara000000000000', '${W1}', 'default', 'Clara Simmons', '["Clara"]', 'Designer', 'Vortek', '["clara@vortek.id"]', '[]', 'Works on the redesign', 0, ${NOW - 5000}, ${NOW - 5000}),
    ('per_budi0000000000000', '${W1}', 'default', 'Budi Santoso', '[]', NULL, NULL, '[]', '["@budi"]', '', 1, ${NOW - 4000}, ${NOW - 3000}),
    ('per_other000000000000', '${W2}', 'default', 'Other Person', '[]', NULL, NULL, '[]', '[]', '', 0, ${NOW - 4000}, ${NOW - 4000})`,
  `INSERT INTO person_memories (person_id, memory_id, workspace_id, created_at) SELECT 'per_clara000000000000', id, workspace_id, ${NOW} FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 2`,
  `INSERT INTO person_memories (person_id, memory_id, workspace_id, created_at) SELECT 'per_budi0000000000000', id, workspace_id, ${NOW} FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 2`,
  // Dated facts for upcoming and review flags.
  `UPDATE memories SET due_date = '2026-11-20', due_precision = 'day' WHERE id = (SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 3)`,
  `UPDATE memories SET due_date = '2026-11', due_precision = 'month', event_date = '2025', event_precision = 'year' WHERE id = (SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 4)`,
  `UPDATE memories SET due_date = '2026-11-01', due_precision = 'day' WHERE id = (SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'work' AND valid_to IS NULL ORDER BY rowid LIMIT 1)`,
  `UPDATE memories SET review_note = 'Conflicts with another fact' WHERE id = (SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 5)`,
  `INSERT INTO profiles (workspace_id, container, static_text, dynamic_text, updated_at) VALUES ('${W1}', 'default', 'Static profile text.', 'Dynamic profile text.', ${NOW - 60000})`,
]
const refs: Record<string, string> = {
  m1: `SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1`,
  m2: `SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 1`,
  m6: `SELECT id FROM memories WHERE workspace_id = '${W1}' AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1 OFFSET 5`,
  mBeta: `SELECT id FROM memories WHERE workspace_id = '${W2}' ORDER BY rowid LIMIT 1`,
  d1: `SELECT id FROM documents WHERE workspace_id = '${W1}' ORDER BY rowid LIMIT 1`,
  d2: `SELECT id FROM documents WHERE workspace_id = '${W1}' ORDER BY rowid LIMIT 1 OFFSET 1`,
  dBeta: `SELECT id FROM documents WHERE workspace_id = '${W2}' ORDER BY rowid LIMIT 1`,
}

type Step = { method: string; path: string; key?: keyof typeof keys | "none" | "bad"; headers?: Record<string, string>; body?: string; multipart?: { fields: Record<string, string>; file?: { name: string; type: string; text: string } }; resolveRef?: string }
const j = (v: unknown) => JSON.stringify(v)
const steps: Step[] = [
  { method: "GET", path: "/health", key: "none" },
  { method: "GET", path: "/.well-known/oauth-protected-resource", key: "none" },
  { method: "GET", path: "/.well-known/oauth-protected-resource/mcp", key: "none" },
  { method: "GET", path: "/.well-known/oauth-protected-resource/other", key: "none" },
  { method: "POST", path: "/mcp", key: "none", body: j({ jsonrpc: "2.0", id: 1, method: "ping" }) },
  { method: "POST", path: "/mcp", key: "revoked", body: j({ jsonrpc: "2.0", id: 1, method: "ping" }) },
  // Auth.
  { method: "GET", path: "/v1/containers", key: "none" },
  { method: "GET", path: "/v1/containers", key: "bad" },
  { method: "GET", path: "/v1/containers", key: "revoked" },
  { method: "GET", path: "/v1/containers", key: "none", headers: { "x-api-key": keys.alpha } },
  { method: "GET", path: "/v1/containers", key: "none", headers: { authorization: `bearer   ${keys.alpha}  ` } },
  { method: "GET", path: "/v1/containers", key: "none", headers: { authorization: `Basic ${keys.alpha}`, "x-api-key": keys.beta } },
  { method: "GET", path: "/v1/containers" },
  { method: "GET", path: "/v1/containers", key: "beta" },
  // Listing.
  { method: "GET", path: "/v1/memories" },
  { method: "GET", path: "/v1/memories?container=work" },
  { method: "GET", path: "/v1/memories?limit=2" },
  { method: "GET", path: "/v1/memories?limit=2&cursor={cursor:16}" },
  { method: "GET", path: "/v1/memories?history=1&limit=3" },
  { method: "GET", path: "/v1/memories?review=true" },
  { method: "GET", path: "/v1/memories?kind=static&limit=2" },
  { method: "GET", path: "/v1/memories?container=&limit=1" },
  { method: "GET", path: "/v1/memories?container=%20%20" },
  { method: "GET", path: `/v1/memories?container=${"c".repeat(129)}` },
  { method: "GET", path: "/v1/memories?limit=0" },
  { method: "GET", path: "/v1/memories?limit=abc&history=yes&kind=other" },
  { method: "GET", path: "/v1/memories?limit=2.5" },
  { method: "GET", path: "/v1/memories?cursor=garbage&limit=1" },
  { method: "GET", path: "/v1/memories/{m1}" },
  { method: "GET", path: "/v1/memories/{mBeta}" },
  { method: "GET", path: "/v1/memories/mem_nope" },
  // Documents.
  { method: "GET", path: "/v1/documents/{d1}" },
  { method: "GET", path: "/v1/documents/{dBeta}" },
  { method: "DELETE", path: "/v1/documents/{d2}" },
  { method: "DELETE", path: "/v1/documents/{d2}" },
  // People.
  { method: "GET", path: "/v1/people" },
  { method: "GET", path: "/v1/people?sort=name" },
  { method: "GET", path: "/v1/people?q=CLARA" },
  { method: "GET", path: "/v1/people?limit=1&offset=1" },
  { method: "GET", path: "/v1/people?sort=age&offset=-1" },
  { method: "GET", path: "/v1/people", key: "beta" },
  { method: "GET", path: "/v1/people/per_clara000000000000" },
  { method: "GET", path: "/v1/people/per_clara000000000000?history=1" },
  { method: "GET", path: "/v1/people/per_other000000000000" },
  { method: "GET", path: "/v1/people/per_nope?history=maybe" },
  // Profile, search, upcoming.
  { method: "GET", path: "/v1/profile" },
  { method: "GET", path: "/v1/profile?container=work" },
  { method: "GET", path: "/v1/profile?q=coffee" },
  { method: "GET", path: `/v1/profile?q=${"x".repeat(2001)}` },
  { method: "POST", path: "/v1/search", body: j({ q: "what shoes do I wear" }) },
  { method: "POST", path: "/v1/search", body: j({ q: "kopi", limit: "3", include: ["memories", "chunks"] }) },
  { method: "POST", path: "/v1/search", body: j({ q: "kopi", container: "work", include: ["chunks"] }) },
  { method: "POST", path: "/v1/search", body: j({ q: "   " }) },
  { method: "POST", path: "/v1/search", body: j({ q: "x", limit: 101, include: ["docs"] }) },
  { method: "POST", path: "/v1/search", body: "not json" },
  { method: "POST", path: "/v1/search", body: "[1,2]" },
  { method: "POST", path: "/v1/search", body: "" },
  { method: "GET", path: "/v1/upcoming" },
  { method: "GET", path: "/v1/upcoming?days=10&past_days=15&tz=Asia/Makassar" },
  { method: "GET", path: "/v1/upcoming?all=1&days=366&limit=2" },
  { method: "GET", path: "/v1/upcoming?container=work&past_days=30" },
  { method: "GET", path: "/v1/upcoming?days=0&tz=Mars/Base" },
  { method: "GET", path: "/v1/upcoming?days=367&past_days=x&limit=501" },
  // Edits.
  { method: "PATCH", path: "/v1/memories/{m2}", body: j({}) },
  { method: "PATCH", path: "/v1/memories/{m2}", body: j({ event_date: "2026-13", due_date: "soon", kind: "other" }) },
  { method: "PATCH", path: "/v1/memories/{m2}", body: j({ fact: "  ", keywords: "k".repeat(501) }) },
  { method: "PATCH", path: "/v1/memories/{m6}", body: j({ resolve: true }) },
  { method: "PATCH", path: "/v1/memories/{m6}", body: j({ resolve: false }) },
  { method: "PATCH", path: "/v1/memories/{m2}", body: j({ fact: "Edited through the API", due_date: "2027-01" }) },
  { method: "PATCH", path: "/v1/memories/{m2}", body: j({ fact: "Too late" }) },
  { method: "PATCH", path: "/v1/memories/mem_nope", body: j({ fact: "x" }) },
  { method: "PATCH", path: "/v1/memories/{mBeta}", body: j({ fact: "x" }) },
  { method: "PATCH", path: "/v1/memories/{m1}", body: "{bad" },
  { method: "GET", path: "/v1/memories/{m2}" },
  { method: "DELETE", path: "/v1/memories/{m1}" },
  { method: "DELETE", path: "/v1/memories/{m1}" },
  { method: "DELETE", path: "/v1/memories/{mBeta}" },
  // Adds.
  { method: "POST", path: "/v1/memories", body: j({ content: "I moved to Bandarjaya last month.\nSecond line." }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "Same source", source_id: "test:1", container: "work", title: "Titled", metadata: { a: 1 } }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "Same source again", source_id: " test:1 ", container: "work" }) },
  { method: "POST", path: "/v1/memories", body: j({ messages: [{ role: "user", content: "hi" }], document_date: "2026-11-01T10:00:00Z" }) },
  { method: "POST", path: "/v1/memories", body: j({ url: "https://example.com/page", type: "url" }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "x", url: "https://example.com" }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "   " }) },
  { method: "POST", path: "/v1/memories", body: j({ url: "ftp://example.com" }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "x", document_date: "2026-11", wait: "yes", wait_timeout: 500 }) },
  { method: "POST", path: "/v1/memories", body: j({ content: `${"A very long first line ".repeat(10)}` }) },
  { method: "POST", path: "/v1/memories?wait=1&wait_timeout=0", body: j({ content: "Waited for" }) },
  { method: "POST", path: "/v1/memories?wait=maybe", body: j({ content: "x" }) },
  { method: "POST", path: "/v1/memories", body: j({ content: "Body wait", wait: true, wait_timeout: 0 }) },
  { method: "POST", path: "/v1/memories", body: "x".repeat(10 * 1024 * 1024 + 1) },
  { method: "POST", path: "/v1/memories", multipart: { fields: { container: "work", title: " Upload ", source_id: "file:1", document_date: "2026-11-02", metadata: j({ from: "test" }) }, file: { name: "notes.txt", type: "text/plain", text: "Notes in a file." } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: { title: "x" } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: { metadata: "{bad" }, file: { name: "a.txt", type: "text/plain", text: "a" } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: { document_date: "2026-11" }, file: { name: "a.txt", type: "text/plain", text: "a" } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: { source_id: "s".repeat(201) }, file: { name: "a.txt", type: "text/plain", text: "a" } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: { wait: "maybe" }, file: { name: "a.txt", type: "text/plain", text: "a" } } },
  { method: "POST", path: "/v1/memories", multipart: { fields: {}, file: { name: "", type: "", text: "no name" } } },
  // Ask and the Gmail import.
  { method: "POST", path: "/v1/ask", body: j({ question: "What do I drink?" }) },
  { method: "POST", path: "/v1/ask", body: j({ question: "" }) },
  { method: "POST", path: "/v1/connectors/gmail/import", body: j({ message_id: "18c2f0a1b2c3d4e5" }) },
  { method: "POST", path: "/v1/connectors/gmail/import", key: "owned", body: j({ message_id: "18c2f0a1b2c3d4e5", container: "work" }) },
  { method: "POST", path: "/v1/connectors/gmail/import", key: "owned", body: j({ message_id: "not an id!" }) },
  { method: "GET", path: "/v1/containers" },
  // MCP: the SDK's stateless Streamable HTTP transport with JSON responses.
  ...mcpSteps(),
]

function mcpSteps(): Step[] {
  const accept = "application/json, text/event-stream"
  const rpc = (method: string, params?: unknown, id: number | string | null = 1, extra: Partial<Step> = {}): Step => ({
    method: "POST",
    path: "/mcp",
    headers: { accept, ...(extra.headers ?? {}) },
    body: JSON.stringify({ jsonrpc: "2.0", ...(id === null ? {} : { id }), method, ...(params === undefined ? {} : { params }) }),
    ...("key" in extra ? { key: extra.key } : {}),
  })
  const call = (name: string, args?: unknown, id: number | string = 1) => rpc("tools/call", { name, ...(args === undefined ? {} : { arguments: args }) }, id)
  const init = (version: string) => rpc("initialize", { protocolVersion: version, capabilities: {}, clientInfo: { name: "golden", version: "0.0.1" } })
  return [
    init("2025-06-18"),
    init("2024-11-05"),
    init("2025-03-26"),
    init("1999-01-01"),
    rpc("notifications/initialized", undefined, null),
    rpc("ping", undefined, "abc"),
    rpc("ping", {}),
    rpc("tools/list"),
    rpc("tools/list", undefined, 7, { key: "beta" }),
    rpc("resources/list"),
    rpc("no/such/method"),
    call("search_memories", { query: "what shoes do I wear" }),
    call("search_memories", { query: "kopi", include_chunks: true, limit: 3, container: "work" }),
    call("search_memories", { query: "" }),
    call("search_memories", { query: 5, limit: 0 }),
    call("search_memories"),
    call("get_profile", {}),
    call("get_profile", { query: "coffee", container: "default" }),
    call("list_memories", { limit: 2 }),
    call("list_memories", { needs_review: true }),
    call("list_memories", { history: true, limit: 3, container: "work" }),
    call("list_memories", { limit: 500 }),
    call("upcoming", {}),
    call("upcoming", { days: 10, past_days: 15, timezone: "Asia/Makassar", all_containers: true }),
    call("upcoming", { timezone: "Mars/Base" }),
    call("upcoming", { days: 0 }),
    call("list_people", {}),
    call("list_people", { query: "clara", sort: "name" }),
    call("list_people", { limit: 1, offset: 1 }),
    call("get_person", { name: "budi santoso" }),
    call("get_person", { id: "per_clara000000000000", history: true }),
    call("get_person", { name: "Nobody" }),
    call("get_person", { id: "per_other000000000000" }),
    call("get_person", {}),
    call("ask_memory", { question: "What do I drink?" }),
    call("ask_memory", { question: "  " }),
    call("add_memory", {}),
    call("add_memory", { content: "   " }),
    call("add_memory", { content: "Saved over MCP", title: "MCP note", source_id: "mcp:1", document_date: "2026-11-01" }),
    call("add_memory", { content: "Saved again", source_id: "mcp:1" }),
    call("add_memory", { url: "ftp://example.com" }),
    call("add_memory", { url: "https://example.com/doc.pdf", container: "work" }),
    call("add_memory", { content: "x", document_date: "11/01/2026" }),
    call("no_such_tool", {}),
    { method: "POST", path: "/mcp", headers: { accept }, body: "{not json" },
    { method: "POST", path: "/mcp", headers: { accept }, body: JSON.stringify([{ jsonrpc: "2.0", id: 1, method: "ping" }, { jsonrpc: "2.0", id: 2, method: "tools/list" }]) },
    { method: "POST", path: "/mcp", headers: { accept }, body: JSON.stringify({ jsonrpc: "2.0", id: 1 }) },
    { method: "POST", path: "/mcp", headers: { accept }, body: JSON.stringify({ id: 1, method: "ping" }) },
    { method: "POST", path: "/mcp", headers: { accept: "application/json" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }) },
    { method: "POST", path: "/mcp", headers: { accept: "text/event-stream" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }) },
    { method: "POST", path: "/mcp", headers: { accept, "mcp-protocol-version": "1999-01-01" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }) },
    { method: "POST", path: "/mcp", headers: { accept, "mcp-protocol-version": "2025-06-18" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }) },
    { method: "POST", path: "/mcp", headers: { accept, "content-type": "text/plain" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }) },
    { method: "GET", path: "/mcp", headers: { accept: "text/event-stream" } },
    { method: "DELETE", path: "/mcp", headers: { accept } },
    { method: "POST", path: "/mcp", headers: { accept }, body: JSON.stringify({ jsonrpc: "2.0", id: 9, result: {} }) },
  ]
}

for (const sql of setup) db.$client.exec(sql)
const refValues: Record<string, string> = {}
for (const [name, sql] of Object.entries(refs)) refValues[name] = (db.$client.prepare(sql).get() as { id: string }).id

const out: { step: Step; status: number; headers: Record<string, string>; body: string }[] = []
for (const [i, step] of steps.entries()) {
  const fill = (s: string) =>
    s.replace(/\{(\w+)\}/g, (_, name: string) => refValues[name] ?? `{${name}}`).replace(/\{cursor:(\d+)\}/g, (_, n: string) => {
      const prev = JSON.parse(out[Number(n)]!.body) as { nextCursor: string }
      return encodeURIComponent(prev.nextCursor)
    })
  const p = fill(step.path)
  const headers = new Headers(step.headers)
  const key = step.key ?? "alpha"
  if (key === "bad") headers.set("authorization", "Bearer rcl_notarealkey")
  else if (key !== "none") headers.set("authorization", `Bearer ${keys[key]}`)
  let body: BodyInit | undefined
  if (step.multipart) {
    const form = new FormData()
    for (const [k, v] of Object.entries(step.multipart.fields)) form.set(k, v)
    const f = step.multipart.file
    if (f) form.set("file", new File([f.text], f.name, { type: f.type }))
    body = form
  } else if (step.body !== undefined) {
    body = fill(step.body)
    if (!headers.has("content-type")) headers.set("content-type", "application/json")
  }
  const url = new URL(p, "https://recall.test")
  const request = new Request(url, { method: step.method, headers, body })
  const { route, params } = match(url.pathname)
  const handler = (routes as Record<string, { options: { server: { handlers: Record<string, (ctx: unknown) => Promise<Response> | Response> } } }>)[route]!.options.server.handlers[step.method]!
  const res = await handler({ request, params })
  const resHeaders: Record<string, string> = {}
  res.headers.forEach((v, k) => (resHeaders[k] = v))
  out.push({ step, status: res.status, headers: resHeaders, body: await res.text() })
  if (i % 20 === 0) console.log(`step ${i}`)
}
stopEmbedder()

// Ids by first appearance in the output text; timingMs is clock time.
const seen = new Map<string, string>()
const counts = new Map<string, number>()
const idPattern = /\b(mem|doc|chk)_[0-9a-z]{16}\b/g
const normText = (s: string) =>
  s
    .replace(idPattern, (id, prefix: string) => {
      if (!seen.has(id)) {
        const n = (counts.get(prefix) ?? 0) + 1
        counts.set(prefix, n)
        seen.set(id, `${prefix}_${n}`)
      }
      return seen.get(id)!
    })
    .replace(/"timingMs":\d+/g, '"timingMs":"<ms>"')
    // The same as ISO text (MCP results).
    .replace(/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z/g, (m) => (Date.parse(m) >= NOW ? "<now-iso>" : m))
    // A time the request wrote: Date.now (fixed) or SQLite's clock (real).
    .replace(/("(?:createdAt|updatedAt|validFrom|validTo|lastActivity|lastMentionedAt|lastUsedAt|runAfter)":)(\d{13})/g, (m, k: string, v: string) => (Number(v) >= NOW ? `${k}"<now>"` : m))
const steps2 = out.map((o) => {
  // A cursor is base64url of "createdAt:id": show it decoded.
  const body = o.body.replace(/"nextCursor":"([A-Za-z0-9_-]+)"/g, (_, c: string) => `"nextCursor":"cursor(${Buffer.from(c, "base64url").toString()})"`)
  return { ...o, body: normText(body) }
})
const vectors: Record<string, string> = {}
if (fs.existsSync(embedLog))
  for (const line of fs.readFileSync(embedLog, "utf8").split("\n").filter(Boolean)) {
    const r = JSON.parse(line) as { kind: string; texts: string[]; vectors: string[] }
    r.texts.forEach((t, k) => (vectors[`${r.kind}:${t}`] ??= r.vectors[k]!))
  }
const tables = Object.fromEntries(
  ["documents", "memories", "jobs", "api_keys"].map((t) => [
    t,
    normText(
      // Columns have no keys here: any time at or after the fixed clock.
      JSON.stringify(
        db.$client
          .prepare(
            {
              documents: "SELECT id, workspace_id, container, source_type, title, url, content, status, metadata, source_id, created_at FROM documents ORDER BY rowid",
              memories: "SELECT id, workspace_id, container, fact, keywords, kind, supersedes_id, valid_from, valid_to, review_note, event_date, event_precision, due_date, due_precision, created_at FROM memories ORDER BY rowid",
              jobs: "SELECT id, workspace_id, type, payload, status, run_after FROM jobs ORDER BY id",
              api_keys: "SELECT id, last_used_at FROM api_keys ORDER BY id",
            }[t]!,
          )
          .raw()
          .all(),
      ).replace(/\b(\d{13})\b/g, (m, v: string) => (Number(v) >= NOW && Number(v) < 1e13 ? '"<now>"' : m)),
    ),
  ]),
)
const file = path.resolve(import.meta.dirname, "../rest.json")
fs.writeFileSync(file, JSON.stringify({ now: NOW, keys, setup, refs, steps: steps2, tables, vectors }, null, 1) + "\n")
fs.rmSync(tmp, { recursive: true, force: true })
console.log(`wrote ${file}: ${out.length} steps, ${Object.keys(vectors).length} vectors`)
process.exit(0)
