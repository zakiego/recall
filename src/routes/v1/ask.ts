import { createFileRoute } from "@tanstack/react-router"
import { ask, askInput } from "@/server/ask"
import { json, readJson, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/ask")({
  server: {
    handlers: {
      POST: withKey(async ({ request, db }) => json(await ask(db, await readJson(request, askInput), { signal: request.signal }))),
    },
  },
})
