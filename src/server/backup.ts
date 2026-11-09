// Consistent, gzipped snapshot of the live database. Uses SQLite's online
// backup API through better-sqlite3, so it's safe while the app is writing.
import fs from "node:fs"
import path from "node:path"
import { pipeline } from "node:stream/promises"
import { createGzip } from "node:zlib"
import Database from "better-sqlite3"

export type BackupResult = { dest: string; bytes: number; dbBytes: number }

export async function backupDb(src: string, dest: string): Promise<BackupResult> {
  fs.mkdirSync(path.dirname(dest), { recursive: true })
  const tmp = `${dest}.tmp-${process.pid}`
  // Plain connection: no migrations, no extensions. The backup copies pages,
  // so vec0 and FTS5 tables come along without their modules loaded.
  const live = new Database(src, { readonly: true, fileMustExist: true })
  try {
    live.pragma("busy_timeout = 5000")
    await live.backup(tmp)
  } finally {
    live.close()
  }
  try {
    const snap = new Database(tmp)
    try {
      // The copy inherits WAL mode. Switch it back so the snapshot is one
      // self-contained file; the app turns WAL on again when it opens it.
      snap.pragma("journal_mode = DELETE")
      const check = snap.pragma("quick_check", { simple: true })
      if (check !== "ok") throw new Error(`snapshot failed quick_check: ${String(check)}`)
    } finally {
      snap.close()
    }
    const dbBytes = fs.statSync(tmp).size
    await pipeline(fs.createReadStream(tmp), createGzip({ level: 9 }), fs.createWriteStream(dest))
    return { dest, bytes: fs.statSync(dest).size, dbBytes }
  } finally {
    for (const f of [tmp, `${tmp}-wal`, `${tmp}-shm`]) fs.rmSync(f, { force: true })
  }
}
