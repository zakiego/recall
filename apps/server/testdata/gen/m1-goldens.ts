// M1 goldens from the real TS code: schema after migrations, config defaults
// and parsing, ids, API keys, password hashes of both forms, connector crypto,
// usernames, the rate limiter and /health. Writes testdata/m1.json.
//
// Run from apps/app: npx tsx ../server/testdata/gen/m1-goldens.ts
import fs from "node:fs"
import path from "node:path"
import { bearer, generateApiKey, hashLegacyPassword, hashPassword, randomToken, sha256, verifyPassword } from "../../../app/src/server/auth.ts"
import { config } from "../../../app/src/server/config.ts"
import { seal, unseal } from "../../../app/src/server/connectors/crypto.ts"
import { openDb } from "../../../app/src/server/db/index.ts"
import { migrations } from "../../../app/src/server/db/migrate.ts"
import { health } from "../../../app/src/server/health.ts"
import { newId } from "../../../app/src/server/ids.ts"
import { RateLimiter } from "../../../app/src/server/rate-limit.ts"
import { normalizeUsername, placeholderEmail, usernameProblem } from "../../../app/src/server/usernames.ts"

const out = path.resolve(import.meta.dirname, "../m1.json")

const ENV_KEYS = [
  "RECALL_URL",
  "DATA_DIR",
  "RECALL_DB_PATH",
  "MODELS_DIR",
  "RECALL_EMBED",
  "NODE_ENV",
  "RECALL_EMBED_IDLE_MS",
  "GATEWAY_URL",
  "GATEWAY_KEY",
  "EXTRACT_MODEL",
  "FALLBACK_MODEL",
  "RECALL_DEFAULT_CONTAINER",
  "ADMIN_PASSWORD_HASH",
  "SESSION_SECRET",
  "PASSKEY_RP_ID",
  "RECALL_AUTH_RATE_LIMIT",
  "RECALL_JOBS",
  "GOOGLE_CLIENT_ID",
  "GOOGLE_CLIENT_SECRET",
  "CONNECTOR_SECRET",
  "RECALL_GMAIL_FAKE",
  "RESEND_API_KEY",
  "RECALL_EMAIL_FROM",
]

function withEnv(env: Record<string, string>) {
  const saved: Record<string, string | undefined> = {}
  for (const k of ENV_KEYS) {
    saved[k] = process.env[k]
    delete process.env[k]
  }
  Object.assign(process.env, env)
  try {
    return config()
  } finally {
    for (const k of ENV_KEYS) {
      if (saved[k] === undefined) delete process.env[k]
      else process.env[k] = saved[k]
    }
  }
}

const configCases: Record<string, string>[] = [
  {},
  { NODE_ENV: "production" },
  { NODE_ENV: "production", RECALL_EMBED: "inline" },
  { RECALL_EMBED: "child" },
  { RECALL_EMBED: "bogus", NODE_ENV: "production" },
  {
    RECALL_URL: "https://Recall.Example.com///",
    DATA_DIR: "/data",
    RECALL_EMBED_IDLE_MS: "1500",
    GATEWAY_URL: "http://gw:8793//",
    GATEWAY_KEY: "k",
    EXTRACT_MODEL: "m1",
    FALLBACK_MODEL: "m2",
    RECALL_DEFAULT_CONTAINER: "zane",
    RECALL_AUTH_RATE_LIMIT: "off",
    RECALL_JOBS: "off",
    GOOGLE_CLIENT_ID: "  id  ",
    GOOGLE_CLIENT_SECRET: "\tsecret\n",
    CONNECTOR_SECRET: " s ",
    RECALL_GMAIL_FAKE: "1",
    RESEND_API_KEY: "re_x",
    RECALL_EMAIL_FROM: "Recall <r@x.com>",
  },
  { DATA_DIR: "/d", RECALL_DB_PATH: "/x/y.db", MODELS_DIR: "/m" },
  { RECALL_EMBED_IDLE_MS: "abc" },
  { RECALL_EMBED_IDLE_MS: "0" },
  { RECALL_EMBED_IDLE_MS: " 0x10 " },
  { RECALL_EMBED_IDLE_MS: "1e3" },
  { RECALL_EMBED_IDLE_MS: "-5" },
  { RECALL_URL: "http://localhost:4800", PASSKEY_RP_ID: "example.org" },
  { RECALL_URL: "", EXTRACT_MODEL: "", RECALL_DEFAULT_CONTAINER: "" },
  { RECALL_AUTH_RATE_LIMIT: "OFF", RECALL_JOBS: "no" },
  { RECALL_URL: "http://bücher.example" },
  { RECALL_URL: "http://localhost:4800 " },
  { RECALL_URL: "http://127.1" },
  { RECALL_URL: "http://[0:0:0:0:0:0:0:1]:4800" },
  { RECALL_URL: "HTTPS://Recall.EXAMPLE.com:443/app/" },
  { DATA_DIR: "", PASSKEY_RP_ID: "", GATEWAY_URL: "" },
  { RECALL_EMBED_IDLE_MS: "1e999" },
]

