// Starts the Gmail connect flow: redirects to Google's consent screen.
// Served at /app/connectors/google/start (the router strips the /app basepath).
import { createFileRoute } from "@tanstack/react-router"
import { handleStart } from "@/server/connectors/oauth"
import { getDb } from "@/server/db"

export const Route = createFileRoute("/connectors/google/start")({
  server: {
    handlers: {
      GET: ({ request }: { request: Request }) => handleStart(getDb(), request),
    },
  },
})
