import { createFileRoute } from "@tanstack/react-router"
import { ask, askInput } from "@/server/ask"
import { json, readJson, withKey } from "@/server/http"
import { limitAsk } from "@/server/rate-limit"

export const Route = createFileRoute("/v1/ask")({
  server: {
    handlers: {
      POST: withKey(async ({ request, db, ws, key }) => {
        limitAsk(key.id)
        return json(await ask(db, ws, await readJson(request, askInput), { signal: request.signal }))
      }),
    },
  },
})
