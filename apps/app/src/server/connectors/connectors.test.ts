// The Gmail connector against a fake Google (fixtures behind an injected fetch):
// OAuth, encrypted tokens, search, import through the job loop, PDF
// attachments, disconnect, tenant isolation and what reaches the logs.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { eq } from "drizzle-orm"
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from "vitest"
import { Route as ImportRoute } from "@/server/routes-legacy/v1/connectors/gmail/import"
import { createApiKey } from "../auth"
import { authHandler } from "../better-auth"
import { openDb, setDb, type DB } from "../db"
import { connectorAccounts, documents, member } from "../db/schema"
import type { chatJson } from "../gateway/client"
import { JobRunner } from "../jobs/runner"
import { createHandlers } from "../pipeline"
import { addMember, createUserWithPassword, createWorkspace } from "../users"
import { SealError, seal, unseal } from "./crypto"
import { createFakeGoogle } from "./fake-google"
import { MESSAGES } from "./fixtures/gmail"
import {
  accessToken,
  ConnectorError,
  connectorsConfigured,
  disconnect,
  getConnection,
  importGmail,
  listConnections,
  listGmailImports,
  mayRetry,
  missingKeys,
  resolveEmail,
  searchGmail,
} from "./gmail"
import { decodeEntities, parseMessage } from "./gmail-message"
import { GMAIL_SCOPE } from "./google"
import { handleCallback, handleStart, STATE_COOKIE } from "./oauth"

type Handler = (ctx: { request: Request; params: Record<string, string> }) => Promise<Response>
const handlers = (route: unknown) => (route as { options: { server: { handlers: Record<string, Handler> } } }).options.server.handlers

const ORIGIN = "http://localhost:4800"
const PW = "correct horse battery staple"
const TICKET = "18c2a1f0b9e4d701"
const KICKOFF = "18c29e7d3a5b6c02"
const BILL = "18c29b11e0f4a903"
const INVOICE = "18c2950a7f1c3d05"
// Strings that exist only inside fixture mail or tokens. None may reach a log.
const SECRET_WORDS = ["KX7Q2M", "Banjarbaru", "412.500", "Rina owns", "fake-refresh", "fake-access", "INV-2026-1107"]

const ENV = {
  GOOGLE_CLIENT_ID: "test-client.apps.googleusercontent.com",
  GOOGLE_CLIENT_SECRET: "test-client-secret",
  CONNECTOR_SECRET: "test-connector-secret-0123456789abcdefghijklmnop",
  RECALL_URL: ORIGIN,
}
const saved: Record<string, string | undefined> = {}

let db: DB
let dataDir: string
const ws = { a: "", b: "" }
const users = { zaki: "", eva: "", budi: "", mallory: "" }
const cookies = { zaki: "", eva: "", budi: "", mallory: "" }

function cookieHeader(res: Response) {
  return res.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ")
}

async function signIn(username: string) {
  const res = await authHandler(db)(
    new Request(`${ORIGIN}/api/auth/sign-in/username`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: ORIGIN, "x-forwarded-for": "203.0.113.9" },
      body: JSON.stringify({ username, password: PW }),
    }),
  )
  expect(res.status).toBe(200)
  return cookieHeader(res)
}

beforeAll(async () => {
  for (const k of [...Object.keys(ENV), "DATA_DIR", "RECALL_AUTH_RATE_LIMIT"]) saved[k] = process.env[k]
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-gmail-"))
  Object.assign(process.env, ENV, { DATA_DIR: dataDir, RECALL_AUTH_RATE_LIMIT: "off" })
  db = openDb(":memory:")
  setDb(db)
  ws.a = createWorkspace(db, { name: "Alpha" }).id
  ws.b = createWorkspace(db, { name: "Beta" }).id
  for (const [name, w, role] of [
    ["zaki", ws.a, "owner"],
    ["eva", ws.a, "admin"],
    ["budi", ws.a, "member"],
    ["mallory", ws.b, "owner"],
  ] as const) {
    const u = await createUserWithPassword(db, { username: name, password: PW })
    users[name] = u.id
    addMember(db, { workspaceId: w, userId: u.id, role })
    cookies[name] = await signIn(name)
  }
})

