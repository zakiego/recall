import { createFileRoute } from "@tanstack/react-router"
import { search, searchInput } from "@/server/api"
import { json, readJson, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/search")({
  server: {
    handlers: {
      POST: withKey(async ({ request, db }) => json(await search(db, await readJson(request, searchInput)))),
    },
  },
})
