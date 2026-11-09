// One-off: date the facts stored before dated facts existed. Started by hand
// with `cli backfill-dates`, never on deploy. Each `backfill_dates` job sends
// one batch to the gateway, writes what it found, and queues the next batch,
// so asks and new documents still get turns in the single job slot.
//
// Dates are written onto the current row, not as a new version: the fact says
// the same thing, it just gains a date. Only facts with no dates at all are
// sent, and a fact edited or dated meanwhile is left alone.
import { and, asc, count, eq, gt, isNull, or } from "drizzle-orm"
import type { DB } from "./db"
import { memories } from "./db/schema"
import { chatJson } from "./gateway/client"
import { BACKFILL_SYSTEM, backfillPrompt, backfillResult } from "./gateway/backfill"
import { enqueue, type JobPayloads } from "./jobs/queue"

export const BACKFILL_BATCH = 40

type Deps = { chat: typeof chatJson }

function undatedAfter(db: DB, workspaceId: string, payload: JobPayloads["backfill_dates"], limit: number) {
  const cur = payload.cursor
  return db
    .select()
    .from(memories)
    .where(
      and(
        eq(memories.workspaceId, workspaceId),
        payload.container ? eq(memories.container, payload.container) : undefined,
        isNull(memories.validTo),
        isNull(memories.eventDate),
        isNull(memories.dueDate),
        cur ? or(gt(memories.createdAt, cur.createdAt), and(eq(memories.createdAt, cur.createdAt), gt(memories.id, cur.id))) : undefined,
      ),
    )
    .orderBy(asc(memories.createdAt), asc(memories.id))
    .limit(limit)
    .all()
}

export function countUndated(db: DB, workspaceId: string, container?: string) {
  const row = db
    .select({ n: count() })
    .from(memories)
    .where(
      and(
        eq(memories.workspaceId, workspaceId),
        container ? eq(memories.container, container) : undefined,
        isNull(memories.validTo),
        isNull(memories.eventDate),
        isNull(memories.dueDate),
      ),
    )
    .get()
  return row?.n ?? 0
}

export type BackfillBatch = { checked: number; dated: number; next: boolean }

export async function backfillBatch(db: DB, workspaceId: string, payload: JobPayloads["backfill_dates"], deps: Deps = { chat: chatJson }): Promise<BackfillBatch> {
  const rows = undatedAfter(db, workspaceId, payload, BACKFILL_BATCH)
  if (!rows.length) return { checked: 0, dated: 0, next: false }
  const result = await deps.chat({
    system: BACKFILL_SYSTEM,
    prompt: backfillPrompt(rows.map((m, i) => ({ ref: i + 1, fact: m.fact, stored: new Date(m.createdAt).toISOString().slice(0, 10) }))),
    schema: backfillResult,
  })
  let dated = 0
  db.transaction(() => {
    for (const d of result.dates) {
      const m = rows[d.ref - 1]
      if (!m || (!d.event_date && !d.due_date)) continue
      const changed = db
        .update(memories)
        .set({
          eventDate: d.event_date?.date ?? null,
          eventPrecision: d.event_date?.precision ?? null,
          dueDate: d.due_date?.date ?? null,
          duePrecision: d.due_date?.precision ?? null,
        })
        .where(and(eq(memories.id, m.id), eq(memories.workspaceId, workspaceId), isNull(memories.validTo), isNull(memories.eventDate), isNull(memories.dueDate)))
        .run().changes
      dated += changed
    }
  })
  const last = rows.at(-1)!
  const next = rows.length === BACKFILL_BATCH
  if (next) {
    enqueue(db, workspaceId, "backfill_dates", {
      container: payload.container,
      cursor: { createdAt: last.createdAt, id: last.id },
      checked: (payload.checked ?? 0) + rows.length,
      dated: (payload.dated ?? 0) + dated,
    })
  }
  return { checked: rows.length, dated, next }
}
