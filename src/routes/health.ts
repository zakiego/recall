import { createFileRoute } from "@tanstack/react-router"
import { getDb } from "@/server/db"
import { health } from "@/server/health"

export const Route = createFileRoute("/health")({
  server: {
    handlers: {
      GET: () => health(getDb()),
    },
  },
})
