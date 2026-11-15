// Server function golden: calls the real dashboard server functions
// (src/server/fns/: fns.ts, connector-fns.ts, oauth-fns.ts, which were
// src/lib/ before M8 made those files fetch wrappers) on a copy of
// testdata/search/seeded.db with workspaces, members and Better Auth
// sessions added, and writes testdata/rpc.json: each call's input, who made
// it, what it returned or threw, and the rows a write left behind.
//
// rpc-loader.mjs points @tanstack/react-start and @tanstack/react-start/server
// at rpc-shim.ts, so each export is { method, call(input) } running the real
// validator and handler, and getRequest() is the step's request (cookie,
// origin, client IP). session.ts and Better Auth are the real ones.
//
// The clock is fixed (Date and Date.now). Random values stay raw: the Go
// test pairs them with its own (internal/httpapi/rpc_golden_test.go).
// Embeddings go through embed-recorder.ts so Go replays the same vectors.
//
// Run from apps/app: npx tsx ../server/testdata/gen/rpc-goldens.ts
import fs from "node:fs"
import { register } from "node:module"
import os from "node:os"
import path from "node:path"
import { pathToFileURL } from "node:url"

const gen = import.meta.dirname
register(pathToFileURL(path.join(gen, "rpc-loader.mjs")), { data: { shim: pathToFileURL(path.join(gen, "rpc-shim.ts")).href } })

const NOW = Date.UTC(2026, 10, 12, 3, 0, 0)
const RealDate = Date
class FakeDate extends RealDate {
  constructor(...args: unknown[]) {
    if (args.length === 0) super(NOW)
    else super(...(args as [number]))
  }
  static now() {
    return NOW
  }
}
globalThis.Date = FakeDate as DateConstructor

const ORIGIN = "http://localhost:4800"
const SECRET = "rpc-golden-session-secret-0123456789abcdef"
const CONNECTOR_SECRET = "rpc-golden-connector-secret-0123456789abcdef"
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "recall-rpc-golden-"))
const embedLog = path.join(tmp, "embed.jsonl")
Object.assign(process.env, {
  GATEWAY_KEY: "zkg_test",
  GATEWAY_URL: "http://gateway.test",
  RECALL_EMBED: "child",
  RECALL_EMBED_CHILD_PATH: path.join(gen, "embed-recorder.ts"),
  EMBED_LOG: embedLog,
  MODELS_DIR: path.resolve("data/models"),
  DATA_DIR: tmp,
  RECALL_JOBS: "off",
  RECALL_URL: ORIGIN,
  SESSION_SECRET: SECRET,
  CONNECTOR_SECRET,
})
globalThis.fetch = (async (input: unknown) => {
  throw new TypeError("fetch failed", { cause: new Error(`network access refused: ${String(input)}`) })
}) as typeof fetch

const dbFile = path.join(tmp, "recall.db")
fs.copyFileSync(path.resolve(gen, "../search/seeded.db"), dbFile)
process.env.RECALL_DB_PATH = dbFile

const shim = await import("./rpc-shim.ts")
const { getDb } = await import("../../../app/src/server/db/index.ts")
const { authHandler } = await import("../../../app/src/server/better-auth.ts")
const { stopEmbedder } = await import("../../../app/src/server/embed.ts")
const { createUserWithPassword, createWorkspace, addMember } = await import("../../../app/src/server/users.ts")
const { makeSignature } = await import("../../../app/node_modules/better-auth/dist/crypto/index.mjs")
const fnModules = {
  ...(await import("../../../app/src/server/fns/fns.ts")),
  ...(await import("../../../app/src/server/fns/connector-fns.ts")),
  ...(await import("../../../app/src/server/fns/oauth-fns.ts")),
} as Record<string, unknown>
type ServerFn = import("./rpc-shim.ts").ServerFn
const fns = Object.fromEntries(Object.entries(fnModules).filter(([, v]) => !!v && typeof v === "object" && "call" in (v as object))) as Record<string, ServerFn>

