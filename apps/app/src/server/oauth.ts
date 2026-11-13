// OAuth for the MCP endpoint, so claude.ai's custom connectors (and any other
// MCP client that speaks the MCP authorization spec) can connect without an
// API key. Better Auth's oauth-provider plugin is the authorization server at
// /api/auth/oauth2/*. This file holds the resource-server side: discovery
// documents, the 401 challenge, and turning an access token into a workspace.
// See docs/oauth.md.
import { createHash } from "node:crypto"
import { and, eq } from "drizzle-orm"
import { verifyApiKey } from "./auth"
import { AUTH_BASE_PATH } from "./better-auth"
import { OAUTH_LOGIN_FLAG, OAUTH_QUERY_COOKIE } from "@/lib/oauth-query"
import { config } from "./config"
import type { DB } from "./db"
import { member, oauthAccessToken, oauthClient } from "./db/schema"

// Shown on tokens so they're easy to tell apart from API keys (rcl_).
export const ACCESS_TOKEN_PREFIX = "rco_"
export const REFRESH_TOKEN_PREFIX = "rcr_"

// Scopes don't gate anything yet: a valid token reaches every MCP tool in its
// workspace. "claudeai" is there because claude.ai asks for it when it
// registers, and registration fails on any scope the server doesn't list.
export const OAUTH_SCOPES = ["mcp", "offline_access", "claudeai"]

// Better Auth redirects to these server routes, which open the pages below.
export const OAUTH_LOGIN_HANDOFF = "/app/oauth/sign-in"
export const OAUTH_CONSENT_HANDOFF = "/app/oauth/approve"
export const OAUTH_LOGIN_PAGE = `/app/login?${OAUTH_LOGIN_FLAG}=1`
export const OAUTH_CONSENT_PAGE = "/app/oauth/consent"

export function mcpResource(url = config().url) {
  return `${url}/mcp`
}

export function oauthIssuer(url = config().url) {
  return `${url}${AUTH_BASE_PATH}`
}

export function protectedResourceMetadataUrl(url = config().url) {
  return `${url}/.well-known/oauth-protected-resource/mcp`
}

// RFC 9728. Tells the client which authorization server issues tokens for /mcp.
export function protectedResourceMetadata(url = config().url) {
  return {
    resource: mcpResource(url),
    authorization_servers: [oauthIssuer(url)],
    scopes_supported: OAUTH_SCOPES,
    bearer_methods_supported: ["header"],
    resource_name: "Recall",
    resource_documentation: `${url}/docs/mcp`,
  }
}

export function protectedResourceMetadataResponse(): Response {
  return Response.json(protectedResourceMetadata(), {
    headers: { "cache-control": "public, max-age=300", "access-control-allow-origin": "*" },
  })
}

// RFC 6750 + RFC 9728: points the client at the metadata so it can start OAuth.
export function mcpUnauthorized(message = "missing or invalid API key or access token"): Response {
  return Response.json(
    { jsonrpc: "2.0", error: { code: -32001, message }, id: null },
    {
      status: 401,
      headers: {
        "www-authenticate": `Bearer realm="recall", resource_metadata="${protectedResourceMetadataUrl()}"`,
        "access-control-expose-headers": "www-authenticate",
      },
    },
  )
}

// Same hash the plugin stores: SHA-256, base64url, no padding.
function storedTokenHash(token: string) {
  return createHash("sha256").update(token).digest("base64url")
}

function parseList(v: string | null): string[] {
  if (!v) return []
  try {
    const parsed = JSON.parse(v)
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === "string") : []
  } catch {
    return []
  }
}

export type OAuthTokenAuth = { tokenId: string; clientId: string; userId: string; workspaceId: string }

// An access token is good for /mcp when it's unexpired and unrevoked, its
// client is still enabled, it was issued for this resource, and its user is
// still a member of the workspace they picked at consent.
export function verifyOAuthAccessToken(db: DB, token: string | null | undefined, now = Date.now()): OAuthTokenAuth | null {
  if (!token || !token.startsWith(ACCESS_TOKEN_PREFIX)) return null
  const row = db
    .select({
      id: oauthAccessToken.id,
      clientId: oauthAccessToken.clientId,
      userId: oauthAccessToken.userId,
      referenceId: oauthAccessToken.referenceId,
      resources: oauthAccessToken.resources,
      expiresAt: oauthAccessToken.expiresAt,
      revoked: oauthAccessToken.revoked,
      clientDisabled: oauthClient.disabled,
    })
    .from(oauthAccessToken)
    .innerJoin(oauthClient, eq(oauthClient.clientId, oauthAccessToken.clientId))
    .where(eq(oauthAccessToken.token, storedTokenHash(token.slice(ACCESS_TOKEN_PREFIX.length))))
    .get()
  if (!row || row.revoked || row.clientDisabled || !row.userId || !row.referenceId) return null
  if (!row.expiresAt || row.expiresAt.getTime() <= now) return null
  const resources = parseList(row.resources)
  if (resources.length && !resources.includes(mcpResource())) return null
  const membership = db
    .select({ id: member.id })
    .from(member)
    .where(and(eq(member.userId, row.userId), eq(member.organizationId, row.referenceId)))
    .get()
  if (!membership) return null
  return { tokenId: row.id, clientId: row.clientId, userId: row.userId, workspaceId: row.referenceId }
}

// Who is calling /mcp: an API key (rcl_) or an OAuth access token (rco_).
export function mcpCaller(db: DB, token: string | null | undefined): { workspaceId: string; keyId: string } | null {
  const key = verifyApiKey(db, token)
  if (key) return { workspaceId: key.workspaceId, keyId: key.id }
  const oauth = verifyOAuthAccessToken(db, token)
  if (oauth) return { workspaceId: oauth.workspaceId, keyId: `oauth:${oauth.clientId}:${oauth.userId}` }
  return null
}

// Where Better Auth sends the browser for login and consent. Keeps the signed
// query in a cookie and opens the page without it (see src/lib/oauth-query.ts).
export function handOffSignedQuery(request: Request, page: string): Response {
  const query = new URL(request.url).search.slice(1)
  if (!query) return new Response(null, { status: 302, headers: { location: page } })
  const secure = config().url.startsWith("https://") ? "; Secure" : ""
  return new Response(null, {
    status: 302,
    headers: {
      location: page,
      "set-cookie": `${OAUTH_QUERY_COOKIE}=${encodeURIComponent(query)}; Path=/app; Max-Age=600; SameSite=Lax${secure}`,
      "cache-control": "no-store",
    },
  })
}
