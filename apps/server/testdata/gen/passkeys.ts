// Passkey golden (copied from Palui's port, pointed at Recall): registers
// software authenticators through the real Better Auth passkey endpoints
// (Recall's authHandler, so the origin check and rate limits are the
// production ones), signs in with each, and writes testdata/passkeys.json:
//
//   - registrations: the attestation response Better Auth verified and the
//     `passkey` row it stored (every column as SQLite holds it)
//   - tsLogins: assertions Better Auth accepted (proves the stored row works)
//   - assertions: more assertions signed by the same keys, with the challenge
//     and the stored counter they were made for, plus ones that must fail
//
// The Go test reads the stored rows, verifies every assertion, and registers
// the same attestation responses to check it writes the same encoding.
//
// Run from apps/app: npx tsx ../server/testdata/gen/passkeys.ts
import { createHash, createSign, generateKeyPairSync, randomBytes, type KeyObject } from "node:crypto"
import { mkdtempSync, writeFileSync } from "node:fs"
import os from "node:os"
import path from "node:path"

const ORIGIN = "https://recall.example.com"
const RP_ID = "recall.example.com"
process.env.RECALL_URL = ORIGIN
process.env.SESSION_SECRET = "passkey-golden-auth-secret-0123456789abcdef"
process.env.DATA_DIR = mkdtempSync(path.join(os.tmpdir(), "recall-passkeys-"))
const { openDb } = await import("../../../app/src/server/db/index.ts")
const { authHandler, getAuth } = await import("../../../app/src/server/better-auth.ts")
const { createUserWithPassword, createWorkspace, addMember } = await import("../../../app/src/server/users.ts")

const db = openDb(":memory:")
const handle = authHandler(db)
const b64url = (b: Uint8Array | Buffer) => Buffer.from(b).toString("base64url")
const sha256 = (b: Uint8Array | Buffer | string) => createHash("sha256").update(b).digest()

// --- minimal CBOR (what attestation objects and COSE keys need) --------------------

function head(major: number, n: number): Buffer {
  if (n < 24) return Buffer.from([(major << 5) | n])
  if (n < 0x100) return Buffer.from([(major << 5) | 24, n])
  if (n < 0x10000) return Buffer.from([(major << 5) | 25, n >> 8, n & 0xff])
  const b = Buffer.alloc(5)
  b[0] = (major << 5) | 26
  b.writeUInt32BE(n, 1)
  return b
}
type Cbor = number | string | Buffer | Map<Cbor, Cbor>
function cbor(v: Cbor): Buffer {
  if (typeof v === "number") return v >= 0 ? head(0, v) : head(1, -1 - v)
  if (typeof v === "string") {
    const s = Buffer.from(v, "utf8")
    return Buffer.concat([head(3, s.length), s])
  }
  if (Buffer.isBuffer(v)) return Buffer.concat([head(2, v.length), v])
  const parts: Buffer[] = [head(5, v.size)]
  for (const [k, val] of v) parts.push(cbor(k), cbor(val))
  return Buffer.concat(parts)
}

// --- software authenticator --------------------------------------------------------

type Authenticator = {
  name: string
  alg: -7 | -257
  privateKey: KeyObject
  cose: Buffer
  credId: Buffer
  flags: number
  counter: number
  aaguid: Buffer
  transports: string[]
}

function makeAuthenticator(name: string, alg: -7 | -257, opts: { flags: number; counter: number; aaguid: string; transports: string[] }): Authenticator {
  let privateKey: KeyObject
  let cose: Buffer
  if (alg === -7) {
    const kp = generateKeyPairSync("ec", { namedCurve: "P-256" })
    privateKey = kp.privateKey
    const jwk = kp.publicKey.export({ format: "jwk" })
    cose = cbor(
      new Map<Cbor, Cbor>([
        [1, 2],
        [3, -7],
        [-1, 1],
        [-2, Buffer.from(jwk.x!, "base64url")],
        [-3, Buffer.from(jwk.y!, "base64url")],
      ]),
    )
  } else {
    const kp = generateKeyPairSync("rsa", { modulusLength: 2048 })
    privateKey = kp.privateKey
    const jwk = kp.publicKey.export({ format: "jwk" })
    cose = cbor(
      new Map<Cbor, Cbor>([
        [1, 3],
        [3, -257],
        [-1, Buffer.from(jwk.n!, "base64url")],
        [-2, Buffer.from(jwk.e!, "base64url")],
      ]),
    )
  }
  return { name, alg, privateKey, cose, credId: randomBytes(name === "rsa" ? 32 : 16), flags: opts.flags, counter: opts.counter, aaguid: Buffer.from(opts.aaguid.replace(/-/g, ""), "hex"), transports: opts.transports }
}

