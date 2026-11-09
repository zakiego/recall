import { describe, expect, it } from "vitest"
import { bearer, createApiKey, hashLegacyPassword, hashPassword, listApiKeys, revokeApiKey, verifyApiKey, verifyLegacyPassword, verifyPassword } from "./auth"
import { openDb } from "./db"

describe("api keys", () => {
  it("verifies a created key until it's revoked, and carries its workspace", () => {
    const db = openDb(":memory:")
    const { key, row } = createApiKey(db, { workspaceId: "ws_a", name: "test" })
    expect(key).toMatch(/^rcl_[0-9A-Za-z]{32}$/)
    expect(row.keyHash).not.toContain(key)
    expect(verifyApiKey(db, key)).toMatchObject({ id: row.id, workspaceId: "ws_a" })
    expect(verifyApiKey(db, `${key}x`)).toBeNull()
    expect(verifyApiKey(db, null)).toBeNull()
    expect(revokeApiKey(db, "ws_a", row.id)).toBe(true)
    expect(verifyApiKey(db, key)).toBeNull()
  })

  it("lists and revokes only inside the key's workspace", () => {
    const db = openDb(":memory:")
    const a = createApiKey(db, { workspaceId: "ws_a", name: "a" })
    createApiKey(db, { workspaceId: "ws_b", name: "b" })
    expect(listApiKeys(db, "ws_a").map((k) => k.name)).toEqual(["a"])
    expect(listApiKeys(db, "ws_b").map((k) => k.name)).toEqual(["b"])
    expect(revokeApiKey(db, "ws_b", a.row.id)).toBe(false)
    expect(verifyApiKey(db, a.key)).not.toBeNull()
  })

  it("reads bearer and x-api-key headers", () => {
    expect(bearer(new Request("http://x", { headers: { authorization: "Bearer rcl_a" } }))).toBe("rcl_a")
    expect(bearer(new Request("http://x", { headers: { "x-api-key": "rcl_b" } }))).toBe("rcl_b")
  })
})

describe("passwords", () => {
  it("hashes new passwords with scrypt", async () => {
    const hash = await hashPassword("correct horse battery")
    expect(hash).not.toMatch(/^pbkdf2/)
    expect(await verifyPassword({ hash, password: "correct horse battery" })).toBe(true)
    expect(await verifyPassword({ hash, password: "wrong" })).toBe(false)
  })

  it("still verifies the old pbkdf2 admin hash", async () => {
    const hash = await hashLegacyPassword("correct horse battery")
    expect(hash).toMatch(/^pbkdf2\$sha256\$100000\$/)
    expect(await verifyLegacyPassword("correct horse battery", hash)).toBe(true)
    expect(await verifyPassword({ hash, password: "correct horse battery" })).toBe(true)
    expect(await verifyPassword({ hash, password: "wrong" })).toBe(false)
    expect(await verifyPassword({ hash: "garbage", password: "x" })).toBe(false)
  })
})
