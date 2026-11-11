// Users, workspaces, memberships and invite links. Users are only ever created
// here: by the CLI, the first-boot bootstrap and invite acceptance.
import { and, asc, count, desc, eq, gt, inArray, isNull } from "drizzle-orm"
import { hashPassword, MAX_PASSWORD_LENGTH, MIN_PASSWORD_LENGTH, randomToken, sha256 } from "./auth"
import { config } from "./config"
import type { DB } from "./db"
import { account, apiKeys, member, organization, passkey, user, workspaceInvites, type Role } from "./db/schema"
import { newId } from "./ids"
import { normalizeUsername, placeholderEmail, usernameProblem } from "./usernames"

export class UserError extends Error {}

export const LEGACY_WORKSPACE = { id: "ws_zaki", name: "Zaki", slug: "zaki" } as const
export const LEGACY_USERNAME = "zaki"
export const INVITE_TTL_MS = 7 * 24 * 60 * 60 * 1000

export function passwordProblem(pw: string): string | null {
  if (pw.length < MIN_PASSWORD_LENGTH) return `Use at least ${MIN_PASSWORD_LENGTH} characters.`
  if (pw.length > MAX_PASSWORD_LENGTH) return `Use at most ${MAX_PASSWORD_LENGTH} characters.`
  return null
}

export function userCount(db: DB) {
  return db.select({ n: count() }).from(user).get()!.n
}

export function findUserByUsername(db: DB, username: string) {
  return db.select().from(user).where(eq(user.username, normalizeUsername(username))).get()
}

