// Email reminders for facts with a due date. Settings are per user and cover
// every workspace the user is a member of, never any other.
//
// The `reminders` job runs at the top of every hour. A user gets at most one
// digest a day, once it is past SEND_HOUR in their time zone. Each reminder
// (fact, due date, lead time) is sent once: it is recorded in reminders_sent as
// 'pending' before the email goes out and marked 'sent' after.
import { createHash } from "node:crypto"
import { and, eq, sql } from "drizzle-orm"
import { addDays, daysBetween, formatDate, hourIn, isTimezone, relativeDays, todayIn, type Dated } from "../lib/dates"
import { APP_BASE } from "../lib/app-path"
import { config } from "./config"
import type { DB } from "./db"
import { jobs, reminderSettings, remindersSent, user, type Memory } from "./db/schema"
import { emailConfigured, escapeHtml, sendEmail, type SendEmail } from "./email"
import { enqueue } from "./jobs/queue"
import { dueOf, listUpcoming } from "./memories"
import { listMemberships } from "./users"

export const SEND_HOUR = 8
export const DEFAULT_LEAD_DAYS = [30, 1]
export const MAX_LEAD_DAYS = 365
export const MAX_LEADS = 6
// The reminders job serves every workspace, but jobs.workspace_id can't be empty.
export const SYSTEM_WORKSPACE = "_system"

export type ReminderSettings = { enabled: boolean; email: string | null; leadDays: number[]; timezone: string; updatedAt: number | null }

export function normalizeLeads(leads: number[]) {
  return [...new Set(leads.filter((n) => Number.isInteger(n) && n >= 0 && n <= MAX_LEAD_DAYS))].sort((a, b) => b - a).slice(0, MAX_LEADS)
}

export function getReminderSettings(db: DB, userId: string): ReminderSettings {
  const row = db.select().from(reminderSettings).where(eq(reminderSettings.userId, userId)).get()
  if (!row) return { enabled: false, email: null, leadDays: DEFAULT_LEAD_DAYS, timezone: "UTC", updatedAt: null }
  const leads = Array.isArray(row.leadDays) ? normalizeLeads(row.leadDays) : DEFAULT_LEAD_DAYS
  return { enabled: row.enabled, email: row.email, leadDays: leads, timezone: isTimezone(row.timezone) ? row.timezone : "UTC", updatedAt: row.updatedAt }
}

export function saveReminderSettings(db: DB, userId: string, input: { enabled: boolean; email: string | null; leadDays: number[]; timezone: string }) {
  const values = {
    enabled: input.enabled,
    email: input.email?.trim() || null,
    leadDays: normalizeLeads(input.leadDays),
    timezone: isTimezone(input.timezone) ? input.timezone : "UTC",
    updatedAt: Date.now(),
  }
  db.insert(reminderSettings)
    .values({ userId, ...values })
    .onConflictDoUpdate({ target: reminderSettings.userId, set: values })
    .run()
  return getReminderSettings(db, userId)
}

// --- which reminders are due ------------------------------------------------------

// The lead time a fact is in today: the smallest lead that covers the days
// left. With leads [30, 1], a fact due in 20 days is in its 30-day reminder and
// one due tomorrow in its 1-day reminder. Null when no lead covers it yet, or
// it is past due. A fact added late skips the leads it missed: one due in 5
// days gets the 30-day reminder now and the 1-day reminder later.
export function activeLead(daysLeft: number, leads: number[]): number | null {
  if (daysLeft < 0) return null
  const covering = leads.filter((l) => l >= daysLeft)
  return covering.length ? Math.min(...covering) : null
}

export type DueReminder = {
  memory: Memory
  workspaceId: string
  workspaceName: string
  due: Dated
  daysLeft: number
  lead: number
}

// Ids of this memory and every version before it. A reminder sent for an
// older version counts, so editing a fact doesn't send its reminder again.
function chainBack(db: DB, workspaceId: string, id: string): string[] {
  return db.$client
    .prepare(
      `WITH RECURSIVE chain(id, prev, n) AS (
         SELECT id, supersedes_id, 0 FROM memories WHERE id = ? AND workspace_id = ?
         UNION ALL SELECT m.id, m.supersedes_id, chain.n + 1 FROM memories m JOIN chain ON m.id = chain.prev
         WHERE chain.n < 100 AND m.workspace_id = ?
       ) SELECT id FROM chain`,
    )
    .pluck()
    .all(id, workspaceId, workspaceId) as string[]
}

