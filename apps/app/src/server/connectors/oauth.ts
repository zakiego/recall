// GET /app/connectors/google/start and /app/connectors/google/callback.
// The OAuth state, PKCE verifier, user, workspace and return container travel
// in one sealed, short-lived cookie scoped to the callback path. The callback
// only stores tokens when the cookie opens, the state matches, and the signed-in
// user is the one who started, still a member of that workspace.
import { randomBytes, timingSafeEqual } from "node:crypto"
import { APP_BASE } from "@/lib/app-path"
import { config } from "../config"
import type { DB } from "../db"
import { MAX_CONTAINER_LEN } from "../memories"
import { checkSameOrigin, viewerFromHeaders } from "../session"
import { getRole } from "../users"
import { SealError, seal, unseal } from "./crypto"
import { connectorSecret, connectorsConfigured, saveConnection } from "./gmail"
import { authorizationUrl, CALLBACK_PATH, exchangeCode, GMAIL_SCOPE, getProfile, pkcePair, redirectUri, revokeToken, type Fetch } from "./google"

export const STATE_COOKIE = "recall.gmail_oauth"
const STATE_TTL_MS = 10 * 60_000
const STATE_CONTEXT = "gmail-oauth-state"
const COOKIE_PATH = `${APP_BASE}/connectors/google`

type StatePayload = { state: string; verifier: string; userId: string; workspaceId: string; container: string | null; exp: number }

export type ConnectOutcome = "connected" | "denied" | "failed" | "expired" | "not_configured"

function connectorsPage(outcome: ConnectOutcome | null, container: string | null) {
  const q = new URLSearchParams()
  if (outcome) q.set("gmail", outcome)
  if (container) q.set("c", container)
  const s = q.toString()
  return `${APP_BASE}/connectors${s ? `?${s}` : ""}`
}

function redirect(location: string, cookie?: string) {
  const headers = new Headers({ location, "cache-control": "no-store" })
  if (cookie) headers.append("set-cookie", cookie)
  return new Response(null, { status: 302, headers })
}

function cookie(value: string, maxAgeSeconds: number) {
  const secure = config().url.startsWith("https:") ? "; Secure" : ""
  return `${STATE_COOKIE}=${value}; Path=${COOKIE_PATH}; HttpOnly; SameSite=Lax; Max-Age=${maxAgeSeconds}${secure}`
}

const clearCookie = () => cookie("", 0)

function readCookie(request: Request) {
  for (const part of (request.headers.get("cookie") ?? "").split(";")) {
    const [k, ...v] = part.trim().split("=")
    if (k === STATE_COOKIE) return v.join("=")
  }
  return null
}

function sameString(a: string, b: string) {
  const x = Buffer.from(a)
  const y = Buffer.from(b)
  return x.length === y.length && timingSafeEqual(x, y)
}

export async function handleStart(db: DB, request: Request, now = Date.now()): Promise<Response> {
  const url = new URL(request.url)
  const raw = url.searchParams.get("c")?.trim()
  const container = raw && raw.length <= MAX_CONTAINER_LEN ? raw : null
  // A navigation from another site can't start a connect flow for you.
  try {
    checkSameOrigin(request.headers)
  } catch {
    return redirect(connectorsPage("failed", container))
  }
  const viewer = await viewerFromHeaders(db, request.headers)
  if (!viewer) return redirect(`${APP_BASE}/login?next=${encodeURIComponent("/connectors")}`)
  if (!viewer.workspace) return redirect(`${APP_BASE}/`)
  if (!connectorsConfigured()) return redirect(connectorsPage("not_configured", container))

  const { verifier, challenge } = pkcePair()
  const payload: StatePayload = {
    state: randomBytes(24).toString("base64url"),
    verifier,
    userId: viewer.user.id,
    workspaceId: viewer.workspace.id,
    container,
    exp: now + STATE_TTL_MS,
  }
  const sealed = seal(connectorSecret(), JSON.stringify(payload), STATE_CONTEXT)
  // The dev fake has no consent screen: it hands back a code straight away.
  const location = config().gmailFake
    ? `${redirectUri()}?${new URLSearchParams({ code: "fake-code", state: payload.state, scope: GMAIL_SCOPE })}`
    : authorizationUrl({ clientId: config().googleClientId, state: payload.state, challenge })
  return redirect(location, cookie(sealed, STATE_TTL_MS / 1000))
}

export async function handleCallback(db: DB, request: Request, fetchImpl: Fetch, now = Date.now()): Promise<Response> {
  const url = new URL(request.url)
  const raw = readCookie(request)
  let payload: StatePayload | null = null
  if (raw) {
    try {
      payload = JSON.parse(unseal(connectorSecret(), raw, STATE_CONTEXT)) as StatePayload
    } catch (err) {
      if (!(err instanceof SealError)) throw err
    }
  }
  const back = (outcome: ConnectOutcome) => redirect(connectorsPage(outcome, payload?.container ?? null), clearCookie())

  if (!connectorsConfigured()) return back("not_configured")
  const state = url.searchParams.get("state") ?? ""
  if (!payload || !state || !sameString(state, payload.state)) return back("failed")
  if (payload.exp < now) return back("expired")
  // Google sends ?error=access_denied when the user says no.
  if (url.searchParams.get("error")) return back("denied")

  const viewer = await viewerFromHeaders(db, request.headers)
  if (!viewer || viewer.user.id !== payload.userId) return back("failed")
  if (!getRole(db, payload.userId, payload.workspaceId)) return back("failed")

  const code = url.searchParams.get("code")
  if (!code) return back("failed")
  try {
    const tokens = await exchangeCode(fetchImpl, { code, verifier: payload.verifier, now })
    // The user can untick the Gmail box on Google's consent screen.
    if (!tokens.scope.split(" ").includes(GMAIL_SCOPE)) {
      await revokeToken(fetchImpl, tokens.refreshToken)
      return back("denied")
    }
    const profile = await getProfile(fetchImpl, tokens.accessToken)
    const { replacedRefreshToken } = saveConnection(db, {
      workspaceId: payload.workspaceId,
      userId: payload.userId,
      email: profile.emailAddress ?? null,
      scope: tokens.scope,
      tokens: { accessToken: tokens.accessToken, refreshToken: tokens.refreshToken, expiresAt: tokens.expiresAt },
      now,
    })
    if (replacedRefreshToken) await revokeToken(fetchImpl, replacedRefreshToken)
  } catch (err) {
    // Status only: the token response and profile never reach the log.
    console.error(`[recall:connectors] gmail connect failed: ${err instanceof Error ? err.message : "unknown error"}`)
    return back("failed")
  }
  return back("connected")
}

export { CALLBACK_PATH }
