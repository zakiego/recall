import type Database from "better-sqlite3"
import { migrations } from "./migrations.gen"

export { migrations }

// `list` is for the migration tests, which stop at an older schema first.
export function migrate(db: Database.Database, list: { name: string; sql: string }[] = migrations) {
  db.exec(`CREATE TABLE IF NOT EXISTS _migrations (
    name TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
  )`)
  const applied = new Set(db.prepare("SELECT name FROM _migrations").pluck().all() as string[])
  const record = db.prepare("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)")
  for (const m of list) {
    if (applied.has(m.name)) continue
    db.transaction(() => {
      for (const stmt of m.sql.split("--> statement-breakpoint")) {
        if (stmt.trim()) db.exec(stmt)
      }
      record.run(m.name, Date.now())
    })()
  }
}
