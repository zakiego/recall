// Server functions behind the dashboard. Each one resolves the caller's session
// and active workspace itself, and scopes every read and write to it.
import { createServerFn } from "@tanstack/react-start"
import { getRequest } from "@tanstack/react-start/server"
import { eq } from "drizzle-orm"
import { z } from "zod"
import { addContent, addFile, search as runSearch, MAX_FILE_BYTES, upcoming } from "@/server/api"
import { emailConfigured } from "@/server/email"
import { getReminderSettings, MAX_LEAD_DAYS, MAX_LEADS, saveReminderSettings, sendTestReminder } from "@/server/reminders"
import { addDays, isTimezone, parseDate, todayIn } from "@/lib/dates"
import { ask } from "@/server/ask"
import { createApiKey, listApiKeys, MAX_PASSWORD_LENGTH, revokeApiKey } from "@/server/auth"
import { authConfigured, getAuth } from "@/server/better-auth"
import { config } from "@/server/config"
import { mayRetry } from "@/server/connectors/gmail"
import { getDb } from "@/server/db"
import { ROLES, session } from "@/server/db/schema"
import { buildGraph } from "@/server/graph"
import { enqueue } from "@/server/jobs/queue"
import {
  countDocuments,
  countUpcoming,
  deleteDocument,
  editMemory,
  forgetMemory,
  getDocument,
  getDocumentTitle,
  getMemory,
  hasNewerVersion,
  getProfile,
  listContainers,
  listDocuments,
  listMemories,
  MAX_CONTAINER_LEN,
  memoryChain,
  reviewCount,
  resolveConflict,
  versionCounts,
} from "@/server/memories"
import { deletePerson, getPerson, listPeople, mergePeople, peopleForMemories, PersonError, suggestPeopleMerges, updatePerson } from "@/server/people"
import { RateLimiter } from "@/server/rate-limit"
import { clientIp, ForbiddenError, getViewer, requireMember, requireSameOrigin, requireUser, roleAtLeast } from "@/server/session"
import {
  acceptInviteAsUser,
  acceptInviteWithNewUser,
  addMember,
  canInvite,
  changeMemberRole,
  createInvite,
  createWorkspace,
  deletePasskey,
  getRole,
  inviteProblem,
  listInvites,
  listMembers,
  listPasskeys,
  lookupInvite,
  removeMember,
  revokeInvite,
  UserError,
  userCount,
} from "@/server/users"
import { USERNAME_MAX } from "@/server/usernames"
import { APP_BASE } from "@/lib/app-path"

const container = z.string().trim().min(1).max(MAX_CONTAINER_LEN)
const id = z.string().min(1).max(64)
const role = z.enum(ROLES)
const dateField = z
  .string()
  .nullable()
  .optional()
  .refine((v) => v == null || parseDate(v) !== null, { message: "Use a full date, a month or a year." })

// --- session ----------------------------------------------------------------

// The zone "today" is counted in: the one saved for reminders, else the
// browser's zone from the cookie the root page sets, else UTC.
function viewerTimezone(saved: { timezone: string; updatedAt: number | null } | null) {
  if (saved?.updatedAt != null) return saved.timezone
  const cookie = getRequest().headers.get("cookie") ?? ""
  const raw = /(?:^|;\s*)recall\.tz=([^;]+)/.exec(cookie)?.[1]
  if (!raw) return "UTC"
  try {
    const tz = decodeURIComponent(raw)
    return isTimezone(tz) ? tz : "UTC"
  } catch {
    return "UTC"
  }
}

export const getSession = createServerFn({ method: "GET" }).handler(async () => {
  const viewer = await getViewer()
  const timezone = viewerTimezone(viewer ? getReminderSettings(getDb(), viewer.user.id) : null)
  return {
    viewer,
    today: todayIn(timezone),
    configured: authConfigured(),
    hasUsers: userCount(getDb()) > 0,
    defaultContainer: config().defaultContainer,
  }
})

