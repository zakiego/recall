import { z } from "zod"
import { bearer, verifyApiKey } from "./auth"
import { config } from "./config"
import { getDb } from "./db"
import { MAX_CONTAINER_LEN } from "./memories"

export function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return Response.json(body, { status, headers: { "cache-control": "no-store", ...headers } })
}

export function apiError(status: number, type: string, message: string) {
  return json({ error: { type, message } }, status)
}

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly type: string,
    message: string,
  ) {
    super(message)
  }
}

export const containerSchema = z
  .string()
  .trim()
  .min(1)
  .max(MAX_CONTAINER_LEN)
  .optional()
  .transform((c) => c || config().defaultContainer)

export async function readJson<T extends z.ZodType>(request: Request, schema: T): Promise<z.infer<T>> {
  let body: unknown
  try {
    body = await request.json()
  } catch {
    throw new HttpError(400, "invalid_json", "body must be JSON")
  }
  const parsed = schema.safeParse(body)
  if (!parsed.success) throw new HttpError(400, "invalid_request", z.prettifyError(parsed.error))
  return parsed.data
}

export function parseQuery<T extends z.ZodType>(request: Request, schema: T): z.infer<T> {
  const params = Object.fromEntries(new URL(request.url).searchParams)
  const parsed = schema.safeParse(params)
  if (!parsed.success) throw new HttpError(400, "invalid_request", z.prettifyError(parsed.error))
  return parsed.data
}

type Ctx = { request: Request; params: Record<string, string> }

// Wraps a /v1 handler: bearer API key, then the handler, with HttpError and
// unexpected errors turned into JSON. `ws` is the key's workspace; handlers
// pass it to every read and write.
export function withKey(handler: (ctx: Ctx & { db: ReturnType<typeof getDb>; ws: string }) => Promise<Response> | Response) {
  return async (ctx: Ctx) => {
    try {
      const db = getDb()
      const key = verifyApiKey(db, bearer(ctx.request))
      if (!key) return apiError(401, "unauthorized", "missing or invalid API key")
      return await handler({ ...ctx, db, ws: key.workspaceId })
    } catch (err) {
      if (err instanceof HttpError) return apiError(err.status, err.type, err.message)
      console.error("[recall] api error:", err)
      return apiError(500, "internal", err instanceof Error ? err.message : "internal error")
    }
  }
}
