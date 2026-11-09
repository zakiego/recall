import { createFileRoute } from "@tanstack/react-router"
import { listPeopleInput, people } from "@/server/api"
import { json, parseQuery, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/people")({
  server: {
    handlers: {
      GET: withKey(({ request, db, ws }) => json(people(db, ws, parseQuery(request, listPeopleInput)))),
    },
  },
})
