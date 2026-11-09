// Server functions behind the dashboard. Each checks the admin session itself.
import { createServerFn } from "@tanstack/react-start"
import { deleteCookie, setCookie } from "@tanstack/react-start/server"
import { z } from "zod"
import { addContent, addFile, search as runSearch, MAX_FILE_BYTES } from "@/server/api"
import { ask } from "@/server/ask"
import { createApiKey, listApiKeys, revokeApiKey, SESSION_COOKIE, SESSION_TTL_MS, signSession, verifyPassword } from "@/server/auth"
import { config } from "@/server/config"
import { getDb } from "@/server/db"
import {
  editMemory,
  forgetMemory,
  getProfile,
  listContainers,
  listDocuments,
  listMemories,
  memoryChain,
  reviewCount,
  setReviewNote,
  versionCounts,
  deleteDocument,
  getDocument,
  MAX_CONTAINER_LEN,
} from "@/server/memories"
import { enqueue } from "@/server/jobs/queue"
import { cookieSecure, isAuthed, requireAdmin, requireSameOrigin } from "@/server/session"
import { buildGraph } from "@/server/graph"

const container = z.string().trim().min(1).max(MAX_CONTAINER_LEN)

// --- session ----------------------------------------------------------------

const failures: number[] = []
const LOCK_WINDOW_MS = 15 * 60_000
const LOCK_AFTER = 5

export const getSession = createServerFn({ method: "GET" }).handler(async () => {
  const cfg = config()
  return {
    authed: isAuthed(),
    configured: !!cfg.adminPasswordHash && cfg.sessionSecret.length >= 32,
    defaultContainer: cfg.defaultContainer,
  }
})

export const login = createServerFn({ method: "POST" })
  .validator(z.object({ password: z.string().min(1).max(1000) }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const cfg = config()
    if (!cfg.adminPasswordHash || cfg.sessionSecret.length < 32) {
      return { ok: false as const, reason: "not_configured" as const }
    }
    const now = Date.now()
    while (failures.length && failures[0]! < now - LOCK_WINDOW_MS) failures.shift()
    if (failures.length >= LOCK_AFTER) {
      const retryAt = failures[0]! + LOCK_WINDOW_MS
      return { ok: false as const, reason: "locked" as const, retryInMin: Math.ceil((retryAt - now) / 60_000) }
    }
    if (!(await verifyPassword(data.password, cfg.adminPasswordHash))) {
      failures.push(now)
      return { ok: false as const, reason: "wrong" as const, left: LOCK_AFTER - failures.length }
    }
    failures.length = 0
    setCookie(SESSION_COOKIE, signSession(cfg.sessionSecret), {
      httpOnly: true,
      sameSite: "strict",
      secure: cookieSecure(),
      path: "/",
      maxAge: SESSION_TTL_MS / 1000,
    })
    return { ok: true as const }
  })

export const logout = createServerFn({ method: "POST" }).handler(async () => {
  requireSameOrigin()
  deleteCookie(SESSION_COOKIE, { path: "/" })
  return { ok: true }
})

// --- containers and memories -------------------------------------------------

export const getContainers = createServerFn({ method: "GET" }).handler(async () => {
  requireAdmin()
  return { containers: listContainers(getDb()), defaultContainer: config().defaultContainer }
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
    requireAdmin()
    const db = getDb()
    const page = listMemories(db, { ...data, limit: data.limit ?? 100 })
    const counts = versionCounts(
      db,
      page.memories.map((m) => m.id),
    )
    const docIds = [...new Set(page.memories.map((m) => m.sourceDocumentId).filter((d): d is string => !!d))]
    const titles = Object.fromEntries(docIds.map((id) => [id, getDocument(db, id)?.title ?? null]))
    return {
      memories: page.memories.map((m) => ({
        ...m,
        versions: (counts[m.id] ?? 0) + 1,
        sourceTitle: m.sourceDocumentId ? (titles[m.sourceDocumentId] ?? null) : null,
      })),
      nextCursor: page.nextCursor,
      reviewCount: reviewCount(db, data.container),
      profile: getProfile(db, data.container),
    }
  })

