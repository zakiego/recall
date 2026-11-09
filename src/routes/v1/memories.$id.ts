import { createFileRoute } from "@tanstack/react-router"
import { z } from "zod"
import { apiError, json, readJson, withKey } from "@/server/http"
import { editMemory, forgetMemory, getMemory, memoryChain } from "@/server/memories"

const patchInput = z
  .object({
    fact: z.string().trim().min(1).max(500).optional(),
    keywords: z.string().trim().max(500).optional(),
    kind: z.enum(["static", "dynamic"]).optional(),
  })
  .refine((v) => v.fact !== undefined || v.keywords !== undefined || v.kind !== undefined, { message: "nothing to change" })

export const Route = createFileRoute("/v1/memories/$id")({
  server: {
    handlers: {
      GET: withKey(({ params, db }) => {
        const memory = getMemory(db, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory, history: memoryChain(db, memory.id) })
      }),
      PATCH: withKey(async ({ request, params, db }) => {
        const patch = await readJson(request, patchInput)
        const existing = getMemory(db, params.id!)
        if (!existing) return apiError(404, "not_found", "no such memory")
        if (existing.validTo !== null) return apiError(409, "not_current", "this memory was superseded or deleted; edit the current version")
        return json({ memory: await editMemory(db, existing.id, patch) })
      }),
      DELETE: withKey(({ params, db }) => {
        const memory = forgetMemory(db, params.id!)
        if (!memory) return apiError(404, "not_found", "no such memory")
        return json({ memory })
      }),
    },
  },
})
