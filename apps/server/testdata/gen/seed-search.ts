// Seeds testdata/search/seeded.db through the real TS code (openDb runs the
// migrations, indexChunk and indexMemory fill both FTS5 and both vec0 tables
// with real e5 vectors), then records what the TS build's FTS and KNN queries
// return on it in testdata/search/seeded.json.
//
// Run from apps/app: MODELS_DIR=data/models RECALL_EMBED=inline npx tsx ../server/testdata/gen/seed-search.ts
import fs from "node:fs"
import path from "node:path"
import { openDb } from "../../../app/src/server/db/index.ts"
import { chunks, documents, memories } from "../../../app/src/server/db/schema.ts"
import { embed } from "../../../app/src/server/embed.ts"
import { ftsQuery, hybridSearch, indexChunk, indexMemory, rrf, vecMemories } from "../../../app/src/server/search.ts"

globalThis.fetch = async (input: unknown) => {
  throw new Error(`network access refused: ${String(input)}`)
}

const dir = path.resolve(import.meta.dirname, "../search")
const file = path.join(dir, "seeded.db")

type Scope = { workspaceId: string; container: string }
const scopes: Scope[] = [
  { workspaceId: "ws_alpha00000000000", container: "default" },
  { workspaceId: "ws_alpha00000000000", container: "work" },
  { workspaceId: "ws_beta000000000000", container: "default" },
]

const facts: [string, string, "static" | "dynamic"][] = [
  ["Prefers Adidas running shoes", "sneakers sepatu lari footwear brand", "static"],
  ["Is allergic to peanuts", "alergi kacang food allergy", "static"],
  ["Lives in Bandarjaya, South Kalimantan", "tinggal kota domisili home city", "static"],
  ["Works as a software engineer on self-hosted tools", "pekerjaan job engineer", "static"],
  ["Thesis results seminar is on 16 November 2026", "semhas skripsi seminar", "dynamic"],
  ["Drinks black coffee every morning", "kopi hitam pagi minuman", "dynamic"],
  ["Uses Go for backends and TypeScript for frontends", "bahasa pemrograman golang typescript", "static"],
  ["Has a cat named Mochi", "kucing peliharaan pet", "static"],
  ["Mother's birthday is 12 March", "ulang tahun ibu ulang tahun", "static"],
  ["Plans to visit Tokyo in April 2027", "liburan jepang travel trip", "dynamic"],
  ["Rumah dijual di Bandarjaya dengan harga 850 juta", "properti rumah harga", "dynamic"],
  ["Suka makan soto Banjar di dekat kantor", "makanan favorit kuliner", "dynamic"],
  ["Owes Budi 200 ribu from last week's dinner", "utang hutang uang", "dynamic"],
  ["Runs 5 km three times a week", "olahraga lari jogging fitness", "dynamic"],
  ["Car insurance renews on 14 December", "asuransi mobil perpanjangan", "dynamic"],
  ["Speaks Indonesian, Banjarese and English", "bahasa language", "static"],
  ["Meeting with the design team moved to Thursday", "rapat desain jadwal", "dynamic"],
  ["Favorite IDE is Zed", "editor tools", "static"],
  ["Keeps backups on R2 encrypted with age", "backup cadangan storage", "static"],
  ["Allergic to shellfish as well", "alergi udang seafood", "static"],
]

const docs: { title: string; text: string[] }[] = [
  {
    title: "Trip notes",
    text: [
      "Tokyo trip planning: book flights in January, hotel near Shinjuku, budget 30 million rupiah.",
      "Day two: visit Asakusa and the Senso-ji temple, then ramen in Shibuya.",
      "Remember the JR pass and a pocket wifi.",
    ],
  },
  {
    title: "Catatan rapat",
    text: [
      "Rapat koordinasi membahas jadwal rilis versi baru dan pembagian tugas tim.",
      "Keputusan: migrasi database dilakukan hari Sabtu malam, backup dulu sebelum mulai.",
      "Tindak lanjut: Zane menyiapkan skrip pre-deploy dan uji restore.",
    ],
  },
  {
    title: null as unknown as string,
    text: ["Grocery list: eggs, rice, chili, shallots, garlic, coffee beans, cat food for Mochi."],
  },
  {
    title: "Recipe",
    text: [
      "Soto Banjar: chicken broth with spices, rice cakes, boiled egg, and perkedel.",
      "Serve with lime, sambal and fried shallots.",
    ],
  },
]

