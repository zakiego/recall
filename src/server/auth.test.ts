import { describe, expect, it } from "vitest"
import { bearer, createApiKey, hashPassword, revokeApiKey, signSession, verifyApiKey, verifyPassword, verifySession } from "./auth"
import { openDb } from "./db"

describe("api keys", () => {
  it("verifies a created key until it's revoked", () => {
    const db = openDb(":memory:")
    const { key, row } = createApiKey(db, "test")
    expect(key).toMatch(/^rcl_[0-9A-Za-z]{32}$/)
    expect(row.keyHash).not.toContain(key)
    expect(verifyApiKey(db, key)).toMatchObject({ id: row.id })
    expect(verifyApiKey(db, `${key}x`)).toBeNull()
    expect(verifyApiKey(db, null)).toBeNull()
    expect(revokeApiKey(db, row.id)).toBe(true)
    expect(verifyApiKey(db, key)).toBeNull()
  })

  it("reads bearer and x-api-key headers", () => {
    expect(bearer(new Request("http://x", { headers: { authorization: "Bearer rcl_a" } }))).toBe("rcl_a")
    expect(bearer(new Request("http://x", { headers: { "x-api-key": "rcl_b" } }))).toBe("rcl_b")
  })
})

describe("admin auth", () => {
  it("hashes and verifies passwords", async () => {
    const hash = await hashPassword("correct horse battery")
    expect(hash).toMatch(/^pbkdf2\$sha256\$100000\$/)
    expect(await verifyPassword("correct horse battery", hash)).toBe(true)
    expect(await verifyPassword("wrong", hash)).toBe(false)
    expect(await verifyPassword("x", "garbage")).toBe(false)
  })

  it("signs sessions that expire and can't be forged", () => {
    const secret = "s".repeat(40)
    const token = signSession(secret, 1_000, 10_000)
    expect(verifySession(token, secret, 5_000)).toBe(true)
    expect(verifySession(token, secret, 20_000)).toBe(false)
    expect(verifySession(token, "other".repeat(10), 5_000)).toBe(false)
    expect(verifySession(`${token.split(".")[0]}x.${token.split(".")[1]}`, secret, 5_000)).toBe(false)
    expect(verifySession(token, "", 5_000)).toBe(false)
  })
})
