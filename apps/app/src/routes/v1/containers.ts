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
