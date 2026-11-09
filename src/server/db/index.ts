import fs from "node:fs"
import path from "node:path"
import Database from "better-sqlite3"
import { drizzle, type BetterSQLite3Database } from "drizzle-orm/better-sqlite3"
import * as sqliteVec from "sqlite-vec"
import { config } from "../config"
import { migrate } from "./migrate"
import * as schema from "./schema"

export type Sqlite = Database.Database
export type DB = BetterSQLite3Database<typeof schema> & { $client: Sqlite }

export function openDb(file: string): DB {
  if (file !== ":memory:") fs.mkdirSync(path.dirname(file), { recursive: true })
  const sqlite = new Database(file)
  sqlite.pragma("journal_mode = WAL")
  sqlite.pragma("synchronous = NORMAL")
  sqlite.pragma("foreign_keys = ON")
  sqlite.pragma("busy_timeout = 5000")
  sqliteVec.load(sqlite)
  migrate(sqlite)
  return drizzle(sqlite, { schema })
}

let instance: DB | undefined

// One connection per process. better-sqlite3 is synchronous, so sharing it
// between the request path and the job loop is safe.
export function getDb(): DB {
  instance ??= openDb(config().dbPath)
  return instance
}

// Tests swap in a temp DB.
export function setDb(db: DB | undefined) {
  instance = db
}

export { schema }
