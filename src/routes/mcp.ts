import { createFileRoute } from "@tanstack/react-router"
import { bearer, verifyApiKey } from "@/server/auth"
import { getDb } from "@/server/db"
import { handleMcp } from "@/server/mcp"

async function handle({ request }: { request: Request }) {
  const db = getDb()
  if (!verifyApiKey(db, bearer(request))) {
    return Response.json(
      { jsonrpc: "2.0", error: { code: -32001, message: "missing or invalid API key" }, id: null },
      { status: 401, headers: { "www-authenticate": 'Bearer realm="recall"' } },
    )
  }
  return handleMcp(db, request)
}

export const Route = createFileRoute("/mcp")({
  server: {
    handlers: { GET: handle, POST: handle, DELETE: handle },
  },
})
