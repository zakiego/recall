import { sql } from "drizzle-orm"
import { index, integer, primaryKey, sqliteTable, text, uniqueIndex } from "drizzle-orm/sqlite-core"

const now = sql`(cast(unixepoch('subsec') * 1000 as integer))`

export const DATE_PRECISIONS = ["day", "month", "year"] as const
export type DatePrecision = (typeof DATE_PRECISIONS)[number]

// --- Better Auth (username, passkey and organization plugins) --------------------
// Property names are Better Auth's field names; columns are snake_case. Dates are
// unix milliseconds like the rest of the schema.

const ms = (name: string) => integer(name, { mode: "timestamp_ms" })

export const user = sqliteTable("user", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email").notNull().unique(),
  emailVerified: integer("email_verified", { mode: "boolean" }).notNull().default(false),
  image: text("image"),
  username: text("username").unique(),
  displayUsername: text("display_username"),
  createdAt: ms("created_at").notNull(),
  updatedAt: ms("updated_at").notNull(),
})

export const session = sqliteTable(
  "session",
  {
    id: text("id").primaryKey(),
    expiresAt: ms("expires_at").notNull(),
    token: text("token").notNull().unique(),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
    ipAddress: text("ip_address"),
    userAgent: text("user_agent"),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    activeOrganizationId: text("active_organization_id"),
  },
  (t) => [index("session_user_id_idx").on(t.userId)],
)

export const account = sqliteTable(
  "account",
  {
    id: text("id").primaryKey(),
    accountId: text("account_id").notNull(),
    providerId: text("provider_id").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    accessToken: text("access_token"),
    refreshToken: text("refresh_token"),
    idToken: text("id_token"),
    accessTokenExpiresAt: ms("access_token_expires_at"),
    refreshTokenExpiresAt: ms("refresh_token_expires_at"),
    scope: text("scope"),
    password: text("password"),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
  },
  (t) => [index("account_user_id_idx").on(t.userId)],
)

export const verification = sqliteTable(
  "verification",
  {
    id: text("id").primaryKey(),
    identifier: text("identifier").notNull(),
    value: text("value").notNull(),
    expiresAt: ms("expires_at").notNull(),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
  },
  (t) => [index("verification_identifier_idx").on(t.identifier)],
)

export const passkey = sqliteTable(
  "passkey",
  {
    id: text("id").primaryKey(),
    name: text("name"),
    publicKey: text("public_key").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    credentialID: text("credential_id").notNull().unique(),
    counter: integer("counter").notNull(),
    deviceType: text("device_type").notNull(),
    backedUp: integer("backed_up", { mode: "boolean" }).notNull(),
    transports: text("transports"),
    createdAt: ms("created_at"),
    aaguid: text("aaguid"),
  },
  (t) => [index("passkey_user_id_idx").on(t.userId)],
)

// A workspace. The UI never says "organization".
export const organization = sqliteTable("organization", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  slug: text("slug").notNull().unique(),
  logo: text("logo"),
  metadata: text("metadata"),
  createdAt: ms("created_at").notNull(),
})

export const member = sqliteTable(
  "member",
  {
    id: text("id").primaryKey(),
    organizationId: text("organization_id")
      .notNull()
      .references(() => organization.id, { onDelete: "cascade" }),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    role: text("role").notNull(),
    createdAt: ms("created_at").notNull(),
  },
  (t) => [uniqueIndex("member_organization_user_unique").on(t.organizationId, t.userId), index("member_user_id_idx").on(t.userId)],
)

// Required by the organization plugin. Recall's own invite links live in workspaceInvites.
export const invitation = sqliteTable("invitation", {
  id: text("id").primaryKey(),
  organizationId: text("organization_id")
    .notNull()
    .references(() => organization.id, { onDelete: "cascade" }),
  email: text("email").notNull(),
  role: text("role"),
  status: text("status").notNull(),
  expiresAt: ms("expires_at").notNull(),
  createdAt: ms("created_at").notNull(),
  inviterId: text("inviter_id")
    .notNull()
    .references(() => user.id, { onDelete: "cascade" }),
})

