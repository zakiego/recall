import { and, desc, eq, inArray, isNull, or } from "drizzle-orm"
import type { DB } from "./db"
import { documents, memories, type Memory } from "./db/schema"

export const GRAPH_MAX_DOCUMENTS = 150

const iso = (ms: number) => new Date(ms).toISOString()

// Shapes documents and their memories (with version chains) for
// @supermemory/memory-graph. Only the newest GRAPH_MAX_DOCUMENTS documents are
// drawn, with their own facts. Facts with no source document (added by hand, or
// left behind when their document was deleted) hang off one synthetic node.
// Facts from older documents are left out and counted in hiddenFacts, so they
// never land in that node as if they had no source.
export function buildGraph(db: DB, workspaceId: string, container: string) {
  const scope = (t: typeof documents | typeof memories) => and(eq(t.workspaceId, workspaceId), eq(t.container, container))
  const docs = db
    .select({ id: documents.id, title: documents.title, url: documents.url, sourceType: documents.sourceType, createdAt: documents.createdAt })
    .from(documents)
    .where(scope(documents))
    .orderBy(desc(documents.createdAt), desc(documents.id))
    .limit(GRAPH_MAX_DOCUMENTS)
    .all()
  const totalDocuments = db.$client
    .prepare("SELECT count(*) AS n FROM documents WHERE workspace_id = ? AND container = ?")
    .get(workspaceId, container) as { n: number }
  const docIds = docs.map((d) => d.id)
  const shown = db
    .select()
    .from(memories)
    .where(
      and(scope(memories), docIds.length ? or(inArray(memories.sourceDocumentId, docIds), isNull(memories.sourceDocumentId)) : isNull(memories.sourceDocumentId)),
    )
    .all()
  // Version links for the whole container, so version numbers and "is there a
  // newer version" stay right when the other end of a chain isn't drawn.
  const links = db.select({ id: memories.id, supersedesId: memories.supersedesId }).from(memories).where(scope(memories)).all()
  const parentOf = new Map<string, string>()
  const nextOf = new Map<string, string>()
  for (const l of links) {
    if (!l.supersedesId) continue
    parentOf.set(l.id, l.supersedesId)
    nextOf.set(l.supersedesId, l.id)
  }

  const version = (m: Memory) => {
    let n = 1
    let cur = m.id
    for (let parent = parentOf.get(cur); parent && n < 100; parent = parentOf.get(cur)) {
      cur = parent
      n++
    }
    return { n, root: cur }
  }

  const toMemory = (m: Memory) => {
    const v = version(m)
    return {
      id: m.id,
      memory: m.fact,
      content: m.keywords,
      isStatic: m.kind === "static",
      spaceId: container,
      isLatest: !nextOf.has(m.id) && m.validTo === null,
      isForgotten: m.validTo !== null && !nextOf.has(m.id),
      forgetAfter: null,
      forgetReason: null,
      version: v.n,
      parentMemoryId: m.supersedesId,
      rootMemoryId: v.root,
      createdAt: iso(m.createdAt),
      updatedAt: iso(m.validTo ?? m.createdAt),
      relation: m.supersedesId ? ("updates" as const) : null,
      updatesMemoryId: m.supersedesId,
      nextVersionId: nextOf.get(m.id) ?? null,
      spaceContainerTag: container,
    }
  }

  const grouped = new Map<string, Memory[]>()
  const orphans: Memory[] = []
  for (const m of shown) {
    if (m.sourceDocumentId) {
      const list = grouped.get(m.sourceDocumentId) ?? []
      list.push(m)
      grouped.set(m.sourceDocumentId, list)
    } else orphans.push(m)
  }

  const out = docs.map((d) => ({
    id: d.id,
    title: d.title ?? d.url ?? "Untitled",
    summary: null,
    documentType: d.sourceType,
    createdAt: iso(d.createdAt),
    updatedAt: iso(d.createdAt),
    memories: (grouped.get(d.id) ?? []).map(toMemory),
  }))
  if (orphans.length) {
    out.push({
      id: `manual:${container}`,
      title: "No source",
      summary: null,
      documentType: "text",
      createdAt: iso(orphans.reduce((t, m) => Math.min(t, m.createdAt), Infinity)),
      updatedAt: iso(orphans.reduce((t, m) => Math.max(t, m.createdAt), -Infinity)),
      memories: orphans.map(toMemory),
    })
  }
  return {
    documents: out,
    shownDocuments: docs.length,
    totalDocuments: totalDocuments.n,
    capped: totalDocuments.n > docs.length,
    // Facts (current and older versions) from documents outside the window.
    hiddenFacts: links.length - shown.length,
  }
}
