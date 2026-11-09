import { createHash, pbkdf2 as pbkdf2Cb, randomBytes, timingSafeEqual } from "node:crypto"
import { promisify } from "node:util"
import { hashPassword as scryptHash, verifyPassword as scryptVerify } from "better-auth/crypto"
import { and, desc, eq, isNull } from "drizzle-orm"
import type { DB } from "./db"
import { apiKeys, user } from "./db/schema"
import { newId } from "./ids"

const pbkdf2 = promisify(pbkdf2Cb)

// --- API keys ---------------------------------------------------------------
// Every key belongs to one workspace and can only reach that workspace's rows.

const KEY_PREFIX = "rcl_"
const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

export function sha256(s: string) {
  return createHash("sha256").update(s).digest("hex")
}

export function randomToken(bytes = 32): string {
  let out = ""
  for (const b of randomBytes(bytes)) out += BASE62[b % 62]
  return out
}

export function generateApiKey(): string {
  return KEY_PREFIX + randomToken(32)
}

// Returns the plaintext key once. Only its hash is stored.
export function createApiKey(db: DB, input: { workspaceId: string; name: string; createdBy?: string | null }) {
  const key = generateApiKey()
  const row = db
    .insert(apiKeys)
    .values({
      id: newId("key"),
      workspaceId: input.workspaceId,
      name: input.name.trim() || "key",
      keyHash: sha256(key),
      prefix: key.slice(0, KEY_PREFIX.length + 6),
      createdBy: input.createdBy ?? null,
    })
    .returning()
    .get()
  return { key, row }
}

export function listApiKeys(db: DB, workspaceId: string) {
  return db
    .select({
      id: apiKeys.id,
      name: apiKeys.name,
      prefix: apiKeys.prefix,
      createdAt: apiKeys.createdAt,
      lastUsedAt: apiKeys.lastUsedAt,
      revokedAt: apiKeys.revokedAt,
      createdBy: user.displayUsername,
    })
    .from(apiKeys)
    .leftJoin(user, eq(user.id, apiKeys.createdBy))
    .where(eq(apiKeys.workspaceId, workspaceId))
    .orderBy(desc(apiKeys.createdAt))
    .all()
}

export function revokeApiKey(db: DB, workspaceId: string, id: string) {
  return (
    db
      .update(apiKeys)
      .set({ revokedAt: Date.now() })
      .where(and(eq(apiKeys.id, id), eq(apiKeys.workspaceId, workspaceId), isNull(apiKeys.revokedAt)))
      .run().changes > 0
  )
}

const lastUsedWrites = new Map<string, number>()

// createdBy is null for keys made before users existed.
export type KeyAuth = { id: string; name: string; workspaceId: string; createdBy: string | null }

export function verifyApiKey(db: DB, key: string | null | undefined): KeyAuth | null {
  if (!key || !key.startsWith(KEY_PREFIX)) return null
  const row = db
    .select({ id: apiKeys.id, name: apiKeys.name, workspaceId: apiKeys.workspaceId, createdBy: apiKeys.createdBy })
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

// --- Passwords ----------------------------------------------------------------
// New passwords use Better Auth's scrypt. The pbkdf2 format is what
// `pnpm cli hash-password` wrote for ADMIN_PASSWORD_HASH before users existed,
// and it still verifies, so the old admin password logs in as the first user.

const PBKDF2_ITERATIONS = 100_000
const PBKDF2_KEYLEN = 32
export const MIN_PASSWORD_LENGTH = 12
export const MAX_PASSWORD_LENGTH = 256

export async function hashLegacyPassword(password: string): Promise<string> {
  const salt = randomBytes(16)
  const hash = await pbkdf2(password, salt, PBKDF2_ITERATIONS, PBKDF2_KEYLEN, "sha256")
  return `pbkdf2$sha256$${PBKDF2_ITERATIONS}$${salt.toString("base64")}$${hash.toString("base64")}`
}

export async function verifyLegacyPassword(password: string, stored: string): Promise<boolean> {
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

export function isLegacyHash(stored: string) {
  return stored.startsWith("pbkdf2$")
}

export function hashPassword(password: string): Promise<string> {
  return scryptHash(password)
}

export async function verifyPassword({ hash, password }: { hash: string; password: string }): Promise<boolean> {
  if (isLegacyHash(hash)) return verifyLegacyPassword(password, hash)
  try {
    return await scryptVerify({ hash, password })
  } catch {
    return false
  }
}
