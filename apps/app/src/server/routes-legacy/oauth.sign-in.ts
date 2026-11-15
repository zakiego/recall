// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
// Better Auth's loginPage. Served at /app/oauth/sign-in.
import { createFileRoute } from "@tanstack/react-router"
import { handOffSignedQuery, OAUTH_LOGIN_PAGE } from "@/server/oauth"

export const Route = createFileRoute("/oauth/sign-in")({
  server: { handlers: { GET: ({ request }: { request: Request }) => handOffSignedQuery(request, OAUTH_LOGIN_PAGE) } },
})