export const switchWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, viewer } = await requireUser()
    if (!getRole(db, viewer.user.id, data.id)) throw new ForbiddenError("You're not a member of that workspace.")
    db.update(session).set({ activeOrganizationId: data.id }).where(eq(session.id, viewer.sessionId)).run()
    return { ok: true }
  })

export const newWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ name: z.string().trim().min(1).max(80) }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, viewer } = await requireUser()
    const ws = db.transaction(() => {
      const ws = createWorkspace(db, { name: data.name })
      addMember(db, { workspaceId: ws.id, userId: viewer.user.id, role: "owner" })
      db.update(session).set({ activeOrganizationId: ws.id }).where(eq(session.id, viewer.sessionId)).run()
      return ws
    })
    return { id: ws.id, name: ws.name }
  })

// --- containers and memories -------------------------------------------------

export const getContainers = createServerFn({ method: "GET" }).handler(async () => {
  const { db, ws } = await requireMember()
  return { containers: listContainers(db, ws), defaultContainer: config().defaultContainer }
})

export const getMemories = createServerFn({ method: "GET" })
  .validator(
    z.object({
      container,
      cursor: z.string().optional(),
      history: z.boolean().optional(),
      review: z.boolean().optional(),
      limit: z.number().int().min(1).max(200).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    const page = listMemories(db, { ...data, workspaceId: ws, limit: data.limit ?? 100 })
    const counts = versionCounts(
      db,
      ws,
      page.memories.map((m) => m.id),
    )
    const docIds = [...new Set(page.memories.map((m) => m.sourceDocumentId).filter((d): d is string => !!d))]
    const titles = Object.fromEntries(docIds.map((d) => [d, getDocumentTitle(db, ws, d)]))
    // The other side of each flagged fact, so the review queue can link the pair.
    const other = (id: string | null) => {
      const m = id ? getMemory(db, ws, id) : undefined
      return m ? { id: m.id, fact: m.fact, current: m.validTo === null } : null
    }
    const linked = peopleForMemories(
      db,
      ws,
      page.memories.map((m) => m.id),
    )
    return {
      memories: page.memories.map((m) => ({
        ...m,
        versions: (counts[m.id] ?? 0) + 1,
        sourceTitle: m.sourceDocumentId ? (titles[m.sourceDocumentId] ?? null) : null,
        conflictWith: m.reviewNote ? other(m.conflictsWithId) : null,
        people: linked[m.id] ?? [],
      })),
      nextCursor: page.nextCursor,
      reviewCount: reviewCount(db, ws, data.container),
      profile: getProfile(db, ws, data.container),
    }
  })

export const getChain = createServerFn({ method: "GET" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    const chain = memoryChain(db, ws, data.id)
    // The walk stops after 500 versions each way; number rows from the real start.
    const first = chain[0] ? (versionCounts(db, ws, [chain[0].id])[chain[0].id] ?? 0) + 1 : 1
    const last = chain.at(-1)
    return {
      firstVersion: first,
      // The walk also stops 500 versions forward; then the last row isn't deleted.
      hasNewer: last ? last.validTo !== null && hasNewerVersion(db, ws, last.id) : false,
      versions: chain.map((m) => ({
        ...m,
        sourceTitle: m.sourceDocumentId ? getDocumentTitle(db, ws, m.sourceDocumentId) : null,
      })),
    }
  })

export const updateMemory = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id,
      fact: z.string().trim().min(1).max(500),
      keywords: z.string().trim().max(500).optional(),
      kind: z.enum(["static", "dynamic"]).optional(),
      // "2026-11-05", "2026-11" or "2026"; null clears it, missing keeps it.
      eventDate: dateField,
      dueDate: dateField,
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    const { eventDate, dueDate, ...rest } = data
    const toDated = (v: string | null | undefined) => (v === undefined ? undefined : v === null ? null : parseDate(v))
    const row = await editMemory(db, ws, data.id, { ...rest, event: toDated(eventDate), due: toDated(dueDate) })
    if (!row) throw new Error("This fact changed since you opened it. Reload to edit the current version.")
    return row
  })

