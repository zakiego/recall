import type Database from "better-sqlite3"

// The SQL files are inlined at build time, so .output needs no drizzle/ folder
// next to it. drizzle-kit writes them; this applies them in name order.
const files = import.meta.glob<string>("../../../drizzle/*.sql", {
  query: "?raw",
  import: "default",
  eager: true,
})

export const migrations = Object.entries(files)
  .map(([file, sql]) => ({ name: file.split("/").pop()!.replace(/\.sql$/, ""), sql }))
  .sort((a, b) => a.name.localeCompare(b.name))

export function migrate(db: Database.Database) {
  db.exec(`CREATE TABLE IF NOT EXISTS _migrations (
    name TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
  )`)
  const applied = new Set(
    db
      .prepare("SELECT name FROM _migrations")
      .pluck()
      .all() as string[],
  )
  const record = db.prepare("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)")
  for (const m of migrations) {
    if (applied.has(m.name)) continue
    db.transaction(() => {
      for (const stmt of m.sql.split("--> statement-breakpoint")) {
        if (stmt.trim()) db.exec(stmt)
      }
      record.run(m.name, Date.now())
    })()
  }
}
