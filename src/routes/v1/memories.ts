import { createFileRoute } from "@tanstack/react-router"
import { handleAdd } from "@/server/add"
import { list, listInput } from "@/server/api"
import { json, parseQuery, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/memories")({
  server: {
    handlers: {
      GET: withKey(({ request, db }) => json(list(db, parseQuery(request, listInput)))),
      POST: withKey(({ request, db }) => handleAdd(db, request)),
    },
  },
})
