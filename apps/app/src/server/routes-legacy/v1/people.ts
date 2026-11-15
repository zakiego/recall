// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
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
