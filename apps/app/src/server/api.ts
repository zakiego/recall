// The /v1 operations, shared by the REST routes and the MCP tools. The
// workspace always comes from the caller's API key or session, never the body.
import { z } from "zod"
import type { DB } from "./db"
import type { Document } from "./db/schema"
import { embedOne } from "./embed"
import { containerSchema, HttpError } from "./http"
import { onJobFinished } from "./jobs/queue"
import {
  createDocument,
  editMemory,
  getDocument,
  getMemory,
  getProfile,
  listMemories,
  memoriesFromDocument,
  resolveConflict,
  versionCounts,
} from "./memories"
import { hybridSearch } from "./search"

export const MAX_CONTENT_CHARS = 1_000_000
export const MAX_FILE_BYTES = 20 * 1024 * 1024

export const DEFAULT_WAIT_MS = 60_000
export const MAX_WAIT_MS = 120_000
export const MAX_SOURCE_ID_LEN = 200
export const sourceIdSchema = z.string().trim().min(1).max(MAX_SOURCE_ID_LEN)

const chatMessage = z.object({ role: z.string().trim().min(1).max(40), content: z.string().max(MAX_CONTENT_CHARS) })

export const addInput = z
  .object({
    container: containerSchema,
    content: z.string().max(MAX_CONTENT_CHARS).optional(),
    messages: z.array(chatMessage).min(1).max(2000).optional(),
    url: z.url({ protocol: /^https?$/ }).optional(),
    type: z.enum(["text", "chat", "url"]).optional(),
    title: z.string().trim().max(300).optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
    source_id: sourceIdSchema.optional(),
    // Only read by POST /v1/memories: hold the response until facts are ready.
    wait: z.boolean().optional(),
    wait_timeout: z.number().min(0).max(MAX_WAIT_MS / 1000).optional(),
  })
  .refine((v) => [v.content?.trim(), v.messages, v.url].filter(Boolean).length === 1, {
    message: "send exactly one of content, messages or url",
  })
export type AddInput = z.infer<typeof addInput>

// What every add returns. duplicate means source_id matched an existing
// document and nothing new was queued.
export type AddResult = { id: string; container: string; status: Document["status"]; sourceId: string | null; duplicate: boolean }

function addResult({ doc, duplicate }: { doc: Document; duplicate: boolean }): AddResult {
  return { id: doc.id, container: doc.container, status: doc.status, sourceId: doc.sourceId, duplicate }
}

export function addContent(db: DB, workspaceId: string, input: AddInput): AddResult {
  const sourceType = input.url ? "url" : input.messages || input.type === "chat" ? "chat" : "text"
  const created = createDocument(db, {
    workspaceId,
    container: input.container,
    sourceType,
    url: input.url,
    title: input.title ?? (input.url ? null : firstLine(input.content ?? input.messages?.[0]?.content ?? "")),
    content: input.messages ? undefined : input.content,
    metadata: { ...input.metadata, ...(input.messages ? { messages: input.messages } : {}) },
    sourceId: input.source_id,
  })
  return addResult(created)
}

export function addFile(
  db: DB,
  workspaceId: string,
  input: { container: string; title?: string; metadata?: Record<string, unknown>; sourceId?: string; file: File; bytes: Uint8Array },
): AddResult {
  if (input.bytes.byteLength > MAX_FILE_BYTES) throw new HttpError(413, "too_large", `file is larger than ${MAX_FILE_BYTES / 1024 / 1024} MB`)
  const created = createDocument(db, {
    workspaceId,
    container: input.container,
    sourceType: "file",
    title: input.title ?? input.file.name,
    metadata: input.metadata,
    file: { name: input.file.name || "upload", mime: input.file.type || "application/octet-stream", bytes: input.bytes },
    sourceId: input.sourceId,
  })
  return addResult(created)
}

// --- wait ---------------------------------------------------------------------

const settled = (doc: Document | undefined) => doc?.status === "done" || doc?.status === "failed"

// Resolves with the document once it is done or failed, or null on timeout,
// abort or deletion. It wakes when a job for this document finishes (the job
// loop does the work), so it never polls and never holds a transaction.
export function waitForDocument(db: DB, workspaceId: string, id: string, timeoutMs: number, signal?: AbortSignal): Promise<Document | null> {
  const now = getDocument(db, workspaceId, id)
  if (!now || settled(now)) return Promise.resolve(now ?? null)
  if (timeoutMs <= 0 || signal?.aborted) return Promise.resolve(null)
  return new Promise((resolve) => {
    const finish = (doc: Document | null) => {
      clearTimeout(timer)
      off()
      signal?.removeEventListener("abort", onAbort)
      resolve(doc)
    }
    const onAbort = () => finish(null)
    const off = onJobFinished((job) => {
      if ((job.payload as { documentId?: unknown }).documentId !== id) return
      const doc = getDocument(db, workspaceId, id)
      if (!doc) finish(null)
      else if (settled(doc)) finish(doc)
    })
    const timer = setTimeout(() => finish(null), timeoutMs)
    signal?.addEventListener("abort", onAbort, { once: true })
  })
}

