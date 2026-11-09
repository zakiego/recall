import { sql } from "drizzle-orm"
import { index, integer, sqliteTable, text, uniqueIndex } from "drizzle-orm/sqlite-core"

const now = sql`(cast(unixepoch('subsec') * 1000 as integer))`

export const apiKeys = sqliteTable("api_keys", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  keyHash: text("key_hash").notNull().unique(),
  prefix: text("prefix").notNull(),
  createdAt: integer("created_at").notNull().default(now),
  lastUsedAt: integer("last_used_at"),
  revokedAt: integer("revoked_at"),
})

export const documents = sqliteTable(
  "documents",
  {
    id: text("id").primaryKey(),
    container: text("container").notNull(),
    sourceType: text("source_type", { enum: ["text", "chat", "url", "file"] }).notNull(),
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
    index("documents_container_idx").on(t.container, t.createdAt),
    uniqueIndex("documents_container_source_id_unique")
      .on(t.container, t.sourceId)
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
    container: text("container").notNull(),
    idx: integer("idx").notNull(),
    text: text("text").notNull(),
  },
  (t) => [index("chunks_document_idx").on(t.documentId, t.idx)],
)

export const memories = sqliteTable(
  "memories",
  {
    id: text("id").primaryKey(),
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
  },
  (t) => [
    index("memories_current_idx").on(t.container, t.validTo, t.createdAt),
    index("memories_supersedes_idx").on(t.supersedesId),
  ],
)

export const profiles = sqliteTable("profiles", {
  container: text("container").primaryKey(),
  staticText: text("static_text").notNull().default(""),
  dynamicText: text("dynamic_text").notNull().default(""),
  updatedAt: integer("updated_at").notNull().default(now),
})

export const jobs = sqliteTable(
  "jobs",
  {
    id: integer("id").primaryKey({ autoIncrement: true }),
    type: text("type", { enum: ["ingest", "extract", "profile", "ask"] }).notNull(),
    payload: text("payload", { mode: "json" }).$type<Record<string, unknown>>().notNull(),
    status: text("status", { enum: ["queued", "running", "done", "failed"] })
      .notNull()
      .default("queued"),
    attempts: integer("attempts").notNull().default(0),
    runAfter: integer("run_after").notNull().default(now),
    lastError: text("last_error"),
    createdAt: integer("created_at").notNull().default(now),
  },
  (t) => [index("jobs_pending_idx").on(t.status, t.runAfter)],
)

export type Memory = typeof memories.$inferSelect
export type Document = typeof documents.$inferSelect
export type Job = typeof jobs.$inferSelect
