import { and, desc, eq } from "drizzle-orm"
import type { DB } from "./db"
import { documents, memories, type Memory } from "./db/schema"

export const GRAPH_MAX_DOCUMENTS = 150

const iso = (ms: number) => new Date(ms).toISOString()

// Shapes documents and their memories (with version chains) for
// @supermemory/memory-graph. Memories added by hand have no document, so they
// hang off one synthetic "Added by hand" node.
export function buildGraph(db: DB, workspaceId: string, container: string) {
  const docs = db
    .select({ id: documents.id, title: documents.title, url: documents.url, sourceType: documents.sourceType, createdAt: documents.createdAt })
    .from(documents)
    .where(and(eq(documents.workspaceId, workspaceId), eq(documents.container, container)))
    .orderBy(desc(documents.createdAt))
    .limit(GRAPH_MAX_DOCUMENTS)
    .all()
  const totalDocuments = db.$client
    .prepare("SELECT count(*) AS n FROM documents WHERE workspace_id = ? AND container = ?")
    .get(workspaceId, container) as { n: number }
  const all = db
    .select()
    .from(memories)
    .where(and(eq(memories.workspaceId, workspaceId), eq(memories.container, container)))
    .all()
  const docIds = new Set(docs.map((d) => d.id))
  const nextOf = new Map<string, string>()
  for (const m of all) if (m.supersedesId) nextOf.set(m.supersedesId, m.id)
  const byId = new Map(all.map((m) => [m.id, m]))

  const version = (m: Memory) => {
    let n = 1
    let cur = m
    while (cur.supersedesId && n < 100) {
      const prev = byId.get(cur.supersedesId)
      if (!prev) break
      cur = prev
      n++
    }
    return { n, root: cur.id }
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
  for (const m of all) {
    if (m.sourceDocumentId && docIds.has(m.sourceDocumentId)) {
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
      title: "Other sources",
      summary: null,
      documentType: "text",
      createdAt: iso(Math.min(...orphans.map((m) => m.createdAt))),
      updatedAt: iso(Math.max(...orphans.map((m) => m.createdAt))),
      memories: orphans.map(toMemory),
    })
  }
  return { documents: out, totalDocuments: totalDocuments.n, capped: totalDocuments.n > GRAPH_MAX_DOCUMENTS }
}