const db = getDb()
const PW = "a long enough password"
const ALPHA = "ws_alpha00000000000"
const BETA = "ws_beta000000000000"
const setup = {
  users: ["zaki", "adam", "mia", "lone"].map((username) => ({ username, password: PW })),
  workspaces: [
    { id: ALPHA, name: "Alpha" },
    { id: BETA, name: "Beta" },
  ],
  members: [
    { workspace: ALPHA, user: "zaki", role: "owner" },
    { workspace: BETA, user: "zaki", role: "member" },
    { workspace: ALPHA, user: "adam", role: "admin" },
    { workspace: ALPHA, user: "mia", role: "member" },
  ],
}
const userIds: Record<string, string> = {}
for (const u of setup.users) userIds[u.username] = (await createUserWithPassword(db, u)).id
for (const w of setup.workspaces) createWorkspace(db, w)
for (const m of setup.members) addMember(db, { workspaceId: m.workspace, userId: userIds[m.user], role: m.role as "owner" })
// Members joined at distinct times, so lists ordered by join time have no ties
// (a tie falls back to the random user ids). The Go test does the same.
db.$client.prepare("UPDATE member SET created_at = created_at + rowid").run()

// Better Auth sessions, one per user, through the real sign-in.
const auth = authHandler(db)
const jars: Record<string, string> = {}
for (const u of setup.users) {
  const res = await auth(
    new Request(`${ORIGIN}/api/auth/sign-in/username`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: ORIGIN, "x-forwarded-for": "198.51.100.1" },
      body: JSON.stringify({ username: u.username, password: PW }),
    }),
  )
  if (res.status !== 200) throw new Error(`sign-in ${u.username}: ${res.status}`)
  jars[u.username] = res.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ")
}

const TABLES: Record<string, string> = {
  organization: "SELECT * FROM organization ORDER BY rowid",
  member: "SELECT * FROM member ORDER BY rowid",
  session: "SELECT id, user_id, active_organization_id FROM session ORDER BY rowid",
  invitation: "SELECT * FROM invitation ORDER BY rowid",
  api_keys: "SELECT * FROM api_keys ORDER BY rowid",
  memories: "SELECT * FROM memories ORDER BY rowid",
  documents: "SELECT * FROM documents ORDER BY rowid",
  jobs: "SELECT id, workspace_id, type, payload, status, attempts, run_after, created_at FROM jobs ORDER BY rowid",
  people: "SELECT * FROM people ORDER BY rowid",
  reminder_settings: "SELECT * FROM reminder_settings ORDER BY rowid",
  passkey: "SELECT * FROM passkey ORDER BY rowid",
  app_connectors: "SELECT * FROM app_connectors ORDER BY rowid",
}
let last: Record<string, string> = {}
function changedRows() {
  const out: Record<string, unknown> = {}
  for (const [t, q] of Object.entries(TABLES)) {
    let rows: unknown[]
    try {
      rows = db.$client.prepare(q).all()
    } catch {
      continue
    }
    const text = JSON.stringify(rows)
    if (text !== last[t]) out[t] = rows
    last[t] = text
  }
  return Object.keys(out).length ? out : undefined
}
changedRows()

type Step = { name: string; fn?: string; sql?: string; input?: unknown; multipart?: Record<string, unknown>; as?: string; cookie?: string; headers?: Record<string, string>; res?: unknown; rows?: unknown }
const steps: Step[] = []
let ip = 0

function errorOf(err: unknown) {
  const e = err as { name?: string; message?: string; issues?: unknown; constructor?: { name?: string } }
  return { name: e?.constructor?.name ?? e?.name ?? "Error", message: String(e?.message ?? err), issues: e?.issues }
}

async function call(name: string, fn: string, input?: unknown, opts: { as?: string; cookie?: string; headers?: Record<string, string>; multipart?: Record<string, unknown> } = {}) {
  const f = fns[fn]
  if (!f) throw new Error(`no server function ${fn}`)
  const headers: Record<string, string> = { host: "localhost:4800", "x-forwarded-for": `203.0.113.${(++ip % 250) + 1}` }
  if (f.method === "POST") headers.origin = ORIGIN
  Object.assign(headers, opts.headers ?? {})
  for (const [k, v] of Object.entries(headers)) if (v === "") delete headers[k]
  const sent = { ...headers }
  const cookies = [opts.as ? jars[opts.as] : "", opts.cookie ?? ""].filter(Boolean)
  if (cookies.length) headers.cookie = cookies.join("; ")
  shim.setRequest(new Request(`${ORIGIN}/_serverFn/${fn}`, { method: f.method, headers }))
  let arg: unknown = input
  if (opts.multipart) {
    const form = new FormData()
    for (const [k, v] of Object.entries(opts.multipart)) {
      if (v && typeof v === "object" && "content" in (v as object)) {
        const file = v as { name: string; type: string; content: string }
        form.set(k, new File([file.content], file.name, { type: file.type }))
      } else form.set(k, String(v))
    }
    arg = form
  }
  let res: unknown
  try {
    res = { ok: await f.call(arg) }
  } catch (err) {
    res = { error: errorOf(err) }
  }
  steps.push({ name, fn, input, multipart: opts.multipart, as: opts.as, headers: sent, cookie: opts.cookie, res: JSON.parse(JSON.stringify(res) ?? "null"), rows: changedRows() })
  return res as { ok?: Record<string, unknown>; error?: { message: string } }
}

