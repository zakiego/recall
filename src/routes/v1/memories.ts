import { createFileRoute } from "@tanstack/react-router"
import { addContent, addFile, addInput, list, listInput } from "@/server/api"
import { containerSchema, HttpError, json, parseQuery, readJson, withKey } from "@/server/http"

export const Route = createFileRoute("/v1/memories")({
  server: {
    handlers: {
      GET: withKey(({ request, db }) => json(list(db, parseQuery(request, listInput)))),
      POST: withKey(async ({ request, db }) => {
        const type = request.headers.get("content-type") ?? ""
        if (type.startsWith("multipart/form-data")) {
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
          const bytes = new Uint8Array(await file.arrayBuffer())
          return json(addFile(db, { container, title, metadata, file, bytes }), 202)
        }
        return json(addContent(db, await readJson(request, addInput)), 202)
      }),
    },
  },
})
