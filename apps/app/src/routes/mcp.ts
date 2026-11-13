import { createFileRoute } from "@tanstack/react-router"
import { bearer } from "@/server/auth"
import { getDb } from "@/server/db"
import { handleMcp } from "@/server/mcp"
import { mcpCaller, mcpUnauthorized } from "@/server/oauth"

// An API key, or an OAuth access token from a connector like claude.ai. The
// 401 points OAuth clients at /.well-known/oauth-protected-resource/mcp.
async function handle({ request }: { request: Request }) {
  const db = getDb()
  const caller = mcpCaller(db, bearer(request))
  if (!caller) return mcpUnauthorized()
  return handleMcp(db, caller.workspaceId, request, { keyId: caller.keyId })
}

export const Route = createFileRoute("/mcp")({
  server: {
    handlers: { GET: handle, POST: handle, DELETE: handle },
  },
})
