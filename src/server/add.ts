// POST /v1/memories, kept out of the route file so tests can call it with a
// plain Request.
import { z } from "zod"
import { addContent, addFile, addInput, DEFAULT_WAIT_MS, MAX_WAIT_MS, sourceIdSchema, waitForAdd, type AddResult } from "./api"
import type { DB } from "./db"
import { containerSchema, HttpError, json, readJson } from "./http"

// `wait` and `wait_timeout` (seconds) as query params or multipart fields.
const waitFields = z.object({
  wait: z
    .enum(["1", "0", "true", "false"])
    .optional()
    .transform((v) => (v === undefined ? undefined : v === "1" || v === "true")),
  wait_timeout: z.coerce
    .number()
    .min(0)
    .max(MAX_WAIT_MS / 1000)
    .optional(),
})
type WaitOpts = { wait?: boolean; wait_timeout?: number }

function parseWait(values: Record<string, unknown>, where: string): WaitOpts {
  const parsed = waitFields.safeParse(values)
  if (!parsed.success) throw new HttpError(400, "invalid_request", `${where}: wait is 1/0/true/false, wait_timeout is 0 to ${MAX_WAIT_MS / 1000} seconds`)
  return parsed.data
}

export async function handleAdd(db: DB, request: Request): Promise<Response> {
  const query = parseWait(Object.fromEntries(new URL(request.url).searchParams), "query")
  const type = request.headers.get("content-type") ?? ""
  let res: AddResult
  let body: WaitOpts
  if (type.startsWith("multipart/form-data")) {
    ;({ res, body } = await addMultipart(db, request))
  } else {
    const input = await readJson(request, addInput)
    res = addContent(db, input)
    body = { wait: input.wait, wait_timeout: input.wait_timeout }
  }
  const wait = body.wait ?? query.wait ?? false
  // A duplicate queued nothing new, so it's 200 even while still processing.
  if (!wait) return json(res, res.duplicate ? 200 : 202)
  const seconds = body.wait_timeout ?? query.wait_timeout
  const out = await waitForAdd(db, res, seconds === undefined ? DEFAULT_WAIT_MS : seconds * 1000, request.signal)
  return json(out, out.settled || res.duplicate ? 200 : 202)
}

async function addMultipart(db: DB, request: Request) {
  const form = await request.formData()
  const file = form.get("file")
  if (!(file instanceof File)) throw new HttpError(400, "invalid_request", "multipart body needs a file field")
  const container = containerSchema.parse(form.get("container")?.toString())
  let metadata: Record<string, unknown> | undefined
  const rawMeta = form.get("metadata")?.toString()
  if (rawMeta) {
    try {
      metadata = JSON.parse(rawMeta)
    } catch {
      throw new HttpError(400, "invalid_request", "metadata must be a JSON object")
    }
  }
  const title = form.get("title")?.toString().trim() || undefined
  const rawSourceId = form.get("source_id")?.toString()
  let sourceId: string | undefined
  if (rawSourceId?.trim()) {
    const parsed = sourceIdSchema.safeParse(rawSourceId)
    if (!parsed.success) throw new HttpError(400, "invalid_request", "source_id must be 1 to 200 characters")
    sourceId = parsed.data
  }
  const body = parseWait(
    { wait: form.get("wait")?.toString() || undefined, wait_timeout: form.get("wait_timeout")?.toString() || undefined },
    "form",
  )
  const bytes = new Uint8Array(await file.arrayBuffer())
  return { res: addFile(db, { container, title, metadata, sourceId, file, bytes }), body }
}