function sql(name: string, statement: string) {
  db.$client.prepare(statement).run()
  steps.push({ name, sql: statement, rows: changedRows() })
}

const one = (q: string, ...args: unknown[]) => db.$client.prepare(q).get(...args) as Record<string, string>
const memId = one(`SELECT id FROM memories WHERE workspace_id = ? AND container = 'default' AND valid_to IS NULL ORDER BY rowid LIMIT 1`, ALPHA).id
const chained = one(`SELECT id FROM memories WHERE workspace_id = ? AND supersedes_id IS NOT NULL ORDER BY rowid LIMIT 1`, ALPHA)?.id ?? memId
const docId = one(`SELECT id FROM documents WHERE workspace_id = ? ORDER BY rowid LIMIT 1`, ALPHA).id
const betaMem = one(`SELECT id FROM memories WHERE workspace_id = ? ORDER BY rowid LIMIT 1`, BETA).id

// --- session and workspaces -------------------------------------------------------
await call("getSession signed out", "getSession")
await call("getSession", "getSession", undefined, { as: "zaki" })
await call("getSession with the tz cookie", "getSession", undefined, { as: "mia", cookie: "recall.tz=Asia%2FJakarta" })
await call("getSession with a bad tz cookie", "getSession", undefined, { as: "mia", cookie: "recall.tz=Not%2FAZone" })
await call("switchWorkspace to Beta", "switchWorkspace", { id: BETA }, { as: "zaki" })
await call("getContainers in Beta", "getContainers", undefined, { as: "zaki" })
await call("switchWorkspace to a stranger's", "switchWorkspace", { id: "ws_nope" }, { as: "mia" })
await call("switchWorkspace back", "switchWorkspace", { id: ALPHA }, { as: "zaki" })
await call("switchWorkspace from another site", "switchWorkspace", { id: ALPHA }, { as: "zaki", headers: { origin: "https://evil.example" } })
await call("switchWorkspace bad input", "switchWorkspace", { id: "" }, { as: "zaki" })
await call("newWorkspace", "newWorkspace", { name: "  Side project  " }, { as: "mia" })
await call("newWorkspace too long", "newWorkspace", { name: "x".repeat(81) }, { as: "mia" })
await call("switch mia back", "switchWorkspace", { id: ALPHA }, { as: "mia" })
await call("getContainers signed out", "getContainers")
await call("getContainers without a workspace", "getContainers", undefined, { as: "lone" })
await call("getContainers", "getContainers", undefined, { as: "zaki" })

// --- memories ------------------------------------------------------------------------
await call("getMemories", "getMemories", { container: "default" }, { as: "zaki" })
const page = await call("getMemories page", "getMemories", { container: "big", limit: 5 }, { as: "zaki" })
await call("getMemories next page", "getMemories", { container: "big", limit: 5, cursor: page.ok?.nextCursor }, { as: "zaki" })
await call("getMemories history and review", "getMemories", { container: "default", history: true, review: true }, { as: "mia" })
await call("getMemories bad limit", "getMemories", { container: "default", limit: 0 }, { as: "zaki" })
await call("getMemories blank container", "getMemories", { container: "  " }, { as: "zaki" })
await call("getChain", "getChain", { id: chained }, { as: "zaki" })
await call("getChain of another workspace's", "getChain", { id: betaMem }, { as: "zaki" })
await call("updateMemory", "updateMemory", { id: memId, fact: "  Edited fact  ", keywords: "edit", kind: "dynamic", eventDate: "2026-11", dueDate: null }, { as: "zaki" })
await call("updateMemory stale", "updateMemory", { id: memId, fact: "Again" }, { as: "zaki" })
await call("updateMemory bad date", "updateMemory", { id: memId, fact: "x", eventDate: "soon" }, { as: "zaki" })
const current = one(`SELECT id FROM memories WHERE workspace_id = ? AND container = 'default' AND valid_to IS NULL ORDER BY rowid DESC LIMIT 1`, ALPHA).id
await call("resolveMemory", "resolveMemory", { id: current }, { as: "zaki" })
await call("deleteMemory", "deleteMemory", { id: current }, { as: "zaki" })
await call("deleteMemory again", "deleteMemory", { id: current }, { as: "zaki" })

