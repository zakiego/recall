import { createFileRoute } from "@tanstack/react-router"
import { patchInput, patchMemory } from "@/server/api"
import { apiError, json, readJson, withKey } from "@/server/http"
import { forgetMemory, getMemory, memoryChain } from "@/server/memories"

export const Route = createFileRoute("/v1/memories/$id")({
  server: {
    handlers: {
      GET: withKey(({ params, db }) => {
        const memory = getMemory(db, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory, history: memoryChain(db, memory.id) })
      }),
      PATCH: withKey(async ({ request, params, db }) => json(await patchMemory(db, params.id!, await readJson(request, patchInput)))),
      DELETE: withKey(({ params, db }) => {
        const memory = forgetMemory(db, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory })
      }),
    },
  },
})
