// OAuth golden: runs scripted claude.ai-style connector flows through the
// real Better Auth handler (Recall's authHandler, the root metadata route
// and the /app/oauth hand-offs) and records every request, response and the
// oauth_* rows after each step. testdata/auth.json.
//
// The clock is fixed (Date and Date.now) and moves only on "advance" steps.
// Values Better Auth makes at random (client ids, codes, tokens, row ids,
// signatures) stay raw here: the Go test pairs them with its own by
// position and translates before comparing. Signing in is each build's own
// (the dashboard endpoints differ by design), so a "signIn" step records
// only a response that carries an OAuth continuation.
//
// Run from apps/app: npx tsx ../server/testdata/gen/auth-goldens.ts
import { createHash } from "node:crypto"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const NOW = Date.UTC(2026, 10, 12, 3, 0, 0)
let clock = NOW
const RealDate = Date
class FakeDate extends RealDate {
  constructor(...args: unknown[]) {
    if (args.length === 0) super(clock)
    else super(...(args as [number]))
  }
  static now() {
    return clock
  }
}
globalThis.Date = FakeDate as DateConstructor

const ORIGIN = "http://localhost:4800"
const SECRET = "auth-golden-session-secret-0123456789abcdef"
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "recall-auth-golden-"))
process.env.RECALL_URL = ORIGIN
process.env.SESSION_SECRET = SECRET
process.env.DATA_DIR = tmp
process.env.RECALL_JOBS = "off"
globalThis.fetch = (async (input: unknown) => {
  throw new Error(`network access refused: ${String(input)}`)
}) as typeof fetch

const { openDb } = await import("../../../app/src/server/db/index.ts")
const { authHandler, getAuth } = await import("../../../app/src/server/better-auth.ts")
const { oauthProviderAuthServerMetadata } = await import("../../../app/node_modules/@better-auth/oauth-provider/dist/index.mjs")
const { handOffSignedQuery, mcpCaller, OAUTH_LOGIN_PAGE, OAUTH_CONSENT_PAGE } = await import("../../../app/src/server/oauth.ts")
const { createUserWithPassword, createWorkspace, addMember } = await import("../../../app/src/server/users.ts")

const db = openDb(":memory:")
const auth = authHandler(db)
const PW = "a long enough password"
const REDIRECT = "https://claude.ai/api/mcp/auth_callback"
const RESOURCE = `${ORIGIN}/mcp`
const VERIFIER = "golden-pkce-verifier-0123456789-abcdefghijklmnopqrstuvwxyz"
const CHALLENGE = createHash("sha256").update(VERIFIER).digest("base64url")
const VERIFIER2 = "golden-pkce-verifier-two-0123456789-abcdefghijklmnopqrstu"
const CHALLENGE2 = createHash("sha256").update(VERIFIER2).digest("base64url")

const setup = {
  users: [
    { username: "zane", password: PW },
    { username: "loner", password: PW },
  ],
  workspaces: [
    { id: "ws_home", name: "Home" },
    { id: "ws_work", name: "Work" },
  ],
  members: [
    { workspace: "ws_home", user: "zane", role: "owner" },
    { workspace: "ws_work", user: "zane", role: "member" },
  ],
}
const userIds: Record<string, string> = {}
for (const u of setup.users) userIds[u.username] = (await createUserWithPassword(db, u)).id
for (const w of setup.workspaces) createWorkspace(db, w)
for (const m of setup.members) addMember(db, { workspaceId: m.workspace, userId: userIds[m.user], role: m.role as "owner" })

const TABLES = ["oauth_resource", "oauth_client", "oauth_client_resource", "oauth_consent", "oauth_access_token", "oauth_refresh_token", "verification"]
// The oauth rows after a step, or undefined when they didn't change.
let lastRows = ""
function rows() {
  const out: Record<string, unknown[]> = {}
  for (const t of TABLES) {
    const where = t === "verification" ? " WHERE value LIKE '%authorization_code%'" : ""
    out[t] = db.$client.prepare(`SELECT * FROM ${t}${where} ORDER BY rowid`).all()
  }
  const text = JSON.stringify(out)
  if (text === lastRows) return undefined
  lastRows = text
  return out
}