// --- OAuth provider (@better-auth/oauth-provider) -------------------------------
// Lets MCP clients like claude.ai connect with OAuth instead of an API key. The
// string[] and json fields are JSON text: the Drizzle adapter on SQLite stores
// them that way. Access and refresh tokens are SHA-256 hashes. See docs/oauth.md.

export const oauthClient = sqliteTable(
  "oauth_client",
  {
    id: text("id").primaryKey(),
    clientId: text("client_id").notNull().unique(),
    clientSecret: text("client_secret"),
    clientDiscoveryId: text("client_discovery_id"),
    disabled: integer("disabled", { mode: "boolean" }).default(false),
    skipConsent: integer("skip_consent", { mode: "boolean" }),
    enableEndSession: integer("enable_end_session", { mode: "boolean" }),
    subjectType: text("subject_type"),
    scopes: text("scopes"),
    clientCredentialsScopes: text("client_credentials_scopes"),
    userId: text("user_id").references(() => user.id, { onDelete: "cascade" }),
    createdAt: ms("created_at"),
    updatedAt: ms("updated_at"),
    name: text("name"),
    uri: text("uri"),
    icon: text("icon"),
    contacts: text("contacts"),
    tos: text("tos"),
    policy: text("policy"),
    softwareId: text("software_id"),
    softwareVersion: text("software_version"),
    softwareStatement: text("software_statement"),
    redirectUris: text("redirect_uris").notNull(),
    postLogoutRedirectUris: text("post_logout_redirect_uris"),
    backchannelLogoutUri: text("backchannel_logout_uri"),
    backchannelLogoutSessionRequired: integer("backchannel_logout_session_required", { mode: "boolean" }),
    tokenEndpointAuthMethod: text("token_endpoint_auth_method"),
    applicationType: text("application_type"),
    jwks: text("jwks"),
    jwksUri: text("jwks_uri"),
    grantTypes: text("grant_types"),
    responseTypes: text("response_types"),
    requirePKCE: integer("require_pkce", { mode: "boolean" }),
    dpopBoundAccessTokens: integer("dpop_bound_access_tokens", { mode: "boolean" }).default(false),
    referenceId: text("reference_id"),
    metadata: text("metadata"),
  },
  (t) => [index("oauth_client_user_id_idx").on(t.userId)],
)

export const oauthResource = sqliteTable("oauth_resource", {
  id: text("id").primaryKey(),
  identifier: text("identifier").notNull().unique(),
  name: text("name").notNull(),
  accessTokenTtl: integer("access_token_ttl"),
  refreshTokenTtl: integer("refresh_token_ttl"),
  signingAlgorithm: text("signing_algorithm"),
  signingKeyId: text("signing_key_id"),
  allowedScopes: text("allowed_scopes"),
  customClaims: text("custom_claims"),
  dpopBoundAccessTokensRequired: integer("dpop_bound_access_tokens_required", { mode: "boolean" }).default(false),
  disabled: integer("disabled", { mode: "boolean" }).default(false),
  createdAt: ms("created_at"),
  updatedAt: ms("updated_at"),
  policyVersion: integer("policy_version").default(1),
  metadata: text("metadata"),
})

export const oauthClientResource = sqliteTable(
  "oauth_client_resource",
  {
    id: text("id").primaryKey(),
    clientId: text("client_id")
      .notNull()
      .references(() => oauthClient.clientId, { onDelete: "cascade" }),
    resourceId: text("resource_id")
      .notNull()
      .references(() => oauthResource.identifier, { onDelete: "cascade" }),
    metadata: text("metadata"),
    createdAt: ms("created_at"),
  },
  (t) => [uniqueIndex("oauth_client_resource_unique").on(t.clientId, t.resourceId), index("oauth_client_resource_resource_idx").on(t.resourceId)],
)

