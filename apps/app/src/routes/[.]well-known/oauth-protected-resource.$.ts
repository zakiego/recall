// RFC 9728 metadata for /mcp, at the root and in the path-inserted form
// (/.well-known/oauth-protected-resource/mcp). claude.ai reads it after the 401.
import { createFileRoute } from "@tanstack/react-router"
import { protectedResourceMetadataResponse } from "@/server/oauth"

export const Route = createFileRoute("/.well-known/oauth-protected-resource/$")({
  server: {
    handlers: {
      GET: ({ params }: { params: { _splat?: string } }) =>
        !params._splat || params._splat === "mcp" ? protectedResourceMetadataResponse() : new Response("Not found", { status: 404 }),
    },
  },
})
