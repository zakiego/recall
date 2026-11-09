import { beforeEach, describe, expect, it } from "vitest"
import { createApiKey, hashLegacyPassword, verifyApiKey } from "./auth"
import { authHandler } from "./better-auth"
import { openDb, type DB } from "./db"
import { checkSameOrigin, viewerFromHeaders } from "./session"
import {
  acceptInviteAsUser,
  acceptInviteWithNewUser,
  addMember,
  bootstrapUsers,
  canInvite,
  changeMemberRole,
  createInvite,
  createUserWithPassword,
  createWorkspace,
  getRole,
  INVITE_TTL_MS,
  listInvites,
  listMembers,
  lookupInvite,
  removeMember,
  revokeInvite,
  UserError,
} from "./users"

let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

const PW = "a long enough password"

describe("bootstrap", () => {
  it("does nothing without ADMIN_PASSWORD_HASH", () => {
    expect(bootstrapUsers(db, "")).toEqual({ created: false, reason: "no_admin_hash" })
  })

  it("creates zaki as owner of Zaki once, then never again", async () => {
    const hash = await hashLegacyPassword(PW)
    const res = bootstrapUsers(db, hash)
    expect(res).toMatchObject({ created: true, workspaceId: "ws_zaki" })
    const members = listMembers(db, "ws_zaki")
    expect(members).toEqual([expect.objectContaining({ username: "zaki", role: "owner" })])
    expect(bootstrapUsers(db, hash)).toEqual({ created: false, reason: "users_exist" })
  })
})

describe("users", () => {
  it("normalizes usernames and refuses duplicates and bad ones", async () => {
    const u = await createUserWithPassword(db, { username: "Eva.G", password: PW })
    expect(u.username).toBe("eva.g")
    expect(u.displayUsername).toBe("Eva.G")
    expect(u.email).toBe("eva.g@users.recall.invalid")
    await expect(createUserWithPassword(db, { username: "eva.g", password: PW })).rejects.toThrow(UserError)
    await expect(createUserWithPassword(db, { username: "no spaces", password: PW })).rejects.toThrow(/letters, numbers/)
    await expect(createUserWithPassword(db, { username: "ok_name", password: "short" })).rejects.toThrow(/at least 12/)
  })
})

describe("username collisions", () => {
  it("turns a placeholder email clash into a UserError, not a 500", async () => {
    const renamed = await createUserWithPassword(db, { username: "mallory", password: PW })
    // A rename from before usernames were immutable keeps the old placeholder email.
    db.$client.prepare("UPDATE user SET username = 'eva' WHERE id = ?").run(renamed.id)
    const ws = createWorkspace(db, { name: "Team" }).id
    const owner = await createUserWithPassword(db, { username: "owner", password: PW })
    addMember(db, { workspaceId: ws, userId: owner.id, role: "owner" })
    const { token } = createInvite(db, { workspaceId: ws, role: "member", createdBy: owner.id })
    await expect(acceptInviteWithNewUser(db, token, { username: "mallory", password: PW })).rejects.toThrow(UserError)
    await expect(createUserWithPassword(db, { username: "Mallory", password: PW })).rejects.toThrow(/taken/)
    expect(lookupInvite(db, token)).toMatchObject({ ok: true })
  })
})