const KEEP = ["content-type", "location", "cache-control", "pragma", "www-authenticate", "x-retry-after", "access-control-allow-origin", "allow"]
type Req = { method: string; path: string; headers: Record<string, string>; body?: string; jar?: string }
type Step = { name: string; kind: string; req?: Req; user?: string; jar?: string; oauthQuery?: string; token?: string; sql?: string; ms?: number; res?: unknown; rows?: unknown }
const steps: Step[] = []
const jars: Record<string, string> = {}
let ipN = 0

async function record(res: Response, setCookie: boolean) {
  const headers: Record<string, string> = {}
  for (const k of KEEP) {
    const v = res.headers.get(k)
    if (v !== null) headers[k] = v
  }
  if (setCookie) headers["set-cookie"] = res.headers.getSetCookie().join("\n")
  const text = await res.text()
  let body: unknown = text
  let json = false
  try {
    body = JSON.parse(text)
    json = true
  } catch {}
  return { status: res.status, headers, body, json, empty: text === "" }
}

function cookieOf(res: Response) {
  return res.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .filter((c) => !c.endsWith("="))
    .join("; ")
}

type HttpOpts = { json?: unknown; form?: Record<string, string | string[]>; headers?: Record<string, string>; jar?: string; ip?: string }
async function http(name: string, method: string, p: string, o: HttpOpts = {}) {
  const headers: Record<string, string> = { "x-forwarded-for": o.ip ?? `203.0.113.${++ipN}`, ...o.headers }
  let body: string | undefined
  if (o.json !== undefined) {
    headers["content-type"] ??= "application/json"
    body = typeof o.json === "string" ? o.json : JSON.stringify(o.json)
  } else if (o.form) {
    headers["content-type"] ??= "application/x-www-form-urlencoded"
    const f = new URLSearchParams()
    for (const [k, v] of Object.entries(o.form)) for (const x of Array.isArray(v) ? v : [v]) f.append(k, x)
    body = f.toString()
  }
  const sent = { ...headers }
  if (o.jar) headers.cookie = jars[o.jar]
  const request = new Request(`${ORIGIN}${p}`, { method, headers, body })
  let res: Response
  const pathOnly = p.split("?")[0]
  if (pathOnly.startsWith("/.well-known/oauth-authorization-server")) {
    const splat = pathOnly.slice("/.well-known/oauth-authorization-server".length).replace(/^\//, "")
    res = !splat || splat === "api/auth" ? await oauthProviderAuthServerMetadata(getAuth(db), { headers: { "access-control-allow-origin": "*" } })(request) : new Response("Not found", { status: 404 })
  } else if (pathOnly === "/app/oauth/sign-in") res = handOffSignedQuery(request, OAUTH_LOGIN_PAGE)
  else if (pathOnly === "/app/oauth/approve") res = handOffSignedQuery(request, OAUTH_CONSENT_PAGE)
  else res = await auth(request)
  const out = await record(res, pathOnly.startsWith("/app/oauth/"))
  steps.push({ name, kind: "http", req: { method, path: p, headers: sent, body, jar: o.jar }, res: out, rows: rows() })
  return out
}

async function signIn(name: string, user: string, jar: string, oauthQuery?: string) {
  const res = await auth(
    new Request(`${ORIGIN}/api/auth/sign-in/username`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: ORIGIN, "x-forwarded-for": `198.51.100.${++ipN}` },
      body: JSON.stringify({ username: user, password: PW, ...(oauthQuery ? { oauth_query: oauthQuery } : {}) }),
    }),
  )
  if (res.status !== 200) throw new Error(`${name}: sign-in ${res.status} ${await res.text()}`)
  jars[jar] = cookieOf(res)
  const out = await record(res, false)
  steps.push({ name, kind: "signIn", user, jar, oauthQuery, res: oauthQuery ? out : undefined, rows: rows() })
  return out
}

function caller(name: string, token: string) {
  const c = mcpCaller(db, token)
  steps.push({ name, kind: "caller", token, res: c })
  return c
}

function sql(name: string, statement: string) {
  db.$client.prepare(statement).run()
  steps.push({ name, kind: "sql", sql: statement, rows: rows() })
}

function advance(name: string, ms: number) {
  clock += ms
  steps.push({ name, kind: "advance", ms })
}

