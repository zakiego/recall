// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { json, withKey } from "@/server/http"
import { listContainers } from "@/server/memories"

export const Route = createFileRoute("/v1/containers")({
  server: {
    handlers: {
      GET: withKey(({ db, ws }) => json({ containers: listContainers(db, ws) })),
    },
  },
})
