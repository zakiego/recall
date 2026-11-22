// Which paths on the site's domain belong to the app on your server (through
// the tunnel) and which to the static site on Cloudflare Pages.
//
// The app owns:
//   /app/*       the dashboard, its assets and its server functions
//   /v1/*        the REST API
//   /mcp         the MCP server (streamable HTTP)
//   /health      the healthcheck
//   /api/auth/*  Better Auth (sessions, passkeys, the OAuth server)
//   /.well-known/oauth-*  OAuth discovery for MCP clients like claude.ai
//   the dashboard's old root paths (/login, /search, /settings/...), which the
//   app answers with a redirect to the same page under /app
//
// Everything else ("/", /docs, /_next, /og, robots.txt...) is the site.
const APP_PREFIXES = [
  "/app",
  "/v1",
  "/mcp",
  "/health",
  "/api/auth",
  "/.well-known/oauth-authorization-server",
  "/.well-known/oauth-protected-resource",
  "/.well-known/openid-configuration",
]
const LEGACY_DASHBOARD = ["/login", "/search", "/ask", "/graph", "/add", "/keys", "/settings", "/invite"]

const underAny = (pathname: string, prefixes: string[]) =>
  prefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`))

export function isAppPath(pathname: string): boolean {
  return underAny(pathname, APP_PREFIXES) || underAny(pathname, LEGACY_DASHBOARD)
}
