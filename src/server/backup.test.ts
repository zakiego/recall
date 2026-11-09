import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { gunzipSync } from "node:zlib"
import Database from "better-sqlite3"
import { afterEach, describe, expect, it } from "vitest"
import { backupDb } from "./backup"
import { openDb } from "./db"

const dirs: string[] = []
afterEach(() => {
  for (const d of dirs.splice(0)) fs.rmSync(d, { recursive: true, force: true })
})

describe("backupDb", () => {
  it("writes a gzipped snapshot that includes uncheckpointed WAL writes", async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-backup-"))
    dirs.push(dir)
    const db = openDb(path.join(dir, "recall.db"))
    const sqlite = db.$client
    sqlite.pragma("wal_autocheckpoint = 0") // keep the rows in the WAL
    const insert = sqlite.prepare(
      "INSERT INTO memories (id, container, fact, keywords, kind, valid_from, created_at) VALUES (?, 'c', ?, '', 'static', 0, 0)",
    )
    for (let i = 0; i < 3; i++) insert.run(`mem_${i}`, `fact ${i}`)
    expect(fs.statSync(path.join(dir, "recall.db-wal")).size).toBeGreaterThan(0)

    const dest = path.join(dir, "backups", "snap.db.gz")
    const res = await backupDb(path.join(dir, "recall.db"), dest)
    sqlite.close()

    expect(res.dest).toBe(dest)
    expect(res.bytes).toBe(fs.statSync(dest).size)
    expect(fs.readdirSync(path.dirname(dest))).toEqual(["snap.db.gz"]) // temp file removed

    const restored = path.join(dir, "restored.db")
    fs.writeFileSync(restored, gunzipSync(fs.readFileSync(dest)))
    expect(fs.statSync(restored).size).toBe(res.dbBytes)
    const check = new Database(restored, { readonly: true })
    expect(check.prepare("SELECT count(*) FROM memories").pluck().get()).toBe(3)
    check.close()
  })

  it("fails when the source does not exist", async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-backup-"))
    dirs.push(dir)
    await expect(backupDb(path.join(dir, "missing.db"), path.join(dir, "x.db.gz"))).rejects.toThrow()
  })
})
