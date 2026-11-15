// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { handleAdd } from "@/server/add"
import { list, listInput } from "@/server/api"
import { json, parseQuery, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/memories")({
  server: {
    handlers: {
      GET: withKey(({ request, db, ws }) => json(list(db, ws, parseQuery(request, listInput)))),
      POST: withKey(({ request, db, ws }) => handleAdd(db, ws, request)),
    },
  },
})
