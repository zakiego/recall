// Better Auth's consentPage. Served at /app/oauth/approve.
import { createFileRoute } from "@tanstack/react-router"
import { handOffSignedQuery, OAUTH_CONSENT_PAGE } from "@/server/oauth"

export const Route = createFileRoute("/oauth/approve")({
  server: { handlers: { GET: ({ request }: { request: Request }) => handOffSignedQuery(request, OAUTH_CONSENT_PAGE) } },
})
