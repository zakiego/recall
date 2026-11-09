import { createFileRoute } from "@tanstack/react-router"
import { apiError, json, withKey } from "@/server/http"
import { deleteDocument, getDocument } from "@/server/memories"

export const Route = createFileRoute("/v1/documents/$id")({
  server: {
    handlers: {
      GET: withKey(({ params, db }) => {
        const doc = getDocument(db, params.id!)
        if (!doc) return apiError(404, "not_found", "no such document")
        const { content, ...rest } = doc
        return json({ document: { ...rest, contentLength: content?.length ?? 0 } })
      }),
      DELETE: withKey(({ params, db }) => {
        if (!getDocument(db, params.id!)) return apiError(404, "not_found", "no such document")
        deleteDocument(db, params.id!)
        return json({ deleted: true })
      }),
    },
  },
})