async function main() {
  fs.mkdirSync(dir, { recursive: true })
  for (const f of [file, `${file}-wal`, `${file}-shm`]) fs.rmSync(f, { force: true })
  const db = openDb(file)
  const sqlite = db.$client
  let n = 0
  const id = (prefix: string) => `${prefix}_${String(++n).padStart(16, "0")}`

  const memoryRows: { id: string; scope: Scope; fact: string; keywords: string }[] = []
  for (const [si, scope] of scopes.entries()) {
    // The third scope gets fewer rows, the first all of them.
    const mine = facts.filter((_, i) => si === 0 || i % (si + 1) === 0)
    const vectors = await embed(
      mine.map(([fact, keywords]) => `${fact}. ${keywords}`),
      "passage",
    )
    mine.forEach(([fact, keywords, kind], i) => {
      const mid = id("mem")
      db.insert(memories)
        .values({ id: mid, workspaceId: scope.workspaceId, container: scope.container, fact, keywords, kind, createdAt: 1_700_000_000_000 + n, validFrom: 1_700_000_000_000 + n })
        .run()
      indexMemory(sqlite, { id: mid, workspaceId: scope.workspaceId, container: scope.container, fact, keywords }, vectors[i]!)
      memoryRows.push({ id: mid, scope, fact, keywords })
    })
    for (const d of docs) {
      const did = id("doc")
      db.insert(documents)
        .values({ id: did, workspaceId: scope.workspaceId, container: scope.container, sourceType: "text", title: d.title, content: d.text.join("\n\n"), status: "done", createdAt: 1_700_000_000_000 + n })
        .run()
      const cv = await embed(d.text, "passage")
      d.text.forEach((text, idx) => {
        const cid = id("chk")
        db.insert(chunks).values({ id: cid, documentId: did, workspaceId: scope.workspaceId, container: scope.container, idx, text }).run()
        indexChunk(sqlite, { id: cid, workspaceId: scope.workspaceId, container: scope.container, text }, cv[idx]!)
      })
    }
  }
  // A superseded memory: it stays in the table but is not indexed.
  const old = memoryRows[0]!
  sqlite.prepare("UPDATE memories SET valid_to = ? WHERE id = ?").run(1_700_000_500_000, memoryRows[5]!.id)

  // A scope with more rows than the candidate pool (30), so KNN and FTS cut
  // their lists and some hits come from one list only, plus index entries
  // that must not surface: a memory row in another container, a chunk row in
  // another container, a chunk whose document is in another workspace, and a
  // zero vector (vec0 gives a NULL distance for it).
  const big: Scope = { workspaceId: "ws_alpha00000000000", container: "big" }
  const bigFacts = Array.from({ length: 45 }, (_, i) => {
    const [fact, keywords, kind] = facts[i % facts.length]!
    return [`${fact} (note ${i})`, `${keywords} catatan${i % 7}`, kind] as const
  })
  const bigVectors = await embed(
    bigFacts.map(([fact, keywords]) => `${fact}. ${keywords}`),
    "passage",
  )
  bigFacts.forEach(([fact, keywords, kind], i) => {
    const mid = id("mem")
    db.insert(memories)
      .values({ id: mid, workspaceId: big.workspaceId, container: big.container, fact, keywords, kind, createdAt: 1_700_000_000_000 + n, validFrom: 1_700_000_000_000 + n, eventDate: i % 9 === 0 ? "2026-11-16" : null, eventPrecision: i % 9 === 0 ? "day" : null, dueDate: i % 11 === 0 ? "2027-01" : null, duePrecision: i % 11 === 0 ? "month" : null, sourceDocumentId: null })
      .run()
    indexMemory(sqlite, { id: mid, workspaceId: big.workspaceId, container: big.container, fact, keywords }, bigVectors[i]!)
  })
  const bigDoc = id("doc")
  db.insert(documents).values({ id: bigDoc, workspaceId: big.workspaceId, container: big.container, sourceType: "text", title: null, content: "x", status: "done", createdAt: 1_700_000_000_000 + n }).run()
  const bigChunks = Array.from({ length: 35 }, (_, i) => `${docs[i % docs.length]!.text[0]} Part ${i} of the long file, catatan${i % 7}.`)
  const bigChunkVectors = await embed(bigChunks, "passage")
  bigChunks.forEach((text, idx) => {
    const cid = id("chk")
    db.insert(chunks).values({ id: cid, documentId: bigDoc, workspaceId: big.workspaceId, container: big.container, idx, text }).run()
    indexChunk(sqlite, { id: cid, workspaceId: big.workspaceId, container: big.container, text }, bigChunkVectors[idx]!)
  })
  // Stray index entries.
  const strayMem = memoryRows.find((m) => m.scope.container === "work")!
  indexMemory(sqlite, { id: strayMem.id, workspaceId: big.workspaceId, container: big.container, fact: strayMem.fact + " catatan1", keywords: strayMem.keywords }, bigVectors[1]!)
  const strayChunk = sqlite.prepare("SELECT id, text FROM chunks WHERE container = 'default' LIMIT 1").get() as { id: string; text: string }
  sqlite.prepare("DELETE FROM chunks_vec WHERE chunk_id = ?").run(strayChunk.id)
  sqlite.prepare("DELETE FROM chunks_fts WHERE chunk_id = ?").run(strayChunk.id)
  indexChunk(sqlite, { id: strayChunk.id, workspaceId: big.workspaceId, container: big.container, text: strayChunk.text + " catatan2" }, bigChunkVectors[2]!)
  const betaDoc = sqlite.prepare("SELECT id FROM documents WHERE workspace_id = 'ws_beta000000000000' LIMIT 1").get() as { id: string }
  const crossChunk = id("chk")
  db.insert(chunks).values({ id: crossChunk, documentId: betaDoc.id, workspaceId: big.workspaceId, container: big.container, idx: 99, text: "Cross workspace chunk catatan3" }).run()
  indexChunk(sqlite, { id: crossChunk, workspaceId: big.workspaceId, container: big.container, text: "Cross workspace chunk catatan3" }, bigChunkVectors[3]!)
  const zeroMem = id("mem")
  db.insert(memories).values({ id: zeroMem, workspaceId: big.workspaceId, container: big.container, fact: "Zero vector catatan4", keywords: "", createdAt: 1_700_000_000_000 + n, validFrom: 1_700_000_000_000 + n }).run()
  indexMemory(sqlite, { id: zeroMem, workspaceId: big.workspaceId, container: big.container, fact: "Zero vector catatan4", keywords: "" }, new Float32Array(384))
  scopes.push(big)

  const queries = [
    "catatan1 catatan2 catatan3 catatan4",
    "zero vector",
    "running shoes",
    "alergi",
    "where does he live",
    "kopi",
    "Tokyo trip",
    "rapat migrasi database",
    "soto",
    "cat",
    "!! ?",
    "R2 backup age",
    "OR NEAR(x) \"quoted\"",
    "550 juta",
  ]
  const results = []
  for (const q of queries) {
    const [vector] = await embed([q], "query")
    for (const scope of scopes) {
      const match = ftsQuery(q)
      const ftsMem = match
        ? sqlite
            .prepare(
              `SELECT memory_id AS id, bm25(memories_fts, 1.0, 0.5) AS raw
       FROM memories_fts WHERE memories_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`,
            )
            .all(match, scope.workspaceId, scope.container, 30)
        : []
      const ftsChk = match
        ? sqlite
            .prepare(
              `SELECT chunk_id AS id, bm25(chunks_fts) AS raw
       FROM chunks_fts WHERE chunks_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`,
            )
            .all(match, scope.workspaceId, scope.container, 30)
        : []
      const vecMem = vecMemories(sqlite, scope, vector!, 30)
      const vecChk = sqlite
        .prepare(
          `SELECT chunk_id AS id, distance AS raw FROM chunks_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`,
        )
        .all(Buffer.from(vector!.buffer, vector!.byteOffset, vector!.byteLength), 30, scope.workspaceId, scope.container)
      const hybrid = hybridSearch(sqlite, { ...scope, q, vector: vector!, limit: 10, include: ["memories", "chunks"] })
      const variants = [
        {},
        { limit: 1 },
        { limit: 3, include: ["chunks"] },
        { limit: 0, include: ["memories", "chunks"] },
        { limit: 150, include: ["chunks", "memories"] },
        { include: [] },
        { limit: -4 },
      ] as { limit?: number; include?: ("memories" | "chunks")[] }[]
      const hybridVariants = variants.map((params) => ({
        params,
        json: JSON.stringify(hybridSearch(sqlite, { ...scope, q, vector: vector!, ...params })),
      }))
      results.push({
        q,
        scope,
        vector: Buffer.from(vector!.buffer, vector!.byteOffset, vector!.byteLength).toString("base64"),
        match,
        ftsMemories: ftsMem,
        ftsChunks: ftsChk,
        vecMemories: vecMem.map(({ id, raw }) => ({ id, raw })),
        vecChunks: vecChk,
        hybrid,
        hybridVariants,
      })
    }
  }
  const ftsInputs = [
    "Adidas OR \"shoes\" NEAR(x)",
    "!! ?",
    "",
    "a b c",
    "a 1 b 22",
    "ΟΔΟΣ Σίσυφος",
    "İstanbul GÖTEBORG",
    "Ｆｕｌｌｗｉｄｔｈ ＡＢＣ １２３",
    "ﬁne ﬂower",
    "x² y³ ½",
    "我喜欢 东京 日本語",
    "kopi kopi KOPI Kopi",
    "e\u0301clair café",
    "it's don't won't",
    "foo_bar foo-bar foo.bar",
    "emoji 😀 test 👨‍👩‍👧",
    "١٢٣ arabic digits ٤",
    "Ⅻ roman ⅻ",
    Array.from({ length: 40 }, (_, i) => `w${i}`).join(" "),
    Array.from({ length: 40 }, (_, i) => `term${i % 5}`).join(" "),
    "\u{1ccd6}\u{1ccf0} new letters",
    "tab\tnew\nline",
    "\u00a0nbsp\u3000ideo",
    "ΑΣ.ΑΣ",
    Array.from({ length: 40 }, (_, i) => (i % 3 === 0 ? "x" : `t${i}`)).join(" "),
    Array.from({ length: 40 }, (_, i) => `dup${i % 3}`).join(" ") + " late1 late2",
    "𠀀 𠀀𠀁 x 𝔸",
    "a".repeat(1) + " " + "7",
  ]
  const ftsQueries = ftsInputs.map((q) => ({ q, match: ftsQuery(q) }))
  const rrfCases = [
    [
      [
        { id: "a", rank: 1, raw: -3 },
        { id: "b", rank: 2, raw: -2 },
      ],
      [
        { id: "b", rank: 1, raw: 0.1 },
        { id: "c", rank: 2, raw: 0.2 },
      ],
    ],
    [[], [{ id: "x", rank: 1, raw: 0.5 }]],
    [
      [
        { id: "d", rank: 1, raw: -2 },
        { id: "e", rank: 2, raw: -1 },
        { id: "d", rank: 3, raw: -0.5 },
      ],
      [
        { id: "e", rank: 1, raw: 0.1 },
        { id: "e", rank: 2, raw: 0.3 },
      ],
    ],
    [[{ id: "x", rank: 1, raw: -1 }], []],
    [
      [
        { id: "p", rank: 1, raw: -1 },
        { id: "q", rank: 2, raw: -1 },
      ],
      [
        { id: "q", rank: 1, raw: 0.1 },
        { id: "p", rank: 2, raw: 0.2 },
      ],
    ],
  ].map(([fts, vec]) => ({ fts, vec, fused: rrf(fts as never, vec as never) }))
  const vec = (sqlite.prepare("SELECT vec_version() AS v").get() as { v: string }).v
  sqlite.pragma("wal_checkpoint(TRUNCATE)")
  sqlite.close()
  void old
  fs.writeFileSync(path.join(dir, "seeded.json"), JSON.stringify({ vec, scopes, results, ftsQueries, rrf: rrfCases }, null, 1) + "\n")
  console.log(`seeded ${file}: ${n} ids, ${results.length} query results`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
