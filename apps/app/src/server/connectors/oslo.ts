// Maps Oslo's changes feed to one digest document per local day: the tasks
// completed and the projects created or completed that day, each with its
// link. A day is written only after it is over, so its document never needs
// to change (source_id oslo:day:YYYY-MM-DD). Until then its events wait in
// app_connector_staging, so nothing is lost when the cursor moves past them.
import { and, eq, isNull, like, lt, ne, sql } from "drizzle-orm"
import { formatDate, isTimezone } from "../../lib/dates"
import type { DB } from "../db"
import { appConnectorStaging, type AppConnector } from "../db/schema"
import { findBySourceId } from "../memories"
import type { ChangeItem } from "./changes-feed"

export const DEFAULT_TIMEZONE = "Asia/Makassar"

export type OsloEvent = {
  type: "task" | "project_created" | "project_completed"
  id: string
  title: string
  url: string
  // When it happened, ISO.
  at: string
  project?: string
}

export const osloSourceId = (day: string) => `oslo:day:${day}`

// The calendar date of an instant in a time zone, as YYYY-MM-DD.
export function localDay(iso: string, timezone: string): string | null {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return null
  const tz = isTimezone(timezone) ? timezone : DEFAULT_TIMEZONE
  return new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(t)
}

function localTime(iso: string, timezone: string) {
  const tz = isTimezone(timezone) ? timezone : DEFAULT_TIMEZONE
  return new Intl.DateTimeFormat("en-GB", { timeZone: tz, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(Date.parse(iso))
}

const str = (v: unknown) => (typeof v === "string" ? v.trim() : "")
const iso = (v: unknown) => {
  const s = str(v)
  return s && !Number.isNaN(Date.parse(s)) ? s : null
}

// --- staging -----------------------------------------------------------------------

type Ctx = { timezone: string; caughtUp: boolean; backfillFrom: string | null; now: number }

function dayWritten(db: DB, conn: AppConnector, day: string) {
  return !!findBySourceId(db, conn.workspaceId, conn.container, osloSourceId(day))
}

// Drops events for an item that haven't been written yet, except `keep`.
function unstage(db: DB, conn: AppConnector, prefix: string, keep?: string) {
  db.delete(appConnectorStaging)
    .where(
      and(
        eq(appConnectorStaging.connectorId, conn.id),
        // substr, not LIKE: an id may contain _ or %.
        sql`substr(${appConnectorStaging.itemKey}, 1, ${prefix.length}) = ${prefix}`,
        isNull(appConnectorStaging.writtenAt),
        keep ? ne(appConnectorStaging.itemKey, keep) : undefined,
      ),
    )
    .run()
}

function stage(db: DB, conn: AppConnector, ctx: Ctx, key: string, event: OsloEvent): boolean {
  const day = localDay(event.at, ctx.timezone)
  if (!day) return false
  // The first sync only reaches back so far.
  if (!ctx.caughtUp && ctx.backfillFrom && day < ctx.backfillFrom) return false
  // That day's digest is out already; it doesn't change.
  if (dayWritten(db, conn, day)) return false
  db.insert(appConnectorStaging)
    .values({ connectorId: conn.id, day, itemKey: key, payload: event })
    .onConflictDoUpdate({
      target: [appConnectorStaging.connectorId, appConnectorStaging.itemKey],
      set: { payload: event },
      setWhere: isNull(appConnectorStaging.writtenAt),
    })
    .run()
  return true
}

// Stages one page of Oslo items. Returns how many events were staged.
export function stageOsloItems(db: DB, conn: AppConnector, items: ChangeItem[], ctx: Ctx): number {
  let staged = 0
  for (const item of items) {
    const d = item.data
    if (item.kind === "task") {
      const completedAt = iso(d.completedAt)
      const prefix = `task:${item.id}:`
      // Deleted, reopened or cancelled: whatever was waiting for this task goes.
      if (item.deleted || str(d.status) !== "done" || !completedAt) {
        unstage(db, conn, prefix)
        continue
      }
      const day = localDay(completedAt, ctx.timezone)
      if (!day) continue
      // One line per task per day. A later completion replaces an unwritten earlier one.
      const key = `${prefix}${day}`
      unstage(db, conn, prefix, key)
      const project = str(d.projectName)
      if (stage(db, conn, ctx, key, { type: "task", id: item.id, title: str(d.title) || "(untitled task)", url: item.url, at: completedAt, ...(project ? { project } : {}) })) staged++
      continue
    }
    if (item.kind === "project") {
      if (item.deleted) {
        unstage(db, conn, `project-completed:${item.id}:`)
        continue
      }
      const name = str(d.name) || "(untitled project)"
      // Created: reported once ever. The contract has no createdAt, so the
      // first time Recall sees a project after the backfill counts as its
      // creation. Projects that already existed are remembered without a line.
      const createdKey = `project-created:${item.id}`
      const seen = db
        .select({ k: appConnectorStaging.itemKey })
        .from(appConnectorStaging)
        .where(and(eq(appConnectorStaging.connectorId, conn.id), eq(appConnectorStaging.itemKey, createdKey)))
        .get()
      if (!seen) {
        const createdAt = iso(d.createdAt)
        const event: OsloEvent = { type: "project_created", id: item.id, title: name, url: item.url, at: createdAt ?? item.updatedAt }
        const reportable = createdAt !== null || ctx.caughtUp
        if (reportable && stage(db, conn, ctx, createdKey, event)) staged++
        else {
          db.insert(appConnectorStaging)
            .values({ connectorId: conn.id, day: localDay(event.at, ctx.timezone) ?? "", itemKey: createdKey, payload: event, writtenAt: ctx.now })
            .onConflictDoNothing()
            .run()
        }
      }
      const completedAt = iso(d.completedAt)
      const prefix = `project-completed:${item.id}:`
      if (!completedAt) {
        unstage(db, conn, prefix)
        continue
      }
      const day = localDay(completedAt, ctx.timezone)
      if (!day) continue
      const key = `${prefix}${day}`
      unstage(db, conn, prefix, key)
      if (stage(db, conn, ctx, key, { type: "project_completed", id: item.id, title: name, url: item.url, at: completedAt })) staged++
    }
  }
  return staged
}

// --- the digest --------------------------------------------------------------------

export function osloDigest(day: string, events: OsloEvent[], timezone: string): { title: string; content: string } {
  const sorted = [...events].sort((a, b) => a.at.localeCompare(b.at) || a.id.localeCompare(b.id))
  const tasks = sorted.filter((e) => e.type === "task")
  const created = sorted.filter((e) => e.type === "project_created")
  const completed = sorted.filter((e) => e.type === "project_completed")
  const line = (e: OsloEvent) => `- ${e.title}${e.project ? ` (project: ${e.project})` : ""}, at ${localTime(e.at, timezone)}.${e.url ? ` ${e.url}` : ""}`
  const weekday = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", weekday: "long" }).format(Date.parse(`${day}T00:00:00Z`))
  const dateText = formatDate({ date: day, precision: "day" })
  const sections: string[] = [`Activity in Oslo, the todo app, on ${weekday}, ${dateText} (${timezone} time).`]
  if (tasks.length) sections.push([`Tasks completed (${tasks.length}):`, ...tasks.map(line)].join("\n"))
  if (created.length) sections.push([`Projects created (${created.length}):`, ...created.map(line)].join("\n"))
  if (completed.length) sections.push([`Projects completed (${completed.length}):`, ...completed.map(line)].join("\n"))
  const counts = [
    tasks.length && `${tasks.length} ${tasks.length === 1 ? "task" : "tasks"} done`,
    created.length && `${created.length} ${created.length === 1 ? "project" : "projects"} started`,
    completed.length && `${completed.length} ${completed.length === 1 ? "project" : "projects"} finished`,
  ].filter(Boolean)
  return { title: `Oslo on ${dateText}: ${counts.join(", ")}`, content: sections.join("\n\n") }
}

// Days with unwritten events that are over in the time zone, oldest first.
export function closedDays(db: DB, conn: AppConnector, today: string): string[] {
  return db
    .selectDistinct({ day: appConnectorStaging.day })
    .from(appConnectorStaging)
    .where(and(eq(appConnectorStaging.connectorId, conn.id), isNull(appConnectorStaging.writtenAt), lt(appConnectorStaging.day, today)))
    .orderBy(appConnectorStaging.day)
    .all()
    .map((r) => r.day)
}

export function dayEvents(db: DB, conn: AppConnector, day: string): OsloEvent[] {
  return db
    .select({ payload: appConnectorStaging.payload })
    .from(appConnectorStaging)
    .where(and(eq(appConnectorStaging.connectorId, conn.id), eq(appConnectorStaging.day, day), isNull(appConnectorStaging.writtenAt)))
    .all()
    .map((r) => r.payload as OsloEvent)
}

// After a day's digest is written: its events are done. Project-created keys
// stay (marked written) so the project is never reported as new again.
export function markDayWritten(db: DB, conn: AppConnector, day: string, now: number) {
  db.update(appConnectorStaging)
    .set({ writtenAt: now })
    .where(
      and(
        eq(appConnectorStaging.connectorId, conn.id),
        eq(appConnectorStaging.day, day),
        isNull(appConnectorStaging.writtenAt),
        like(appConnectorStaging.itemKey, "project-created:%"),
      ),
    )
    .run()
  db.delete(appConnectorStaging)
    .where(and(eq(appConnectorStaging.connectorId, conn.id), eq(appConnectorStaging.day, day), sql`${appConnectorStaging.itemKey} NOT LIKE 'project-created:%'`))
    .run()
}
