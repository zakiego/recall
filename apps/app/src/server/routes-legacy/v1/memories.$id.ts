// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { patchInput, patchMemory } from "@/server/api"
import { apiError, json, readJson, withKey } from "@/server/http"
import { forgetMemory, getMemory, memoryChain } from "@/server/memories"

export const Route = createFileRoute("/v1/memories/$id")({
  server: {
    handlers: {
      GET: withKey(({ params, db, ws }) => {
        const memory = getMemory(db, ws, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory, history: memoryChain(db, ws, memory.id) })
      }),
      PATCH: withKey(async ({ request, params, db, ws }) => json(await patchMemory(db, ws, params.id!, await readJson(request, patchInput)))),
      DELETE: withKey(({ params, db, ws }) => {
        const memory = forgetMemory(db, ws, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory })
      }),
    },
  },
})
