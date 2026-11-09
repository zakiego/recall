// GET /health, no auth. The compose healthcheck and deploy.sh read its status,
// so anything that makes the app unsafe to serve must turn it non-200.
import type { DB } from "./db"
import { unknownMigrations } from "./db/migrate"

export function health(db: DB): Response {
  const unknown = unknownMigrations(db.$client)
  if (unknown.length) {
    return Response.json(
      {
        ok: false,
        error: "db_newer_than_app",
        message: `The database has migrations this build doesn't know (${unknown.join(", ")}). It was migrated by a newer version; deploy that version again or restore a backup taken before it.`,
        unknownMigrations: unknown,
      },
      { status: 503 },
    )
  }
  const { vec } = db.$client.prepare("SELECT vec_version() AS vec").get() as { vec: string }
  return Response.json({ ok: true, vec })
}