afterAll(() => {
  for (const [k, v] of Object.entries(saved)) {
    if (v === undefined) delete process.env[k]
    else process.env[k] = v
  }
  fs.rmSync(dataDir, { recursive: true, force: true })
})

beforeEach(() => {
  db.delete(connectorAccounts).run()
  db.delete(documents).run()
})

function startReq(cookie: string, extra: Record<string, string> = {}) {
  return new Request(`${ORIGIN}/app/connectors/google/start?c=inbox`, { headers: { cookie, "sec-fetch-site": "same-origin", ...extra } })
}

// Runs the whole connect flow for a user and returns the callback response.
async function connect(name: keyof typeof users, fake = createFakeGoogle(), tweak?: { state?: string; cookie?: string; query?: string }) {
  const start = await handleStart(db, startReq(cookies[name]))
  expect(start.status).toBe(302)
  const state = new URL(start.headers.get("location")!).searchParams.get("state")!
  const stateCookie = start.headers.getSetCookie()[0]!.split(";")[0]!
  const cookie = tweak?.cookie ?? `${cookies[name]}; ${stateCookie}`
  const query = tweak?.query ?? `code=4%2F0Afake&state=${tweak?.state ?? state}&scope=${encodeURIComponent(GMAIL_SCOPE)}`
  return handleCallback(db, new Request(`${ORIGIN}/app/connectors/google/callback?${query}`, { headers: { cookie } }), fake.fetch)
}

const silentChat = vi.fn(async (req: { schema: { parse: (x: unknown) => unknown }; system: string }) =>
  req.schema.parse(req.system.includes("profile") ? { static: "", dynamic: "" } : { add: [] }),
) as unknown as typeof chatJson

async function drain(fake: ReturnType<typeof createFakeGoogle>, log: (m: string) => void = () => {}) {
  const runner = new JobRunner(db, createHandlers(db, { chat: silentChat, now: () => new Date("2026-11-10"), gmailFetch: fake.fetch }), log)
  for (let i = 0; i < 50; i++) if (!(await runner.runOnce(Date.now() + 3_600_000))) return
}

const docBySource = (sourceId: string) => db.select().from(documents).where(eq(documents.sourceId, sourceId)).all()

// --- crypto ---------------------------------------------------------------------------

describe("sealed values", () => {
  const secret = ENV.CONNECTOR_SECRET
  it("round-trips and binds to its context", () => {
    const sealed = seal(secret, '{"refreshToken":"abc"}', "connector:con_1:ws:usr")
    expect(sealed).not.toContain("abc")
    expect(unseal(secret, sealed, "connector:con_1:ws:usr")).toBe('{"refreshToken":"abc"}')
    expect(() => unseal(secret, sealed, "connector:con_2:ws:usr")).toThrow(SealError)
    expect(() => unseal(`${secret}x`, sealed, "connector:con_1:ws:usr")).toThrow(SealError)
    const [v, iv, body, tag] = sealed.split(".")
    const flipped = `${body![0] === "A" ? "B" : "A"}${body!.slice(1)}`
    expect(() => unseal(secret, [v, iv, flipped, tag].join("."), "connector:con_1:ws:usr")).toThrow(SealError)
  })

  it("refuses a short secret", () => {
    expect(() => seal("short", "x", "c")).toThrow(SealError)
  })
})

// --- message parsing --------------------------------------------------------------------

describe("parseMessage", () => {
  it("prefers the plain text part and finds PDF attachments only", () => {
    const p = parseMessage(MESSAGES.find((m) => m.id === TICKET)!)
    expect(p.subject).toBe("E-ticket: Jakarta to Banjarbaru, 14 Nov")
    expect(p.text).toMatch(/^Subject: E-ticket/)
    expect(p.text).toContain("From: Garuda Indonesia")
    expect(p.text).toContain("Booking code: KX7Q2M")
    expect(p.text).not.toContain("\r")
    expect(p.pdfs).toEqual([expect.objectContaining({ partId: "2", attachmentId: "ANGjdJ8w-ticket-part-2", filename: "e-ticket-GA532.pdf" })])
  })

  it("reads an HTML-only message as text", () => {
    const p = parseMessage(MESSAGES.find((m) => m.id === BILL)!)
    expect(p.text).toContain("Rp 412.500")
    expect(p.text).not.toContain("<p>")
    expect(p.text).not.toContain("color:red")
  })

  it("finds an inline PDF sent as octet-stream", () => {
    const p = parseMessage(MESSAGES.find((m) => m.id === INVOICE)!)
    expect(p.pdfs).toEqual([expect.objectContaining({ partId: "1", attachmentId: null, filename: "INV-2026-1107.pdf" })])
    expect(p.pdfs[0]!.inlineData).toBeTruthy()
  })

  it("decodes snippet entities", () => {
    expect(decodeEntities("a &amp; b &#39;c&#39; &lt;d&gt; &#x41;")).toBe("a & b 'c' <d> A")
  })
})

