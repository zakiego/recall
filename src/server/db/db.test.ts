import { describe, expect, it } from "vitest"
import { migrations } from "./migrate"
import { openDb } from "./index"

describe("openDb", () => {
  it("loads sqlite-vec and applies every migration", () => {
    const db = openDb(":memory:")
    const sqlite = db.$client
    expect(sqlite.prepare("SELECT vec_version() AS v").get()).toMatchObject({ v: expect.stringMatching(/^v0\./) })
    const applied = sqlite.prepare("SELECT name FROM _migrations ORDER BY name").pluck().all()
    expect(applied).toEqual(migrations.map((m) => m.name))
    const tables = sqlite
      .prepare("SELECT name FROM sqlite_master WHERE type = 'table'")
      .pluck()
      .all()
    for (const t of ["documents", "chunks", "memories", "profiles", "jobs", "api_keys", "memories_fts", "memories_vec", "chunks_fts", "chunks_vec"]) {
      expect(tables).toContain(t)
    }
  })

  it("is idempotent", async () => {
    const { migrate } = await import("./migrate")
    const db = openDb(":memory:")
    expect(() => migrate(db.$client)).not.toThrow()
  })
})
