import { createFileRoute } from "@tanstack/react-router"
import { z } from "zod"
import { profile } from "@/server/api"
import { containerSchema, json, parseQuery, withKey } from "@/server/http"

const input = z.object({ container: containerSchema, q: z.string().trim().max(2000).optional() })

export const Route = createFileRoute("/v1/profile")({
  server: {
    handlers: {
      GET: withKey(async ({ request, db }) => json(await profile(db, parseQuery(request, input)))),
    },
  },
})