async function main() {
  const db = openDb(":memory:")
  const sqlite = db.$client
  const schema = sqlite.prepare("SELECT type, name, tbl_name AS tblName, sql FROM sqlite_master ORDER BY type, name").all()

  const freshHealth = health(db)
  const newer = openDb(":memory:")
  newer.$client.prepare("INSERT INTO _migrations (name, applied_at) VALUES ('0099_from_the_future', 1), ('0098_also', 2)").run()
  const newerHealth = health(newer)

  const prefixes = ["mem", "doc", "chk", "key", "usr", "acc", "mbr", "ws", "inv", "con", "per"] as const
  const ids = prefixes.flatMap((p) => Array.from({ length: 20 }, () => newId(p)))

  const keys = Array.from({ length: 20 }, () => {
    const key = generateApiKey()
    return { key, hash: sha256(key), prefix: key.slice(0, 10) }
  })
  const tokens = Array.from({ length: 10 }, (_, i) => randomToken(i + 1))

  const passwords = [
    "correct horse battery",
    "ﬁne ligature password",
    "Ｆｕｌｌｗｉｄｔｈ ｐａｓｓ",
    "éclair café",
    "émoji 😀 pass phrase",
    "x".repeat(256),
    "short",
    "",
  ]
  const scrypt = []
  for (const p of passwords) scrypt.push({ password: p, hash: await hashPassword(p) })
  const legacy = []
  for (const p of passwords) legacy.push({ password: p, hash: await hashLegacyPassword(p) })
  const verifyCases = []
  const tries: [string, string][] = [
    [scrypt[0]!.hash, "correct horse battery"],
    [scrypt[0]!.hash, "wrong"],
    [scrypt[1]!.hash, "fine ligature password"],
    [scrypt[2]!.hash, "Fullwidth pass"],
    [scrypt[3]!.hash, "éclair café"],
    [legacy[0]!.hash, "correct horse battery"],
    [legacy[0]!.hash, "wrong"],
    [legacy[1]!.hash, "fine ligature password"],
    ["garbage", "x"],
    ["", "x"],
    ["abc:", "x"],
    [":abc", "x"],
    [`${scrypt[0]!.hash}:extra`, "correct horse battery"],
    [scrypt[0]!.hash.toUpperCase(), "correct horse battery"],
    ["pbkdf2$sha256$0$AAAA$AAAA", "x"],
    ["pbkdf2$sha256$1.5$AAAA$AAAA", "x"],
    ["pbkdf2$sha256$1$$AAAA", "x"],
    ["pbkdf2$sha512$100000$AAAA$AAAA", "x"],
    ["pbkdf2$sha256$100000$AAAA", "x"],
    ["pbkdf2$sha256$2$c2FsdA==$" + "A".repeat(43) + "=", "x"],
    ["pbkdf2$sha256$ 2 $c2FsdA==$AAAA", "x"],
    ["pbkdf2$sha256$1e2$c2FsdA==$AAAA", "x"],
  ]
  // Legacy hashes the TS parser accepts in unusual spellings: iterations as
  // "1e3" or " 1000 ", url-safe and unpadded base64, a short key.
  const { pbkdf2Sync } = await import("node:crypto")
  const salt = Buffer.from("0123456789abcdef0123456789abcdef", "hex")
  const k32 = pbkdf2Sync("pw one", salt, 1000, 32, "sha256")
  const k8 = pbkdf2Sync("pw one", salt, 1000, 8, "sha256")
  tries.push(
    [`pbkdf2$sha256$1e3$${salt.toString("base64")}$${k32.toString("base64")}`, "pw one"],
    [`pbkdf2$sha256$ 1000 $${salt.toString("base64")}$${k32.toString("base64")}`, "pw one"],
    [`pbkdf2$sha256$0x3e8$${salt.toString("base64")}$${k32.toString("base64")}`, "pw one"],
    [`pbkdf2$sha256$1000$${salt.toString("base64url")}$${k32.toString("base64url")}`, "pw one"],
    [`pbkdf2$sha256$1000$${salt.toString("base64").replace(/=+$/, "")}$${k8.toString("base64").replace(/=+$/, "")}`, "pw one"],
    [`pbkdf2$sha256$1000$${salt.toString("base64")}$${k8.toString("base64")}`, "pw two"],
    [`pbkdf2$sha256$1000.0$${salt.toString("base64")}$${k32.toString("base64")}`, "pw one"],
  )
  for (const [hash, password] of tries) verifyCases.push({ hash, password, ok: await verifyPassword({ hash, password }) })

  const secret = "a-connector-secret-that-is-long-enough-123"
  const sealCases = [
    { plaintext: "ya29.token", context: "connector:con_1:access" },
    { plaintext: "", context: "ctx" },
    { plaintext: "unicode ✓ 😀", context: "" },
    { plaintext: "x".repeat(5000), context: "state" },
  ].map((c) => ({ ...c, sealed: seal(secret, c.plaintext, c.context) }))
  const attempt = (fn: () => string) => {
    try {
      return { ok: fn() }
    } catch (err) {
      return { error: (err as Error).message, name: (err as Error).constructor.name }
    }
  }
  const s0 = sealCases[0]!.sealed
  const [v, iv, body, tag] = s0.split(".")
  const unsealCases = [
    { secret, sealed: s0, context: sealCases[0]!.context },
    { secret, sealed: s0, context: "connector:con_2:access" },
    { secret: secret + "x", sealed: s0, context: sealCases[0]!.context },
    { secret, sealed: `v2.${iv}.${body}.${tag}`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}.${tag}.extra`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}x.${tag}`, context: sealCases[0]!.context },
    { secret, sealed: sealCases[1]!.sealed, context: "ctx" },
    { secret, sealed: "garbage", context: "x" },
    { secret, sealed: `${v}.${iv}==.${body}.${tag}`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}.${tag}AA`, context: sealCases[0]!.context },
    { secret: "short", sealed: s0, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}.${Buffer.from(tag!, "base64url").subarray(0, 12).toString("base64url")}`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}.${body}中.${tag}`, context: sealCases[0]!.context },
    { secret, sealed: `${v}.${iv}=.${body}.${tag}`, context: sealCases[0]!.context },
  ].map((c) => ({ ...c, result: attempt(() => unseal(c.secret, c.sealed, c.context)) }))
  const sealShort = attempt(() => seal("short", "x", "y"))

  const usernameInputs = ["zane", " Zane ", "ab", "a".repeat(33), "a".repeat(32), "zane.m_1", "zane-m", "zäki", "  ab  ", "Ünïcode", "a b c", "", "   ", "abc "]
  const usernames = usernameInputs.map((u) => ({ input: u, normalized: normalizeUsername(u), problem: usernameProblem(u), email: placeholderEmail(u) }))

  const limiter = new RateLimiter(3, 10_000)
  const steps: [string, number][] = [
    ["a", 0],
    ["a", 1],
    ["a", 2],
    ["a", 3],
    ["a", 9_001],
    ["a", 9_999],
    ["b", 5_000],
    ["a", 10_000],
    ["a", 10_001],
    ["b", 15_000],
    ["b", 15_000],
    ["b", 15_000],
    ["b", 15_001],
  ]
  const rateLimit = steps.map(([key, now]) => ({ key, now, wait: limiter.take(key, now) }))

  const bearers = [
    { authorization: "Bearer rcl_a" },
    { authorization: "bearer   rcl_b  " },
    { authorization: "BEARER rcl_c" },
    { authorization: "Basic xyz", "x-api-key": "rcl_d" },
    { "x-api-key": "rcl_e" },
    { authorization: "Bearer " },
    {},
    { authorization: "Bearerrcl_f" },
  ].map((h) => ({ headers: Object.entries(h), key: bearer(new Request("http://x", { headers: h })) }))
  for (const pairs of [
    [["authorization", "Bearer rcl_K"], ["authorization", "Bearer rcl_K"]],
    [["authorization", "Bearer rcl_K"], ["authorization", "Basic x"]],
    [["x-api-key", "rcl_a"], ["x-api-key", "rcl_b"]],
  ] as [string, string][][]) {
    bearers.push({ headers: pairs, key: bearer(new Request("http://x", { headers: new Headers(pairs) })) })
  }

  fs.writeFileSync(
    out,
    JSON.stringify(
      {
        migrations: migrations.map((m) => m.name),
        schema,
        health: {
          fresh: { status: freshHealth.status, body: await freshHealth.text(), contentType: freshHealth.headers.get("content-type") },
          newer: { status: newerHealth.status, body: await newerHealth.text(), contentType: newerHealth.headers.get("content-type") },
        },
        config: configCases.map((env) => ({ env, cwd: process.cwd(), config: withEnv(env) })),
        ids,
        keys,
        tokens,
        scrypt,
        legacy,
        verifyCases,
        seal: { secret, cases: sealCases, unseal: unsealCases, short: sealShort },
        usernames,
        rateLimit,
        bearers,
      },
      null,
      1,
    ) + "\n",
  )
  console.log(`wrote ${out}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