const q = (o: Record<string, string>) => new URLSearchParams(o).toString()
const authorizePath = (clientId: string, extra: Record<string, string> = {}, challenge = CHALLENGE) =>
  `/api/auth/oauth2/authorize?${q({ response_type: "code", client_id: clientId, redirect_uri: REDIRECT, code_challenge: challenge, code_challenge_method: "S256", state: "xyz", scope: "claudeai offline_access", resource: RESOURCE, ...extra })}`
const locationQuery = (r: { headers: Record<string, string> }) => r.headers.location.split("?").slice(1).join("?")
const codeOf = (url: string) => new URL(url).searchParams.get("code")!
const body = (r: { body: unknown }) => r.body as Record<string, string>

// --- discovery ---------------------------------------------------------------------
await http("metadata at the root", "GET", "/.well-known/oauth-authorization-server")
await http("metadata, path-inserted form", "GET", "/.well-known/oauth-authorization-server/api/auth")
await http("metadata, other splat", "GET", "/.well-known/oauth-authorization-server/nope")
await http("metadata inside /api/auth", "GET", "/api/auth/.well-known/oauth-authorization-server")
await http("metadata inside /api/auth, POST", "POST", "/api/auth/.well-known/oauth-authorization-server")

// --- registration -----------------------------------------------------------------
const claude = body(
  await http("register a public client (claude.ai)", "POST", "/api/auth/oauth2/register", {
    json: { client_name: "Claude", redirect_uris: [REDIRECT], grant_types: ["authorization_code", "refresh_token"], response_types: ["code"], token_endpoint_auth_method: "none", scope: "claudeai" },
  }),
)
const conf = body(
  await http("register a confidential client", "POST", "/api/auth/oauth2/register", {
    json: { client_name: "Server app", redirect_uris: ["https://app.example.com/cb"], token_endpoint_auth_method: "client_secret_post", resources: [RESOURCE], contacts: ["ops@example.com"], client_uri: "https://app.example.com", logo_uri: "https://app.example.com/logo.png", unknown_field: 1 },
  }),
)
await http("register defaults (basic auth, no scope)", "POST", "/api/auth/oauth2/register", { json: { redirect_uris: ["https://basic.example.com/cb"] } })
await http("register a native client (cursor://)", "POST", "/api/auth/oauth2/register", { json: { client_name: "Cursor", redirect_uris: ["cursor://anysphere.cursor-mcp/oauth/callback"], token_endpoint_auth_method: "none" } })
await http("register a loopback client", "POST", "/api/auth/oauth2/register", { json: { client_name: "CLI", redirect_uris: ["http://127.0.0.1:33418/callback"], token_endpoint_auth_method: "none" } })
for (const [name, json, ip] of [
  ["register: http redirect", { redirect_uris: ["http://evil.example/cb"] }],
  ["register: no redirect", { client_name: "x", token_endpoint_auth_method: "none" }],
  ["register: only client_credentials", { redirect_uris: [REDIRECT], grant_types: ["client_credentials"] }],
  ["register: unknown scope", { redirect_uris: [REDIRECT], scope: "admin" }],
  ["register: javascript redirect", { redirect_uris: ["javascript:alert(1)"] }],
  ["register: fragment redirect", { redirect_uris: ["https://a.example/cb#x"] }],
  ["register: other resource", { redirect_uris: [REDIRECT], resources: ["https://other.example/mcp"] }],
  ["register: blank client_name", { redirect_uris: [REDIRECT], client_name: "  " }],
  ["register: bad auth method", { redirect_uris: [REDIRECT], token_endpoint_auth_method: "magic" }],
  ["register: redirect_uris not an array", { redirect_uris: REDIRECT }],
  ["register: empty redirect list", { redirect_uris: [] }],
  ["register: skip_consent", { redirect_uris: [REDIRECT], skip_consent: true }],
  ["register: pairwise", { redirect_uris: [REDIRECT], subject_type: "pairwise" }],
  ["register: backchannel", { redirect_uris: [REDIRECT], backchannel_logout_uri: "https://a.example/logout" }],
  ["register: web client on loopback https", { redirect_uris: ["https://localhost/cb"], application_type: "web" }],
  ["register: grant and response mismatch", { redirect_uris: [REDIRECT], grant_types: ["refresh_token"], response_types: ["code"] }],
  ["register: application_type bad", { redirect_uris: [REDIRECT], application_type: "desktop" }],
] as [string, unknown][])
  await http(name, "POST", "/api/auth/oauth2/register", { json })
