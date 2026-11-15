// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { upcoming, upcomingInput } from "@/server/api"
import { json, parseQuery, withKey } from "@/server/http"

// GET /v1/upcoming?container=&days=30&past_days=0&all=1&tz=Asia/Makassar
export const Route = createFileRoute("/v1/upcoming")({
  server: {
    handlers: {
      GET: withKey(({ request, db, ws }) => json(upcoming(db, ws, parseQuery(request, upcomingInput)))),
    },
  },
})
