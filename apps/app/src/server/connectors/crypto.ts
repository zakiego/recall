// Seals connector secrets (OAuth tokens, the OAuth state cookie) with
// AES-256-GCM. The key is derived from CONNECTOR_SECRET with HKDF, so the env
// value can be any long random string. Each value is bound to a context string
// (the row it belongs to), so a ciphertext copied into another row won't open.
import { createCipheriv, createDecipheriv, hkdfSync, randomBytes } from "node:crypto"

export const MIN_SECRET_LENGTH = 32
const VERSION = "v1"

const keys = new Map<string, Buffer>()
function deriveKey(secret: string) {
  let key = keys.get(secret)
  if (!key) {
    key = Buffer.from(hkdfSync("sha256", secret, "recall-connectors", "connector-tokens-v1", 32))
    keys.set(secret, key)
  }
  return key
}

export class SealError extends Error {}

export function seal(secret: string, plaintext: string, context: string): string {
  if (secret.length < MIN_SECRET_LENGTH) throw new SealError("CONNECTOR_SECRET is too short")
  const iv = randomBytes(12)
  const cipher = createCipheriv("aes-256-gcm", deriveKey(secret), iv)
  cipher.setAAD(Buffer.from(context))
  const body = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()])
  return [VERSION, iv.toString("base64url"), body.toString("base64url"), cipher.getAuthTag().toString("base64url")].join(".")
}

// Throws SealError on a wrong key, a wrong context or a tampered value.
export function unseal(secret: string, sealed: string, context: string): string {
  const [version, iv, body, tag] = sealed.split(".")
  if (version !== VERSION || !iv || !body || !tag) throw new SealError("unreadable sealed value")
  try {
    const decipher = createDecipheriv("aes-256-gcm", deriveKey(secret), Buffer.from(iv, "base64url"))
    decipher.setAAD(Buffer.from(context))
    decipher.setAuthTag(Buffer.from(tag, "base64url"))
    return Buffer.concat([decipher.update(Buffer.from(body, "base64url")), decipher.final()]).toString("utf8")
  } catch {
    throw new SealError("sealed value didn't open (wrong CONNECTOR_SECRET or tampered)")
  }
}
