// Google OAuth (web server flow with PKCE) and the few Gmail API calls the
// connector makes. Every call takes the fetch to use, so tests and the dev
// fake run against fixtures. Errors carry status codes only, never mail content.
import { createHash, randomBytes } from "node:crypto"
import { APP_BASE } from "@/lib/app-path"
import { config } from "../config"

export const GMAIL_SCOPE = "https://www.googleapis.com/auth/gmail.readonly"
export const AUTH_URL = "https://accounts.google.com/o/oauth2/v2/auth"
export const TOKEN_URL = "https://oauth2.googleapis.com/token"
export const REVOKE_URL = "https://oauth2.googleapis.com/revoke"
export const GMAIL_API = "https://gmail.googleapis.com/gmail/v1/users/me"
export const CALLBACK_PATH = `${APP_BASE}/connectors/google/callback`

const TIMEOUT_MS = 30_000

export type Fetch = typeof fetch

// `retryable` false means trying again won't help (bad grant, missing message).
export class GoogleError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: "reauth" | "not_found" | "rate_limited" | "upstream" | "bad_response",
  ) {
    super(message)
    this.name = "GoogleError"
  }
}

export function redirectUri() {
  return `${config().url}${CALLBACK_PATH}`
}

export function pkcePair() {
  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  return { verifier, challenge }
}

export function authorizationUrl(input: { clientId: string; state: string; challenge: string }) {
  const u = new URL(AUTH_URL)
  u.search = new URLSearchParams({
    client_id: input.clientId,
    redirect_uri: redirectUri(),
    response_type: "code",
    scope: GMAIL_SCOPE,
    // offline + consent: Google returns a refresh token every time, also on a reconnect.
    access_type: "offline",
    prompt: "consent",
    include_granted_scopes: "false",
    state: input.state,
    code_challenge: input.challenge,
    code_challenge_method: "S256",
  }).toString()
  return u.toString()
}

export type Tokens = { accessToken: string; refreshToken: string; expiresAt: number }

type TokenResponse = { access_token?: string; refresh_token?: string; expires_in?: number; scope?: string; error?: string }

async function postForm(fetchImpl: Fetch, url: string, body: Record<string, string>) {
  return fetchImpl(url, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(body).toString(),
    signal: AbortSignal.timeout(TIMEOUT_MS),
  })
}

export async function exchangeCode(fetchImpl: Fetch, input: { code: string; verifier: string; now?: number }): Promise<Tokens & { scope: string }> {
  const { googleClientId, googleClientSecret } = config()
  const res = await postForm(fetchImpl, TOKEN_URL, {
    code: input.code,
    client_id: googleClientId,
    client_secret: googleClientSecret,
    redirect_uri: redirectUri(),
    grant_type: "authorization_code",
    code_verifier: input.verifier,
  })
  const json = (await res.json().catch(() => ({}))) as TokenResponse
  if (!res.ok || !json.access_token) throw new GoogleError(`Google token exchange returned ${res.status}`, res.status, "upstream")
  if (!json.refresh_token) throw new GoogleError("Google returned no refresh token", res.status, "bad_response")
  return {
    accessToken: json.access_token,
    refreshToken: json.refresh_token,
    expiresAt: (input.now ?? Date.now()) + (json.expires_in ?? 3600) * 1000,
    scope: json.scope ?? "",
  }
}

export async function refreshAccessToken(fetchImpl: Fetch, refreshToken: string, now = Date.now()): Promise<Tokens> {
  const { googleClientId, googleClientSecret } = config()
  const res = await postForm(fetchImpl, TOKEN_URL, {
    client_id: googleClientId,
    client_secret: googleClientSecret,
    refresh_token: refreshToken,
    grant_type: "refresh_token",
  })
  const json = (await res.json().catch(() => ({}))) as TokenResponse
  if (json.error === "invalid_grant" || res.status === 401) throw new GoogleError("Gmail access was revoked or expired", res.status, "reauth")
  if (!res.ok || !json.access_token) throw new GoogleError(`Google token refresh returned ${res.status}`, res.status, "upstream")
  return { accessToken: json.access_token, refreshToken: json.refresh_token ?? refreshToken, expiresAt: now + (json.expires_in ?? 3600) * 1000 }
}

// True when Google confirmed, or the token was already dead (400). False on
// anything else, so the caller can tell the user to check their Google account.
export async function revokeToken(fetchImpl: Fetch, token: string): Promise<boolean> {
  try {
    const res = await postForm(fetchImpl, REVOKE_URL, { token })
    return res.ok || res.status === 400
  } catch {
    return false
  }
}

// --- Gmail API ------------------------------------------------------------------

async function gmail<T>(fetchImpl: Fetch, accessToken: string, path: string, query?: Record<string, string | string[]>): Promise<T> {
  const u = new URL(`${GMAIL_API}${path}`)
  for (const [k, v] of Object.entries(query ?? {})) for (const one of [v].flat()) u.searchParams.append(k, one)
  const res = await fetchImpl(u, { headers: { authorization: `Bearer ${accessToken}`, accept: "application/json" }, signal: AbortSignal.timeout(TIMEOUT_MS) })
  if (res.ok) return (await res.json()) as T
  const code = res.status === 401 ? "reauth" : res.status === 404 ? "not_found" : res.status === 429 ? "rate_limited" : "upstream"
  throw new GoogleError(`Gmail API returned ${res.status}`, res.status, code)
}

export type GmailHeader = { name: string; value: string }
export type GmailPart = {
  partId?: string
  mimeType?: string
  filename?: string
  headers?: GmailHeader[]
  body?: { size?: number; data?: string; attachmentId?: string }
  parts?: GmailPart[]
}
export type GmailMessage = {
  id: string
  threadId?: string
  labelIds?: string[]
  snippet?: string
  internalDate?: string
  sizeEstimate?: number
  payload?: GmailPart
}

export function getProfile(fetchImpl: Fetch, accessToken: string) {
  return gmail<{ emailAddress: string }>(fetchImpl, accessToken, "/profile")
}

export function listMessages(fetchImpl: Fetch, accessToken: string, input: { q: string; pageToken?: string; maxResults: number }) {
  return gmail<{ messages?: { id: string; threadId: string }[]; nextPageToken?: string; resultSizeEstimate?: number }>(fetchImpl, accessToken, "/messages", {
    q: input.q,
    maxResults: String(input.maxResults),
    ...(input.pageToken ? { pageToken: input.pageToken } : {}),
  })
}

export function getMessageMetadata(fetchImpl: Fetch, accessToken: string, id: string) {
  return gmail<GmailMessage>(fetchImpl, accessToken, `/messages/${encodeURIComponent(id)}`, {
    format: "metadata",
    metadataHeaders: ["From", "Subject", "Date"],
  })
}

export function getMessage(fetchImpl: Fetch, accessToken: string, id: string) {
  return gmail<GmailMessage>(fetchImpl, accessToken, `/messages/${encodeURIComponent(id)}`, { format: "full" })
}

export async function getAttachment(fetchImpl: Fetch, accessToken: string, messageId: string, attachmentId: string): Promise<Uint8Array> {
  const r = await gmail<{ data?: string; size?: number }>(
    fetchImpl,
    accessToken,
    `/messages/${encodeURIComponent(messageId)}/attachments/${encodeURIComponent(attachmentId)}`,
  )
  return Buffer.from(r.data ?? "", "base64url")
}