export const oauthRefreshToken = sqliteTable(
  "oauth_refresh_token",
  {
    id: text("id").primaryKey(),
    token: text("token").notNull().unique(),
    clientId: text("client_id")
      .notNull()
      .references(() => oauthClient.clientId, { onDelete: "cascade" }),
    sessionId: text("session_id").references(() => session.id, { onDelete: "set null" }),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    referenceId: text("reference_id"),
    authorizationCodeId: text("authorization_code_id"),
    resources: text("resources"),
    requestedUserInfoClaims: text("requested_user_info_claims"),
    expiresAt: ms("expires_at"),
    createdAt: ms("created_at"),
    revoked: ms("revoked"),
    rotatedAt: ms("rotated_at"),
    rotationReplayResponse: text("rotation_replay_response"),
    rotationReplayExpiresAt: ms("rotation_replay_expires_at"),
    authTime: ms("auth_time"),
    confirmation: text("confirmation"),
    scopes: text("scopes").notNull(),
  },
  (t) => [
    index("oauth_refresh_token_client_idx").on(t.clientId),
    index("oauth_refresh_token_session_idx").on(t.sessionId),
    index("oauth_refresh_token_user_idx").on(t.userId),
    index("oauth_refresh_token_code_idx").on(t.authorizationCodeId),
  ],
)

export const oauthAccessToken = sqliteTable(
  "oauth_access_token",
  {
    id: text("id").primaryKey(),
    token: text("token").unique(),
    clientId: text("client_id")
      .notNull()
      .references(() => oauthClient.clientId, { onDelete: "cascade" }),
    sessionId: text("session_id").references(() => session.id, { onDelete: "set null" }),
    userId: text("user_id").references(() => user.id, { onDelete: "cascade" }),
    referenceId: text("reference_id"),
    authorizationCodeId: text("authorization_code_id"),
    resources: text("resources"),
    requestedUserInfoClaims: text("requested_user_info_claims"),
    refreshId: text("refresh_id").references(() => oauthRefreshToken.id, { onDelete: "cascade" }),
    expiresAt: ms("expires_at"),
    createdAt: ms("created_at"),
    revoked: ms("revoked"),
    confirmation: text("confirmation"),
    scopes: text("scopes").notNull(),
  },
  (t) => [
    index("oauth_access_token_client_idx").on(t.clientId),
    index("oauth_access_token_session_idx").on(t.sessionId),
    index("oauth_access_token_user_idx").on(t.userId),
    index("oauth_access_token_code_idx").on(t.authorizationCodeId),
    index("oauth_access_token_refresh_idx").on(t.refreshId),
  ],
)

export const oauthConsent = sqliteTable(
  "oauth_consent",
  {
    id: text("id").primaryKey(),
    clientId: text("client_id")
      .notNull()
      .references(() => oauthClient.clientId, { onDelete: "cascade" }),
    userId: text("user_id").references(() => user.id, { onDelete: "cascade" }),
    referenceId: text("reference_id"),
    resources: text("resources"),
    requestedUserInfoClaims: text("requested_user_info_claims"),
    scopes: text("scopes").notNull(),
    createdAt: ms("created_at"),
    updatedAt: ms("updated_at"),
  },
  (t) => [index("oauth_consent_client_idx").on(t.clientId), index("oauth_consent_user_idx").on(t.userId)],
)

// Replay tombstones for client assertion JWTs.
export const oauthClientAssertion = sqliteTable("oauth_client_assertion", {
  id: text("id").primaryKey(),
  expiresAt: ms("expires_at").notNull(),
})

export const ROLES = ["owner", "admin", "member"] as const
export type Role = (typeof ROLES)[number]

export const workspaceInvites = sqliteTable(
  "workspace_invites",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id")
      .notNull()
      .references(() => organization.id, { onDelete: "cascade" }),
    role: text("role", { enum: ROLES }).notNull(),
    tokenHash: text("token_hash").notNull().unique(),
    createdBy: text("created_by").references(() => user.id, { onDelete: "set null" }),
    createdAt: integer("created_at").notNull().default(now),
    expiresAt: integer("expires_at").notNull(),
    acceptedAt: integer("accepted_at"),
    acceptedBy: text("accepted_by").references(() => user.id, { onDelete: "set null" }),
    revokedAt: integer("revoked_at"),
  },
  (t) => [index("workspace_invites_workspace_idx").on(t.workspaceId, t.createdAt)],
)

