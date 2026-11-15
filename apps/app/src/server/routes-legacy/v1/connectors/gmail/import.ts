// @ts-nocheck (a TanStack Start route kept until M10b; it is no longer in the route tree)
// POST /v1/connectors/gmail/import {"message_id": "...", "container": "..."}
// Imports one Gmail message with the Gmail connection of the user who created
// the API key, in the key's workspace. Same dedupe as the dashboard import.
import { createFileRoute } from "@tanstack/react-router"
import { z } from "zod"
import { ConnectorError, importGmail, MESSAGE_ID } from "@/server/connectors/gmail"
import { getDocument } from "@/server/memories"
import { apiError, containerSchema, json, readJson, withKey } from "@/server/http"
import { getRole } from "@/server/users"

const body = z.object({ message_id: z.string().trim().regex(MESSAGE_ID, "not a Gmail message id"), container: containerSchema })

const STATUS: Record<ConnectorError["code"], number> = { not_configured: 503, not_connected: 409, reauth: 409, invalid: 400 }

export const Route = createFileRoute("/v1/connectors/gmail/import")({
  server: {
    handlers: {
      POST: withKey(async ({ request, db, ws, key }) => {
        const input = await readJson(request, body)
        // A connection is personal: only keys its owner created (and who is still a member) may use it.
        if (!key.createdBy || !getRole(db, key.createdBy, ws)) {
          return apiError(403, "forbidden", "this key has no owner who can use a Gmail connection; create a key in the dashboard as the user who connected Gmail")
        }
        try {
          const result = importGmail(db, { workspaceId: ws, userId: key.createdBy, container: input.container, messages: [{ id: input.message_id }] })
          const d = result.documents[0]!
          const doc = getDocument(db, ws, d.id)!
          return json(
            { id: doc.id, container: doc.container, status: doc.status, sourceId: doc.sourceId, duplicate: d.duplicate },
            d.duplicate ? 200 : 202,
          )
        } catch (err) {
          if (err instanceof ConnectorError) return apiError(STATUS[err.code], err.code, err.message)
          throw err
        }
      }),
    },
  },
})
