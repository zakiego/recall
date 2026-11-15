// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
// Google's OAuth redirect URI: https://recall.zakiego.com/app/connectors/google/callback.
// Under /app, so the edge Worker already sends it to the app.
import { createFileRoute } from "@tanstack/react-router"
import { googleFetch } from "@/server/connectors/gmail"
import { handleCallback } from "@/server/connectors/oauth"
import { getDb } from "@/server/db"

export const Route = createFileRoute("/connectors/google/callback")({
  server: {
    handlers: {
      GET: async ({ request }: { request: Request }) => handleCallback(getDb(), request, await googleFetch()),
    },
  },
})