// --- Recall data. Every row belongs to one workspace. ---------------------------

export const apiKeys = sqliteTable("api_keys", {
  id: text("id").primaryKey(),
  workspaceId: text("workspace_id").notNull(),
  name: text("name").notNull(),
  keyHash: text("key_hash").notNull().unique(),
  prefix: text("prefix").notNull(),
  createdAt: integer("created_at").notNull().default(now),
  lastUsedAt: integer("last_used_at"),
  revokedAt: integer("revoked_at"),
  createdBy: text("created_by").references(() => user.id, { onDelete: "set null" }),
}, (t) => [index("api_keys_workspace_idx").on(t.workspaceId, t.createdAt)])

export const documents = sqliteTable(
  "documents",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id").notNull(),
    container: text("container").notNull(),
    sourceType: text("source_type", { enum: ["text", "chat", "url", "file", "email"] }).notNull(),
    title: text("title"),
    url: text("url"),
    content: text("content"),
    status: text("status", { enum: ["queued", "processing", "done", "failed"] })
      .notNull()
      .default("queued"),
    error: text("error"),
    metadata: text("metadata", { mode: "json" }).$type<Record<string, unknown>>(),
    // Caller's own id for the source (gmail:<id>, a file path...). Unique per container.
    sourceId: text("source_id"),
    createdAt: integer("created_at").notNull().default(now),
  },
  (t) => [
    index("documents_workspace_container_idx").on(t.workspaceId, t.container, t.createdAt),
    // A source is ingested once per container inside a workspace (migration 0005).
    uniqueIndex("documents_workspace_source_id_unique")
      .on(t.workspaceId, t.container, t.sourceId)
      .where(sql`${t.sourceId} is not null`),
  ],
)

export const chunks = sqliteTable(
  "chunks",
  {
    id: text("id").primaryKey(),
    documentId: text("document_id")
      .notNull()
      .references(() => documents.id, { onDelete: "cascade" }),
    workspaceId: text("workspace_id").notNull(),
    container: text("container").notNull(),
    idx: integer("idx").notNull(),
    text: text("text").notNull(),
  },
  (t) => [index("chunks_document_idx").on(t.documentId, t.idx), index("chunks_workspace_idx").on(t.workspaceId, t.container)],
)

export const memories = sqliteTable(
  "memories",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id").notNull(),
    container: text("container").notNull(),
    fact: text("fact").notNull(),
    keywords: text("keywords").notNull().default(""),
    kind: text("kind", { enum: ["static", "dynamic"] }).notNull().default("dynamic"),
    sourceDocumentId: text("source_document_id").references(() => documents.id, {
      onDelete: "set null",
    }),
    supersedesId: text("supersedes_id"),
    validFrom: integer("valid_from").notNull().default(now),
    validTo: integer("valid_to"),
    createdAt: integer("created_at").notNull().default(now),
    // Set when this fact conflicts with another and a human should decide.
    reviewNote: text("review_note"),
    // The other side of the newest conflict, so resolving, editing or
    // deleting either one clears both.
    conflictsWithId: text("conflicts_with_id"),
    // When it happened, and when it ends or is due (migration 0007). ISO dates;
    // a month or year date is the first day of that period (see dates.ts).
    eventDate: text("event_date"),
    eventPrecision: text("event_precision", { enum: DATE_PRECISIONS }),
    dueDate: text("due_date"),
    duePrecision: text("due_precision", { enum: DATE_PRECISIONS }),
  },
  (t) => [
    index("memories_current_idx").on(t.workspaceId, t.container, t.validTo, t.createdAt),
    index("memories_supersedes_idx").on(t.supersedesId),
    index("memories_conflicts_with_idx").on(t.conflictsWithId),
    index("memories_source_document_idx").on(t.sourceDocumentId),
    index("memories_due_idx").on(t.workspaceId, t.dueDate).where(sql`${t.validTo} IS NULL AND ${t.dueDate} IS NOT NULL`),
  ],
)