// reviewNote is set when the fact conflicts with another one and a human should decide.
type FactView = { id: string; fact: string; keywords: string; kind: "static" | "dynamic"; supersedesId: string | null; reviewNote: string | null }

export type WaitResult = AddResult & {
  // false when the wait timed out; the other fields below are then missing.
  settled: boolean
  error?: string | null
  added?: FactView[]
  updated?: FactView[]
  invalidated?: number
}

export async function waitForAdd(db: DB, workspaceId: string, res: AddResult, timeoutMs: number, signal?: AbortSignal): Promise<WaitResult> {
  const doc = await waitForDocument(db, workspaceId, res.id, Math.min(timeoutMs, MAX_WAIT_MS), signal)
  if (!doc) return { ...res, status: getDocument(db, workspaceId, res.id)?.status ?? res.status, settled: false }
  const facts = memoriesFromDocument(db, workspaceId, doc.id).map(
    (m): FactView => ({ id: m.id, fact: m.fact, keywords: m.keywords, kind: m.kind, supersedesId: m.supersedesId, reviewNote: m.reviewNote }),
  )
  const stats = (doc.metadata?.extract ?? {}) as { invalidated?: number }
  return {
    ...res,
    status: doc.status,
    settled: true,
    error: doc.error,
    added: facts.filter((m) => !m.supersedesId),
    updated: facts.filter((m) => m.supersedesId),
    invalidated: stats.invalidated ?? 0,
  }
}

function firstLine(s: string) {
  const line = s.trim().split("\n")[0] ?? ""
  return line.length > 80 ? `${line.slice(0, 77)}...` : line || null
}

export const searchInput = z.object({
  container: containerSchema,
  q: z.string().trim().min(1).max(2000),
  limit: z.coerce.number().int().min(1).max(100).optional(),
  include: z.array(z.enum(["memories", "chunks"])).optional(),
})
export type SearchInput = z.infer<typeof searchInput>

export async function search(db: DB, workspaceId: string, input: SearchInput) {
  const started = performance.now()
  const vector = await embedOne(input.q, "query")
  const results = hybridSearch(db.$client, { ...input, workspaceId, vector })
  return { container: input.container, q: input.q, results, timingMs: Math.round(performance.now() - started) }
}

const flag = z
  .enum(["1", "0", "true", "false"])
  .optional()
  .transform((v) => v === "1" || v === "true")

export const listInput = z.object({
  container: containerSchema,
  cursor: z.string().optional(),
  history: flag,
  // Only current facts flagged for review.
  review: flag,
  limit: z.coerce.number().int().min(1).max(200).optional(),
  kind: z.enum(["static", "dynamic"]).optional(),
})

export function list(db: DB, workspaceId: string, input: z.infer<typeof listInput>) {
  const page = listMemories(db, { ...input, workspaceId })
  const counts = versionCounts(
    db,
    workspaceId,
    page.memories.map((m) => m.id),
  )
  return { ...page, memories: page.memories.map((m) => ({ ...m, versions: (counts[m.id] ?? 0) + 1 })) }
}

export const patchInput = z
  .object({
    fact: z.string().trim().min(1).max(500).optional(),
    keywords: z.string().trim().max(500).optional(),
    kind: z.enum(["static", "dynamic"]).optional(),
    // Clears the review note without writing a new version.
    resolve: z.literal(true).optional(),
  })
  .refine((v) => v.fact !== undefined || v.keywords !== undefined || v.kind !== undefined || v.resolve, { message: "nothing to change" })

// An edit writes a new version (which starts unflagged). `resolve` alone just
// clears the review note on the current version.
export async function patchMemory(db: DB, workspaceId: string, id: string, input: z.infer<typeof patchInput>) {
  const existing = getMemory(db, workspaceId, id)
  if (!existing) throw new HttpError(404, "not_found", "no such memory")
  const notCurrent = () => new HttpError(409, "not_current", "this memory was superseded or deleted; edit the current version")
  if (existing.validTo !== null) throw notCurrent()
  const { resolve, ...edit } = input
  if (edit.fact === undefined && edit.keywords === undefined && edit.kind === undefined) {
    return { memory: resolve ? (resolveConflict(db, workspaceId, existing.id) ?? existing) : existing }
  }
  // null: it was superseded or forgotten while the new version was embedded.
  const memory = await editMemory(db, workspaceId, existing.id, edit)
  if (!memory) throw notCurrent()
  return { memory }
}

export async function profile(db: DB, workspaceId: string, input: { container: string; q?: string }) {
  const p = getProfile(db, workspaceId, input.container)
  if (!input.q) return p
  const { results } = await search(db, workspaceId, { container: input.container, q: input.q, limit: 10 })
  return { ...p, searchResults: results }
}
