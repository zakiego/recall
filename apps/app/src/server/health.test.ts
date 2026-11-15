import { afterEach, describe, expect, it } from "vitest"
import { Route } from "@/server/routes-legacy/health"
import { openDb, setDb } from "./db"

type Handler = () => Response | Promise<Response>
const get = () => (Route as unknown as { options: { server: { handlers: { GET: Handler } } } }).options.server.handlers.GET()

afterEach(() => setDb(undefined))

describe("/health", () => {
  it("is ok on a database this build migrated", async () => {
    setDb(openDb(":memory:"))
    const res = await get()
    expect(res.status).toBe(200)
    expect(await res.json()).toMatchObject({ ok: true, vec: expect.any(String) })
  })

  it("answers 503 when a newer build migrated the database", async () => {
    const db = openDb(":memory:")
    db.$client.prepare("INSERT INTO _migrations (name, applied_at) VALUES ('0099_from_the_future', 1)").run()
    setDb(db)
    const res = await get()
    expect(res.status).toBe(503)
    expect(await res.json()).toMatchObject({ ok: false, error: "db_newer_than_app", unknownMigrations: ["0099_from_the_future"] })
  })
})