export const getChain = createServerFn({ method: "GET" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    const db = getDb()
    return memoryChain(db, data.id).map((m) => ({
      ...m,
      sourceTitle: m.sourceDocumentId ? (getDocument(db, m.sourceDocumentId)?.title ?? null) : null,
    }))
  })

export const updateMemory = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id: z.string().min(1).max(64),
      fact: z.string().trim().min(1).max(500),
      keywords: z.string().trim().max(500).optional(),
      kind: z.enum(["static", "dynamic"]).optional(),
    }),
  )
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    const row = await editMemory(getDb(), data.id, data)
    if (!row) throw new Error("This fact changed since you opened it. Reload to edit the current version.")
    return row
  })

export const resolveMemory = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    const row = setReviewNote(getDb(), data.id, null)
    if (!row) throw new Error("This fact changed since you opened it. Reload to see the current version.")
    return row
  })

export const deleteMemory = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    const row = forgetMemory(getDb(), data.id)
    if (!row) throw new Error("No such memory.")
    return row
  })

// --- search -----------------------------------------------------------------

export const searchMemories = createServerFn({ method: "POST" })
  .validator(z.object({ container, q: z.string().trim().min(1).max(2000), chunks: z.boolean().optional(), limit: z.number().int().min(1).max(50).optional() }))
  .handler(async ({ data }) => {
    requireAdmin()
    return runSearch(getDb(), {
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
    requireAdmin()
    requireSameOrigin()
    return ask(getDb(), { container: data.container, question: data.question, include_chunks: data.chunks ?? false })
  })

// --- documents ----------------------------------------------------------------

export const getDocuments = createServerFn({ method: "GET" })
  .validator(z.object({ container }))
  .handler(async ({ data }) => {
    requireAdmin()
    return listDocuments(getDb(), data.container, 50).map(({ metadata, ...d }) => {
      const ex = (metadata as { extract?: { added: number; updated: number; invalidated: number; conflicts?: number } } | null)?.extract
      return { ...d, extract: ex ? { added: ex.added, updated: ex.updated, invalidated: ex.invalidated, conflicts: ex.conflicts ?? 0 } : null }
    })
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
    requireAdmin()
    requireSameOrigin()
    if (data.kind === "url") {
      const url = z.url({ protocol: /^https?$/ }).safeParse(data.value)
      if (!url.success) throw new Error("Enter a full http or https URL.")
      return addContent(getDb(), { container: data.container, url: url.data, title: data.title || undefined })
    }
    return addContent(getDb(), { container: data.container, content: data.value, title: data.title || undefined })
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
    requireAdmin()
    requireSameOrigin()
    const bytes = new Uint8Array(await data.file.arrayBuffer())
    return addFile(getDb(), { container: data.container, file: data.file, bytes })
  })

export const retryDocument = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    const db = getDb()
    const doc = getDocument(db, data.id)
    if (!doc) throw new Error("No such document.")
    // Uploaded files are deleted after a successful read, so a file that
    // failed before then still has its bytes; anything else re-ingests.
    db.$client.prepare("UPDATE documents SET status = 'queued', error = NULL WHERE id = ?").run(doc.id)
    enqueue(db, "ingest", { documentId: doc.id })
    return { ok: true }
  })

export const removeDocument = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    deleteDocument(getDb(), data.id)
    return { ok: true }
  })

// --- graph --------------------------------------------------------------------

export const getGraph = createServerFn({ method: "GET" })
  .validator(z.object({ container }))
  .handler(async ({ data }) => {
    requireAdmin()
    return buildGraph(getDb(), data.container)
  })

// --- keys -----------------------------------------------------------------------

export const getKeys = createServerFn({ method: "GET" }).handler(async () => {
  requireAdmin()
  return listApiKeys(getDb())
})

export const createKey = createServerFn({ method: "POST" })
  .validator(z.object({ name: z.string().trim().min(1).max(80) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    const { key, row } = createApiKey(getDb(), data.name)
    return { key, id: row.id, name: row.name, prefix: row.prefix }
  })

export const revokeKey = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    requireAdmin()
    requireSameOrigin()
    return { ok: revokeApiKey(getDb(), data.id) }
  })