await http("register: form body", "POST", "/api/auth/oauth2/register", { form: { client_name: "x" } })
await http("register: bad JSON", "POST", "/api/auth/oauth2/register", { json: "{nope" })
await http("register: with a bearer token", "POST", "/api/auth/oauth2/register", { json: { redirect_uris: [REDIRECT] }, headers: { authorization: "Bearer abc" } })
await http("register: malformed bearer", "POST", "/api/auth/oauth2/register", { json: { redirect_uris: [REDIRECT] }, headers: { authorization: "Bearer a b" } })

// --- sign in and consent ------------------------------------------------------------
await signIn("zane signs in", "zane", "A")
const signedOut = await http("authorize while signed out", "GET", authorizePath(claude.client_id))
await http("hand-off to the login page", "GET", `/app/oauth/sign-in?${locationQuery(signedOut)}`)
await http("hand-off without a query", "GET", "/app/oauth/approve")
await signIn("sign in with the OAuth query", "zane", "B", locationQuery(signedOut))
const needConsent = await http("authorize needs consent", "GET", authorizePath(claude.client_id), { jar: "A" })
await http("authorize as a fetch gets JSON", "GET", authorizePath(claude.client_id), { jar: "A", headers: { accept: "application/json" } })
const consentQuery = locationQuery(needConsent)
await http("consent with a bad signature", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: true, oauth_query: consentQuery.replace(/sig=[^&]+/, "sig=AAAA") } })
await http("consent without a session", "POST", "/api/auth/oauth2/consent", { headers: { origin: ORIGIN }, json: { accept: true, oauth_query: consentQuery } })
await http("consent from another origin", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: "https://evil.example" }, json: { accept: true, oauth_query: consentQuery } })
await http("consent without an origin", "POST", "/api/auth/oauth2/consent", { jar: "A", json: { accept: true, oauth_query: consentQuery } })
await http("consent without accept", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { oauth_query: consentQuery } })
await http("consent with an extra scope", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: true, scope: "mcp", oauth_query: consentQuery } })
await http("consent denied", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: false, oauth_query: consentQuery } })
const approved = await http("consent accepted", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: true, oauth_query: consentQuery } })
const code1 = codeOf(body(approved).url)

// --- token --------------------------------------------------------------------------
const tokenForm = (code: string, extra: Record<string, string> = {}) => ({ grant_type: "authorization_code", code, redirect_uri: REDIRECT, client_id: claude.client_id, code_verifier: VERIFIER, resource: RESOURCE, ...extra })
await http("token as JSON", "POST", "/api/auth/oauth2/token", { json: tokenForm(code1) })
await http("token without grant_type", "POST", "/api/auth/oauth2/token", { form: { code: code1 } })
await http("token with an unknown grant", "POST", "/api/auth/oauth2/token", { form: { grant_type: "password" } })
const t1 = body(await http("token for the code", "POST", "/api/auth/oauth2/token", { form: tokenForm(code1) }))
caller("the access token reaches /mcp", t1.access_token)
await http("the code again", "POST", "/api/auth/oauth2/token", { form: tokenForm(code1) })
caller("a replayed code revokes its tokens", t1.access_token)

