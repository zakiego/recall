// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
import { createFileRoute } from "@tanstack/react-router"
import { z } from "zod"
import { profile } from "@/server/api"
import { containerSchema, json, parseQuery, withKey } from "@/server/http"

const input = z.object({ container: containerSchema, q: z.string().trim().max(2000).optional() })

export const Route = createFileRoute("/v1/profile")({
  server: {
    handlers: {
      GET: withKey(async ({ request, db, ws }) => json(await profile(db, ws, parseQuery(request, input)))),
    },
  },
})