export const resolveMemory = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    const row = resolveConflict(db, ws, data.id)
    if (!row) throw new Error("This fact changed since you opened it. Reload to see the current version.")
    return row
  })

export const deleteMemory = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    const row = forgetMemory(db, ws, data.id)
    if (!row) throw new Error("No such memory.")
    return row
  })

// --- upcoming and reminders ----------------------------------------------------

export const UPCOMING_WINDOWS = [7, 30, 90, 365] as const
// Facts that came due this recently still show, as past due.
const PAST_DAYS = 30

export const getUpcoming = createServerFn({ method: "GET" })
  .validator(z.object({ container, days: z.number().int().min(1).max(366), all: z.boolean().optional() }))
  .handler(async ({ data }) => {
    const { db, ws, viewer } = await requireMember()
    const settings = getReminderSettings(db, viewer.user.id)
    const base = { container: data.container, all: data.all ?? false, tz: viewerTimezone(settings) }
    const res = upcoming(db, ws, { ...base, days: data.days, past_days: PAST_DAYS })
    // Counted, not read from the list, which stops at its limit.
    const range = { workspaceId: ws, container: base.all ? undefined : base.container }
    const counts = Object.fromEntries(
      UPCOMING_WINDOWS.map((d) => [d, countUpcoming(db, { ...range, from: res.today, to: addDays(res.today, d) })]),
    ) as Record<(typeof UPCOMING_WINDOWS)[number], number>
    const counted = versionCounts(
      db,
      ws,
      res.memories.map((m) => m.id),
    )
    return {
      today: res.today,
      memories: res.memories.map((m) => ({ ...m, versions: (counted[m.id] ?? 0) + 1 })),
      counts,
      // Everything in the shown range; more than memories.length when the list hit its limit.
      total: countUpcoming(db, { ...range, from: res.from, to: res.to }),
      pastDays: PAST_DAYS,
      reminders: { enabled: settings.enabled, email: settings.email, leadDays: settings.leadDays, timezone: settings.timezone, configured: emailConfigured() },
    }
  })

export const getReminders = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  return { settings: getReminderSettings(db, viewer.user.id), configured: emailConfigured(), from: config().emailFrom || null }
})

export const saveReminders = createServerFn({ method: "POST" })
  .validator(
    z.object({
      enabled: z.boolean(),
      email: z.union([z.email({ message: "Enter an email address like you@example.com." }).max(254), z.literal("")]),
      leadDays: z.array(z.number().int().min(0).max(MAX_LEAD_DAYS)).max(MAX_LEADS * 2),
      timezone: z.string().max(64),
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, viewer } = await requireUser()
    if (data.enabled && !data.email) throw new Error("Add an email address to turn reminders on.")
    if (data.enabled && data.leadDays.length === 0) throw new Error("Pick at least one time to be reminded.")
    if (!isTimezone(data.timezone)) throw new Error("Pick a time zone from the list.")
    return saveReminderSettings(db, viewer.user.id, { ...data, email: data.email || null })
  })

const testSends = new RateLimiter(5, 10 * 60_000)

export const sendReminderTest = createServerFn({ method: "POST" }).handler(async () => {
  requireSameOrigin()
  const { db, viewer } = await requireUser()
  const wait = testSends.take(viewer.user.id)
  if (wait > 0) throw new Error(`That's a lot of test emails. Try again in ${Math.ceil(wait / 60)} minutes.`)
  return sendTestReminder(db, viewer.user.id)
})

// --- people -----------------------------------------------------------------

const personName = z.string().trim().min(1).max(120)
const shortList = z.array(z.string().trim().min(1).max(120)).max(20)

export const getPeople = createServerFn({ method: "GET" })
  .validator(z.object({ container, q: z.string().trim().max(200).optional(), sort: z.enum(["recent", "name"]).optional() }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    return listPeople(db, { workspaceId: ws, container: data.container, q: data.q, sort: data.sort, limit: 200 })
  })