// --- configuration ------------------------------------------------------------------------

describe("configuration", () => {
  it("is off and says which keys are missing when they aren't set", async () => {
    const keep = { ...ENV }
    delete process.env.GOOGLE_CLIENT_ID
    process.env.CONNECTOR_SECRET = "too-short"
    try {
      expect(connectorsConfigured()).toBe(false)
      expect(missingKeys()).toEqual(["GOOGLE_CLIENT_ID", "CONNECTOR_SECRET"])
      const res = await handleStart(db, startReq(cookies.zaki))
      expect(res.headers.get("location")).toBe("/app/connectors?gmail=not_configured&c=inbox")
      expect(() => importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })).toThrow(/isn't set up/)
    } finally {
      Object.assign(process.env, keep)
    }
    expect(connectorsConfigured()).toBe(true)
  })
})

// --- OAuth ------------------------------------------------------------------------------------

describe("connect", () => {
  it("sends the user to Google for gmail.readonly with offline access and PKCE", async () => {
    const res = await handleStart(db, startReq(cookies.zaki))
    const loc = new URL(res.headers.get("location")!)
    expect(`${loc.origin}${loc.pathname}`).toBe("https://accounts.google.com/o/oauth2/v2/auth")
    expect(Object.fromEntries(loc.searchParams)).toMatchObject({
      client_id: ENV.GOOGLE_CLIENT_ID,
      redirect_uri: `${ORIGIN}/app/connectors/google/callback`,
      response_type: "code",
      scope: GMAIL_SCOPE,
      access_type: "offline",
      prompt: "consent",
      code_challenge_method: "S256",
    })
    expect(loc.searchParams.get("code_challenge")).toMatch(/^[A-Za-z0-9_-]{43}$/)
    const set = res.headers.getSetCookie()[0]!
    expect(set).toMatch(new RegExp(`^${STATE_COOKIE.replace(".", "\\.")}=v1\\.`))
    expect(set).toMatch(/HttpOnly/)
    expect(set).toMatch(/SameSite=Lax/)
    expect(set).toMatch(/Path=\/app\/connectors\/google/)
  })

  it("sends a signed-out visitor to login and refuses a cross-site start", async () => {
    const out = await handleStart(db, startReq(""))
    expect(out.headers.get("location")).toMatch(/^\/app\/login/)
    const cross = await handleStart(db, startReq(cookies.zaki, { "sec-fetch-site": "cross-site" }))
    expect(cross.headers.get("location")).toBe("/app/connectors?gmail=failed&c=inbox")
    expect(cross.headers.get("set-cookie")).toBeNull()
  })

  it("stores the tokens sealed, for that user and workspace only", async () => {
    const res = await connect("zaki")
    expect(res.headers.get("location")).toBe("/app/connectors?gmail=connected&c=inbox")
    expect(res.headers.getSetCookie()[0]).toMatch(/Max-Age=0/)
    const row = getConnection(db, ws.a, users.zaki)!
    expect(row).toMatchObject({ provider: "gmail", accountEmail: "zaki@example.com", status: "active", scope: GMAIL_SCOPE })
    expect(row.tokens).toMatch(/^v1\./)
    expect(row.tokens).not.toMatch(/fake-(access|refresh)/)
    expect(getConnection(db, ws.b, users.zaki)).toBeUndefined()
    expect(getConnection(db, ws.a, users.eva)).toBeUndefined()
  })

  it("refuses a wrong state, a missing cookie, another user's session and an expired flow", async () => {
    expect((await connect("zaki", undefined, { state: "forged" })).headers.get("location")).toMatch(/gmail=failed/)
    expect((await connect("zaki", undefined, { cookie: cookies.zaki })).headers.get("location")).toMatch(/gmail=failed/)
    // Eva's browser finishing a flow Zaki started.
    const start = await handleStart(db, startReq(cookies.zaki))
    const state = new URL(start.headers.get("location")!).searchParams.get("state")!
    const stateCookie = start.headers.getSetCookie()[0]!.split(";")[0]
    const stolen = await handleCallback(
      db,
      new Request(`${ORIGIN}/app/connectors/google/callback?code=x&state=${state}`, { headers: { cookie: `${cookies.eva}; ${stateCookie}` } }),
      createFakeGoogle().fetch,
    )
    expect(stolen.headers.get("location")).toMatch(/gmail=failed/)
    const late = await handleCallback(
      db,
      new Request(`${ORIGIN}/app/connectors/google/callback?code=x&state=${state}`, { headers: { cookie: `${cookies.zaki}; ${stateCookie}` } }),
      createFakeGoogle().fetch,
      Date.now() + 11 * 60_000,
    )
    expect(late.headers.get("location")).toMatch(/gmail=expired/)
    expect(db.select().from(connectorAccounts).all()).toEqual([])
  })

  it("handles a refusal on Google's consent screen", async () => {
    const res = await connect("zaki", undefined, { query: "error=access_denied" })
    expect(res.headers.get("location")).toMatch(/gmail=failed/)
    const start = await handleStart(db, startReq(cookies.zaki))
    const state = new URL(start.headers.get("location")!).searchParams.get("state")!
    const denied = await handleCallback(
      db,
      new Request(`${ORIGIN}/app/connectors/google/callback?error=access_denied&state=${state}`, {
        headers: { cookie: `${cookies.zaki}; ${start.headers.getSetCookie()[0]!.split(";")[0]}` },
      }),
      createFakeGoogle().fetch,
    )
    expect(denied.headers.get("location")).toBe("/app/connectors?gmail=denied&c=inbox")
    expect(getConnection(db, ws.a, users.zaki)).toBeUndefined()
  })

  it("revokes the old grant when the user connects again", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    await connect("zaki", fake)
    expect(fake.revokedTokens).toEqual(["fake-refresh-1"])
    expect(db.select().from(connectorAccounts).all()).toHaveLength(1)
  })
})

