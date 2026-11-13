// Better Auth on the app's own better-sqlite3 connection, through the Drizzle
// adapter. Username + password, passkeys, and organizations shown as workspaces.
// See docs/multi-user.md.
import { randomBytes } from "node:crypto"
import { oauthProvider, oauthProviderAuthServerMetadata } from "@better-auth/oauth-provider"
import { passkey } from "@better-auth/passkey"
import { betterAuth } from "better-auth"
import { APIError } from "better-auth/api"
import { drizzleAdapter } from "better-auth/adapters/drizzle"
import { organization } from "better-auth/plugins/organization"
import { username } from "better-auth/plugins/username"
import { tanstackStartCookies } from "better-auth/tanstack-start"
import { asc, eq } from "drizzle-orm"
import { hashPassword, MAX_PASSWORD_LENGTH, MIN_PASSWORD_LENGTH, verifyPassword } from "./auth"
import { config } from "./config"
import { getDb, type DB } from "./db"
import * as schema from "./db/schema"
import { ACCESS_TOKEN_PREFIX, mcpResource, OAUTH_CONSENT_HANDOFF, OAUTH_LOGIN_HANDOFF, OAUTH_SCOPES, REFRESH_TOKEN_PREFIX } from "./oauth"
import { normalizeUsername, USERNAME_MAX, USERNAME_MIN, USERNAME_PATTERN, usernameProblem } from "./usernames"

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
        oauthClient: schema.oauthClient,
        oauthResource: schema.oauthResource,
        oauthClientResource: schema.oauthClientResource,
        oauthRefreshToken: schema.oauthRefreshToken,
        oauthAccessToken: schema.oauthAccessToken,
        oauthConsent: schema.oauthConsent,
        oauthClientAssertion: schema.oauthClientAssertion,
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
      user: {
        update: {
          // The display name is only ever the username with its own casing.
          // Anything else would let one user show up under another's name.
          before: async (data, ctx) => {
            if (!("displayUsername" in data) || !data.displayUsername) return
            const session = (ctx?.context as { session?: { user?: { username?: string | null } } } | undefined)?.session
            const username = (typeof data.username === "string" ? data.username : null) ?? session?.user?.username ?? null
            if (!username || normalizeUsername(String(data.displayUsername)) !== normalizeUsername(username)) {
              throw new APIError("BAD_REQUEST", { message: "The display name must be your username." })
            }
          },
        },
      },
    },
    plugins: [
      username({
        minUsernameLength: USERNAME_MIN,
        maxUsernameLength: USERNAME_MAX,
        usernameValidator: (u) => USERNAME_PATTERN.test(u),
        // Invites, keys and the CLI refer to people by username, and the
        // placeholder email is derived from it, so it never changes.
        immutableUsername: true,
        // A display name must look like a username. The plugin then treats it
        // as the username, so immutableUsername rejects anyone else's; the
        // user update hook above covers a request that sends both fields.
        displayUsernameValidator: (d) => usernameProblem(d) === null,
      }),
      passkey({ rpID: cfg.passkeyRpId, rpName: "Recall", origin }),
      organization({
        creatorRole: "owner",
        allowUserToCreateOrganization: true,
        disableOrganizationDeletion: true,
        membershipLimit: 200,
      }),
      // The OAuth server for /mcp (claude.ai custom connectors). See src/server/oauth.ts.
      oauthProvider({
        loginPage: OAUTH_LOGIN_HANDOFF,
        consentPage: OAUTH_CONSENT_HANDOFF,
        scopes: OAUTH_SCOPES,
        grantTypes: ["authorization_code", "refresh_token"],
        clientRegistrationDefaultScopes: OAUTH_SCOPES,
        // claude.ai registers itself (RFC 7591) before the user ever logs in.
        // Registering grants nothing: every token still needs a logged-in
        // member to approve it on the consent page.
        allowDynamicClientRegistration: true,
        allowUnauthenticatedClientRegistration: true,
        clientRegistrationDefaultApplicationType: "infer",
        resources: [{ identifier: mcpResource(cfg.url), name: "Recall MCP" }],
        clientRegistrationDefaultResources: [mcpResource(cfg.url)],
        // Opaque tokens checked against the database, so revoking works at once.
        disableJwtPlugin: true,
        prefix: { opaqueAccessToken: ACCESS_TOKEN_PREFIX, refreshToken: REFRESH_TOKEN_PREFIX },
        // A token reaches one workspace: the one active when the user approved.
        postLogin: {
          page: OAUTH_CONSENT_HANDOFF,
          shouldRedirect: () => false,
          consentReferenceId: ({ session }) => {
            const ws = (session as { activeOrganizationId?: string | null }).activeOrganizationId
            if (!ws) throw new APIError("FORBIDDEN", { message: "Join a workspace before connecting an app." })
            return ws
          },
        },
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

// The organization plugin is only there for its tables and the session's
// activeOrganizationId. Its HTTP endpoints (create, rename, change slug, roles,
// invitations) would skip Recall's own role rules in src/server/users.ts, and
// the dashboard never calls them, so they don't exist from outside.
export function blockedAuthPath(request: Request): boolean {
  let path = new URL(request.url).pathname
  try {
    path = decodeURIComponent(path)
  } catch {
    return true
  }
  path = path.replace(/\/{2,}/g, "/").toLowerCase()
  if (!path.startsWith(`${AUTH_BASE_PATH}/`)) return false
  const rest = path.slice(AUTH_BASE_PATH.length)
  return rest === "/organization" || rest.startsWith("/organization/")
}

export function authHandler(db: DB) {
  return async (request: Request): Promise<Response> => {
    if (blockedAuthPath(request)) return Response.json({ code: "NOT_FOUND", message: "Not found" }, { status: 404 })
    if (crossOriginWrite(request)) return Response.json({ code: "INVALID_ORIGIN", message: "Invalid origin" }, { status: 403 })
    return getAuth(db).handler(await withClientIp(request))
  }
}

export function handleAuth(request: Request): Promise<Response> {
  return authHandler(getDb())(request)
}

// RFC 8414 metadata for the OAuth server. Served at the root
// (/.well-known/oauth-authorization-server and .../api/auth), since Better
// Auth only answers under /api/auth.
export function handleAuthServerMetadata(request: Request): Promise<Response> {
  return oauthProviderAuthServerMetadata(getAuth(), { headers: { "access-control-allow-origin": "*" } })(request)
}
