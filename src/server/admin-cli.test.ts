import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { describe, expect, it, vi } from "vitest"
import { runCli, type CliIo } from "./admin-cli"
import { verifyApiKey, verifyPassword } from "./auth"
import { openDb } from "./db"

function harness(password = "") {
  const db = openDb(":memory:")
  const out: string[] = []
  const err: string[] = []
  const io: CliIo = { db: () => db, out: (l) => out.push(l), err: (l) => err.push(l), readPassword: async () => password }
  return { db, out, err, run: (...argv: string[]) => runCli(argv, io) }
}

describe("admin cli", () => {
  it("create-key --quiet prints only the key", async () => {
    const h = harness()
    expect(await h.run("create-key", "ci", "--quiet")).toBe(0)
    expect(h.out).toHaveLength(1)
    expect(h.out[0]).toMatch(/^rcl_[0-9A-Za-z]{32}$/)
    expect(verifyApiKey(h.db, h.out[0])).toMatchObject({ name: "ci" })
  })

  it("creates, lists and revokes with --json", async () => {
    const h = harness()
    await h.run("create-key", "my", "agent", "--json")
    const created = JSON.parse(h.out[0] ?? "")
    expect(created).toMatchObject({ name: "my agent", key: expect.stringMatching(/^rcl_/) })

    await h.run("list-keys", "--json")
    expect(JSON.parse(h.out[1] ?? "")).toMatchObject([{ id: created.id, revokedAt: null }])

    expect(await h.run("revoke-key", created.id, "--json")).toBe(0)
    expect(JSON.parse(h.out[2] ?? "")).toEqual({ id: created.id, revoked: true })
    expect(verifyApiKey(h.db, created.key)).toBeNull()
    expect(await h.run("revoke-key", created.id)).toBe(1)
    expect(h.err).toEqual(["no such active key"])
  })

  it("hashes a password and rejects short ones", async () => {
    const ok = harness("correct horse battery")
    expect(await ok.run("hash-password")).toBe(0)
    expect(await verifyPassword("correct horse battery", ok.out[0] ?? "")).toBe(true)
    expect(await harness("short").run("hash-password")).toBe(1)
  })

  it("backup snapshots RECALL_DB_PATH and needs a destination", async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "recall-cli-backup-"))
    try {
      const file = path.join(dir, "recall.db")
      openDb(file).$client.close()
      vi.stubEnv("RECALL_DB_PATH", file)
      const h = harness()
      expect(await h.run("backup")).toBe(1)
      const dest = path.join(dir, "backups", "snap.db.gz")
      expect(await h.run("backup", dest, "--json")).toBe(0)
      expect(JSON.parse(h.out[0] ?? "")).toMatchObject({ dest, bytes: fs.statSync(dest).size })
    } finally {
      vi.unstubAllEnvs()
      fs.rmSync(dir, { recursive: true, force: true })
    }
  })

  it("fails on unknown commands and flags", async () => {
    expect(await harness().run("nope")).toBe(1)
    expect(await harness().run("list-keys", "--bogus")).toBe(1)
    expect(await harness().run()).toBe(0)
  })
})