// Likely duplicate pairs for the People page. Read-only: merging stays a click.
export const getMergeSuggestions = createServerFn({ method: "GET" })
  .validator(z.object({ container }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    const side = (p: { id: string; name: string; role: string | null; organization: string | null; factCount: number }) => ({
      id: p.id,
      name: p.name,
      role: p.role,
      organization: p.organization,
      factCount: p.factCount,
    })
    const found = await suggestPeopleMerges(db, { workspaceId: ws, container: data.container }, { limit: 20 })
    return found.map((s) => ({ keep: side(s.keep), drop: side(s.drop), reasons: s.reasons, similarity: s.similarity }))
  })

export const getPersonPage = createServerFn({ method: "GET" })
  .validator(z.object({ id, history: z.boolean().optional() }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    const found = getPerson(db, ws, data.id, { history: data.history })
    if (!found) return null
    // Others in the same container, for the merge picker.
    const others = listPeople(db, { workspaceId: ws, container: found.person.container, sort: "name", limit: 200 }).people.filter((p) => p.id !== data.id)
    return { ...found, others: others.map((p) => ({ id: p.id, name: p.name, role: p.role, organization: p.organization, factCount: p.factCount })) }
  })

export const savePerson = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id,
      name: personName,
      aliases: shortList,
      role: z.string().trim().max(120).nullable(),
      organization: z.string().trim().max(120).nullable(),
      summary: z.string().trim().max(600),
      emails: z.array(z.email().max(254)).max(10),
      handles: z.array(z.string().trim().min(1).max(80)).max(10),
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    const { id: personId, ...patch } = data
    const row = updatePerson(db, ws, personId, patch)
    if (!row) throw new Error("No such person. Someone may have merged or deleted them.")
    return row
  })

export const mergePerson = createServerFn({ method: "POST" })
  .validator(z.object({ keepId: id, dropId: id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    try {
      return mergePeople(db, ws, data.keepId, data.dropId)
    } catch (err) {
      if (err instanceof PersonError) throw new Error(err.message)
      throw err
    }
  })

export const removePerson = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    if (!deletePerson(db, ws, data.id)) throw new Error("No such person.")
    return { ok: true }
  })

// --- search -----------------------------------------------------------------

export const searchMemories = createServerFn({ method: "POST" })
  .validator(z.object({ container, q: z.string().trim().min(1).max(2000), chunks: z.boolean().optional(), limit: z.number().int().min(1).max(50).optional() }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    return runSearch(db, ws, {
      container: data.container,
      q: data.q,
      limit: data.limit ?? 20,
      include: data.chunks ? ["memories", "chunks"] : ["memories"],
    })
  })

// --- ask ----------------------------------------------------------------------

export const askMemory = createServerFn({ method: "POST" })
  .validator(z.object({ container, question: z.string().trim().min(1).max(2000), chunks: z.boolean().optional() }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    // Cancel in the dashboard aborts the fetch, which aborts this request:
    // a queued ask is dropped and a running one stops its gateway call.
    return ask(db, ws, { container: data.container, question: data.question, include_chunks: data.chunks ?? false }, { signal: getRequest().signal })
  })

// --- documents ----------------------------------------------------------------

export const getDocuments = createServerFn({ method: "GET" })
  .validator(z.object({ container, cursor: z.string().max(200).optional(), limit: z.number().int().min(1).max(200).optional() }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    const page = listDocuments(db, ws, data.container, { cursor: data.cursor, limit: data.limit ?? 50 })
    return {
      nextCursor: page.nextCursor,
      // Counted for the first page only; later pages keep the first count.
      total: data.cursor ? null : countDocuments(db, ws, data.container),
      documents: page.documents.map(({ metadata, ...d }) => {
        const ex = (metadata as { extract?: { added: number; updated: number; invalidated: number; conflicts?: number } } | null)?.extract
        return { ...d, extract: ex ? { added: ex.added, updated: ex.updated, invalidated: ex.invalidated, conflicts: ex.conflicts ?? 0 } : null }
      }),
    }
  })