function alreadySent(db: DB, userId: string, ids: string[], dueDate: string, lead: number) {
  if (!ids.length) return false
  const row = db.$client
    .prepare(
      `SELECT 1 FROM reminders_sent WHERE user_id = ? AND due_date = ? AND lead_days = ? AND status = 'sent'
       AND memory_id IN (${ids.map(() => "?").join(",")}) LIMIT 1`,
    )
    .get(userId, dueDate, lead, ...ids)
  return !!row
}

// What a user should hear about today. Only workspaces they belong to.
export function dueRemindersFor(db: DB, userId: string, settings: Pick<ReminderSettings, "leadDays">, today: string): DueReminder[] {
  const leads = normalizeLeads(settings.leadDays)
  if (!leads.length) return []
  const horizon = addDays(today, Math.max(...leads))
  const out: DueReminder[] = []
  for (const ws of listMemberships(db, userId)) {
    for (const memory of listUpcoming(db, { workspaceId: ws.id, from: today, to: horizon, limit: 500 })) {
      const due = dueOf(memory)
      if (!due) continue
      const daysLeft = daysBetween(today, due.date)
      const lead = activeLead(daysLeft, leads)
      if (lead === null) continue
      if (alreadySent(db, userId, chainBack(db, ws.id, memory.id), due.date, lead)) continue
      out.push({ memory, workspaceId: ws.id, workspaceName: ws.name, due, daysLeft, lead })
    }
  }
  return out.sort((a, b) => a.daysLeft - b.daysLeft)
}

// --- the email ---------------------------------------------------------------------

function upcomingUrl() {
  return `${config().url}${APP_BASE}/upcoming`
}

export function reminderEmail(items: Pick<DueReminder, "memory" | "due" | "daysLeft" | "workspaceName">[], opts: { test?: boolean } = {}) {
  const when = (i: (typeof items)[number]) => `${formatDate(i.due)}, ${relativeDays(i.daysLeft)}`
  const where = (i: (typeof items)[number]) => `${i.workspaceName} / ${i.memory.container}`
  const first = items[0]
  const subject = opts.test
    ? "Recall test: reminders work"
    : items.length === 1 && first
      ? `Due ${relativeDays(first.daysLeft)}: ${first.memory.fact.length > 60 ? `${first.memory.fact.slice(0, 57)}...` : first.memory.fact}`
      : `${items.length} things due soon`
  const intro = opts.test
    ? items.length
      ? "This is a test from Recall. Real reminders look like this, with these facts that are coming up:"
      : "This is a test from Recall. Reminders will reach this address. Nothing is due soon right now."
    : items.length === 1
      ? "One fact in your memory is coming due:"
      : `${items.length} facts in your memory are coming due:`
  const lines = items.map((i) => `- ${i.memory.fact}\n  Due ${when(i)} (${where(i)})`)
  const text = [intro, "", ...lines, "", `See everything coming up: ${upcomingUrl()}`, "", "You get this because reminders are on in Recall. Turn them off under Settings, Reminders."].join("\n")
  const rows = items
    .map(
      (i) =>
        `<li style="margin:0 0 12px"><div>${escapeHtml(i.memory.fact)}</div><div style="color:#71717a;font-size:13px">Due ${escapeHtml(when(i))} · ${escapeHtml(where(i))}</div></li>`,
    )
    .join("")
  const html = `<div style="font-family:system-ui,sans-serif;font-size:15px;line-height:1.5;color:#18181b">
<p>${escapeHtml(intro)}</p>${rows ? `<ul style="padding-left:18px">${rows}</ul>` : ""}
<p><a href="${escapeHtml(upcomingUrl())}">See everything coming up</a></p>
<p style="color:#71717a;font-size:13px">You get this because reminders are on in Recall. Turn them off under Settings, Reminders.</p></div>`
  return { subject, text, html }
}

// --- the job -----------------------------------------------------------------------

