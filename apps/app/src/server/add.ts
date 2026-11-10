// POST /v1/memories, kept out of the route file so tests can call it with a
// plain Request.
import { z } from "zod"
import { parseDate } from "../lib/dates"
import { addContent, addFile, addInput, DEFAULT_WAIT_MS, MAX_FILE_BYTES, MAX_WAIT_MS, sourceIdSchema, waitForAdd, type AddResult } from "./api"
import type { DB } from "./db"
import { containerSchema, HttpError, json, readBodyCapped, readJson } from "./http"

// The file plus room for the other fields and the multipart boundaries.
export const MAX_MULTIPART_BYTES = MAX_FILE_BYTES + 1024 * 1024

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

// `workspaceId` comes from the API key; the body never chooses it.
export async function handleAdd(db: DB, workspaceId: string, request: Request): Promise<Response> {
  const query = parseWait(Object.fromEntries(new URL(request.url).searchParams), "query")
  const type = request.headers.get("content-type") ?? ""
  let res: AddResult
  let body: WaitOpts
  if (type.startsWith("multipart/form-data")) {
    ;({ res, body } = await addMultipart(db, workspaceId, request))
  } else {
    const input = await readJson(request, addInput)
    res = addContent(db, workspaceId, input)
    body = { wait: input.wait, wait_timeout: input.wait_timeout }
  }
  const wait = body.wait ?? query.wait ?? false
  // A duplicate queued nothing new, so it's 200 even while still processing.
  if (!wait) return json(res, res.duplicate ? 200 : 202)
  const seconds = body.wait_timeout ?? query.wait_timeout
  const out = await waitForAdd(db, workspaceId, res, seconds === undefined ? DEFAULT_WAIT_MS : seconds * 1000, request.signal)
  return json(out, out.settled || res.duplicate ? 200 : 202)
}

async function addMultipart(db: DB, workspaceId: string, request: Request) {
  // formData() would buffer any size, so read under the cap first.
  const bytes = await readBodyCapped(request, MAX_MULTIPART_BYTES)
  let form: FormData
  try {
    form = await new Response(bytes, { headers: { "content-type": request.headers.get("content-type") ?? "" } }).formData()
  } catch {
    throw new HttpError(400, "invalid_request", "couldn't read the multipart body")
  }
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
  const rawDate = form.get("document_date")?.toString().trim()
  const documentDate = rawDate ? parseDate(rawDate) : null
  if (rawDate && documentDate?.precision !== "day") throw new HttpError(400, "invalid_request", "document_date must be YYYY-MM-DD")
  if (documentDate) metadata = { ...metadata, documentDate: documentDate.date }
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
  const fileBytes = new Uint8Array(await file.arrayBuffer())
  return { res: addFile(db, workspaceId, { container, title, metadata, sourceId, file, bytes: fileBytes }), body }
}