// Sync, so it can share a transaction with a membership or an invite. Hash the
// password first (hashPassword is async).
export function insertUser(db: DB, input: { username: string; passwordHash: string; name?: string }) {
  const problem = usernameProblem(input.username)
  if (problem) throw new UserError(`Username: ${problem}`)
  const username = normalizeUsername(input.username)
  const taken = () => new UserError("That username is taken.")
  // The email check catches a user renamed before usernames were immutable,
  // who still holds this username's placeholder email.
  if (findUserByUsername(db, username) || db.select({ id: user.id }).from(user).where(eq(user.email, placeholderEmail(username))).get()) throw taken()
  const now = new Date()
  const id = newId("usr")
  let row: typeof user.$inferSelect
  try {
    row = db
      .insert(user)
      .values({
        id,
        name: input.name?.trim() || input.username.trim(),
        email: placeholderEmail(username),
        emailVerified: false,
        username,
        displayUsername: input.username.trim(),
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
  } catch (err) {
    // Anything that slipped past the checks above is still a taken name, not a 500.
    if (err instanceof Error && /UNIQUE constraint failed: user\.(email|username)/.test(err.message)) throw taken()
    throw err
  }
  // Better Auth's credential account: account_id is the user id.
  db.insert(account)
    .values({ id: newId("acc"), accountId: id, providerId: "credential", userId: id, password: input.passwordHash, createdAt: now, updatedAt: now })
    .run()
  return row
}

export async function createUserWithPassword(db: DB, input: { username: string; password: string; name?: string }) {
  const problem = passwordProblem(input.password)
  if (problem) throw new UserError(`Password: ${problem}`)
  const passwordHash = await hashPassword(input.password)
  return db.transaction(() => insertUser(db, { ...input, passwordHash }))
}

// --- workspaces -----------------------------------------------------------------

export function slugify(name: string) {
  const base =
    name
      .toLowerCase()
      .normalize("NFKD")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 40) || "workspace"
  return base
}

function uniqueSlug(db: DB, name: string) {
  const base = slugify(name)
  for (let i = 0; i < 1000; i++) {
    const slug = i === 0 ? base : `${base}-${i + 1}`
    if (!db.select({ id: organization.id }).from(organization).where(eq(organization.slug, slug)).get()) return slug
  }
  return `${base}-${randomToken(6).toLowerCase()}`
}

export function createWorkspace(db: DB, input: { name: string; id?: string; slug?: string }) {
  const name = input.name.trim()
  if (!name || name.length > 80) throw new UserError("Name the workspace (up to 80 characters).")
  return db
    .insert(organization)
    .values({ id: input.id ?? newId("ws"), name, slug: input.slug ?? uniqueSlug(db, name), createdAt: new Date() })
    .returning()
    .get()
}

export function findWorkspace(db: DB, idOrSlug: string) {
  return (
    db.select().from(organization).where(eq(organization.id, idOrSlug)).get() ??
    db.select().from(organization).where(eq(organization.slug, idOrSlug.toLowerCase())).get()
  )
}

export function addMember(db: DB, input: { workspaceId: string; userId: string; role: Role }) {
  const existing = db
    .select()
    .from(member)
    .where(and(eq(member.organizationId, input.workspaceId), eq(member.userId, input.userId)))
    .get()
  if (existing) return existing
  return db
    .insert(member)
    .values({ id: newId("mbr"), organizationId: input.workspaceId, userId: input.userId, role: input.role, createdAt: new Date() })
    .returning()
    .get()
}

export type Membership = { id: string; name: string; slug: string; role: Role }

export function listMemberships(db: DB, userId: string): Membership[] {
  return db
    .select({ id: organization.id, name: organization.name, slug: organization.slug, role: member.role })
    .from(member)
    .innerJoin(organization, eq(organization.id, member.organizationId))
    .where(eq(member.userId, userId))
    .orderBy(asc(member.createdAt))
    .all() as Membership[]
}

export function getRole(db: DB, userId: string, workspaceId: string): Role | null {
  const row = db
    .select({ role: member.role })
    .from(member)
    .where(and(eq(member.userId, userId), eq(member.organizationId, workspaceId)))
    .get()
  return (row?.role as Role | undefined) ?? null
}

export function listMembers(db: DB, workspaceId: string) {
  return db
    .select({
      memberId: member.id,
      userId: user.id,
      username: user.username,
      displayUsername: user.displayUsername,
      name: user.name,
      role: member.role,
      joinedAt: member.createdAt,
    })
    .from(member)
    .innerJoin(user, eq(user.id, member.userId))
    .where(eq(member.organizationId, workspaceId))
    .orderBy(asc(member.createdAt))
    .all()
    .map((m) => ({ ...m, role: m.role as Role, joinedAt: m.joinedAt.getTime() }))
}

// --- invites --------------------------------------------------------------------

export const INVITE_PREFIX = "rci_"

export function createInvite(db: DB, input: { workspaceId: string; role: Role; createdBy: string; now?: number }) {
  const token = INVITE_PREFIX + randomToken(32)
  const now = input.now ?? Date.now()
  const row = db
    .insert(workspaceInvites)
    .values({
      id: newId("inv"),
      workspaceId: input.workspaceId,
      role: input.role,
      tokenHash: sha256(token),
      createdBy: input.createdBy,
      createdAt: now,
      expiresAt: now + INVITE_TTL_MS,
    })
    .returning()
    .get()
  return { token, row }
}

export function listInvites(db: DB, workspaceId: string, now = Date.now()) {
  return db
    .select({
      id: workspaceInvites.id,
      role: workspaceInvites.role,
      createdAt: workspaceInvites.createdAt,
      expiresAt: workspaceInvites.expiresAt,
      createdBy: user.displayUsername,
    })
    .from(workspaceInvites)
    .leftJoin(user, eq(user.id, workspaceInvites.createdBy))
    .where(
      and(
        eq(workspaceInvites.workspaceId, workspaceId),
        isNull(workspaceInvites.acceptedAt),
        isNull(workspaceInvites.revokedAt),
        gt(workspaceInvites.expiresAt, now),
      ),
    )
    .orderBy(desc(workspaceInvites.createdAt))
    .all()
}

export function revokeInvite(db: DB, workspaceId: string, id: string) {
  return (
    db
      .update(workspaceInvites)
      .set({ revokedAt: Date.now() })
      .where(and(eq(workspaceInvites.id, id), eq(workspaceInvites.workspaceId, workspaceId), isNull(workspaceInvites.acceptedAt), isNull(workspaceInvites.revokedAt)))
      .run().changes > 0
  )
}

export type InviteLookup =
  | { ok: true; invite: typeof workspaceInvites.$inferSelect; workspace: { id: string; name: string } }
  | { ok: false; reason: "not_found" | "used" | "revoked" | "expired" }

export function lookupInvite(db: DB, token: string, now = Date.now()): InviteLookup {
  if (!token.startsWith(INVITE_PREFIX)) return { ok: false, reason: "not_found" }
  const invite = db.select().from(workspaceInvites).where(eq(workspaceInvites.tokenHash, sha256(token))).get()
  if (!invite) return { ok: false, reason: "not_found" }
  if (invite.revokedAt) return { ok: false, reason: "revoked" }
  if (invite.acceptedAt) return { ok: false, reason: "used" }
  if (invite.expiresAt <= now) return { ok: false, reason: "expired" }
  const ws = db.select({ id: organization.id, name: organization.name }).from(organization).where(eq(organization.id, invite.workspaceId)).get()
  if (!ws) return { ok: false, reason: "not_found" }
  // A link is only as good as its creator's current role: someone removed or
  // demoted since can't let people (themselves included) in through it.
  const creatorRole = invite.createdBy ? getRole(db, invite.createdBy, invite.workspaceId) : null
  if (!creatorRole || !canInvite(creatorRole, invite.role)) return { ok: false, reason: "revoked" }
  return { ok: true, invite, workspace: ws }
}

const INVITE_PROBLEMS = {
  not_found: "This invite link isn't valid.",
  used: "This invite link was already used.",
  revoked: "This invite link was revoked.",
  expired: "This invite link expired. Ask for a new one.",
} as const

export function inviteProblem(reason: keyof typeof INVITE_PROBLEMS) {
  return INVITE_PROBLEMS[reason]
}

// Single use: the invite is claimed and the membership written in one transaction.
function claimInvite(db: DB, token: string, userId: string, now: number) {
  const found = lookupInvite(db, token, now)
  if (!found.ok) throw new UserError(inviteProblem(found.reason))
  const claimed = db
    .update(workspaceInvites)
    .set({ acceptedAt: now, acceptedBy: userId })
    .where(and(eq(workspaceInvites.id, found.invite.id), isNull(workspaceInvites.acceptedAt), isNull(workspaceInvites.revokedAt)))
    .run().changes
  if (claimed !== 1) throw new UserError(inviteProblem("used"))
  addMember(db, { workspaceId: found.invite.workspaceId, userId, role: found.invite.role })
  return found.workspace
}

export function acceptInviteAsUser(db: DB, token: string, userId: string, now = Date.now()) {
  return db.transaction(() => claimInvite(db, token, userId, now))
}

export async function acceptInviteWithNewUser(db: DB, token: string, input: { username: string; password: string }, now = Date.now()) {
  const pre = lookupInvite(db, token, now)
  if (!pre.ok) throw new UserError(inviteProblem(pre.reason))
  const problem = passwordProblem(input.password)
  if (problem) throw new UserError(`Password: ${problem}`)
  const passwordHash = await hashPassword(input.password)
  return db.transaction(() => {
    const u = insertUser(db, { username: input.username, passwordHash })
    const workspace = claimInvite(db, token, u.id, now)
    return { user: u, workspace }
  })
}

// --- first boot -----------------------------------------------------------------

export type BootstrapResult = { created: false; reason: "users_exist" | "no_admin_hash" } | { created: true; userId: string; workspaceId: string }

// The first boot after migration 0004: the old single admin becomes user
// `zaki`, owner of workspace "Zaki", which the migration filled with every
// existing row. Does nothing once any user exists.
export function bootstrapUsers(db: DB, adminPasswordHash = config().adminPasswordHash): BootstrapResult {
  return db.transaction(() => {
    if (userCount(db) > 0) return { created: false as const, reason: "users_exist" as const }
    if (!adminPasswordHash.trim()) return { created: false as const, reason: "no_admin_hash" as const }
    const u = insertUser(db, { username: LEGACY_USERNAME, name: "Zaki", passwordHash: adminPasswordHash.trim() })
    const ws = findWorkspace(db, LEGACY_WORKSPACE.id) ?? createWorkspace(db, { ...LEGACY_WORKSPACE })
    addMember(db, { workspaceId: ws.id, userId: u.id, role: "owner" })
    return { created: true as const, userId: u.id, workspaceId: ws.id }
  })
}

// --- member management ------------------------------------------------------------
// Admins manage members and admins. Only owners touch owners. A workspace always
// keeps at least one owner.

export type Actor = { userId: string; role: Role }

function memberIn(db: DB, workspaceId: string, memberId: string) {
  return db
    .select()
    .from(member)
    .where(and(eq(member.id, memberId), eq(member.organizationId, workspaceId)))
    .get()
}

function ownerCount(db: DB, workspaceId: string) {
  return db
    .select({ n: count() })
    .from(member)
    .where(and(eq(member.organizationId, workspaceId), eq(member.role, "owner")))
    .get()!.n
}

// Pending invites `userId` created in the workspace, optionally only those
// matching `which`, are revoked. Returns how many.
function revokeInvitesBy(db: DB, workspaceId: string, userId: string, which: (role: Role) => boolean = () => true) {
  const pending = db
    .select({ id: workspaceInvites.id, role: workspaceInvites.role })
    .from(workspaceInvites)
    .where(
      and(
        eq(workspaceInvites.workspaceId, workspaceId),
        eq(workspaceInvites.createdBy, userId),
        isNull(workspaceInvites.acceptedAt),
        isNull(workspaceInvites.revokedAt),
      ),
    )
    .all()
    .filter((i) => which(i.role))
  if (pending.length === 0) return 0
  db.update(workspaceInvites)
    .set({ revokedAt: Date.now() })
    .where(inArray(workspaceInvites.id, pending.map((i) => i.id)))
    .run()
  return pending.length
}

export function changeMemberRole(db: DB, workspaceId: string, actor: Actor, memberId: string, role: Role) {
  return db.transaction(() => {
    const target = memberIn(db, workspaceId, memberId)
    if (!target) throw new UserError("No such member.")
    if (actor.role === "member") throw new UserError("Only owners and admins can change roles.")
    if (actor.role !== "owner" && (target.role === "owner" || role === "owner")) throw new UserError("Only owners can make or change owners.")
    if (target.role === "owner" && role !== "owner" && ownerCount(db, workspaceId) <= 1) {
      throw new UserError("A workspace needs at least one owner. Make someone else an owner first.")
    }
    db.update(member).set({ role }).where(eq(member.id, target.id)).run()
    // Links they made at a role they can no longer hand out stop working,
    // and so do keys they made once their new role can't create keys.
    revokeInvitesBy(db, workspaceId, target.userId, (invited) => !canInvite(role, invited))
    const revokedKeys = canManageKeys(role) ? 0 : revokeKeysBy(db, workspaceId, target.userId)
    return { ...target, role, revokedKeys }
  })
}

export function removeMember(db: DB, workspaceId: string, actor: Actor, memberId: string) {
  return db.transaction(() => {
    const target = memberIn(db, workspaceId, memberId)
    if (!target) throw new UserError("No such member.")
    const self = target.userId === actor.userId
    if (!self && actor.role === "member") throw new UserError("Only owners and admins can remove members.")
    if (!self && actor.role !== "owner" && target.role === "owner") throw new UserError("Only owners can remove an owner.")
    if (target.role === "owner" && ownerCount(db, workspaceId) <= 1) throw new UserError("A workspace needs at least one owner.")
    db.delete(member).where(eq(member.id, target.id)).run()
    // Without this, a saved invite link of their own would let them back in,
    // and keys they made would keep reading the workspace.
    revokeInvitesBy(db, workspaceId, target.userId)
    const revokedKeys = revokeKeysBy(db, workspaceId, target.userId)
    return { ...target, revokedKeys }
  })
}

function revokeKeysBy(db: DB, workspaceId: string, userId: string) {
  return db
    .update(apiKeys)
    .set({ revokedAt: Date.now() })
    .where(and(eq(apiKeys.workspaceId, workspaceId), eq(apiKeys.createdBy, userId), isNull(apiKeys.revokedAt)))
    .run().changes
}

// Matches the createKey server fn, which requires admin or above.
export function canManageKeys(role: Role) {
  return role !== "member"
}

export function canInvite(actor: Role, role: Role) {
  if (actor === "member") return false
  return role !== "owner" || actor === "owner"
}

// --- passkeys (listing and removal; adding goes through WebAuthn) -------------------

export function listPasskeys(db: DB, userId: string) {
  return db
    .select({ id: passkey.id, name: passkey.name, deviceType: passkey.deviceType, backedUp: passkey.backedUp, createdAt: passkey.createdAt })
    .from(passkey)
    .where(eq(passkey.userId, userId))
    .orderBy(desc(passkey.createdAt))
    .all()
    .map((p) => ({ ...p, createdAt: p.createdAt?.getTime() ?? null }))
}

export function deletePasskey(db: DB, userId: string, id: string) {
  return db.delete(passkey).where(and(eq(passkey.id, id), eq(passkey.userId, userId))).run().changes > 0
}
