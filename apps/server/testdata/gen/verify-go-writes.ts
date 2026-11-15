// Opens a database the Go build wrote to (internal/db TestWritesEveryIndexedTable
// with RECALL_M0_OUT=<file>) through the TS code, checks it, and finds the
// rows Go put in each indexed table. This is the rollback direction: the old
// image must keep working on a file the Go binary touched.
//
// Run from apps/app: npx tsx ../server/testdata/gen/verify-go-writes.ts <file>
import { openDb } from "../../../app/src/server/db/index.ts"
import { hybridSearch, indexMemory, unindexMemory, vecMemories } from "../../../app/src/server/search.ts"

const file = process.argv[2]
if (!file) throw new Error("usage: verify-go-writes.ts <file>")

const db = openDb(file)
const sqlite = db.$client
const check = (sqlite.prepare("PRAGMA integrity_check").pluck().get() as string) ?? ""
const scope = { workspaceId: "ws_alpha00000000000", container: "default" }
const mem = sqlite.prepare("SELECT embedding FROM memories_vec WHERE memory_id = ?").get("mem_gowrite000000000") as { embedding: Buffer }
const vector = new Float32Array(mem.embedding.buffer.slice(mem.embedding.byteOffset, mem.embedding.byteOffset + mem.embedding.byteLength))
const knn = vecMemories(sqlite, scope, vector, 1)
const hits = hybridSearch(sqlite, { ...scope, q: "gowritten zebra", vector, limit: 5, include: ["memories", "chunks"] })
// And TS can still change what Go wrote.
unindexMemory(sqlite, "mem_gowrite000000000")
indexMemory(sqlite, { id: "mem_gowrite000000000", ...scope, fact: "Gowritten zebra memory", keywords: "zebra" }, vector)
const after = vecMemories(sqlite, scope, vector, 1)
const result = {
  integrity: check,
  knn: knn.map((r) => r.id),
  hits: hits.map((h) => `${h.type}:${h.id}`),
  reindexed: after.map((r) => r.id),
}
console.log(JSON.stringify(result))
const ok =
  check === "ok" &&
  knn[0]?.id === "mem_gowrite000000000" &&
  hits.some((h) => h.id === "mem_gowrite000000000") &&
  hits.some((h) => h.id === "chk_gowrite000000000") &&
  after[0]?.id === "mem_gowrite000000000"
sqlite.close()
if (!ok) {
  console.error("FAIL")
  process.exit(1)
}
console.log("PASS")
