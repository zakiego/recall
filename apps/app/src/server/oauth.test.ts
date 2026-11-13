// The whole claude.ai connector flow against the real Better Auth handler:
// discovery, dynamic registration, login, consent, code exchange, refresh, and
// the access token reaching /mcp for the right workspace only.
import { createHash, randomBytes } from "node:crypto"
import { oauthProviderAuthServerMetadata } from "@better-auth/oauth-provider"
import { beforeEach, describe, expect, it } from "vitest"
import { authHandler, getAuth } from "./better-auth"
import { openDb, type DB } from "./db"
import { handleMcp } from "./mcp"
import { handOffSignedQuery, mcpCaller, mcpResource, mcpUnauthorized, protectedResourceMetadata, verifyOAuthAccessToken } from "./oauth"
import { addMember, createUserWithPassword, createWorkspace } from "./users"

const ORIGIN = "http://localhost:4800"
const REDIRECT = "https://claude.ai/api/mcp/auth_callback"
const PW = "a long enough password"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

function req(path: string, init: { body?: unknown; form?: Record<string, string>; headers?: Record<string, string> } = {}) {
  const headers: Record<string, string> = { "x-forwarded-for": "203.0.113.7", ...init.headers }
  let body: string | undefined
  if (init.form) {
    headers["content-type"] = "application/x-www-form-urlencoded"
    body = new URLSearchParams(init.form).toString()
  } else if (init.body !== undefined) {
    headers["content-type"] = "application/json"
    body = JSON.stringify(init.body)
  }
  return new Request(`${ORIGIN}${path}`, { method: body === undefined ? "GET" : "POST", headers, body })
}

function cookieHeader(res: Response) {
  return res.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ")
}

async function json(res: Response) {
  const text = await res.text()
  try {
    return JSON.parse(text)
  } catch {
    throw new Error(`not JSON (${res.status}): ${text.slice(0, 300)}`)
  }
}

// Sets up two workspaces for one user and returns a signed-in cookie.
async function signedInUser() {
  const { id: userId } = await createUserWithPassword(db, { username: "zaki", password: PW })
  const home = createWorkspace(db, { name: "Home", id: "ws_home" })
  const work = createWorkspace(db, { name: "Work", id: "ws_work" })
  addMember(db, { workspaceId: home.id, userId, role: "owner" })
  addMember(db, { workspaceId: work.id, userId, role: "member" })
  const res = await authHandler(db)(req("/api/auth/sign-in/username", { body: { username: "zaki", password: PW }, headers: { origin: ORIGIN } }))
  expect(res.status).toBe(200)
  return { userId, cookie: cookieHeader(res) }
}

async function register() {
  const res = await authHandler(db)(
    req("/api/auth/oauth2/register", {
      body: {
        client_name: "Claude",
        redirect_uris: [REDIRECT],
        grant_types: ["authorization_code", "refresh_token"],
        response_types: ["code"],
        token_endpoint_auth_method: "none",
        scope: "claudeai",
      },
    }),
  )
  const body = await json(res)
  expect(res.status, JSON.stringify(body)).toBeLessThan(300)
  return body as { client_id: string }
}

function pkce() {
  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  return { verifier, challenge }
}

function authorizeUrl(clientId: string, challenge: string, scope = "claudeai offline_access") {
  const q = new URLSearchParams({
    response_type: "code",
    client_id: clientId,
    redirect_uri: REDIRECT,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: "xyz",
    scope,
    resource: mcpResource(),
  })
  return `/api/auth/oauth2/authorize?${q}`
}

// Runs authorize → consent and returns the code from the redirect.
async function approve(clientId: string, cookie: string, challenge: string) {
  const auth = authHandler(db)
  const first = await auth(req(authorizeUrl(clientId, challenge), { headers: { cookie } }))
  expect(first.status, await first.clone().text()).toBe(302)
  const consentUrl = new URL(first.headers.get("location")!, ORIGIN)
  expect(consentUrl.pathname).toBe("/app/oauth/approve")
  const res = await auth(
    req("/api/auth/oauth2/consent", { body: { accept: true, oauth_query: consentUrl.search.slice(1) }, headers: { cookie, origin: ORIGIN } }),
  )
  const body = await json(res)
  expect(res.status, JSON.stringify(body)).toBe(200)
  const back = new URL(body.url)
  expect(`${back.origin}${back.pathname}`).toBe(REDIRECT)
  expect(back.searchParams.get("state")).toBe("xyz")
  return back.searchParams.get("code")!
}

async function exchange(form: Record<string, string>) {
  const res = await authHandler(db)(req("/api/auth/oauth2/token", { form }))
  const body = await json(res)
  return { status: res.status, body }
}

async function toolsList(token: string) {
  const caller = mcpCaller(db, token)
  if (!caller) return null
  const res = await handleMcp(
    db,
    caller.workspaceId,
    new Request(`${ORIGIN}/mcp`, {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    }),
    { keyId: caller.keyId },
  )
  return { workspaceId: caller.workspaceId, status: res.status }
}

