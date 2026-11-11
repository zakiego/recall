// Migration 0004 on a database in main's schema (everything before 0004),
// filled the way main fills it. Every old row must end up in workspace "Zaki",
// and the old admin password must log in as `zaki`.
import Database from "better-sqlite3"
import { drizzle } from "drizzle-orm/better-sqlite3"
import * as sqliteVec from "sqlite-vec"
import { describe, expect, it } from "vitest"
import { hashLegacyPassword, sha256, verifyApiKey } from "../auth"
import { authHandler } from "../better-auth"
import { embed, embedOne } from "../embed"
import { memoryChain } from "../memories"
import { hybridSearch, vecParam } from "../search"
import { viewerFromHeaders } from "../session"
import { bootstrapUsers, listMembers } from "../users"
import type { DB } from "./index"
import { migrate, migrations } from "./migrate"
import * as schema from "./schema"

const BEFORE = migrations.filter((m) => m.name < "0004")
const KEYS = { laptop: "rcl_laptopKEY000000000000000000000000", utusan: "rcl_utusanKEY000000000000000000000000", old: "rcl_oldKEY0000000000000000000000000000" }

function openAt(list: typeof migrations) {
  const sqlite = new Database(":memory:")
  sqlite.pragma("foreign_keys = ON")
  sqliteVec.load(sqlite)
  migrate(sqlite, list)
  return sqlite
}

