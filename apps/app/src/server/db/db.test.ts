import fs from "node:fs"
import path from "node:path"
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
    for (const t of [
      "documents", "chunks", "memories", "profiles", "jobs", "api_keys", "memories_fts", "memories_vec", "chunks_fts", "chunks_vec",
      "user", "session", "account", "verification", "passkey", "organization", "member", "invitation", "workspace_invites",
    ]) {
      expect(tables).toContain(t)
    }
  })

  it("is idempotent", async () => {
    const { migrate } = await import("./migrate")
    const db = openDb(":memory:")
    expect(() => migrate(db.$client)).not.toThrow()
  })
})

describe("workspace guard", () => {
  it("refuses data rows without a workspace", () => {
    const sqlite = openDb(":memory:").$client
    for (const sql of [
      "INSERT INTO memories (id, container, fact) VALUES ('m', 'c', 'f')",
      "INSERT INTO documents (id, container, source_type) VALUES ('d', 'c', 'text')",
      "INSERT INTO jobs (type, payload) VALUES ('ingest', '{}')",
      "INSERT INTO api_keys (id, name, key_hash, prefix) VALUES ('k', 'n', 'h', 'p')",
      "INSERT INTO profiles (workspace_id, container) VALUES ('', 'c')",
    ]) {
      expect(() => sqlite.prepare(sql).run()).toThrow(/workspace_id is required/)
    }
    sqlite.prepare("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('m', 'ws', 'c', 'f')").run()
    expect(() => sqlite.prepare("UPDATE memories SET workspace_id = '' WHERE id = 'm'").run()).toThrow(/workspace_id is required/)
  })
})

describe("migration names", () => {
  it("never reuses a number", () => {
    // Both 0006 files shipped before this check and are recorded by name in
    // production, so they stay. Every later migration needs its own number.
    const ALLOWED_DUPLICATES = new Set(["0006"])
    const seen = new Map<string, string[]>()
    for (const m of migrations) {
      const n = m.name.slice(0, 4)
      seen.set(n, [...(seen.get(n) ?? []), m.name])
    }
    const dupes = [...seen].filter(([n, names]) => names.length > 1 && !ALLOWED_DUPLICATES.has(n))
    expect(dupes).toEqual([])
  })
})

describe("hand-written migrations", () => {
  it("are all bundled into migrations.gen.ts (run db:bundle after adding one)", () => {
    const dir = path.resolve(import.meta.dirname, "../../../drizzle")
    const files = fs.readdirSync(dir).filter((f) => f.endsWith(".sql")).sort()
    expect(migrations.map((m) => m.name)).toEqual(files.map((f) => f.replace(/\.sql$/, "")))
    for (const m of migrations) expect(m.sql, m.name).toBe(fs.readFileSync(path.join(dir, `${m.name}.sql`), "utf8"))
  })
})
