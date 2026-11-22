// Sign-in, passkeys, password changes and OAuth consent against the Go
// server's /api/auth routes (apps/server/internal/httpapi/auth.go and the
// OAuth server in internal/oauth), with the browser's WebAuthn API through
// @simplewebauthn/browser.
//
//   POST /api/auth/sign-in/username             { username, password, oauth_query? } -> { session, user }
//   POST /api/auth/sign-out                     { success: true }
//   POST /api/auth/change-password              { currentPassword, newPassword, revokeOtherSessions }
//   GET  /api/auth/passkey/authenticate-options WebAuthn request options JSON
//   POST /api/auth/passkey/authenticate         { response, oauth_query? } -> { session, user }
//   GET  /api/auth/passkey/register-options     WebAuthn creation options JSON (signed in)
//   POST /api/auth/passkey/register             { response, name? } -> the passkey
//   POST /api/auth/oauth2/consent               { accept, oauth_query } -> { redirect, url }
//
// On the OAuth login and consent pages the signed authorization query is in
// the address bar (restoreSignedOAuthQuery); every POST carries it as
// oauth_query, as Better Auth's oauthProviderClient did, and a sign-in then
// answers { redirect: true, url } for the next step.
//
// Each call resolves to { data, error } and never throws, the way the Better
// Auth client did, so the pages keep their error handling.
import { startAuthentication, startRegistration, type PublicKeyCredentialCreationOptionsJSON, type PublicKeyCredentialRequestOptionsJSON } from "@simplewebauthn/browser"

export const AUTH_BASE = "/api/auth"

export type AuthError = { status: number; code?: string; message?: string }
export type AuthResult<T> = { data: T; error: null } | { data: null; error: AuthError }

// buildSignedOAuthQuery: the signed parameters of the page's query, with sig
// and the ba_param list, or undefined when the page has no signed query.
export function signedQueryOf(search: string): string | undefined {
  const params = new URLSearchParams(search)
  if (!params.has("sig")) return undefined
  const names = new Set(params.getAll("ba_param"))
  if (names.size === 0) return undefined
  const out = new URLSearchParams()
  for (const [k, v] of params.entries()) if (k === "sig" || k === "ba_param" || names.has(k)) out.append(k, v)
  return out.toString()
}

async function call<T>(method: "GET" | "POST", path: string, body?: Record<string, unknown>): Promise<AuthResult<T>> {
  let payload = body
  if (method === "POST" && typeof window !== "undefined" && !payload?.oauth_query) {
    const q = window.location?.search ? signedQueryOf(window.location.search) : undefined
    if (q) payload = { ...payload, oauth_query: q }
  }
  let res: Response
  try {
    res = await fetch(`${AUTH_BASE}${path}`, {
      method,
      credentials: "same-origin",
      headers: method === "GET" ? { accept: "application/json" } : { accept: "application/json", "content-type": "application/json" },
      body: method === "GET" ? undefined : JSON.stringify(payload ?? {}),
    })
  } catch (err) {
    return { data: null, error: { status: 0, message: err instanceof Error ? err.message : String(err) } }
  }
  let json: unknown = null
  try {
    const text = await res.text()
    json = text ? JSON.parse(text) : null
  } catch {
    json = null
  }
  if (!res.ok) {
    const e = (json && typeof json === "object" ? json : {}) as { code?: unknown; message?: unknown; error?: unknown; error_description?: unknown }
    // No status text: the pages show their own fallback, as with Better Auth.
    const message = typeof e.message === "string" ? e.message : typeof e.error_description === "string" ? e.error_description : undefined
    const code = typeof e.code === "string" ? e.code : typeof e.error === "string" ? e.error : undefined
    return { data: null, error: { status: res.status, code, message } }
  }
  return { data: json as T, error: null }
}

// go-webauthn answers with the options object; accept the `{ publicKey }`
// envelope (navigator.credentials' shape) too.
function unwrap<T>(opts: unknown): T {
  if (opts && typeof opts === "object" && "publicKey" in opts && (opts as { publicKey?: unknown }).publicKey) return (opts as { publicKey: T }).publicKey
  return opts as T
}

// A WebAuthn failure (the person closed the prompt, the device said no) as an
// AuthError: the DOMException name ("NotAllowedError", "AbortError") is the code.
function webAuthnError(err: unknown): AuthError {
  if (err instanceof Error) {
    const cause = (err as { cause?: unknown }).cause
    const name = cause instanceof Error ? cause.name : err.name
    return { status: 400, code: name, message: err.message }
  }
  return { status: 400, message: String(err) }
}

type SessionBody = { session: { id: string; expiresAt: number; createdAt: number; activeOrganizationId: string | null }; user: { id: string; username: string; name: string } }
// A sign-in during an OAuth flow answers where to go next instead.
type SignInResult = SessionBody | { redirect: true; url: string }

async function signInWithPasskey(): Promise<AuthResult<SignInResult>> {
  const options = await call<unknown>("GET", "/passkey/authenticate-options")
  if (options.error) return options
  let response: unknown
  try {
    response = await startAuthentication({ optionsJSON: unwrap<PublicKeyCredentialRequestOptionsJSON>(options.data) })
  } catch (err) {
    return { data: null, error: webAuthnError(err) }
  }
  return call<SignInResult>("POST", "/passkey/authenticate", { response })
}

type PasskeyItem = { id: string; name: string | null; deviceType: string; backedUp: boolean; createdAt: number }

async function addPasskey(opts: { name?: string } = {}): Promise<AuthResult<PasskeyItem>> {
  const options = await call<unknown>("GET", opts.name ? `/passkey/register-options?${new URLSearchParams({ name: opts.name })}` : "/passkey/register-options")
  if (options.error) return options
  let response: unknown
  try {
    response = await startRegistration({ optionsJSON: unwrap<PublicKeyCredentialCreationOptionsJSON>(options.data) })
  } catch (err) {
    return { data: null, error: webAuthnError(err) }
  }
  return call<PasskeyItem>("POST", "/passkey/register", opts.name === undefined ? { response } : { response, name: opts.name })
}

export const authClient = {
  signIn: {
    username: (body: { username: string; password: string }) => call<SignInResult>("POST", "/sign-in/username", body),
    passkey: signInWithPasskey,
  },
  signOut: () => call<{ success: true }>("POST", "/sign-out"),
  changePassword: (body: { currentPassword: string; newPassword: string; revokeOtherSessions?: boolean }) => call<SessionBody>("POST", "/change-password", body),
  passkey: { addPasskey },
  oauth2: {
    consent: (body: { accept: boolean; scope?: string }) => call<{ redirect: true; url: string }>("POST", "/oauth2/consent", body),
  },
}

// Auth errors carry a status; 429 means the sign-in rate limit kicked in.
export function authErrorMessage(error: { status?: number; message?: string; code?: string } | null | undefined, fallback: string) {
  if (!error) return fallback
  if (error.status === 429) return "Too many tries. Wait a minute and try again."
  if (error.code === "INVALID_USERNAME_OR_PASSWORD" || error.status === 401) return "That username and password don't match."
  return error.message || fallback
}

// WebAuthn errors when the person closes the prompt or the device says no.
export function passkeyCancelled(error: { message?: string; code?: string } | null | undefined) {
  const s = `${error?.code ?? ""} ${error?.message ?? ""}`
  return /NotAllowed|AbortError|cancel|timed out|not allowed/i.test(s)
}