// --- search and import ---------------------------------------------------------------------------

describe("search and import", () => {
  it("searches with Gmail syntax and marks what is already imported", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    const bills = await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "label:bills" }, fake.fetch)
    expect(bills.messages.map((m) => m.id)).toEqual([BILL, INVOICE])
    expect(bills.messages[0]).toMatchObject({ from: "PLN Mobile <tagihan@pln.example>", subject: "Tagihan listrik Oktober 2026", imported: false })
    const gh = await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "from:github" }, fake.fetch)
    expect(gh.messages[0]!.snippet).toContain("Tailwind classes & drop")

    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: BILL }] })
    const again = await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "label:bills" }, fake.fetch)
    expect(again.messages.map((m) => m.imported)).toEqual([true, false])
    // Imported is per container.
    const other = await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "work", q: "label:bills" }, fake.fetch)
    expect(other.messages.map((m) => m.imported)).toEqual([false, false])
  })

  it("imports through the job loop: email text, PDF attachments as their own documents, re-import is a no-op", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    const first = importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET, subject: "E-ticket" }, { id: KICKOFF }] })
    expect(first).toMatchObject({ queued: 2, duplicates: 0 })
    // Nothing was fetched from Gmail during the request itself.
    expect(fake.calls.filter((c) => c.url.includes(`/messages/${TICKET}`))).toEqual([])
    expect(docBySource(`gmail:${TICKET}`)[0]).toMatchObject({ status: "queued", sourceType: "email" })

    await drain(fake)
    const email = docBySource(`gmail:${TICKET}`)[0]!
    expect(email).toMatchObject({ status: "done", sourceType: "email", title: "E-ticket: Jakarta to Banjarbaru, 14 Nov", container: "inbox", workspaceId: ws.a })
    expect(email.content).toContain("Booking code: KX7Q2M")
    expect(email.metadata).toMatchObject({ connector: "gmail", messageId: TICKET, attachments: 1 })
    const pdf = docBySource(`gmail:${TICKET}:2`)[0]!
    expect(pdf).toMatchObject({ status: "done", sourceType: "file", title: "e-ticket-GA532.pdf", container: "inbox" })
    expect(pdf.content).toContain("KX7Q2M")
    expect(docBySource(`gmail:${KICKOFF}`)[0]).toMatchObject({ status: "done", title: "Re: Project kickoff notes" })
    // The PNG logo is not a document.
    expect(db.select().from(documents).all()).toHaveLength(3)

    const again = importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }, { id: KICKOFF }] })
    expect(again).toMatchObject({ queued: 0, duplicates: 2 })
    await drain(fake)
    expect(db.select().from(documents).all()).toHaveLength(3)
    expect(listGmailImports(db, ws.a, "inbox", users.zaki).map((d) => d.sourceId).sort()).toEqual([`gmail:${KICKOFF}`, `gmail:${TICKET}`, `gmail:${TICKET}:2`].sort())
  })

  it("imports an inline PDF and an HTML-only message", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: INVOICE }, { id: BILL }] })
    await drain(fake)
    expect(docBySource(`gmail:${INVOICE}:1`)[0]).toMatchObject({ status: "done", sourceType: "file" })
    expect(docBySource(`gmail:${BILL}`)[0]!.content).toContain("Jatuh tempo 20 November 2026")
  })

  it("rejects ids that aren't Gmail ids and more than 50 at once", async () => {
    await connect("zaki")
    expect(() => importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: "../../x" }] })).toThrow(ConnectorError)
    const many = Array.from({ length: 51 }, (_, i) => ({ id: `18c2${String(i).padStart(12, "0")}` }))
    expect(() => importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: many })).toThrow(/up to 50/)
  })

  it("refreshes an expired access token and keeps it sealed", async () => {
    const fake = createFakeGoogle({ expiresIn: 0 })
    await connect("zaki", fake)
    const before = getConnection(db, ws.a, users.zaki)!.tokens
    await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "" }, fake.fetch)
    const after = getConnection(db, ws.a, users.zaki)!
    expect(after.tokens).not.toBe(before)
    expect(after.tokens).not.toMatch(/fake-/)
    expect(fake.calls.filter((c) => c.body?.includes("grant_type=refresh_token"))).toHaveLength(1)
  })

  it("asks to reconnect when Google revoked the grant", async () => {
    const fake = createFakeGoogle({ expiresIn: 0 })
    await connect("zaki", fake)
    fake.state.revoked = true
    await expect(searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "" }, fake.fetch)).rejects.toMatchObject({ code: "reauth" })
    expect(getConnection(db, ws.a, users.zaki)!.status).toBe("reauth")
    expect(() => importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })).toThrow(/Connect again/)
  })

  it("disconnect revokes at Google and deletes locally; a queued import then fails without content in the error", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })
    expect(await disconnect(db, ws.a, users.zaki, fake.fetch)).toEqual({ removed: true, revoked: true })
    expect(fake.revokedTokens).toEqual(["fake-refresh-1"])
    expect(getConnection(db, ws.a, users.zaki)).toBeUndefined()
    await drain(fake)
    const doc = docBySource(`gmail:${TICKET}`)[0]!
    expect(doc.status).toBe("failed")
    expect(doc.error).toBe("the Gmail connection used for this import was removed")

    // After connecting again, search flags it as failed and importing again retries it.
    await connect("zaki", fake)
    const found = await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "from:garuda" }, fake.fetch)
    expect(found.messages[0]).toMatchObject({ id: TICKET, imported: true, importFailed: true })
    expect(importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })).toMatchObject({ queued: 1, duplicates: 0 })
    await drain(fake)
    expect(docBySource(`gmail:${TICKET}`)).toEqual([expect.objectContaining({ id: doc.id, status: "done", error: null })])
  })

  it("a reconnect doesn't strand imports queued with the old connection", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: KICKOFF }] })
    await connect("zaki", fake)
    await drain(fake)
    expect(docBySource(`gmail:${KICKOFF}`)[0]).toMatchObject({ status: "done" })
  })
})