export const profiles = sqliteTable(
  "profiles",
  {
    workspaceId: text("workspace_id").notNull(),
    container: text("container").notNull(),
    staticText: text("static_text").notNull().default(""),
    dynamicText: text("dynamic_text").notNull().default(""),
    updatedAt: integer("updated_at").notNull().default(now),
  },
  (t) => [primaryKey({ columns: [t.workspaceId, t.container] })],
)

export const jobs = sqliteTable(
  "jobs",
  {
    id: integer("id").primaryKey({ autoIncrement: true }),
    workspaceId: text("workspace_id").notNull(),
    type: text("type", { enum: ["ingest", "extract", "profile", "ask", "reminders", "backfill_dates", "people_backfill", "app_sync"] }).notNull(),
    payload: text("payload", { mode: "json" }).$type<Record<string, unknown>>().notNull(),
    status: text("status", { enum: ["queued", "running", "done", "failed"] })
      .notNull()
      .default("queued"),
    attempts: integer("attempts").notNull().default(0),
    runAfter: integer("run_after").notNull().default(now),
    // Kept after a later attempt succeeds, so a job that needed a retry shows why.
    lastError: text("last_error"),
    createdAt: integer("created_at").notNull().default(now),
    // The latest attempt's start, and when the job last stopped running
    // (migration 0010). Null on rows older than that.
    startedAt: integer("started_at"),
    finishedAt: integer("finished_at"),
  },
  (t) => [index("jobs_pending_idx").on(t.status, t.runAfter)],
)

// A user's connected third-party account in one workspace (migration 0006).
// `tokens` holds sealed OAuth tokens; only connectors/store.ts opens it.
export const connectorAccounts = sqliteTable(
  "connector_accounts",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    provider: text("provider", { enum: ["gmail"] }).notNull(),
    accountEmail: text("account_email"),
    scope: text("scope").notNull(),
    tokens: text("tokens").notNull(),
    // reauth: Google refused the refresh token (revoked or expired); connect again.
    status: text("status", { enum: ["active", "reauth"] }).notNull().default("active"),
    createdAt: integer("created_at").notNull().default(now),
    updatedAt: integer("updated_at").notNull().default(now),
    lastUsedAt: integer("last_used_at"),
  },
  (t) => [uniqueIndex("connector_accounts_owner_unique").on(t.workspaceId, t.userId, t.provider)],
)

export type ConnectorAccount = typeof connectorAccounts.$inferSelect

// --- App connectors (migration 0009). Pull feeds from Oslo and Finansial. -------

export const APP_PROVIDERS = ["oslo", "finansial"] as const
export type AppProvider = (typeof APP_PROVIDERS)[number]

// One per workspace and provider. `apiKey` is sealed; only connectors/apps.ts opens it.
export const appConnectors = sqliteTable(
  "app_connectors",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id")
      .notNull()
      .references(() => organization.id, { onDelete: "cascade" }),
    provider: text("provider", { enum: APP_PROVIDERS }).notNull(),
    // Where the documents go.
    container: text("container").notNull(),
    baseUrl: text("base_url").notNull(),
    apiKey: text("api_key"),
    enabled: integer("enabled", { mode: "boolean" }).notNull().default(false),
    // The feed's opaque cursor. Moves only after a page is stored.
    cursor: text("cursor"),
    // First time a sync reached the end of the feed (the backfill is done).
    caughtUpAt: integer("caught_up_at"),
    lastSyncAt: integer("last_sync_at"),
    lastSuccessAt: integer("last_success_at"),
    lastError: text("last_error"),
    settings: text("settings", { mode: "json" }).$type<Record<string, unknown>>().notNull().default(sql`'{}'`),
    createdBy: text("created_by").references(() => user.id, { onDelete: "set null" }),
    createdAt: integer("created_at").notNull().default(now),
    updatedAt: integer("updated_at").notNull().default(now),
  },
  (t) => [uniqueIndex("app_connectors_workspace_provider_unique").on(t.workspaceId, t.provider)],
)

export type AppConnector = typeof appConnectors.$inferSelect