function counterBytes(n: number): Buffer {
  const b = Buffer.alloc(4)
  b.writeUInt32BE(n)
  return b
}

function attestation(a: Authenticator, challenge: string, origin = ORIGIN) {
  const clientDataJSON = Buffer.from(JSON.stringify({ type: "webauthn.create", challenge, origin, crossOrigin: false }))
  const len = Buffer.alloc(2)
  len.writeUInt16BE(a.credId.length)
  const authData = Buffer.concat([sha256(RP_ID), Buffer.from([a.flags]), counterBytes(a.counter), a.aaguid, len, a.credId, a.cose])
  const attestationObject = cbor(
    new Map<Cbor, Cbor>([
      ["fmt", "none"],
      ["attStmt", new Map()],
      ["authData", authData],
    ]),
  )
  return {
    id: b64url(a.credId),
    rawId: b64url(a.credId),
    type: "public-key",
    response: { clientDataJSON: b64url(clientDataJSON), attestationObject: b64url(attestationObject), transports: a.transports },
    clientExtensionResults: {},
    authenticatorAttachment: "platform",
  }
}

function assertion(a: Authenticator, challenge: string, counter: number, o: { origin?: string; corrupt?: boolean; flags?: number } = {}) {
  const clientDataJSON = Buffer.from(JSON.stringify({ type: "webauthn.get", challenge, origin: o.origin ?? ORIGIN, crossOrigin: false }))
  // Assertions carry UP|UV plus the backup bits, never AT.
  const flags = o.flags ?? a.flags & ~0x40
  const authData = Buffer.concat([sha256(RP_ID), Buffer.from([flags]), counterBytes(counter)])
  const signer = createSign("SHA256")
  signer.update(Buffer.concat([authData, sha256(clientDataJSON)]))
  const signature = signer.sign(a.privateKey)
  if (o.corrupt) signature[signature.length - 1] ^= 0xff
  return {
    id: b64url(a.credId),
    rawId: b64url(a.credId),
    type: "public-key",
    response: { clientDataJSON: b64url(clientDataJSON), authenticatorData: b64url(authData), signature: b64url(signature), userHandle: b64url(Buffer.from("random-user-handle-from-better-auth")) },
    clientExtensionResults: {},
    authenticatorAttachment: "platform",
  }
}

// --- Better Auth round trips ----------------------------------------------------------

