// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
// RFC 8414 metadata for the OAuth server behind /api/auth/oauth2/*, at the root
// and in the path-inserted form for the issuer (.../oauth-authorization-server/api/auth).
import { createFileRoute } from "@tanstack/react-router"
import { handleAuthServerMetadata } from "@/server/better-auth"

export const Route = createFileRoute("/.well-known/oauth-authorization-server/$")({
  server: {
    handlers: {
      GET: ({ request, params }: { request: Request; params: { _splat?: string } }) =>
        !params._splat || params._splat === "api/auth" ? handleAuthServerMetadata(request) : new Response("Not found", { status: 404 }),
    },
  },
})
