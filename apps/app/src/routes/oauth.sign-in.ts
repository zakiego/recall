// Better Auth's loginPage. Served at /app/oauth/sign-in.
import { createFileRoute } from "@tanstack/react-router"
import { handOffSignedQuery, OAUTH_LOGIN_PAGE } from "@/server/oauth"

export const Route = createFileRoute("/oauth/sign-in")({
  server: { handlers: { GET: ({ request }: { request: Request }) => handOffSignedQuery(request, OAUTH_LOGIN_PAGE) } },
})