const fresh = async (name: string, jar = "A", challenge = CHALLENGE) => {
  const r = await http(name, "GET", authorizePath(claude.client_id, {}, challenge), { jar })
  return codeOf(r.headers.location)
}
await http("token: wrong verifier", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize again, consent remembered"), { code_verifier: VERIFIER2 }) })
await http("token: no redirect_uri", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: await fresh("authorize (no redirect)"), client_id: claude.client_id, code_verifier: VERIFIER } })
await http("token: other redirect_uri", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (other redirect)"), { redirect_uri: "https://claude.ai/other" }) })
await http("token: no verifier", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: await fresh("authorize (no verifier)"), client_id: claude.client_id, redirect_uri: REDIRECT } })
await http("token: other client", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (other client)"), { client_id: conf.client_id }) })
await http("token: no client_id", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: "x" } })
await http("token: unknown code", "POST", "/api/auth/oauth2/token", { form: tokenForm("nonexistentcode") })
await http("token: other resource", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (other resource)"), { resource: "https://other.example/mcp" }) })
await http("token: basic auth for a public client", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: "x" }, headers: { authorization: `Basic ${Buffer.from(`${claude.client_id}:nope`).toString("base64")}` } })
await http("token: odd auth scheme", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: "x" }, headers: { authorization: "Digest abc" } })
await http("token: repeated client_id", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: "x", client_id: [claude.client_id, conf.client_id] } })
const t2 = body(await http("token, second grant", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (second grant)")) }))

// --- refresh ------------------------------------------------------------------------
const t3 = body(await http("refresh", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t2.refresh_token, client_id: claude.client_id, resource: RESOURCE } }))
caller("the refreshed token works", t3.access_token)
caller("the first access token still works", t2.access_token)
await http("refresh: narrower scope", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: "rcr_doesnotexistdoesnotexistdoesno", client_id: claude.client_id } })
await http("refresh: scope not granted", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t3.refresh_token, client_id: claude.client_id, scope: "mcp" } })
await http("refresh: no prefix", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: "abc", client_id: claude.client_id } })
await http("refresh: reused token wipes the family", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t2.refresh_token, client_id: claude.client_id } })
caller("after the wipe", t3.access_token)

// --- revoke, introspect, userinfo ------------------------------------------------------
const t4 = body(await http("token for revoking", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (revoke)")) }))
await http("userinfo with an access token", "GET", "/api/auth/oauth2/userinfo", { headers: { authorization: `Bearer ${t4.access_token}` } })
await http("userinfo with garbage", "GET", "/api/auth/oauth2/userinfo", { headers: { authorization: "Bearer nope" } })
await http("introspect as a public client", "POST", "/api/auth/oauth2/introspect", { form: { token: t4.access_token, client_id: claude.client_id } })
await http("revoke the access token", "POST", "/api/auth/oauth2/revoke", { form: { token: t4.access_token, client_id: claude.client_id } })
caller("the revoked token", t4.access_token)
await http("revoke the refresh token", "POST", "/api/auth/oauth2/revoke", { form: { token: t4.refresh_token, client_id: claude.client_id, token_type_hint: "refresh_token" } })
await http("revoke it again", "POST", "/api/auth/oauth2/revoke", { form: { token: t4.refresh_token, client_id: claude.client_id } })
await http("revoke garbage", "POST", "/api/auth/oauth2/revoke", { form: { token: "garbage", client_id: claude.client_id } })
await http("revoke garbage as an access token", "POST", "/api/auth/oauth2/revoke", { form: { token: "garbage", client_id: claude.client_id, token_type_hint: "access_token" } })
await http("revoke without a client", "POST", "/api/auth/oauth2/revoke", { form: { token: "garbage" } })
await http("revoke without a token", "POST", "/api/auth/oauth2/revoke", { form: { client_id: claude.client_id } })

// A confidential client: consent, token with its secret, introspection.
const confAuthorize = `/api/auth/oauth2/authorize?${q({ response_type: "code", client_id: conf.client_id, redirect_uri: "https://app.example.com/cb", code_challenge: CHALLENGE, code_challenge_method: "S256", scope: "mcp offline_access" })}`
const confConsent = await http("confidential: authorize", "GET", confAuthorize, { jar: "A" })
const confApproved = await http("confidential: consent", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: true, oauth_query: locationQuery(confConsent) } })
await http("confidential: token without the secret", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: codeOf(body(confApproved).url), redirect_uri: "https://app.example.com/cb", client_id: conf.client_id, code_verifier: VERIFIER } })
const confCode = codeOf((await http("confidential: authorize again", "GET", confAuthorize, { jar: "A" })).headers.location)
await http("confidential: wrong secret", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: confCode, redirect_uri: "https://app.example.com/cb", client_id: conf.client_id, client_secret: "wrong", code_verifier: VERIFIER } })
const confCode2 = codeOf((await http("confidential: authorize a third time", "GET", confAuthorize, { jar: "A" })).headers.location)
const ct = body(await http("confidential: token with its secret", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: confCode2, redirect_uri: "https://app.example.com/cb", client_id: conf.client_id, client_secret: conf.client_secret, code_verifier: VERIFIER } }))
const t5 = body(await http("token to introspect", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (introspect)")) }))
await http("introspect claude's access token", "POST", "/api/auth/oauth2/introspect", { form: { token: t5.access_token, client_id: conf.client_id, client_secret: conf.client_secret } })
await http("introspect its own refresh token", "POST", "/api/auth/oauth2/introspect", { form: { token: ct.refresh_token, client_id: conf.client_id, client_secret: conf.client_secret } })
await http("introspect garbage", "POST", "/api/auth/oauth2/introspect", { form: { token: "garbage", client_id: conf.client_id, client_secret: conf.client_secret } })
await http("refresh with basic auth", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: ct.refresh_token }, headers: { authorization: `Basic ${Buffer.from(`${conf.client_id}:${conf.client_secret}`).toString("base64")}` } })

