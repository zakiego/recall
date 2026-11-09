import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { describe, expect, it, vi } from "vitest"
import { runCli, type CliIo } from "./admin-cli"
import { verifyApiKey, verifyPassword } from "./auth"
import { openDb } from "./db"
import { getRole, listMembers } from "./users"

function harness(password = "") {
  const db = openDb(":memory:")
  const out: string[] = []
  const err: string[] = []
  const io: CliIo = { db: () => db, out: (l) => out.push(l), err: (l) => err.push(l), readPassword: async () => password }
  return { db, out, err, run: (...argv: string[]) => runCli(argv, io) }
}

describe("admin cli", () => {
  it("create-user makes an owner of a new workspace, or joins a named one", async () => {
    const h = harness("a long enough password")
    expect(await h.run("create-user", "zaki", "--json")).toBe(0)
    const zaki = JSON.parse(h.out[0] ?? "")
    expect(zaki).toMatchObject({ username: "zaki", workspace: "zaki", role: "owner" })
    expect(await h.run("create-user", "eva", "--workspace", "zaki", "--role=member")).toBe(0)
    const eva = h.db.$client.prepare("SELECT id FROM user WHERE username = 'eva'").get() as { id: string }
    const ws = h.db.$client.prepare("SELECT id FROM organization WHERE slug = 'zaki'").get() as { id: string }
    expect(getRole(h.db, eva.id, ws.id)).toBe("member")
    expect(listMembers(h.db, ws.id).map((m) => m.username)).toEqual(["zaki", "eva"])
    expect(await h.run("create-user", "eva")).toBe(1)
    expect(h.err.at(-1)).toMatch(/taken/)
    expect(await h.run("create-user", "x", "--role", "boss")).toBe(1)
  })

  it("create-user refuses an unknown workspace instead of creating it", async () => {
    const h = harness("a long enough password")
    await h.run("create-user", "zaki")
    const workspaces = () => (h.db.$client.prepare("SELECT slug FROM organization ORDER BY slug").all() as { slug: string }[]).map((w) => w.slug)
    const users = () => (h.db.$client.prepare("SELECT username FROM user ORDER BY username").all() as { username: string }[]).map((u) => u.username)
    // A typo fails and leaves nothing behind.
    expect(await h.run("create-user", "eva", "--workspace", "zakii", "--role", "member")).toBe(1)
    expect(h.err.at(-1)).toMatch(/no workspace "zakii".*--new-workspace/)
    // A new workspace can't start without an owner, with or without --workspace.
    expect(await h.run("create-user", "eva", "--workspace", "team", "--new-workspace", "--role", "member")).toBe(1)
    expect(h.err.at(-1)).toMatch(/needs an owner/)
    expect(await h.run("create-user", "eva", "--role", "admin")).toBe(1)
    expect(h.err.at(-1)).toMatch(/needs an owner/)
    // --new-workspace never joins an existing one.
    expect(await h.run("create-user", "eva", "--workspace", "zaki", "--new-workspace")).toBe(1)
    expect(h.err.at(-1)).toMatch(/already exists/)
    expect(workspaces()).toEqual(["zaki"])
    expect(users()).toEqual(["zaki"])

    expect(await h.run("create-user", "eva", "--workspace", "team", "--new-workspace", "--json")).toBe(0)
    expect(JSON.parse(h.out.at(-1)!)).toMatchObject({ username: "eva", workspace: "team", role: "owner" })
    expect(workspaces()).toEqual(["team", "zaki"])
  })

  it("create-user leaves no user behind when the password is too short", async () => {
    const h = harness("short")
    expect(await h.run("create-user", "zaki")).toBe(1)
    expect(h.err.at(-1)).toMatch(/at least 12/)
    expect(h.db.$client.prepare("SELECT count(*) AS n FROM user").get()).toEqual({ n: 0 })
    expect(h.db.$client.prepare("SELECT count(*) AS n FROM organization").get()).toEqual({ n: 0 })
  })

  it("create-key --quiet prints only the key, scoped to the only workspace", async () => {
    const h = harness("a long enough password")
    await h.run("create-user", "zaki")
    expect(await h.run("create-key", "ci", "--quiet")).toBe(0)
    const key = h.out.at(-1)!
    expect(key).toMatch(/^rcl_[0-9A-Za-z]{32}$/)
    expect(verifyApiKey(h.db, key)).toMatchObject({ name: "ci" })
  })

  it("needs --workspace when there are several, and refuses with none", async () => {
    const h = harness("a long enough password")
    expect(await h.run("create-key", "ci")).toBe(1)
    expect(h.err.at(-1)).toMatch(/no workspaces yet/)
    await h.run("create-user", "zaki")
    await h.run("create-user", "rina")
    expect(await h.run("create-key", "ci")).toBe(1)
    expect(h.err.at(-1)).toMatch(/pass --workspace/)
    expect(await h.run("create-key", "ci", "--workspace", "rina", "--json")).toBe(0)
    expect(JSON.parse(h.out.at(-1)!)).toMatchObject({ workspace: "rina" })
  })

  it("creates, lists and revokes with --json", async () => {
    const h = harness("a long enough password")
    await h.run("create-user", "zaki")
    await h.run("create-key", "my", "agent", "--json")
    const created = JSON.parse(h.out.at(-1) ?? "")
    expect(created).toMatchObject({ name: "my agent", key: expect.stringMatching(/^rcl_/) })

    await h.run("list-keys", "--json")
    expect(JSON.parse(h.out.at(-1) ?? "")).toMatchObject([{ id: created.id, revokedAt: null }])

    expect(await h.run("revoke-key", created.id, "--json")).toBe(0)
    expect(JSON.parse(h.out.at(-1) ?? "")).toEqual({ id: created.id, revoked: true })
    expect(verifyApiKey(h.db, created.key)).toBeNull()
    expect(await h.run("revoke-key", created.id)).toBe(1)
    expect(h.err).toEqual(["no such active key"])
  })

  it("hashes a password for ADMIN_PASSWORD_HASH and rejects short ones", async () => {
    const ok = harness("correct horse battery")
    expect(await ok.run("hash-password")).toBe(0)
    expect(ok.out[0]).toMatch(/^pbkdf2\$/)
    expect(await verifyPassword({ hash: ok.out[0] ?? "", password: "correct horse battery" })).toBe(true)
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