export type ReminderDeps = { send: SendEmail; now: () => Date; configured: () => boolean }
export const defaultReminderDeps: ReminderDeps = { send: sendEmail, now: () => new Date(), configured: emailConfigured }

export type ReminderRun = { users: number; emails: number; reminders: number; failed: number; skipped?: "email_not_configured" }

function claim(db: DB, userId: string, items: DueReminder[]): number[] {
  return db.transaction(() =>
    items.map((i) => {
      db.insert(remindersSent)
        .values({ userId, workspaceId: i.workspaceId, memoryId: i.memory.id, dueDate: i.due.date, leadDays: i.lead })
        .onConflictDoNothing()
        .run()
      return db
        .select({ id: remindersSent.id })
        .from(remindersSent)
        .where(and(eq(remindersSent.userId, userId), eq(remindersSent.memoryId, i.memory.id), eq(remindersSent.dueDate, i.due.date), eq(remindersSent.leadDays, i.lead)))
        .get()!.id
    }),
  )
}

// Sends every due digest. Never throws: one user's failure is logged and the
// next run retries it, and the job always schedules the next run.
export async function runReminders(db: DB, deps: ReminderDeps = defaultReminderDeps, log: (m: string) => void = (m) => console.log(`[recall:reminders] ${m}`)): Promise<ReminderRun> {
  const run: ReminderRun = { users: 0, emails: 0, reminders: 0, failed: 0 }
  if (!deps.configured()) return { ...run, skipped: "email_not_configured" }
  const now = deps.now()
  const users = db
    .select({ userId: reminderSettings.userId })
    .from(reminderSettings)
    .innerJoin(user, eq(user.id, reminderSettings.userId))
    .where(and(eq(reminderSettings.enabled, true), sql`${reminderSettings.email} IS NOT NULL`))
    .all()
  for (const { userId } of users) {
    const settings = getReminderSettings(db, userId)
    if (!settings.email || hourIn(settings.timezone, now) < SEND_HOUR) continue
    run.users++
    try {
      const items = dueRemindersFor(db, userId, settings, todayIn(settings.timezone, now))
      if (!items.length) continue
      const ids = claim(db, userId, items)
      const key = createHash("sha256").update(`${userId}:${[...ids].sort((a, b) => a - b).join(",")}`).digest("hex").slice(0, 32)
      await deps.send({ to: settings.email, ...reminderEmail(items), idempotencyKey: `recall-reminders-${key}` })
      db.$client
        .prepare(`UPDATE reminders_sent SET status = 'sent', sent_at = ? WHERE id IN (${ids.map(() => "?").join(",")})`)
        .run(Date.now(), ...ids)
      run.emails++
      run.reminders += items.length
    } catch (err) {
      run.failed++
      log(`user ${userId}: ${err instanceof Error ? err.message : String(err)}`)
    }
  }
  if (run.emails || run.failed) log(`sent ${run.emails} email(s) with ${run.reminders} reminder(s), ${run.failed} failed`)
  return run
}

// One test email to the user's address with what's coming up. Records nothing.
export async function sendTestReminder(db: DB, userId: string, deps: ReminderDeps = defaultReminderDeps) {
  const settings = getReminderSettings(db, userId)
  if (!deps.configured()) throw new Error("Email isn't configured on this server.")
  if (!settings.email) throw new Error("Save an email address first.")
  const today = todayIn(settings.timezone, deps.now())
  const items = dueRemindersFor(db, userId, { leadDays: [Math.max(30, ...settings.leadDays)] }, today).slice(0, 10)
  const res = await deps.send({ to: settings.email, ...reminderEmail(items, { test: true }) })
  return { id: res.id, to: settings.email, items: items.length }
}

// --- scheduling --------------------------------------------------------------------

export function nextHour(now = Date.now()) {
  return Math.floor(now / 3_600_000) * 3_600_000 + 3_600_000
}

// Makes sure exactly one reminders job is queued. Called at boot and after each run.
export function scheduleReminders(db: DB, now = Date.now()) {
  const queued = db
    .select({ id: jobs.id })
    .from(jobs)
    .where(and(eq(jobs.type, "reminders"), eq(jobs.status, "queued")))
    .get()
  if (queued) return null
  return enqueue(db, SYSTEM_WORKSPACE, "reminders", {}, nextHour(now))
}