// --- authorize errors -----------------------------------------------------------------
await http("authorize: unknown client", "GET", authorizePath("nosuchclient"), { jar: "A" })
await http("authorize: unregistered redirect", "GET", authorizePath(claude.client_id, { redirect_uri: "https://claude.ai/other" }), { jar: "A" })
await http("authorize: no response_type", "GET", `/api/auth/oauth2/authorize?${q({ client_id: claude.client_id, redirect_uri: REDIRECT, state: "s1" })}`, { jar: "A" })
await http("authorize: bad response_type", "GET", authorizePath(claude.client_id, { response_type: "token" }), { jar: "A" })
await http("authorize: unknown scope", "GET", authorizePath(claude.client_id, { scope: "admin mcp" }), { jar: "A" })
await http("authorize: unknown resource", "GET", authorizePath(claude.client_id, { resource: "https://other.example/mcp" }), { jar: "A" })
await http("authorize: plain PKCE", "GET", authorizePath(claude.client_id, { code_challenge_method: "plain" }), { jar: "A" })
await http("authorize: no PKCE", "GET", `/api/auth/oauth2/authorize?${q({ response_type: "code", client_id: claude.client_id, redirect_uri: REDIRECT, state: "s2" })}`, { jar: "A" })
await http("authorize: prompt=none signed out", "GET", authorizePath(claude.client_id, { prompt: "none" }))
await http("authorize: prompt=consent", "GET", authorizePath(claude.client_id, { prompt: "consent" }), { jar: "A" })
await http("authorize: prompt=login", "GET", authorizePath(claude.client_id, { prompt: "login" }), { jar: "A" })
await http("authorize: select_account", "GET", authorizePath(claude.client_id, { prompt: "select_account" }), { jar: "A" })
await http("authorize: bad prompt", "GET", authorizePath(claude.client_id, { prompt: "bogus" }), { jar: "A" })
await http("authorize: max_age=0", "GET", authorizePath(claude.client_id, { max_age: "0" }), { jar: "A" })
await http("authorize: max_age satisfied", "GET", authorizePath(claude.client_id, { max_age: "3600" }), { jar: "A" })
await http("authorize: repeated state", "GET", `${authorizePath(claude.client_id)}&state=again`, { jar: "A" })
await http("authorize: request object", "GET", authorizePath(claude.client_id, { request: "eyJ" }), { jar: "A" })
await http("authorize: by POST form", "POST", "/api/auth/oauth2/authorize", { jar: "A", headers: { origin: ORIGIN }, form: Object.fromEntries(new URLSearchParams(authorizePath(claude.client_id).split("?")[1])) })

// --- workspaces -----------------------------------------------------------------------
sql("switch to Work", "UPDATE session SET active_organization_id = 'ws_work'")
const workConsent = await http("authorize in Work needs consent", "GET", authorizePath(claude.client_id), { jar: "A" })
const workApproved = await http("consent for Work", "POST", "/api/auth/oauth2/consent", { jar: "A", headers: { origin: ORIGIN }, json: { accept: true, oauth_query: locationQuery(workConsent) } })
const tw = body(await http("token for Work", "POST", "/api/auth/oauth2/token", { form: tokenForm(codeOf(body(workApproved).url)) }))
caller("the Work token", tw.access_token)
sql("leave Work", `DELETE FROM member WHERE organization_id = 'ws_work' AND user_id = '${userIds.zane}'`)
caller("after leaving Work", tw.access_token)
await signIn("a user without a workspace signs in", "loner", "C")
await http("authorize without a workspace", "GET", authorizePath(claude.client_id), { jar: "C" })