// --- isolation ---------------------------------------------------------------------------------------

describe("isolation", () => {
  it("only the connecting user can use a connection; admins see that it exists, not the mail or address", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    for (const who of ["eva", "budi"] as const) {
      await expect(searchGmail(db, { workspaceId: ws.a, userId: users[who], container: "inbox", q: "" }, fake.fetch)).rejects.toMatchObject({ code: "not_connected" })
      expect(() => importGmail(db, { workspaceId: ws.a, userId: users[who], container: "inbox", messages: [{ id: TICKET }] })).toThrow(/Connect Gmail first/)
    }
    const seenByEva = listConnections(db, ws.a, users.eva)
    expect(seenByEva).toEqual([expect.objectContaining({ username: "zaki", mine: false, accountEmail: null, status: "active" })])
    expect(JSON.stringify(seenByEva)).not.toMatch(/tokens|v1\.|zaki@example\.com/)
    expect(listConnections(db, ws.a, users.zaki)[0]).toMatchObject({ mine: true, accountEmail: "zaki@example.com" })
  })

  it("never crosses workspaces", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    await expect(searchGmail(db, { workspaceId: ws.b, userId: users.mallory, container: "inbox", q: "" }, fake.fetch)).rejects.toMatchObject({ code: "not_connected" })
    // Zaki isn't in B, and his A connection doesn't follow him there.
    await expect(searchGmail(db, { workspaceId: ws.b, userId: users.zaki, container: "inbox", q: "" }, fake.fetch)).rejects.toMatchObject({ code: "not_connected" })
    expect(listConnections(db, ws.b, users.mallory)).toEqual([])

    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })
    expect(listGmailImports(db, ws.b, "inbox", users.mallory)).toEqual([])

    // A document in B that points at A's connection must not read A's mail.
    const conn = getConnection(db, ws.a, users.zaki)!
    db.insert(documents)
      .values({
        id: "doc_forged",
        workspaceId: ws.b,
        container: "inbox",
        sourceType: "email",
        metadata: { connector: "gmail", messageId: TICKET, connectionId: conn.id, userId: users.zaki },
      })
      .run()
    const forged = db.select().from(documents).where(eq(documents.id, "doc_forged")).get()!
    await expect(resolveEmail(db, forged, fake.fetch)).rejects.toThrow(/was removed/)
    expect(fake.calls.filter((c) => c.url.includes(`/messages/${TICKET}`))).toEqual([])
  })

  it("a queued import never falls back to someone else's grant or another mailbox", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })
    await disconnect(db, ws.a, users.zaki, fake.fetch)
    // Eva is connected in the same workspace; Zaki's import must not use her grant.
    await connect("eva", fake)
    // Zaki reconnects with a different Google account.
    await connect("zaki", createFakeGoogle({ email: "zaki.other@example.com" }))
    const calls = fake.calls.length
    await drain(fake)
    const doc = docBySource(`gmail:${TICKET}`)[0]!
    expect(doc).toMatchObject({ status: "failed", error: "the Gmail connection used for this import was removed" })
    expect(fake.calls.slice(calls).filter((c) => c.url.includes("/messages/"))).toEqual([])
  })

  it("only the importer may retry an email that still has to be read from Gmail", async () => {
    const fake = createFakeGoogle()
    await connect("zaki", fake)
    importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }] })
    const doc = docBySource(`gmail:${TICKET}`)[0]!
    expect(mayRetry(doc, users.zaki)).toBe(true)
    expect(mayRetry(doc, users.eva)).toBe(false)
    expect(listGmailImports(db, ws.a, "inbox", users.eva)[0]).toMatchObject({ canRetry: false })
    await drain(fake)
    // Once its text is stored, a retry doesn't touch Gmail, so anyone may.
    expect(mayRetry(docBySource(`gmail:${TICKET}`)[0]!, users.eva)).toBe(true)
    expect(mayRetry(docBySource(`gmail:${TICKET}:2`)[0]!, users.eva)).toBe(true)
  })

  it("drops a member's connection when they leave the workspace", async () => {
    await connect("budi")
    expect(getConnection(db, ws.a, users.budi)).toBeDefined()
    const m = db.select().from(member).where(eq(member.userId, users.budi)).get()!
    db.delete(member).where(eq(member.id, m.id)).run()
    expect(getConnection(db, ws.a, users.budi)).toBeUndefined()
    addMember(db, { workspaceId: ws.a, userId: users.budi, role: "member" })
  })

  it("a token from one row can't be opened as another's", async () => {
    await connect("zaki")
    await connect("eva")
    const z = getConnection(db, ws.a, users.zaki)!
    const e = getConnection(db, ws.a, users.eva)!
    await expect(accessToken(db, { ...e, tokens: z.tokens }, createFakeGoogle().fetch)).rejects.toThrow(SealError)
  })
})