export const addText = createServerFn({ method: "POST" })
  .validator(
    z.object({
      container,
      kind: z.enum(["text", "url"]),
      value: z.string().trim().min(1).max(1_000_000),
      title: z.string().trim().max(300).optional(),
    }),
  )
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    if (data.kind === "url") {
      const url = z.url({ protocol: /^https?$/ }).safeParse(data.value)
      if (!url.success) throw new Error("Enter a full http or https URL.")
      return addContent(db, ws, { container: data.container, url: url.data, title: data.title || undefined })
    }
    return addContent(db, ws, { container: data.container, content: data.value, title: data.title || undefined })
  })

export const uploadFile = createServerFn({ method: "POST" })
  .validator((form: unknown) => {
    if (!(form instanceof FormData)) throw new Error("Expected form data.")
    const file = form.get("file")
    if (!(file instanceof File) || file.size === 0) throw new Error("Choose a file to upload.")
    if (file.size > MAX_FILE_BYTES) throw new Error(`Files can be up to ${MAX_FILE_BYTES / 1024 / 1024} MB.`)
    return { file, container: container.parse(form.get("container")?.toString()) }
  })
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    const bytes = new Uint8Array(await data.file.arrayBuffer())
    return addFile(db, ws, { container: data.container, file: data.file, bytes })
  })

export const retryDocument = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, viewer } = await requireMember()
    const doc = getDocument(db, ws, data.id)
    if (!doc) throw new Error("No such document.")
    // An email that still has to be read from Gmail uses its importer's grant.
    if (!mayRetry(doc, viewer.user.id)) throw new ForbiddenError("Only the person who imported this email can retry it.")
    // Uploaded files are deleted after a successful read, so a file that
    // failed before then still has its bytes; anything else re-ingests.
    db.$client.prepare("UPDATE documents SET status = 'queued', error = NULL WHERE id = ? AND workspace_id = ?").run(doc.id, ws)
    enqueue(db, ws, "ingest", { documentId: doc.id })
    return { ok: true }
  })

export const removeDocument = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember()
    if (!deleteDocument(db, ws, data.id)) throw new Error("No such document.")
    return { ok: true }
  })

// --- graph --------------------------------------------------------------------

export const getGraph = createServerFn({ method: "GET" })
  .validator(z.object({ container }))
  .handler(async ({ data }) => {
    const { db, ws } = await requireMember()
    return buildGraph(db, ws, data.container)
  })

// --- keys (workspace settings) ----------------------------------------------------

export const getKeys = createServerFn({ method: "GET" }).handler(async () => {
  const { db, ws, role: myRole } = await requireMember()
  return { keys: listApiKeys(db, ws), canManage: roleAtLeast(myRole, "admin") }
})

// The Connect page: the public origin from RECALL_URL (so snippets match what
// agents reach, not whatever host the browser used) and whether this member
// may create a key.
export const getConnectInfo = createServerFn({ method: "GET" }).handler(async () => {
  const { role: myRole } = await requireMember()
  return { origin: config().url, canManage: roleAtLeast(myRole, "admin") }
})

export const createKey = createServerFn({ method: "POST" })
  .validator(z.object({ name: z.string().trim().min(1).max(80) }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, viewer } = await requireMember("admin")
    const { key, row } = createApiKey(db, { workspaceId: ws, name: data.name, createdBy: viewer.user.id })
    return { key, id: row.id, name: row.name, prefix: row.prefix }
  })

export const revokeKey = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember("admin")
    return { ok: revokeApiKey(db, ws, data.id) }
  })

// --- members and invites (workspace settings) -----------------------------------

export const getMembers = createServerFn({ method: "GET" }).handler(async () => {
  const { db, ws, role: myRole, viewer } = await requireMember()
  const canManage = roleAtLeast(myRole, "admin")
  return {
    members: listMembers(db, ws),
    invites: canManage ? listInvites(db, ws) : [],
    myRole,
    myUserId: viewer.user.id,
    canManage,
  }
})

function inviteUrl(token: string) {
  return `${config().url}${APP_BASE}/invite/${token}`
}

