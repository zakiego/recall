import { z } from "zod"
import { bearer, verifyApiKey, type KeyAuth } from "./auth"
import { config } from "./config"
import { getDb } from "./db"
import { MAX_CONTAINER_LEN } from "./memories"

export function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return Response.json(body, { status, headers: { "cache-control": "no-store", ...headers } })
}

export function apiError(status: number, type: string, message: string, headers: Record<string, string> = {}) {
  return json({ error: { type, message } }, status, headers)
}

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly type: string,
    message: string,
    readonly headers: Record<string, string> = {},
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

// A 1M-character content field is up to 4 MB as UTF-8, more once escaped.
export const MAX_JSON_BYTES = 10 * 1024 * 1024

// Reads the body without holding more than `max` bytes: a declared length over
// the cap is refused before reading, and a stream that runs past it is cut off.
export async function readBodyCapped(request: Request, max: number): Promise<Uint8Array<ArrayBuffer>> {
  const tooLarge = () => new HttpError(413, "too_large", `body is larger than ${Math.floor(max / 1024 / 1024)} MB`)
  const declared = Number(request.headers.get("content-length"))
  if (declared > max) throw tooLarge()
  const reader = request.body?.getReader()
  if (!reader) return new Uint8Array()
  const parts: Uint8Array[] = []
  let size = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    size += value.byteLength
    if (size > max) {
      await reader.cancel()
      throw tooLarge()
    }
    parts.push(value)
  }
  return Buffer.concat(parts)
}

export async function readJson<T extends z.ZodType>(request: Request, schema: T): Promise<z.infer<T>> {
  const bytes = await readBodyCapped(request, MAX_JSON_BYTES)
  let body: unknown
  try {
    body = JSON.parse(new TextDecoder().decode(bytes))
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
export function withKey(handler: (ctx: Ctx & { db: ReturnType<typeof getDb>; ws: string; key: KeyAuth }) => Promise<Response> | Response) {
  return async (ctx: Ctx) => {
    try {
      const db = getDb()
      const key = verifyApiKey(db, bearer(ctx.request))
      if (!key) return apiError(401, "unauthorized", "missing or invalid API key")
      return await handler({ ...ctx, db, ws: key.workspaceId, key })
    } catch (err) {
      if (err instanceof HttpError) return apiError(err.status, err.type, err.message, err.headers)
      // The message can name tables, file paths or internal hosts, so it stays in the log.
      console.error("[recall] api error:", err)
      return apiError(500, "internal", "internal error")
    }
  }
}