// Rows written with main's columns only (no workspace_id anywhere).
async function seedMain(sqlite: Database.Database) {
  const facts = [
    ["mem_a1", "zaki", "Prefers Adidas running shoes", "sepatu shoes", "dynamic", 1000, 2000, null],
    ["mem_a2", "zaki", "Switched to Puma running shoes", "sepatu shoes puma", "dynamic", 2000, null, "mem_a1"],
    ["mem_a3", "zaki", "Lives in Banjarbaru", "kota city", "static", 1100, null, null],
    ["mem_v1", "vorneo", "Vorneo sells property listings", "properti rumah", "static", 1200, null, null],
    ["mem_e1", "eva", "Drinks oat milk lattes", "kopi coffee", "dynamic", 1300, null, null],
  ] as const
  const vectors = await embed(
    facts.map((f) => `${f[2]}. ${f[3]}`),
    "passage",
  )
  sqlite.prepare("INSERT INTO documents (id, container, source_type, title, content, status, created_at) VALUES (?, ?, 'text', ?, ?, 'done', ?)").run("doc_1", "zaki", "Planning call", "Notes from the planning call about the Q4 roadmap.", 900)
  sqlite.prepare("INSERT INTO documents (id, container, source_type, title, url, status, created_at) VALUES (?, ?, 'url', ?, ?, 'queued', ?)").run("doc_2", "vorneo", "Brief", "https://example.com/brief", 950)
  sqlite.prepare("INSERT INTO chunks (id, document_id, container, idx, text) VALUES ('chk_1', 'doc_1', 'zaki', 0, ?)").run("Notes from the planning call about the Q4 roadmap.")
  sqlite.prepare("INSERT INTO chunks_fts (text, chunk_id, container) VALUES (?, 'chk_1', 'zaki')").run("Notes from the planning call about the Q4 roadmap.")
  sqlite.prepare("INSERT INTO chunks_vec (chunk_id, container, embedding) VALUES ('chk_1', 'zaki', ?)").run(vecParam((await embed(["Notes from the planning call about the Q4 roadmap."], "passage"))[0]!))
  facts.forEach(([id, container, fact, keywords, kind, created, validTo, supersedes], i) => {
    sqlite
      .prepare("INSERT INTO memories (id, container, fact, keywords, kind, source_document_id, supersedes_id, valid_from, valid_to, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
      .run(id, container, fact, keywords, kind, container === "zaki" ? "doc_1" : null, supersedes, created, validTo, created)
    // Only current memories are indexed.
    if (validTo === null) {
      sqlite.prepare("INSERT INTO memories_fts (fact, keywords, memory_id, container) VALUES (?, ?, ?, ?)").run(fact, keywords, id, container)
      sqlite.prepare("INSERT INTO memories_vec (memory_id, container, embedding) VALUES (?, ?, ?)").run(id, container, vecParam(vectors[i]!))
    }
  })
  sqlite.prepare("INSERT INTO profiles (container, static_text, dynamic_text) VALUES ('zaki', 'Engineer in Banjarbaru.', 'Running in Puma.')").run()
  sqlite.prepare("INSERT INTO profiles (container, static_text, dynamic_text) VALUES ('vorneo', 'Property site.', '')").run()
  sqlite.prepare(`INSERT INTO jobs (type, payload, status) VALUES ('profile', '{"container":"eva"}', 'queued')`).run()
  sqlite.prepare(`INSERT INTO jobs (type, payload, status) VALUES ('ingest', '{"documentId":"doc_2"}', 'queued')`).run()
  sqlite.prepare(`INSERT INTO jobs (type, payload, status) VALUES ('extract', '{"documentId":"doc_1"}', 'done')`).run()
  for (const [name, key] of Object.entries(KEYS)) {
    sqlite.prepare("INSERT INTO api_keys (id, name, key_hash, prefix, revoked_at) VALUES (?, ?, ?, ?, ?)").run(`key_${name}`, name, sha256(key), key.slice(0, 10), name === "old" ? 5000 : null)
  }
}

const DATA_TABLES = ["documents", "chunks", "memories", "jobs", "api_keys", "profiles", "memories_fts", "chunks_fts", "memories_vec", "chunks_vec"]

describe("migration 0004 on main's schema", () => {
  it("moves every existing row into workspace Zaki and logs the old admin in as zaki", async () => {
    const sqlite = openAt(BEFORE)
    await seedMain(sqlite)
    const before = Object.fromEntries(DATA_TABLES.map((t) => [t, (sqlite.prepare(`SELECT count(*) AS n FROM ${t}`).get() as { n: number }).n]))

    migrate(sqlite)
    const db = drizzle(sqlite, { schema }) as DB

    // Same row counts, all in ws_zaki.
    for (const t of DATA_TABLES) {
      const rows = sqlite.prepare(`SELECT workspace_id AS ws, count(*) AS n FROM ${t} GROUP BY workspace_id`).all()
      expect(rows, t).toEqual(before[t] ? [{ ws: "ws_zaki", n: before[t] }] : [])
    }
    expect(sqlite.prepare("SELECT id, name, slug FROM organization").all()).toEqual([{ id: "ws_zaki", name: "Zaki", slug: "zaki" }])
    // 0005: source_id is unique per workspace and container, not per container alone.
    const indexes = sqlite.prepare("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'documents'").pluck().all()
    expect(indexes).toContain("documents_workspace_source_id_unique")
    expect(indexes).not.toContain("documents_container_source_id_unique")
    expect(sqlite.pragma("foreign_key_check")).toEqual([])
    expect(sqlite.pragma("integrity_check")).toEqual([{ integrity_check: "ok" }])

    // First boot: the old ADMIN_PASSWORD_HASH becomes user zaki, owner of Zaki.
    const hash = await hashLegacyPassword("old admin password")
    expect(bootstrapUsers(db, hash)).toMatchObject({ created: true, workspaceId: "ws_zaki" })
    expect(listMembers(db, "ws_zaki")).toEqual([expect.objectContaining({ username: "zaki", role: "owner" })])

    const res = await authHandler(db)(
      new Request("http://localhost:4800/api/auth/sign-in/username", {
        method: "POST",
        headers: { "content-type": "application/json", origin: "http://localhost:4800" },
        body: JSON.stringify({ username: "zaki", password: "old admin password" }),
      }),
    )
    expect(res.status).toBe(200)
    const cookie = res.headers.getSetCookie().map((c) => c.split(";")[0]).join("; ")
    const viewer = await viewerFromHeaders(db, new Headers({ cookie }))
    expect(viewer).toMatchObject({ user: { username: "zaki" }, workspace: { id: "ws_zaki", name: "Zaki", role: "owner" } })

    // Old keys work, scoped to Zaki. The revoked one stays revoked.
    expect(verifyApiKey(db, KEYS.laptop)?.workspaceId).toBe("ws_zaki")
    expect(verifyApiKey(db, KEYS.utusan)?.workspaceId).toBe("ws_zaki")
    expect(verifyApiKey(db, KEYS.old)).toBeNull()

    // Search and history still work in the new scope.
    const hits = hybridSearch(sqlite, { workspaceId: "ws_zaki", container: "zaki", q: "running shoes", vector: await embedOne("running shoes", "query"), include: ["memories", "chunks"] })
    expect(hits[0]).toMatchObject({ type: "memory", id: "mem_a2" })
    const chunkHits = hybridSearch(sqlite, { workspaceId: "ws_zaki", container: "zaki", q: "Q4 roadmap", vector: await embedOne("Q4 roadmap", "query"), include: ["chunks"] })
    expect(chunkHits[0]).toMatchObject({ type: "chunk", id: "chk_1" })
    expect(memoryChain(db, "ws_zaki", "mem_a2").map((m) => m.id)).toEqual(["mem_a1", "mem_a2"])
    expect(sqlite.prepare("SELECT container, static_text FROM profiles ORDER BY container").all()).toEqual([
      { container: "vorneo", static_text: "Property site." },
      { container: "zaki", static_text: "Engineer in Banjarbaru." },
    ])
  })

  it("creates no workspace on a fresh database", () => {
    const sqlite = openAt(migrations)
    expect(sqlite.prepare("SELECT count(*) AS n FROM organization").get()).toEqual({ n: 0 })
  })
})

describe("migration 0010", () => {
  it("adds job timing columns, leaving old rows null, and indexes memories by source document", () => {
    const sqlite = openAt(migrations.filter((m) => m.name < "0010"))
    sqlite.prepare(`INSERT INTO jobs (workspace_id, type, payload, status) VALUES ('ws_a', 'extract', '{"documentId":"d"}', 'done')`).run()
    migrate(sqlite)
    expect(sqlite.prepare("SELECT status, started_at, finished_at FROM jobs").all()).toEqual([{ status: "done", started_at: null, finished_at: null }])
    const plan = sqlite
      .prepare("EXPLAIN QUERY PLAN SELECT count(*) FROM memories m WHERE m.source_document_id = ? AND m.valid_to IS NULL")
      .all("doc_1") as { detail: string }[]
    expect(plan.map((p) => p.detail).join(" ")).toContain("memories_source_document_idx")
  })
})