describe("discovery", () => {
  it("serves protected resource metadata that points at the auth server", () => {
    expect(protectedResourceMetadata(ORIGIN)).toMatchObject({
      resource: `${ORIGIN}/mcp`,
      authorization_servers: [`${ORIGIN}/api/auth`],
    })
  })

  it("answers /mcp without credentials with a resource_metadata challenge", () => {
    const res = mcpUnauthorized()
    expect(res.status).toBe(401)
    expect(res.headers.get("www-authenticate")).toContain(`resource_metadata="${ORIGIN}/.well-known/oauth-protected-resource/mcp"`)
  })

  it("serves auth server metadata with registration and PKCE", async () => {
    const res = await oauthProviderAuthServerMetadata(getAuth(db))(req("/.well-known/oauth-authorization-server"))
    const meta = await json(res)
    expect(meta).toMatchObject({
      issuer: `${ORIGIN}/api/auth`,
      authorization_endpoint: `${ORIGIN}/api/auth/oauth2/authorize`,
      token_endpoint: `${ORIGIN}/api/auth/oauth2/token`,
      registration_endpoint: `${ORIGIN}/api/auth/oauth2/register`,
      code_challenge_methods_supported: ["S256"],
    })
  })
})

describe("claude.ai connector flow", () => {
  it("registers, signs in, consents, and reaches /mcp in the chosen workspace", async () => {
    const { cookie } = await signedInUser()
    const { client_id } = await register()
    const { verifier, challenge } = pkce()
    const code = await approve(client_id, cookie, challenge)

    const { status, body } = await exchange({
      grant_type: "authorization_code",
      code,
      redirect_uri: REDIRECT,
      client_id,
      code_verifier: verifier,
      resource: mcpResource(),
    })
    expect(status, JSON.stringify(body)).toBe(200)
    expect(body.access_token).toMatch(/^rco_/)
    expect(body.refresh_token).toMatch(/^rcr_/)

    // The session landed in Home (first workspace), so the token is for Home.
    expect(await toolsList(body.access_token)).toEqual({ workspaceId: "ws_home", status: 200 })

    // Refresh gives a new access token for the same workspace.
    const refreshed = await exchange({ grant_type: "refresh_token", refresh_token: body.refresh_token, client_id, resource: mcpResource() })
    expect(refreshed.status, JSON.stringify(refreshed.body)).toBe(200)
    expect(verifyOAuthAccessToken(db, refreshed.body.access_token)?.workspaceId).toBe("ws_home")
  })

  it("ties the token to the workspace active at consent", async () => {
    const { cookie } = await signedInUser()
    db.$client.prepare("UPDATE session SET active_organization_id = 'ws_work'").run()
    const { client_id } = await register()
    const { verifier, challenge } = pkce()
    const code = await approve(client_id, cookie, challenge)
    const { body } = await exchange({ grant_type: "authorization_code", code, redirect_uri: REDIRECT, client_id, code_verifier: verifier, resource: mcpResource() })
    expect(verifyOAuthAccessToken(db, body.access_token)?.workspaceId).toBe("ws_work")
  })

  it("sends a signed-out user to the login page", async () => {
    const { client_id } = await register()
    const res = await authHandler(db)(req(authorizeUrl(client_id, pkce().challenge)))
    expect(res.status).toBe(302)
    const login = new URL(res.headers.get("location")!, ORIGIN)
    expect(login.pathname).toBe("/app/oauth/sign-in")
    // The hand-off keeps the signed query in a cookie and opens the login page.
    const handoff = handOffSignedQuery(req(`${login.pathname}${login.search}`), "/app/login?oauth=1")
    expect(handoff.headers.get("location")).toBe("/app/login?oauth=1")
    const cookie = handoff.headers.get("set-cookie")!
    expect(decodeURIComponent(cookie.split(";")[0].split("=").slice(1).join("="))).toBe(login.search.slice(1))
    expect(cookie).toContain("Path=/app")
  })

  it("rejects a wrong PKCE verifier", async () => {
    const { cookie } = await signedInUser()
    const { client_id } = await register()
    const code = await approve(client_id, cookie, pkce().challenge)
    const { status } = await exchange({ grant_type: "authorization_code", code, redirect_uri: REDIRECT, client_id, code_verifier: pkce().verifier })
    expect(status).toBeGreaterThanOrEqual(400)
  })

  it("stops working when the user leaves the workspace, the token expires, or the client is disabled", async () => {
    const { cookie, userId } = await signedInUser()
    const { client_id } = await register()
    const { verifier, challenge } = pkce()
    const code = await approve(client_id, cookie, challenge)
    const { body } = await exchange({ grant_type: "authorization_code", code, redirect_uri: REDIRECT, client_id, code_verifier: verifier, resource: mcpResource() })
    const token = body.access_token as string
    expect(verifyOAuthAccessToken(db, token)).not.toBeNull()

    expect(verifyOAuthAccessToken(db, token, Date.now() + 2 * 60 * 60 * 1000)).toBeNull()

    db.$client.prepare("UPDATE oauth_client SET disabled = 1 WHERE client_id = ?").run(client_id)
    expect(verifyOAuthAccessToken(db, token)).toBeNull()
    db.$client.prepare("UPDATE oauth_client SET disabled = 0 WHERE client_id = ?").run(client_id)

    db.$client.prepare("DELETE FROM member WHERE user_id = ? AND organization_id = 'ws_home'").run(userId)
    expect(verifyOAuthAccessToken(db, token)).toBeNull()
  })

  it("ignores garbage and API-key-shaped tokens", () => {
    expect(verifyOAuthAccessToken(db, null)).toBeNull()
    expect(verifyOAuthAccessToken(db, "rco_nope")).toBeNull()
    expect(verifyOAuthAccessToken(db, "rcl_abc")).toBeNull()
    expect(mcpCaller(db, "rco_nope")).toBeNull()
  })
})
