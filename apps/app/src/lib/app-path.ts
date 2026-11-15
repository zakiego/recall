// The dashboard lives under /app (Vite `base`, and so the router basepath).
// The public API (/v1, /mcp, /health) and the auth routes (/api/auth) stay at
// the root, so existing clients and passkeys don't notice. Everything else at the
// root belongs to the static site in production.
export const APP_BASE = "/app"

// Dashboard pages that used to live at the root, before the move to /app.
const LEGACY_PAGES = ["/login", "/search", "/ask", "/graph", "/add", "/keys", "/settings", "/invite"]

// Where an old dashboard URL lives now, or null when the path isn't one.
// Query strings carry over (?c=, ?next=). A temporary redirect on purpose: in
// production "/" is the landing page once the edge worker routes it, and a
// cached permanent redirect would hide that page from anyone who hit "/"
// between the app deploy and the worker going live.
export function legacyDashboardPath(url: URL): string | null {
  const p = url.pathname
  const legacy = p === "/" || LEGACY_PAGES.some((page) => p === page || p.startsWith(`${page}/`))
  return legacy ? `${APP_BASE}${p === "/" ? "/" : p}${url.search}` : null
}