// --- REST ----------------------------------------------------------------------------------------

describe("POST /v1/connectors/gmail/import", () => {
  const post = (key: string, body: unknown) =>
    handlers(ImportRoute).POST!({
      request: new Request(`${ORIGIN}/v1/connectors/gmail/import`, {
        method: "POST",
        headers: { authorization: `Bearer ${key}`, "content-type": "application/json" },
        body: JSON.stringify(body),
      }),
      params: {},
    })

  it("imports with the key creator's connection, once", async () => {
    await connect("zaki")
    const key = createApiKey(db, { workspaceId: ws.a, name: "zaki agent", createdBy: users.zaki }).key
    const res = await post(key, { message_id: TICKET, container: "inbox" })
    expect(res.status).toBe(202)
    expect(await res.json()).toMatchObject({ status: "queued", sourceId: `gmail:${TICKET}`, duplicate: false })
    const dup = await post(key, { message_id: TICKET, container: "inbox" })
    expect(dup.status).toBe(200)
    expect(await dup.json()).toMatchObject({ duplicate: true })
  })

  it("refuses keys whose creator has no connection, keys with no creator, and other workspaces", async () => {
    await connect("zaki")
    const evaKey = createApiKey(db, { workspaceId: ws.a, name: "eva", createdBy: users.eva }).key
    expect((await post(evaKey, { message_id: TICKET })).status).toBe(409)
    const legacy = createApiKey(db, { workspaceId: ws.a, name: "legacy" }).key
    expect((await post(legacy, { message_id: TICKET })).status).toBe(403)
    const bKey = createApiKey(db, { workspaceId: ws.b, name: "b", createdBy: users.mallory }).key
    expect((await post(bKey, { message_id: TICKET })).status).toBe(409)
    expect((await post(evaKey, { message_id: "../x" })).status).toBe(400)
    expect(db.select().from(documents).all()).toEqual([])
  })
})