// --- upcoming and reminders --------------------------------------------------------------
await call("getUpcoming", "getUpcoming", { container: "default", days: 30 }, { as: "zaki" })
await call("getUpcoming all", "getUpcoming", { container: "default", days: 366, all: true }, { as: "zaki" })
await call("getUpcoming bad days", "getUpcoming", { container: "default", days: 400 }, { as: "zaki" })
await call("getReminders", "getReminders", undefined, { as: "zaki" })
await call("saveReminders", "saveReminders", { enabled: true, email: "zaki@example.com", leadDays: [7, 1, 1, 0], timezone: "Asia/Makassar" }, { as: "zaki" })
await call("saveReminders without email", "saveReminders", { enabled: true, email: "", leadDays: [1], timezone: "UTC" }, { as: "zaki" })
await call("saveReminders no leads", "saveReminders", { enabled: true, email: "a@b.co", leadDays: [], timezone: "UTC" }, { as: "zaki" })
await call("saveReminders bad zone", "saveReminders", { enabled: false, email: "", leadDays: [], timezone: "Mars/Base" }, { as: "zaki" })
await call("saveReminders bad email", "saveReminders", { enabled: false, email: "nope", leadDays: [], timezone: "UTC" }, { as: "zaki" })
await call("getUpcoming after saving", "getUpcoming", { container: "default", days: 7 }, { as: "zaki" })
await call("sendReminderTest", "sendReminderTest", undefined, { as: "zaki" })

// --- people ---------------------------------------------------------------------------------
await call("getPeople", "getPeople", { container: "default" }, { as: "zaki" })
await call("getPeople search", "getPeople", { container: "default", q: "  an  ", sort: "name" }, { as: "zaki" })
await call("getMergeSuggestions", "getMergeSuggestions", { container: "default" }, { as: "zaki" })
await call("getPersonPage missing", "getPersonPage", { id: "per_nope" }, { as: "zaki" })
await call("savePerson missing", "savePerson", { id: "per_nope", name: "X", aliases: [], role: null, organization: null, summary: "", emails: [], handles: [] }, { as: "zaki" })
await call("savePerson bad email", "savePerson", { id: "per_nope", name: "X", aliases: [], role: null, organization: null, summary: "", emails: ["nope"], handles: [] }, { as: "zaki" })
await call("mergePerson missing", "mergePerson", { keepId: "per_a", dropId: "per_b" }, { as: "zaki" })
await call("removePerson missing", "removePerson", { id: "per_nope" }, { as: "zaki" })

// --- search and ask -------------------------------------------------------------------------
await call("searchMemories", "searchMemories", { container: "default", q: "coffee" }, { as: "zaki" })
await call("searchMemories with chunks", "searchMemories", { container: "big", q: "project deadline", chunks: true, limit: 5 }, { as: "zaki" })
await call("searchMemories empty", "searchMemories", { container: "default", q: "   " }, { as: "zaki" })
await call("askMemory without the job loop", "askMemory", { container: "default", question: "What do I drink?" }, { as: "zaki" })