class Jar {
  cookies = new Map<string, string>()
  take(res: Response) {
    for (const c of res.headers.getSetCookie()) {
      const [pair] = c.split(";")
      const i = pair!.indexOf("=")
      const name = pair!.slice(0, i)
      const value = pair!.slice(i + 1)
      if (value === "" || /max-age=0/i.test(c)) this.cookies.delete(name)
      else this.cookies.set(name, value)
    }
  }
  header() {
    return [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ")
  }
}

async function call(jar: Jar, method: "GET" | "POST", p: string, body?: unknown) {
  const headers: Record<string, string> = { origin: ORIGIN, "cf-connecting-ip": "203.0.113.7" }
  if (jar.cookies.size) headers.cookie = jar.header()
  if (body !== undefined) headers["content-type"] = "application/json"
  const res = await handle(new Request(`${ORIGIN}/api/auth${p}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) }))
  jar.take(res)
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${p} -> ${res.status} ${text}`)
  return text ? JSON.parse(text) : null
}

// A user with a workspace signs in with a password, as the login page does.
const PW = "a long enough password"
const owner = await createUserWithPassword(db, { username: "zaki", password: PW })
addMember(db, { workspaceId: createWorkspace(db, { name: "Zaki", id: "ws_zaki" }).id, userId: owner.id, role: "owner" })
const ownerJar = new Jar()
{
  const res = await getAuth(db).api.signInUsername({ body: { username: "zaki", password: PW }, asResponse: true })
  ownerJar.take(res)
}

const authenticators = [
  makeAuthenticator("es256-single", -7, { flags: 0x45, counter: 0, aaguid: "00000000-0000-0000-0000-000000000000", transports: ["internal"] }),
  makeAuthenticator("es256-synced", -7, { flags: 0x5d, counter: 3, aaguid: "fbfc3007-154e-4ecc-8c0b-6e020557d7bd", transports: ["hybrid", "internal"] }),
  makeAuthenticator("rsa", -257, { flags: 0x45, counter: 10, aaguid: "08987058-cadc-4b81-b6e1-30de50dcbe96", transports: [] }),
]

const COLUMNS = "id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid"
const registrations: unknown[] = []
for (const a of authenticators) {
  const options = await call(ownerJar, "GET", "/passkey/generate-register-options")
  const response = attestation(a, options.challenge)
  await call(ownerJar, "POST", "/passkey/verify-registration", { response, name: a.name })
  const row = db.$client.prepare(`SELECT ${COLUMNS} FROM passkey WHERE credential_id = ?`).get(b64url(a.credId))
  registrations.push({ name: a.name, challenge: options.challenge, options: { rp: options.rp, pubKeyCredParams: options.pubKeyCredParams, authenticatorSelection: options.authenticatorSelection, attestation: options.attestation }, response, row })
}

// Sign in with each through Better Auth, from a fresh browser (no session, so no allowCredentials).
const tsLogins: unknown[] = []
for (const a of authenticators) {
  const jar = new Jar()
  const options = await call(jar, "GET", "/passkey/generate-authenticate-options")
  a.counter = a.counter === 0 ? 0 : a.counter + 1
  const response = assertion(a, options.challenge, a.counter)
  const out = await call(jar, "POST", "/passkey/verify-authentication", { response })
  const stored = db.$client.prepare("SELECT counter FROM passkey WHERE credential_id = ?").get(b64url(a.credId)) as { counter: number }
  tsLogins.push({ name: a.name, allowCredentials: options.allowCredentials ?? null, userId: out.user.id, counterAfter: stored.counter })
}

// Assertions for the Go side, against the rows as they are now.
const rows = Object.fromEntries(authenticators.map((a) => [a.name, db.$client.prepare(`SELECT ${COLUMNS} FROM passkey WHERE credential_id = ?`).get(b64url(a.credId))]))
const challenge = () => b64url(randomBytes(32))
const assertions: unknown[] = []
for (const a of authenticators) {
  const stored = a.counter
  const next = stored === 0 ? 0 : stored + 1
  const c1 = challenge()
  assertions.push({ name: a.name, case: "ok", challenge: c1, storedCounter: stored, newCounter: next, ok: true, response: assertion(a, c1, next) })
  const c2 = challenge()
  assertions.push({ name: a.name, case: "wrong origin", challenge: c2, storedCounter: stored, ok: false, response: assertion(a, c2, next, { origin: "https://evil.example" }) })
  const c3 = challenge()
  assertions.push({ name: a.name, case: "bad signature", challenge: c3, storedCounter: stored, ok: false, response: assertion(a, c3, next, { corrupt: true }) })
  const c4 = challenge()
  assertions.push({ name: a.name, case: "other challenge", challenge: c4, storedCounter: stored, ok: false, response: assertion(a, challenge(), next) })
  if (stored > 0) {
    const c5 = challenge()
    assertions.push({ name: a.name, case: "counter went back", challenge: c5, storedCounter: stored, ok: false, response: assertion(a, c5, stored - 1) })
  }
}

const out = path.resolve(import.meta.dirname, "..", "passkeys.json")
writeFileSync(out, `${JSON.stringify({ origin: ORIGIN, rpId: RP_ID, ownerId: owner.id, registrations, tsLogins, rows, assertions }, null, 2)}\n`)
console.log(`wrote ${out}: ${registrations.length} registrations, ${tsLogins.length} TS logins, ${assertions.length} assertions`)
