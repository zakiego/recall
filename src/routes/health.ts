import { createFileRoute } from "@tanstack/react-router"
import { getDb } from "@/server/db"

export const Route = createFileRoute("/health")({
  server: {
    handlers: {
      GET: () => {
        const { vec } = getDb()
          .$client.prepare("SELECT vec_version() AS vec")
          .get() as { vec: string }
        return Response.json({ ok: true, vec })
      },
    },
  },
})