// --- documents ----------------------------------------------------------------------------------
const docs = await call("getDocuments", "getDocuments", { container: "default", limit: 2 }, { as: "zaki" })
await call("getDocuments next", "getDocuments", { container: "default", limit: 2, cursor: docs.ok?.nextCursor }, { as: "zaki" })
await call("addText", "addText", { container: "default", kind: "text", value: "  I moved to Banjarbaru in 2024.  ", title: " Move " }, { as: "zaki" })
await call("addText url", "addText", { container: "default", kind: "url", value: "https://example.com/post" }, { as: "zaki" })
await call("addText bad url", "addText", { container: "default", kind: "url", value: "ftp://example.com" }, { as: "zaki" })
await call("addText from another site", "addText", { container: "default", kind: "text", value: "x" }, { as: "zaki", headers: { "sec-fetch-site": "cross-site" } })
await call("uploadFile", "uploadFile", undefined, { as: "zaki", multipart: { container: "default", file: { name: "notes.md", type: "text/markdown", content: "# Notes\n\nI like tea." } } })
await call("uploadFile empty", "uploadFile", undefined, { as: "zaki", multipart: { container: "default", file: { name: "empty.txt", type: "text/plain", content: "" } } })
await call("uploadFile no file", "uploadFile", undefined, { as: "zaki", multipart: { container: "default" } })
await call("retryDocument", "retryDocument", { id: docId }, { as: "zaki" })
await call("retryDocument missing", "retryDocument", { id: "doc_nope" }, { as: "zaki" })
await call("removeDocument", "removeDocument", { id: docId }, { as: "zaki" })
await call("removeDocument again", "removeDocument", { id: docId }, { as: "zaki" })

// --- graph --------------------------------------------------------------------------------------
await call("getGraph", "getGraph", { container: "work" }, { as: "zaki" })

// --- keys -----------------------------------------------------------------------------------
await call("getKeys", "getKeys", undefined, { as: "mia" })
await call("getConnectInfo", "getConnectInfo", undefined, { as: "adam" })
await call("createKey as a member", "createKey", { name: "laptop" }, { as: "mia" })
const key = await call("createKey", "createKey", { name: "  laptop  " }, { as: "adam" })
await call("getKeys as admin", "getKeys", undefined, { as: "adam" })
await call("revokeKey", "revokeKey", { id: key.ok?.id }, { as: "adam" })
await call("revokeKey again", "revokeKey", { id: key.ok?.id }, { as: "zaki" })

// --- members and invites ------------------------------------------------------------------------
await call("getMembers as a member", "getMembers", undefined, { as: "mia" })
await call("getMembers", "getMembers", undefined, { as: "zaki" })
await call("createInviteLink owner by an admin", "createInviteLink", { role: "owner" }, { as: "adam" })
const inv = await call("createInviteLink", "createInviteLink", { role: "member" }, { as: "adam" })
const token = String(inv.ok?.url ?? "").split("/").pop()!
const inv2 = await call("createInviteLink admin", "createInviteLink", { role: "admin" }, { as: "zaki" })
await call("revokeInviteLink", "revokeInviteLink", { id: inv2.ok?.id }, { as: "zaki" })
await call("getInvite signed out", "getInvite", { token })
await call("getInvite as a member", "getInvite", { token }, { as: "mia" })
await call("getInvite unknown", "getInvite", { token: "rci_doesnotexist" })
await call("acceptInvite", "acceptInvite", { token }, { as: "lone" })
await call("acceptInvite again", "acceptInvite", { token }, { as: "lone" })
const inv3 = await call("createInviteLink for a new account", "createInviteLink", { role: "member" }, { as: "zaki" })
const token3 = String(inv3.ok?.url ?? "").split("/").pop()!
await call("acceptInviteNewAccount taken name", "acceptInviteNewAccount", { token: token3, username: "mia", password: PW })
await call("acceptInviteNewAccount short password", "acceptInviteNewAccount", { token: token3, username: "newbie", password: "short" })
const lone = one(`SELECT id FROM member WHERE user_id = ? AND organization_id = ?`, userIds.lone, ALPHA)
await call("setMemberRole", "setMemberRole", { memberId: lone.id, role: "admin" }, { as: "zaki" })
await call("setMemberRole by a member", "setMemberRole", { memberId: lone.id, role: "member" }, { as: "mia" })
await call("setMemberRole last owner", "setMemberRole", { memberId: one(`SELECT id FROM member WHERE user_id = ? AND organization_id = ?`, userIds.zaki, ALPHA).id, role: "member" }, { as: "zaki" })
await call("removeWorkspaceMember", "removeWorkspaceMember", { memberId: lone.id }, { as: "zaki" })
await call("removeWorkspaceMember self", "removeWorkspaceMember", { memberId: one(`SELECT id FROM member WHERE user_id = ? AND organization_id = ?`, userIds.mia, ALPHA).id }, { as: "mia" })