// --- logs ----------------------------------------------------------------------------------------

describe("logs", () => {
  it("carry no mail content or tokens through connect, search, import, failure and disconnect", async () => {
    const lines: string[] = []
    const spies = (["log", "info", "warn", "error", "debug"] as const).map((level) =>
      vi.spyOn(console, level).mockImplementation((...args: unknown[]) => {
        lines.push(args.map((a) => (a instanceof Error ? `${a.message} ${a.stack}` : typeof a === "string" ? a : JSON.stringify(a))).join(" "))
      }),
    )
    try {
      const fake = createFakeGoogle()
      await connect("zaki", fake)
      await searchGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", q: "Banjarbaru" }, fake.fetch)
      importGmail(db, { workspaceId: ws.a, userId: users.zaki, container: "inbox", messages: [{ id: TICKET }, { id: "18c2ffffffff0000" }] })
      await drain(fake, (m) => console.log(`[recall:jobs] ${m}`))
      // A failing token exchange logs its status only.
      const broken = createFakeGoogle()
      const failing = { ...broken, fetch: (async () => Response.json({ error: "server_error", access_token: "fake-access-leak" }, { status: 500 })) as typeof fetch }
      await connect("zaki", failing)
      await disconnect(db, ws.a, users.zaki, fake.fetch)
    } finally {
      for (const s of spies) s.mockRestore()
    }
    expect(lines.some((l) => l.includes("[recall:jobs]"))).toBe(true)
    const all = lines.join("\n")
    for (const word of [...SECRET_WORDS, "fake-access-leak"]) expect(all).not.toContain(word)
  })
})
