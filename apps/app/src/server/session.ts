// Who is calling a dashboard server function, and which workspace they act in.
// Server-only.
import { getRequest } from "@tanstack/react-start/server"
import { eq } from "drizzle-orm"
import { getAuth } from "./better-auth"
import { config } from "./config"
import { getDb, type DB } from "./db"
import { session, type Role } from "./db/schema"
import { getRole, listMemberships, type Membership } from "./users"

export class UnauthorizedError extends Error {
  constructor() {
    super("UNAUTHORIZED")
  }
}

export class ForbiddenError extends Error {}

const RANK: Record<Role, number> = { member: 1, admin: 2, owner: 3 }

export function roleAtLeast(role: Role, min: Role) {
  return RANK[role] >= RANK[min]
}

export type Viewer = {
  sessionId: string
  user: { id: string; username: string; name: string }
  workspace: Membership | null
  workspaces: Membership[]
}

// Reads the session and resolves the active workspace. A session pointing at a
// workspace the user has left falls back to their first one.
export async function viewerFromHeaders(db: DB, headers: Headers): Promise<Viewer | null> {
  const s = await getAuth(db).api.getSession({ headers })
  if (!s) return null
  const workspaces = listMemberships(db, s.user.id)
  const active = s.session.activeOrganizationId
  const workspace = workspaces.find((w) => w.id === active) ?? workspaces[0] ?? null
  if ((workspace?.id ?? null) !== (active ?? null)) {
    db.update(session).set({ activeOrganizationId: workspace?.id ?? null }).where(eq(session.id, s.session.id)).run()
  }
  return {
    sessionId: s.session.id,
    user: { id: s.user.id, username: s.user.displayUsername || s.user.username || s.user.name, name: s.user.name },
    workspace,
    workspaces,
  }
}

export function getViewer() {
  return viewerFromHeaders(getDb(), getRequest().headers)
}

// The caller must be signed in and a member of their active workspace with at
// least `min`. Returns the workspace id every query has to be scoped to.
export async function requireMember(min: Role = "member") {
  const db = getDb()
  const viewer = await viewerFromHeaders(db, getRequest().headers)
  if (!viewer) throw new UnauthorizedError()
  if (!viewer.workspace) throw new ForbiddenError("You aren't in a workspace yet. Ask an owner for an invite link.")
  // Re-read the role so a change takes effect on the next call.
  const role = getRole(db, viewer.user.id, viewer.workspace.id)
  if (!role) throw new ForbiddenError("You're no longer a member of this workspace.")
  if (!roleAtLeast(role, min)) throw new ForbiddenError(`Only workspace ${min === "owner" ? "owners" : "owners and admins"} can do that.`)
  return { db, ws: viewer.workspace.id, role, viewer }
}

export async function requireUser() {
  const db = getDb()
  const viewer = await viewerFromHeaders(db, getRequest().headers)
  if (!viewer) throw new UnauthorizedError()
  return { db, viewer }
}

// Writes from the dashboard must come from the dashboard's own origin.
export function checkSameOrigin(headers: Headers) {
  if (headers.get("sec-fetch-site") === "cross-site") throw new ForbiddenError("cross-origin request refused")
  const origin = headers.get("origin")
  if (!origin) return
  let originHost: string
  try {
    originHost = new URL(origin).host
  } catch {
    throw new ForbiddenError("cross-origin request refused")
  }
  const host = headers.get("x-forwarded-host") ?? headers.get("host")
  if (originHost === new URL(config().url).host) return
  if (!host || originHost !== host) throw new ForbiddenError("cross-origin request refused")
}

export function requireSameOrigin() {
  checkSameOrigin(getRequest().headers)
}

export function clientIp(headers: Headers) {
  return headers.get("cf-connecting-ip") ?? headers.get("x-forwarded-for")?.split(",")[0]?.trim() ?? headers.get("x-real-ip") ?? "local"
}
