// The /v1 operations, shared by the REST routes and the MCP tools.
import { z } from "zod"
import type { DB } from "./db"
import { embedOne } from "./embed"
import { containerSchema, HttpError } from "./http"
import { createDocument, getProfile, listMemories, versionCounts } from "./memories"
import { hybridSearch } from "./search"

export const MAX_CONTENT_CHARS = 1_000_000
export const MAX_FILE_BYTES = 20 * 1024 * 1024

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
  })
  .refine((v) => [v.content?.trim(), v.messages, v.url].filter(Boolean).length === 1, {
    message: "send exactly one of content, messages or url",
  })
export type AddInput = z.infer<typeof addInput>

export function addContent(db: DB, input: AddInput) {
  const sourceType = input.url ? "url" : input.messages || input.type === "chat" ? "chat" : "text"
  const doc = createDocument(db, {
    container: input.container,
    sourceType,
    url: input.url,
    title: input.title ?? (input.url ? null : firstLine(input.content ?? input.messages?.[0]?.content ?? "")),
    content: input.messages ? undefined : input.content,
    metadata: { ...input.metadata, ...(input.messages ? { messages: input.messages } : {}) },
  })
  return { id: doc.id, container: doc.container, status: doc.status }
}

export function addFile(db: DB, input: { container: string; title?: string; metadata?: Record<string, unknown>; file: File; bytes: Uint8Array }) {
  if (input.bytes.byteLength > MAX_FILE_BYTES) throw new HttpError(413, "too_large", `file is larger than ${MAX_FILE_BYTES / 1024 / 1024} MB`)
  const doc = createDocument(db, {
    container: input.container,
    sourceType: "file",
    title: input.title ?? input.file.name,
    metadata: input.metadata,
    file: { name: input.file.name || "upload", mime: input.file.type || "application/octet-stream", bytes: input.bytes },
  })
  return { id: doc.id, container: doc.container, status: doc.status }
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

export async function search(db: DB, input: SearchInput) {
  const started = performance.now()
  const vector = await embedOne(input.q, "query")
  const results = hybridSearch(db.$client, { ...input, vector })
  return { container: input.container, q: input.q, results, timingMs: Math.round(performance.now() - started) }
}

export const listInput = z.object({
  container: containerSchema,
  cursor: z.string().optional(),
  history: z
    .enum(["1", "0", "true", "false"])
    .optional()
    .transform((v) => v === "1" || v === "true"),
  limit: z.coerce.number().int().min(1).max(200).optional(),
  kind: z.enum(["static", "dynamic"]).optional(),
})

export function list(db: DB, input: z.infer<typeof listInput>) {
  const page = listMemories(db, input)
  const counts = versionCounts(
    db,
    page.memories.map((m) => m.id),
  )
  return { ...page, memories: page.memories.map((m) => ({ ...m, versions: (counts[m.id] ?? 0) + 1 })) }
}

export async function profile(db: DB, input: { container: string; q?: string }) {
  const p = getProfile(db, input.container)
  if (!input.q) return p
  const { results } = await search(db, { container: input.container, q: input.q, limit: 10 })
  return { ...p, searchResults: results }
}