describe("invites", () => {
  let owner: string
  let ws: string
  beforeEach(async () => {
    owner = (await createUserWithPassword(db, { username: "owner", password: PW })).id
    ws = createWorkspace(db, { name: "Team" }).id
    addMember(db, { workspaceId: ws, userId: owner, role: "owner" })
  })

  it("creates a user and a membership, once", async () => {
    const { token } = createInvite(db, { workspaceId: ws, role: "member", createdBy: owner })
    expect(listInvites(db, ws)).toHaveLength(1)
    const { user } = await acceptInviteWithNewUser(db, token, { username: "eva", password: PW })
    expect(getRole(db, user.id, ws)).toBe("member")
    expect(listInvites(db, ws)).toHaveLength(0)
    await expect(acceptInviteWithNewUser(db, token, { username: "eva2", password: PW })).rejects.toThrow(/already used/)
    expect(lookupInvite(db, token)).toEqual({ ok: false, reason: "used" })
  })

  it("rolls back the new user when the invite can't be claimed", async () => {
    const { token, row } = createInvite(db, { workspaceId: ws, role: "member", createdBy: owner })
    revokeInvite(db, ws, row.id)
    await expect(acceptInviteWithNewUser(db, token, { username: "ghost", password: PW })).rejects.toThrow(/revoked/)
    expect(db.$client.prepare("SELECT count(*) AS n FROM user WHERE username = 'ghost'").get()).toEqual({ n: 0 })
  })

  it("expires after 7 days and refuses unknown tokens", () => {
    const now = Date.now()
    const { token } = createInvite(db, { workspaceId: ws, role: "admin", createdBy: owner, now })
    expect(lookupInvite(db, token, now + INVITE_TTL_MS + 1)).toEqual({ ok: false, reason: "expired" })
    expect(lookupInvite(db, "rci_nope")).toEqual({ ok: false, reason: "not_found" })
  })

  it("lets an existing user join a second workspace", async () => {
    const other = await createUserWithPassword(db, { username: "joiner", password: PW })
    const { token } = createInvite(db, { workspaceId: ws, role: "admin", createdBy: owner })
    expect(acceptInviteAsUser(db, token, other.id).id).toBe(ws)
    expect(getRole(db, other.id, ws)).toBe("admin")
  })

  it("only owners can invite owners", () => {
    expect(canInvite("owner", "owner")).toBe(true)
    expect(canInvite("admin", "owner")).toBe(false)
    expect(canInvite("admin", "admin")).toBe(true)
    expect(canInvite("member", "member")).toBe(false)
  })

  it("can't be revoked from another workspace", () => {
    const other = createWorkspace(db, { name: "Other" }).id
    const { row } = createInvite(db, { workspaceId: ws, role: "member", createdBy: owner })
    expect(revokeInvite(db, other, row.id)).toBe(false)
    expect(revokeInvite(db, ws, row.id)).toBe(true)
  })
})

describe("roles", () => {
  it("protects owners and keeps one owner", async () => {
    const ws = createWorkspace(db, { name: "Team" }).id
    const ids: Record<string, { user: string; member: string }> = {}
    for (const [name, role] of [
      ["own", "owner"],
      ["adm", "admin"],
      ["mem", "member"],
    ] as const) {
      const u = await createUserWithPassword(db, { username: name, password: PW })
      ids[name] = { user: u.id, member: addMember(db, { workspaceId: ws, userId: u.id, role }).id }
    }
    const admin = { userId: ids.adm!.user, role: "admin" as const }
    const owner = { userId: ids.own!.user, role: "owner" as const }
    const memberActor = { userId: ids.mem!.user, role: "member" as const }

    expect(() => changeMemberRole(db, ws, admin, ids.own!.member, "member")).toThrow(/Only owners/)
    expect(() => changeMemberRole(db, ws, admin, ids.mem!.member, "owner")).toThrow(/Only owners/)
    expect(() => changeMemberRole(db, ws, memberActor, ids.adm!.member, "member")).toThrow(UserError)
    expect(() => changeMemberRole(db, ws, owner, ids.own!.member, "admin")).toThrow(/at least one owner/)
    expect(() => removeMember(db, ws, admin, ids.own!.member)).toThrow(/Only owners/)
    expect(() => removeMember(db, ws, memberActor, ids.adm!.member)).toThrow(UserError)

    changeMemberRole(db, ws, admin, ids.mem!.member, "admin")
    expect(getRole(db, ids.mem!.user, ws)).toBe("admin")
    // Anyone can leave, except the last owner.
    removeMember(db, ws, { userId: ids.mem!.user, role: "admin" }, ids.mem!.member)
    expect(getRole(db, ids.mem!.user, ws)).toBeNull()
    expect(() => removeMember(db, ws, owner, ids.own!.member)).toThrow(/at least one owner/)
    // A member id from another workspace is "no such member".
    const other = createWorkspace(db, { name: "Other" }).id
    expect(() => changeMemberRole(db, other, owner, ids.adm!.member, "member")).toThrow(/No such member/)
  })
})