// --- account ---------------------------------------------------------------------------------
sql("a passkey for zaki", `INSERT INTO passkey (id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid) VALUES ('pk_golden_000000001', 'MacBook', 'pub', '${userIds.zaki}', 'cred-1', 0, 'multiDevice', 1, 'internal', ${NOW}, 'aaguid')`)
await call("getAccount", "getAccount", undefined, { as: "zaki" })
await call("removePasskey of someone else", "removePasskey", { id: "pk_golden_000000001" }, { as: "adam" })
await call("removePasskey", "removePasskey", { id: "pk_golden_000000001" }, { as: "zaki" })

// --- connectors -------------------------------------------------------------------------------
await call("getConnectors", "getConnectors", { container: "default" }, { as: "zaki" })
await call("getConnectors as a member", "getConnectors", { container: "default" }, { as: "mia" })
await call("searchGmailMessages not connected", "searchGmailMessages", { container: "default", q: "invoice" }, { as: "zaki" })
await call("importGmailMessages bad id", "importGmailMessages", { container: "default", messages: [{ id: "../x" }] }, { as: "zaki" })
await call("disconnectGmail not connected", "disconnectGmail", undefined, { as: "zaki" })
await call("saveAppConnectorFn as a member", "saveAppConnectorFn", { provider: "oslo", container: "default", baseUrl: "https://oslo.example.com", enabled: false }, { as: "mia" })
await call("saveAppConnectorFn on without a key", "saveAppConnectorFn", { provider: "oslo", container: "default", baseUrl: "https://oslo.example.com", enabled: true }, { as: "zaki" })
await call("saveAppConnectorFn", "saveAppConnectorFn", { provider: "finansial", container: "money", baseUrl: "https://finansial.example.com/", apiKey: "  fk_secret  ", enabled: true, thresholds: { IDR: 500000 } }, { as: "zaki" })
await call("saveAppConnectorFn http", "saveAppConnectorFn", { provider: "oslo", container: "default", baseUrl: "http://oslo.example.com", apiKey: "k", enabled: false }, { as: "zaki" })
await call("syncAppConnectorFn unset", "syncAppConnectorFn", { provider: "oslo" }, { as: "zaki" })

// --- oauth consent page ----------------------------------------------------------------------
sql("an OAuth client", `INSERT INTO oauth_client (id, client_id, name, uri, redirect_uris, token_endpoint_auth_method, scopes, created_at, updated_at) VALUES ('golden_client_row', 'goldenclientidgoldenclientidgold', 'Claude', 'https://claude.ai', '["https://claude.ai/api/mcp/auth_callback"]', 'none', '["mcp"]', ${NOW}, ${NOW})`)
const params = new URLSearchParams({ response_type: "code", client_id: "goldenclientidgoldenclientidgold", redirect_uri: "https://claude.ai/api/mcp/auth_callback", scope: "mcp offline_access", exp: String(Math.floor(NOW / 1000) + 600) })
const canonical = new URLSearchParams([...params.entries()].sort(([a, x], [b, y]) => (a < b ? -1 : a > b ? 1 : x < y ? -1 : x > y ? 1 : 0)))
params.set("sig", await makeSignature(canonical.toString(), SECRET))
await call("getConsentRequest signed out", "getConsentRequest", { query: params.toString() })
await call("getConsentRequest", "getConsentRequest", { query: `?${params.toString()}` }, { as: "zaki" })
await call("getConsentRequest tampered", "getConsentRequest", { query: params.toString().replace("mcp", "mcpx") }, { as: "zaki" })
await call("getConsentRequest without a workspace", "getConsentRequest", { query: params.toString() }, { as: "lone" })

stopEmbedder()
const vectors: Record<string, string> = {}
if (fs.existsSync(embedLog))
  for (const line of fs.readFileSync(embedLog, "utf8").split("\n").filter(Boolean)) {
    const r = JSON.parse(line) as { kind: string; texts: string[]; vectors: string[] }
    r.texts.forEach((t, k) => (vectors[`${r.kind}:${t}`] ??= r.vectors[k]!))
  }
const out = path.resolve(gen, "../rpc.json")
fs.writeFileSync(out, JSON.stringify({ now: NOW, origin: ORIGIN, secret: SECRET, connectorSecret: CONNECTOR_SECRET, password: PW, setup, steps, vectors }, null, 1) + "\n")
console.log(`wrote ${steps.length} steps to ${out}`)
process.exit(0)