// --- expiry and disabled clients ----------------------------------------------------------
sql("back to Home", "UPDATE session SET active_organization_id = 'ws_home'")
const t6 = body(await http("token before the clock moves", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("authorize (expiry)")) }))
advance("two hours pass", 2 * 60 * 60 * 1000)
caller("an expired access token", t6.access_token)
const t7 = body(await http("refresh after expiry", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t6.refresh_token, client_id: claude.client_id } }))
caller("the new token", t7.access_token)
sql("disable claude", `UPDATE oauth_client SET disabled = 1 WHERE client_id = '${claude.client_id}'`)
caller("a disabled client's token", t7.access_token)
await http("refresh for a disabled client", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t7.refresh_token, client_id: claude.client_id } })
await http("authorize a disabled client", "GET", authorizePath(claude.client_id), { jar: "A" })
sql("enable claude", `UPDATE oauth_client SET disabled = 0 WHERE client_id = '${claude.client_id}'`)
advance("31 days pass", 31 * 24 * 60 * 60 * 1000)
await http("refresh after 31 days", "POST", "/api/auth/oauth2/token", { form: { grant_type: "refresh_token", refresh_token: t7.refresh_token, client_id: claude.client_id } })

// --- hand-over: what Better Auth wrote, for the Go server to accept -----------------------
// testdata/auth-handover.json: every row the flows need (users,
// workspaces, sessions, clients, consents, tokens, an unused code) and the
// raw values only a client holds. The Go test loads the rows into its own
// database and uses the values against its endpoints.
await signIn("zane signs in again", "zane", "D")
const handCode = codeOf((await http("hand-over: a code left unused", "GET", authorizePath(claude.client_id, {}, CHALLENGE2), { jar: "D" })).headers.location)
const handTokens = body(await http("hand-over: tokens", "POST", "/api/auth/oauth2/token", { form: tokenForm(await fresh("hand-over: authorize", "D")) }))
const handConsent = locationQuery(await http("hand-over: a signed consent query", "GET", authorizePath(claude.client_id, { prompt: "consent" }), { jar: "D" }))
const handConfCode = codeOf((await http("hand-over: confidential authorize", "GET", confAuthorize, { jar: "D" })).headers.location)
const handConf = body(await http("hand-over: confidential token", "POST", "/api/auth/oauth2/token", { form: { grant_type: "authorization_code", code: handConfCode, redirect_uri: "https://app.example.com/cb", client_id: conf.client_id, client_secret: conf.client_secret, code_verifier: VERIFIER } }))
{
  const tables: Record<string, unknown[]> = {}
  for (const t of ["user", "account", "organization", "member", "session", ...TABLES]) tables[t] = db.$client.prepare(`SELECT * FROM "${t}" ORDER BY rowid`).all()
  const handover = {
    now: clock,
    tables,
    values: {
      claudeClientId: claude.client_id,
      confClientId: conf.client_id,
      confClientSecret: conf.client_secret,
      accessToken: handTokens.access_token,
      refreshToken: handTokens.refresh_token,
      code: handCode,
      verifier: VERIFIER2,
      consentQuery: handConsent,
      confAccessToken: handConf.access_token,
      confRefreshToken: handConf.refresh_token,
      redirect: REDIRECT,
      resource: RESOURCE,
    },
  }
  fs.writeFileSync(path.resolve(import.meta.dirname, "../auth-handover.json"), JSON.stringify(handover, null, 1) + "\n")
}

// --- rate limits ------------------------------------------------------------------------
for (let i = 1; i <= 6; i++) await http(`register burst ${i}`, "POST", "/api/auth/oauth2/register", { ip: "192.0.2.77", json: { redirect_uris: [REDIRECT], token_endpoint_auth_method: "none" } })

const out = path.resolve(import.meta.dirname, "../auth.json")
fs.writeFileSync(out, JSON.stringify({ now: NOW, origin: ORIGIN, secret: SECRET, password: PW, setup, steps }, null, 1) + "\n")
console.log(`wrote ${steps.length} steps to ${out}`)
process.exit(0)