describe("removed and demoted members", () => {
  let ws: string
  let owner: { userId: string; role: "owner" }
  beforeEach(async () => {
    ws = createWorkspace(db, { name: "Team" }).id
    const u = await createUserWithPassword(db, { username: "owner", password: PW })
    addMember(db, { workspaceId: ws, userId: u.id, role: "owner" })
    owner = { userId: u.id, role: "owner" }
  })

  async function join(username: string, role: "owner" | "admin" | "member") {
    const u = await createUserWithPassword(db, { username, password: PW })
    return { userId: u.id, memberId: addMember(db, { workspaceId: ws, userId: u.id, role }).id }
  }

  it("can't rejoin through their own saved link, and their keys stop working", async () => {
    const admin = await join("adm", "admin")
    const { token } = createInvite(db, { workspaceId: ws, role: "admin", createdBy: admin.userId })
    const { key } = createApiKey(db, { workspaceId: ws, name: "adm laptop", createdBy: admin.userId })
    const ownersKey = createApiKey(db, { workspaceId: ws, name: "owner", createdBy: owner.userId }).key
    const removed = removeMember(db, ws, owner, admin.memberId)
    expect(removed.revokedKeys).toBe(1)
    expect(lookupInvite(db, token)).toEqual({ ok: false, reason: "revoked" })
    expect(() => acceptInviteAsUser(db, token, admin.userId)).toThrow(/revoked/)
    expect(getRole(db, admin.userId, ws)).toBeNull()
    expect(verifyApiKey(db, key)).toBeNull()
    expect(verifyApiKey(db, ownersKey)).not.toBeNull()
  })

  it("revokes only the invites a demoted member can no longer grant", async () => {
    const co = await join("coowner", "owner")
    const ownerLink = createInvite(db, { workspaceId: ws, role: "owner", createdBy: co.userId })
    const adminLink = createInvite(db, { workspaceId: ws, role: "admin", createdBy: co.userId })
    changeMemberRole(db, ws, owner, co.memberId, "admin")
    expect(lookupInvite(db, ownerLink.token)).toEqual({ ok: false, reason: "revoked" })
    expect(lookupInvite(db, adminLink.token)).toMatchObject({ ok: true })
    changeMemberRole(db, ws, owner, co.memberId, "member")
    expect(lookupInvite(db, adminLink.token)).toEqual({ ok: false, reason: "revoked" })
    expect(listInvites(db, ws)).toEqual([])
  })

  it("re-checks the creator's role when the link is used", async () => {
    const admin = await join("adm", "admin")
    const { token } = createInvite(db, { workspaceId: ws, role: "member", createdBy: admin.userId })
    // A role change that didn't go through changeMemberRole still counts.
    db.$client.prepare("UPDATE member SET role = 'member' WHERE id = ?").run(admin.memberId)
    await expect(acceptInviteWithNewUser(db, token, { username: "newbie", password: PW })).rejects.toThrow(/revoked/)
    expect(db.$client.prepare("SELECT count(*) AS n FROM user WHERE username = 'newbie'").get()).toEqual({ n: 0 })
  })
})

// --- Better Auth endpoints ----------------------------------------------------------

const ORIGIN = "http://localhost:4800"

