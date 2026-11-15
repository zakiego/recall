// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { search, searchInput } from "@/server/api"
import { json, readJson, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/search")({
  server: {
    handlers: {
      POST: withKey(async ({ request, db, ws }) => json(await search(db, ws, await readJson(request, searchInput)))),
    },
  },
})
