// Better Auth on the app's own better-sqlite3 connection, through the Drizzle
// adapter. Username + password, passkeys, and organizations shown as workspaces.
// See docs/multi-user.md.
import { randomBytes } from "node:crypto"
import { passkey } from "@better-auth/passkey"
import { betterAuth } from "better-auth"
import { drizzleAdapter } from "better-auth/adapters/drizzle"
import { organization } from "better-auth/plugins/organization"
import { username } from "better-auth/plugins/username"
import { tanstackStartCookies } from "better-auth/tanstack-start"
import { asc, eq } from "drizzle-orm"
import { hashPassword, MAX_PASSWORD_LENGTH, MIN_PASSWORD_LENGTH, verifyPassword } from "./auth"
import { config } from "./config"
import { getDb, type DB } from "./db"
import * as schema from "./db/schema"
import { USERNAME_MAX, USERNAME_MIN, USERNAME_PATTERN } from "./usernames"

export const AUTH_BASE_PATH = "/api/auth"
export const COOKIE_PREFIX = "recall"
// Set by the route handler from the proxy's headers, never trusted from the client.
export const CLIENT_IP_HEADER = "x-recall-client-ip"

// Without a real secret sessions only last until the process restarts, and the
// login page says the server isn't configured.
let fallbackSecret: string | undefined
export function authConfigured() {
  return config().sessionSecret.length >= 32
}

function firstWorkspaceId(db: DB, userId: string): string | null {
  return (
    db
      .select({ id: schema.member.organizationId })
      .from(schema.member)
      .where(eq(schema.member.userId, userId))
      .orderBy(asc(schema.member.createdAt))
      .get()?.id ?? null
  )
}

export function createAuth(db: DB) {
  const cfg = config()
  const origin = new URL(cfg.url).origin
  fallbackSecret ??= randomBytes(32).toString("base64")
  return betterAuth({
    appName: "Recall",
    baseURL: cfg.url,
    basePath: AUTH_BASE_PATH,
    secret: authConfigured() ? cfg.sessionSecret : fallbackSecret,
    trustedOrigins: [origin],
    database: drizzleAdapter(db, {
      provider: "sqlite",
      schema: {
        user: schema.user,
        session: schema.session,
        account: schema.account,
        verification: schema.verification,
        passkey: schema.passkey,
        organization: schema.organization,
        member: schema.member,
        invitation: schema.invitation,
      },
    }),
    emailAndPassword: {
      enabled: true,
      // No open signup. Users come from the CLI, the first-boot bootstrap or an invite.
      disableSignUp: true,
      minPasswordLength: MIN_PASSWORD_LENGTH,
      maxPasswordLength: MAX_PASSWORD_LENGTH,
      password: { hash: hashPassword, verify: verifyPassword },
    },
    session: { expiresIn: 7 * 24 * 60 * 60, updateAge: 24 * 60 * 60 },
    advanced: {
      cookiePrefix: COOKIE_PREFIX,
      useSecureCookies: cfg.url.startsWith("https://"),
      defaultCookieAttributes: { httpOnly: true, sameSite: "lax" },
      ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] },
      // Better Auth turns these off under a test runner. Keep them on so the
      // tests exercise what production runs.
      disableOriginCheck: false,
      disableCSRFCheck: false,
    },
    rateLimit: {
      enabled: cfg.authRateLimit,
      storage: "memory",
      window: 60,
      max: 100,
      customRules: {
        "/sign-in/username": { window: 60, max: 5 },
        "/sign-in/email": { window: 60, max: 5 },
        "/passkey/verify-authentication": { window: 60, max: 5 },
        "/passkey/generate-authenticate-options": { window: 60, max: 10 },
        "/change-password": { window: 60, max: 5 },
      },
    },
    databaseHooks: {
      session: {
        create: {
          // Land in the user's first workspace.
          before: async (s) => ({ data: { ...s, activeOrganizationId: firstWorkspaceId(db, s.userId) } }),
        },
      },
    },
    plugins: [
      username({
        minUsernameLength: USERNAME_MIN,
        maxUsernameLength: USERNAME_MAX,
        usernameValidator: (u) => USERNAME_PATTERN.test(u),
      }),
      passkey({ rpID: cfg.passkeyRpId, rpName: "Recall", origin }),
      organization({
        creatorRole: "owner",
        allowUserToCreateOrganization: true,
        disableOrganizationDeletion: true,
        membershipLimit: 200,
      }),
      // Last, so it forwards cookies set by the plugins above.
      tanstackStartCookies(),
    ],
  })
}

export type Auth = ReturnType<typeof createAuth>

const instances = new WeakMap<DB, Auth>()

export function getAuth(db: DB = getDb()): Auth {
  let auth = instances.get(db)
  if (!auth) {
    auth = createAuth(db)
    instances.set(db, auth)
  }
  return auth
}

// The real client IP from the proxy (Cloudflare tunnel in prod), copied into a
// header the client can't set, so rate limits key on it. Built from parts:
// Nitro's request isn't a native Request, so `new Request(request)` throws.
// Auth bodies are small JSON, so buffering is fine.
export async function withClientIp(request: Request): Promise<Request> {
  const ip =
    request.headers.get("cf-connecting-ip") ??
    request.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ??
    request.headers.get("x-real-ip") ??
    "local"
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, ip)
  const hasBody = request.method !== "GET" && request.method !== "HEAD"
  return new Request(request.url, { method: request.method, headers, body: hasBody ? await request.arrayBuffer() : undefined })
}

// Better Auth only checks Origin on requests that already carry cookies. A
// sign-in has none, so a hostile page could log a visitor into an account of
// its choosing. Every write to /api/auth must come from RECALL_URL.
export function crossOriginWrite(request: Request): boolean {
  if (request.method === "GET" || request.method === "HEAD" || request.method === "OPTIONS") return false
  if (request.headers.get("sec-fetch-site") === "cross-site") return true
  const origin = request.headers.get("origin")
  return !!origin && origin !== new URL(config().url).origin
}

export function authHandler(db: DB) {
  return async (request: Request): Promise<Response> => {
    if (crossOriginWrite(request)) return Response.json({ code: "INVALID_ORIGIN", message: "Invalid origin" }, { status: 403 })
    return getAuth(db).handler(await withClientIp(request))
  }
}

export function handleAuth(request: Request): Promise<Response> {
  return authHandler(getDb())(request)
}
