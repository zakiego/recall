// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createMiddleware, createStart } from "@tanstack/react-start"
import { legacyDashboardPath } from "@/lib/app-path"

// Old dashboard URLs (/login, /search, /settings/keys, invite links...) answer
// with a redirect to the same page under /app. The router would otherwise
// render them at the root, because its basepath only strips /app when present.
const legacyRedirect = createMiddleware().server(({ request, next }) => {
  if (request.method === "GET" || request.method === "HEAD") {
    const to = legacyDashboardPath(new URL(request.url))
    if (to) return new Response(null, { status: 302, headers: { location: to } })
  }
  return next()
})

export const startInstance = createStart(() => ({
  requestMiddleware: [legacyRedirect],
}))