export const createInviteLink = createServerFn({ method: "POST" })
  .validator(z.object({ role }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, role: myRole, viewer } = await requireMember("admin")
    if (!canInvite(myRole, data.role)) throw new ForbiddenError("Only owners can invite owners.")
    const { token, row } = createInvite(db, { workspaceId: ws, role: data.role, createdBy: viewer.user.id })
    return { url: inviteUrl(token), id: row.id, role: row.role, expiresAt: row.expiresAt }
  })

export const revokeInviteLink = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws } = await requireMember("admin")
    return { ok: revokeInvite(db, ws, data.id) }
  })

function userErrors<T>(fn: () => T): T {
  try {
    return fn()
  } catch (err) {
    if (err instanceof UserError) throw new Error(err.message)
    throw err
  }
}

export const setMemberRole = createServerFn({ method: "POST" })
  .validator(z.object({ memberId: id, role }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, role: myRole, viewer } = await requireMember("admin")
    userErrors(() => changeMemberRole(db, ws, { userId: viewer.user.id, role: myRole }, data.memberId, data.role))
    return { ok: true }
  })

export const removeWorkspaceMember = createServerFn({ method: "POST" })
  .validator(z.object({ memberId: id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, ws, role: myRole, viewer } = await requireMember()
    const removed = userErrors(() => removeMember(db, ws, { userId: viewer.user.id, role: myRole }, data.memberId))
    return { ok: true, left: removed.userId === viewer.user.id }
  })

// --- invite acceptance (public page) ----------------------------------------------

const inviteToken = z.string().min(8).max(80)
const inviteAttempts = new RateLimiter(10, 60_000)

function limitInvites() {
  const wait = inviteAttempts.take(clientIp(getRequest().headers))
  if (wait > 0) throw new Error(`Too many tries. Wait ${wait} seconds and try again.`)
}

export const getInvite = createServerFn({ method: "GET" })
  .validator(z.object({ token: inviteToken }))
  .handler(async ({ data }) => {
    limitInvites()
    const db = getDb()
    const found = lookupInvite(db, data.token)
    const viewer = await getViewer()
    if (!found.ok) return { ok: false as const, problem: inviteProblem(found.reason), signedInAs: viewer?.user.username ?? null }
    const alreadyMember = viewer ? !!getRole(db, viewer.user.id, found.workspace.id) : false
    return {
      ok: true as const,
      workspace: found.workspace.name,
      role: found.invite.role,
      expiresAt: found.invite.expiresAt,
      signedInAs: viewer?.user.username ?? null,
      alreadyMember,
    }
  })

export const acceptInvite = createServerFn({ method: "POST" })
  .validator(z.object({ token: inviteToken }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    limitInvites()
    const { db, viewer } = await requireUser()
    const ws = userErrors(() => acceptInviteAsUser(db, data.token, viewer.user.id))
    db.update(session).set({ activeOrganizationId: ws.id }).where(eq(session.id, viewer.sessionId)).run()
    return { ok: true, workspace: ws.name }
  })

export const acceptInviteNewAccount = createServerFn({ method: "POST" })
  .validator(z.object({ token: inviteToken, username: z.string().trim().min(1).max(USERNAME_MAX), password: z.string().min(1).max(MAX_PASSWORD_LENGTH) }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    limitInvites()
    const db = getDb()
    let result: Awaited<ReturnType<typeof acceptInviteWithNewUser>>
    try {
      result = await acceptInviteWithNewUser(db, data.token, { username: data.username, password: data.password })
    } catch (err) {
      if (err instanceof UserError) throw new Error(err.message)
      throw err
    }
    // Sign the new user in. The cookie plugin writes the session cookie, and the
    // session lands in their only workspace: the one they just joined.
    await getAuth(db).api.signInUsername({ body: { username: data.username, password: data.password }, headers: getRequest().headers })
    return { ok: true, workspace: result.workspace.name }
  })

// --- account --------------------------------------------------------------------------

export const getAccount = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  return { username: viewer.user.username, passkeys: listPasskeys(db, viewer.user.id) }
})

export const removePasskey = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const { db, viewer } = await requireUser()
    if (!deletePasskey(db, viewer.user.id, data.id)) throw new Error("No such passkey.")
    return { ok: true }
  })
