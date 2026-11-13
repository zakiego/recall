// Better Auth signs the OAuth authorization request it hands to the login and
// consent pages, and the signature covers the exact query string. The router's
// SSR answers a page URL with repeated keys (`ba_param`) with a 307 to its own
// spelling of the query, which breaks the signature. So Better Auth sends the
// browser to a server route first (/app/oauth/sign-in, /app/oauth/approve) that
// keeps the query in a short-lived cookie and then opens the page. Before each
// auth call the page puts the query back in the address bar, where
// oauthProviderClient() reads it. See src/server/oauth.ts.
export const OAUTH_QUERY_COOKIE = "recall_oauth_query"
// Set on the login page URL so a stale cookie never hijacks a normal login.
export const OAUTH_LOGIN_FLAG = "oauth"

export function signedOAuthQuery(): string {
  if (typeof document === "undefined") return ""
  const hit = document.cookie.split("; ").find((c) => c.startsWith(`${OAUTH_QUERY_COOKIE}=`))
  if (!hit) return ""
  try {
    return decodeURIComponent(hit.slice(OAUTH_QUERY_COOKIE.length + 1))
  } catch {
    return ""
  }
}

export function inOAuthLogin(): boolean {
  return typeof window !== "undefined" && new URLSearchParams(window.location.search).has(OAUTH_LOGIN_FLAG)
}

export function restoreSignedOAuthQuery() {
  const q = signedOAuthQuery()
  if (q) window.history.replaceState(window.history.state, "", `${window.location.pathname}?${q}`)
}

export function clearSignedOAuthQuery() {
  document.cookie = `${OAUTH_QUERY_COOKIE}=; Path=/app; Max-Age=0; SameSite=Lax`
}
