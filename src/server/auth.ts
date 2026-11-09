import { createHash, createHmac, pbkdf2 as pbkdf2Cb, randomBytes, timingSafeEqual } from "node:crypto"
import { promisify } from "node:util"
import { and, desc, eq, isNull } from "drizzle-orm"
import type { DB } from "./db"
import { apiKeys } from "./db/schema"
import { newId } from "./ids"

const pbkdf2 = promisify(pbkdf2Cb)

// --- API keys ---------------------------------------------------------------

const KEY_PREFIX = "rcl_"
const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

export function sha256(s: string) {
  return createHash("sha256").update(s).digest("hex")
}

export function generateApiKey(): string {
  let out = ""
  for (const b of randomBytes(32)) out += BASE62[b % 62]
  return KEY_PREFIX + out
}

// Returns the plaintext key once. Only its hash is stored.
export function createApiKey(db: DB, name: string) {
  const key = generateApiKey()
  const row = db
    .insert(apiKeys)
    .values({ id: newId("key"), name: name.trim() || "key", keyHash: sha256(key), prefix: key.slice(0, KEY_PREFIX.length + 6) })
    .returning()
    .get()
  return { key, row }
}

export function listApiKeys(db: DB) {
  return db
    .select({
      id: apiKeys.id,
      name: apiKeys.name,
      prefix: apiKeys.prefix,
      createdAt: apiKeys.createdAt,
      lastUsedAt: apiKeys.lastUsedAt,
      revokedAt: apiKeys.revokedAt,
    })
    .from(apiKeys)
    .orderBy(desc(apiKeys.createdAt))
    .all()
}

export function revokeApiKey(db: DB, id: string) {
  return db
    .update(apiKeys)
    .set({ revokedAt: Date.now() })
    .where(and(eq(apiKeys.id, id), isNull(apiKeys.revokedAt)))
    .run().changes > 0
}

const lastUsedWrites = new Map<string, number>()

export function verifyApiKey(db: DB, key: string | null | undefined) {
  if (!key || !key.startsWith(KEY_PREFIX)) return null
  const row = db
    .select({ id: apiKeys.id, name: apiKeys.name })
    .from(apiKeys)
    .where(and(eq(apiKeys.keyHash, sha256(key)), isNull(apiKeys.revokedAt)))
    .get()
  if (!row) return null
  // Touch last_used_at at most once a minute per key.
  const now = Date.now()
  if ((lastUsedWrites.get(row.id) ?? 0) < now - 60_000) {
    lastUsedWrites.set(row.id, now)
    db.update(apiKeys).set({ lastUsedAt: now }).where(eq(apiKeys.id, row.id)).run()
  }
  return row
}

export function bearer(request: Request): string | null {
  const h = request.headers.get("authorization")
  if (h?.toLowerCase().startsWith("bearer ")) return h.slice(7).trim()
  return request.headers.get("x-api-key")
}

// --- Admin password + session (same scheme as ai-gateway) ------------------

const PBKDF2_ITERATIONS = 100_000
const PBKDF2_KEYLEN = 32
export const SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000
export const SESSION_COOKIE = "recall_session"

export async function hashPassword(password: string): Promise<string> {
  const salt = randomBytes(16)
  const hash = await pbkdf2(password, salt, PBKDF2_ITERATIONS, PBKDF2_KEYLEN, "sha256")
  return `pbkdf2$sha256$${PBKDF2_ITERATIONS}$${salt.toString("base64")}$${hash.toString("base64")}`
}

export async function verifyPassword(password: string, stored: string): Promise<boolean> {
  const parts = stored.split("$")
  if (parts.length !== 5 || parts[0] !== "pbkdf2" || parts[1] !== "sha256") return false
  const iterations = Number(parts[2])
  if (!Number.isInteger(iterations) || iterations < 1) return false
  const salt = Buffer.from(parts[3] ?? "", "base64")
  const expected = Buffer.from(parts[4] ?? "", "base64")
  if (salt.length === 0 || expected.length === 0) return false
  const actual = await pbkdf2(password, salt, iterations, expected.length, "sha256")
  return timingSafeEqual(actual, expected)
}

export function signSession(secret: string, now = Date.now(), ttlMs = SESSION_TTL_MS): string {
  const payload = Buffer.from(JSON.stringify({ exp: now + ttlMs })).toString("base64url")
  const sig = createHmac("sha256", secret).update(payload).digest("base64url")
  return `${payload}.${sig}`
}

export function verifySession(token: string, secret: string, now = Date.now()): boolean {
  const dot = token.indexOf(".")
  if (dot <= 0 || !secret) return false
  const payload = token.slice(0, dot)
  const sig = Buffer.from(token.slice(dot + 1), "base64url")
  const expected = createHmac("sha256", secret).update(payload).digest()
  if (sig.length !== expected.length || !timingSafeEqual(sig, expected)) return false
  try {
    const { exp } = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as { exp?: unknown }
    return typeof exp === "number" && exp > now
  } catch {
    return false
  }
}
