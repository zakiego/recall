import { createFileRoute } from "@tanstack/react-router"
import { getPersonInput, person } from "@/server/api"
import { json, parseQuery, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/people/$id")({
  server: {
    handlers: {
      GET: withKey(({ request, params, db, ws }) => json(person(db, ws, params.id!, parseQuery(request, getPersonInput)))),
    },
  },
})
