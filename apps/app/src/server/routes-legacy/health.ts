// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
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