function authReq(path: string, body?: unknown, headers: Record<string, string> = {}) {
  return new Request(`${ORIGIN}/api/auth${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: { "content-type": "application/json", origin: ORIGIN, "x-forwarded-for": "203.0.113.7", ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

function cookieHeader(res: Response) {
  return res.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ")
}

describe("better auth", () => {
  it("signs in by username with the old pbkdf2 admin hash and sets a safe cookie", async () => {
    bootstrapUsers(db, await hashLegacyPassword("old admin password"))
    const auth = authHandler(db)
    const res = await auth(authReq("/sign-in/username", { username: "zaki", password: "old admin password" }))
    expect(res.status).toBe(200)
    const set = res.headers.getSetCookie().find((c) => c.startsWith("recall.session_token="))!
    expect(set).toMatch(/HttpOnly/i)
    expect(set).toMatch(/SameSite=Lax/i)
    // The session lands in workspace Zaki.
    const viewer = await viewerFromHeaders(db, new Headers({ cookie: cookieHeader(res) }))
    expect(viewer?.workspace).toMatchObject({ id: "ws_zaki", role: "owner" })

    const wrong = await auth(authReq("/sign-in/username", { username: "zaki", password: "nope nope nope" }))
    expect(wrong.status).toBe(401)
  })

  it("has no open signup", async () => {
    const res = await authHandler(db)(authReq("/sign-up/email", { email: "x@example.com", password: PW, name: "x", username: "intruder" }))
    expect(res.status).toBeGreaterThanOrEqual(400)
    expect(db.$client.prepare("SELECT count(*) AS n FROM user").get()).toEqual({ n: 0 })
  })

  it("rate limits sign-in per client IP", async () => {
    await createUserWithPassword(db, { username: "target", password: PW })
    const auth = authHandler(db)
    const statuses: number[] = []
    for (let i = 0; i < 7; i++) {
      statuses.push((await auth(authReq("/sign-in/username", { username: "target", password: "wrong password!" }, { "cf-connecting-ip": "198.51.100.9" }))).status)
    }
    expect(statuses.slice(0, 5).every((s) => s === 401)).toBe(true)
    expect(statuses.at(-1)).toBe(429)
    // A different client isn't locked out.
    const other = await auth(authReq("/sign-in/username", { username: "target", password: PW }, { "cf-connecting-ip": "198.51.100.10" }))
    expect(other.status).toBe(200)
  })

  it("refuses sign-in from another origin", async () => {
    await createUserWithPassword(db, { username: "target", password: PW })
    const auth = authHandler(db)
    const res = await auth(authReq("/sign-in/username", { username: "target", password: PW }, { origin: "https://evil.example" }))
    expect(res.status).toBe(403)
    expect(res.headers.getSetCookie()).toEqual([])
    const fetchMeta = await auth(authReq("/sign-in/username", { username: "target", password: PW }, { origin: "", "sec-fetch-site": "cross-site" }))
    expect(fetchMeta.status).toBe(403)
    // With a session cookie, Better Auth's own origin check also applies.
    const ok = await auth(authReq("/sign-in/username", { username: "target", password: PW }))
    const signOut = await auth(authReq("/sign-out", {}, { cookie: cookieHeader(ok), origin: "https://evil.example" }))
    expect(signOut.status).toBe(403)
  })

  it("hides the organization plugin's endpoints", async () => {
    const u = await createUserWithPassword(db, { username: "mallory", password: PW })
    const ws = createWorkspace(db, { name: "Team" })
    const m = addMember(db, { workspaceId: ws.id, userId: u.id, role: "admin" })
    const auth = authHandler(db)
    const signedIn = await auth(authReq("/sign-in/username", { username: "mallory", password: PW }))
    expect(signedIn.status).toBe(200)
    const cookie = cookieHeader(signedIn)
    const calls: [string, unknown?][] = [
      ["/organization/create", { name: "Mine", slug: "zaki-typo" }],
      ["/organization/update", { organizationId: ws.id, data: { name: "Renamed", slug: "zaki" } }],
      ["/organization/update-member-role", { memberId: m.id, role: ["member", "admin"] }],
      ["/organization/invite-member", { email: "x@example.com", role: "owner" }],
      ["/organization/set-active", { organizationId: ws.id }],
      ["/organization/list"],
      ["//organization/create", { name: "Slashes" }],
      ["/%6Frganization/create", { name: "Encoded" }],
      ["/ORGANIZATION/create", { name: "Upper" }],
    ]
    for (const [path, body] of calls) {
      const res = await auth(authReq(path, body, { cookie }))
      expect(res.status, path).toBe(404)
    }
    expect(db.$client.prepare("SELECT name, slug FROM organization").all()).toEqual([{ name: "Team", slug: "team" }])
    expect(getRole(db, u.id, ws.id)).toBe("admin")
  })

  it("keeps usernames fixed and display names equal to them", async () => {
    await createUserWithPassword(db, { username: "mallory", password: PW })
    await createUserWithPassword(db, { username: "eva", password: PW })
    const auth = authHandler(db)
    const cookie = cookieHeader(await auth(authReq("/sign-in/username", { username: "mallory", password: PW })))
    const update = async (body: unknown) => (await auth(authReq("/update-user", body, { cookie }))).status
    expect(await update({ username: "mallory2" })).toBe(400)
    expect(await update({ displayUsername: "Eva" })).toBe(400)
    expect(await update({ username: "mallory", displayUsername: "Eva" })).toBe(400)
    expect(await update({ displayUsername: "Eva Smith" })).toBe(400)
    expect(await update({ displayUsername: "MalLory" })).toBe(200)
    expect(db.$client.prepare("SELECT username, display_username AS d FROM user ORDER BY username").all()).toEqual([
      { username: "eva", d: "eva" },
      { username: "mallory", d: "MalLory" },
    ])
  })

  it("stores passkey challenges server-side", async () => {
    const res = await authHandler(db)(authReq("/passkey/generate-authenticate-options"))
    expect(res.status).toBe(200)
    const opts = (await res.json()) as { challenge: string; rpId: string }
    expect(opts.rpId).toBe("localhost")
    const rows = db.$client.prepare("SELECT value FROM verification").all() as { value: string }[]
    expect(rows.some((r) => r.value.includes(opts.challenge))).toBe(true)
  })
})

describe("same-origin check for dashboard writes", () => {
  it("accepts same origin and refuses cross-site", () => {
    expect(() => checkSameOrigin(new Headers({ origin: ORIGIN, host: "localhost:4800" }))).not.toThrow()
    expect(() => checkSameOrigin(new Headers({ origin: "https://evil.example", host: "localhost:4800" }))).toThrow(/cross-origin/)
    expect(() => checkSameOrigin(new Headers({ "sec-fetch-site": "cross-site", host: "localhost:4800" }))).toThrow(/cross-origin/)
    expect(() => checkSameOrigin(new Headers({ origin: "not a url", host: "localhost:4800" }))).toThrow(/cross-origin/)
  })
})