// Oslo events held until their local day is over (see connectors/oslo.ts).
export const appConnectorStaging = sqliteTable(
  "app_connector_staging",
  {
    connectorId: text("connector_id")
      .notNull()
      .references(() => appConnectors.id, { onDelete: "cascade" }),
    day: text("day").notNull(),
    itemKey: text("item_key").notNull(),
    payload: text("payload", { mode: "json" }).$type<Record<string, unknown>>().notNull(),
    writtenAt: integer("written_at"),
    createdAt: integer("created_at").notNull().default(now),
  },
  (t) => [primaryKey({ columns: [t.connectorId, t.itemKey] }), index("app_connector_staging_day_idx").on(t.connectorId, t.day)],
)

// --- Reminders (migration 0007). Per user, across their workspaces. ------------

export const reminderSettings = sqliteTable("reminder_settings", {
  userId: text("user_id")
    .primaryKey()
    .references(() => user.id, { onDelete: "cascade" }),
  enabled: integer("enabled", { mode: "boolean" }).notNull().default(false),
  email: text("email"),
  // Days before the due date, as a JSON array: [30, 1].
  leadDays: text("lead_days", { mode: "json" }).$type<number[]>().notNull().default(sql`'[30,1]'`),
  timezone: text("timezone").notNull().default("UTC"),
  updatedAt: integer("updated_at").notNull().default(now),
})

export const remindersSent = sqliteTable(
  "reminders_sent",
  {
    id: integer("id").primaryKey({ autoIncrement: true }),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    workspaceId: text("workspace_id").notNull(),
    memoryId: text("memory_id").notNull(),
    dueDate: text("due_date").notNull(),
    leadDays: integer("lead_days").notNull(),
    status: text("status", { enum: ["pending", "sent"] }).notNull().default("pending"),
    createdAt: integer("created_at").notNull().default(now),
    sentAt: integer("sent_at"),
  },
  (t) => [uniqueIndex("reminders_sent_unique").on(t.userId, t.memoryId, t.dueDate, t.leadDays), index("reminders_sent_memory_idx").on(t.memoryId)],
)

// --- People (migration 0008) ------------------------------------------------------

const stringList = (name: string) => text(name, { mode: "json" }).$type<string[]>().notNull().default(sql`'[]'`)

export const people = sqliteTable(
  "people",
  {
    id: text("id").primaryKey(),
    workspaceId: text("workspace_id").notNull(),
    container: text("container").notNull(),
    name: text("name").notNull(),
    aliases: stringList("aliases"),
    role: text("role"),
    organization: text("organization"),
    emails: stringList("emails"),
    handles: stringList("handles"),
    summary: text("summary").notNull().default(""),
    // Edited by hand: extraction stops rewriting name, role, organization and summary.
    manual: integer("manual", { mode: "boolean" }).notNull().default(false),
    createdAt: integer("created_at").notNull().default(now),
    updatedAt: integer("updated_at").notNull().default(now),
  },
  (t) => [index("people_scope_idx").on(t.workspaceId, t.container)],
)

export const personMemories = sqliteTable(
  "person_memories",
  {
    personId: text("person_id")
      .notNull()
      .references(() => people.id, { onDelete: "cascade" }),
    memoryId: text("memory_id")
      .notNull()
      .references(() => memories.id, { onDelete: "cascade" }),
    workspaceId: text("workspace_id").notNull(),
    createdAt: integer("created_at").notNull().default(now),
  },
  (t) => [primaryKey({ columns: [t.personId, t.memoryId] }), index("person_memories_memory_idx").on(t.memoryId)],
)

export const peopleScans = sqliteTable("people_scans", {
  memoryId: text("memory_id")
    .primaryKey()
    .references(() => memories.id, { onDelete: "cascade" }),
  workspaceId: text("workspace_id").notNull(),
  scannedAt: integer("scanned_at").notNull().default(now),
})

export type Person = typeof people.$inferSelect

export type Memory = typeof memories.$inferSelect
export type Document = typeof documents.$inferSelect
export type Job = typeof jobs.$inferSelect
export type Workspace = typeof organization.$inferSelect
