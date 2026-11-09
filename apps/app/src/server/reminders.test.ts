// Reminders: which lead time a fact is in, once-only sending, retries,
// scheduling in the job loop, tenant isolation, and the Resend sender.
import { eq } from "drizzle-orm"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { openDb, type DB } from "./db"
import { jobs, remindersSent } from "./db/schema"
import { createResendSender, emailConfigured, type EmailMessage } from "./email"
import { JobRunner } from "./jobs/runner"
import { addMemoryDirect, editMemory, forgetMemory } from "./memories"
import { createHandlers } from "./pipeline"
import {
  activeLead,
  dueRemindersFor,
  getReminderSettings,
  nextHour,
  reminderEmail,
  runReminders,
  saveReminderSettings,
  scheduleReminders,
  sendTestReminder,
  SYSTEM_WORKSPACE,
  type ReminderDeps,
} from "./reminders"
import { addMember, createUserWithPassword, createWorkspace } from "./users"

let db: DB
let now: Date
const env = { ...process.env }
beforeEach(() => {
  db = openDb(":memory:")
  // 09:00 in Makassar (UTC+8) on 2026-11-10.
  now = new Date(Date.UTC(2026, 10, 10, 1, 0))
})
afterEach(() => {
  process.env = { ...env }
})

function mailer(fail = false) {
  const sent: EmailMessage[] = []
  const send = vi.fn(async (msg: EmailMessage) => {
    if (fail) throw new Error("Resend answered 500: boom")
    sent.push(msg)
    return { id: `email_${sent.length}` }
  })
  const deps: ReminderDeps = { send, now: () => now, configured: () => true }
  return { sent, send, deps }
}

const day = (date: string) => ({ date, precision: "day" as const })

async function setup() {
  const alice = await createUserWithPassword(db, { username: "alice", password: "alice-password-123" })
  const bob = await createUserWithPassword(db, { username: "bob", password: "bob-password-1234" })
  const alpha = createWorkspace(db, { name: "Alpha", id: "ws_alpha", slug: "alpha" })
  const beta = createWorkspace(db, { name: "Beta", id: "ws_beta", slug: "beta" })
  addMember(db, { workspaceId: alpha.id, userId: alice.id, role: "owner" })
  addMember(db, { workspaceId: beta.id, userId: bob.id, role: "owner" })
  const insurance = await addMemoryDirect(db, { workspaceId: alpha.id, container: "me", fact: "Car insurance renewal", keywords: "car insurance", kind: "dynamic", due: day("2026-11-30") })
  const tax = await addMemoryDirect(db, { workspaceId: alpha.id, container: "work", fact: "Tax report due", keywords: "tax", kind: "dynamic", due: day("2026-11-11") })
  await addMemoryDirect(db, { workspaceId: alpha.id, container: "me", fact: "Far away renewal", keywords: "far", kind: "dynamic", due: day("2027-06-01") })
  await addMemoryDirect(db, { workspaceId: alpha.id, container: "me", fact: "Expired gym", keywords: "gym", kind: "dynamic", due: day("2026-11-01") })
  const lease = await addMemoryDirect(db, { workspaceId: beta.id, container: "me", fact: "Beta office lease ends", keywords: "lease", kind: "dynamic", due: day("2026-11-12") })
  return { alice, bob, alpha, beta, insurance, tax, lease }
}

const enable = (userId: string, email: string, leadDays = [30, 1]) =>
  saveReminderSettings(db, userId, { enabled: true, email, leadDays, timezone: "Asia/Makassar" })

describe("lead times", () => {
  it("picks the smallest lead that covers the days left", () => {
    expect(activeLead(40, [30, 1])).toBeNull()
    expect(activeLead(30, [30, 1])).toBe(30)
    expect(activeLead(20, [30, 1])).toBe(30)
    expect(activeLead(1, [30, 1])).toBe(1)
    expect(activeLead(0, [30, 1])).toBe(1)
    expect(activeLead(0, [30, 1, 0])).toBe(0)
    expect(activeLead(-1, [30, 1])).toBeNull()
  })

  it("settings default off, and clean up lead days and time zone", async () => {
    const { alice } = await setup()
    expect(getReminderSettings(db, alice.id)).toMatchObject({ enabled: false, email: null, leadDays: [30, 1] })
    const saved = saveReminderSettings(db, alice.id, { enabled: true, email: " a@example.com ", leadDays: [1, 30, 30, -2, 400, 7.5, 0], timezone: "Nope/Zone" })
    expect(saved).toMatchObject({ enabled: true, email: "a@example.com", leadDays: [30, 1, 0], timezone: "UTC" })
  })
})

describe("runReminders", () => {
  it("sends one digest per user with what is due, then never the same reminder twice", async () => {
    const { alice } = await setup()
    enable(alice.id, "alice@example.com")
    const { sent, deps } = mailer()

    const first = await runReminders(db, deps, () => {})
    expect(first).toMatchObject({ users: 1, emails: 1, reminders: 2, failed: 0 })
    expect(sent[0]!.to).toBe("alice@example.com")
    expect(sent[0]!.subject).toBe("2 things due soon")
    expect(sent[0]!.text).toContain("Tax report due\n  Due Nov 11, 2026, tomorrow (Alpha / work)")
    expect(sent[0]!.text).toContain("Car insurance renewal\n  Due Nov 30, 2026, in 20 days (Alpha / me)")
    expect(sent[0]!.text).not.toContain("Far away")
    expect(sent[0]!.text).not.toContain("Expired gym")
    expect(sent[0]!.idempotencyKey).toMatch(/^recall-reminders-[0-9a-f]{32}$/)

    // Same day, next hour: nothing new.
    now = new Date(now.getTime() + 3_600_000)
    expect(await runReminders(db, deps, () => {})).toMatchObject({ emails: 0 })
    // Next day: insurance is still in its 30-day reminder (sent); the tax report
    // is due today but its 1-day reminder went out yesterday.
    now = new Date(now.getTime() + 86_400_000)
    expect(await runReminders(db, deps, () => {})).toMatchObject({ emails: 0 })
    // The day before the insurance is due, its 1-day reminder goes out.
    now = new Date(Date.UTC(2026, 10, 29, 1))
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(2)
    expect(sent[1]!.subject).toBe("Due tomorrow: Car insurance renewal")
    expect(db.select().from(remindersSent).all().map((r) => [r.leadDays, r.status])).toEqual([
      [1, "sent"],
      [30, "sent"],
      [1, "sent"],
    ])
  })

  it("waits until 08:00 in the user's time zone", async () => {
    const { alice } = await setup()
    enable(alice.id, "alice@example.com")
    const { sent, deps } = mailer()
    now = new Date(Date.UTC(2026, 10, 9, 23, 30)) // 07:30 in Makassar
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(0)
    now = new Date(Date.UTC(2026, 10, 10, 0, 0)) // 08:00
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(1)
  })

  it("a failed send stays pending and goes out on the next run, with the same idempotency key", async () => {
    const { alice } = await setup()
    enable(alice.id, "alice@example.com")
    const broken = mailer(true)
    const log: string[] = []
    expect(await runReminders(db, broken.deps, (m) => log.push(m))).toMatchObject({ emails: 0, failed: 1 })
    expect(log[0]).toContain("Resend answered 500")
    expect(db.select().from(remindersSent).all().every((r) => r.status === "pending")).toBe(true)
    const key = broken.send.mock.calls[0]![0].idempotencyKey

    const ok = mailer()
    now = new Date(now.getTime() + 3_600_000)
    expect(await runReminders(db, ok.deps, () => {})).toMatchObject({ emails: 1, reminders: 2 })
    expect(ok.sent[0]!.idempotencyKey).toBe(key)
    expect(db.select().from(remindersSent).all()).toHaveLength(2)
    expect(db.select().from(remindersSent).all().every((r) => r.status === "sent" && r.sentAt)).toBe(true)
  })

  it("editing a fact doesn't resend its reminder; moving its due date does", async () => {
    const { alice, insurance } = await setup()
    enable(alice.id, "alice@example.com")
    const { sent, deps } = mailer()
    await runReminders(db, deps, () => {})
    const v2 = await editMemory(db, "ws_alpha", insurance.id, { fact: "Car insurance with Astra renewal" })
    now = new Date(now.getTime() + 3_600_000)
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(1)
    await editMemory(db, "ws_alpha", v2!.id, { due: day("2026-11-25") })
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(2)
    expect(sent[1]!.text).toContain("Due Nov 25, 2026")
  })

  it("skips deleted facts, disabled users and users without an email", async () => {
    const { alice, bob, tax, insurance } = await setup()
    saveReminderSettings(db, alice.id, { enabled: false, email: "alice@example.com", leadDays: [30, 1], timezone: "UTC" })
    saveReminderSettings(db, bob.id, { enabled: true, email: null, leadDays: [30, 1], timezone: "UTC" })
    const { sent, deps } = mailer()
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(0)
    forgetMemory(db, "ws_alpha", tax.id)
    forgetMemory(db, "ws_alpha", insurance.id)
    enable(alice.id, "alice@example.com")
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(0)
  })

  it("does nothing without email configured", async () => {
    const { alice } = await setup()
    enable(alice.id, "alice@example.com")
    const { sent, deps } = mailer()
    expect(await runReminders(db, { ...deps, configured: () => false }, () => {})).toMatchObject({ skipped: "email_not_configured", emails: 0 })
    expect(sent).toHaveLength(0)
    expect(db.select().from(remindersSent).all()).toHaveLength(0)
  })
})

describe("tenant isolation", () => {
  it("a user only hears about workspaces they belong to", async () => {
    const { alice, bob, beta } = await setup()
    enable(alice.id, "alice@example.com")
    enable(bob.id, "bob@example.com")
    const { sent, deps } = mailer()
    await runReminders(db, deps, () => {})
    const toAlice = sent.find((m) => m.to === "alice@example.com")!
    const toBob = sent.find((m) => m.to === "bob@example.com")!
    expect(toAlice.text).not.toContain("Beta")
    expect(toBob.text).toContain("Beta office lease ends")
    expect(toBob.text).not.toMatch(/Car insurance|Tax report|Alpha/)
    expect(dueRemindersFor(db, alice.id, { leadDays: [365] }, "2026-11-10").every((r) => r.workspaceId === "ws_alpha")).toBe(true)

    // Joining beta adds its facts to Alice's next digest.
    addMember(db, { workspaceId: beta.id, userId: alice.id, role: "member" })
    now = new Date(now.getTime() + 3_600_000)
    await runReminders(db, deps, () => {})
    expect(sent.at(-1)).toMatchObject({ to: "alice@example.com" })
    expect(sent.at(-1)!.text).toContain("Beta office lease ends\n  Due Nov 12, 2026, in 2 days (Beta / me)")
    // Bob's beta reminder is his own; Alice getting it doesn't stop or repeat his.
    expect(db.select().from(remindersSent).where(eq(remindersSent.userId, bob.id)).all()).toHaveLength(1)
  })
})

describe("test email", () => {
  it("sends to the saved address, records nothing, and explains what's missing", async () => {
    const { alice } = await setup()
    const { sent, deps } = mailer()
    await expect(sendTestReminder(db, alice.id, deps)).rejects.toThrow("Save an email address first.")
    enable(alice.id, "alice@example.com")
    await expect(sendTestReminder(db, alice.id, { ...deps, configured: () => false })).rejects.toThrow("Email isn't configured on this server.")
    const res = await sendTestReminder(db, alice.id, deps)
    expect(res).toMatchObject({ to: "alice@example.com", items: 2 })
    expect(sent[0]!.subject).toBe("Recall test: reminders work")
    expect(db.select().from(remindersSent).all()).toHaveLength(0)
    // The real run still sends afterwards.
    await runReminders(db, deps, () => {})
    expect(sent).toHaveLength(2)
  })

  it("escapes fact text in the HTML part", () => {
    const { html } = reminderEmail([
      { memory: { fact: "<script>x</script> & co", container: "me" } as never, due: day("2026-11-11"), daysLeft: 1, workspaceName: "A" },
    ])
    expect(html).toContain("&lt;script&gt;x&lt;/script&gt; &amp; co")
    expect(html).not.toContain("<script>")
  })
})

describe("scheduling in the job loop", () => {
  it("keeps exactly one reminders job queued, at the next top of the hour", async () => {
    const t = Date.UTC(2026, 10, 10, 1, 17)
    const id = scheduleReminders(db, t)
    expect(id).not.toBeNull()
    expect(scheduleReminders(db, t)).toBeNull()
    const job = db.select().from(jobs).where(eq(jobs.type, "reminders")).get()!
    expect(job).toMatchObject({ workspaceId: SYSTEM_WORKSPACE, runAfter: Date.UTC(2026, 10, 10, 2, 0) })
    expect(nextHour(Date.UTC(2026, 10, 10, 2, 0))).toBe(Date.UTC(2026, 10, 10, 3, 0))
  })

  it("the job sends, then queues the next run, even when sending fails", async () => {
    const { alice } = await setup()
    enable(alice.id, "alice@example.com")
    const { sent, deps } = mailer()
    const runner = new JobRunner(db, createHandlers(db, undefined, undefined, deps), () => {})
    // Only the reminders job: the profile rebuilds setup() queued would call the gateway.
    db.delete(jobs).run()
    scheduleReminders(db, now.getTime() - 3_600_000)
    await runner.runOnce(now.getTime())
    expect(sent).toHaveLength(1)
    const queued = db.select().from(jobs).where(eq(jobs.status, "queued")).all()
    expect(queued).toHaveLength(1)
    expect(queued[0]!.type).toBe("reminders")

    const broken = mailer(true)
    const runner2 = new JobRunner(db, createHandlers(db, undefined, undefined, broken.deps), () => {})
    db.update(jobs).set({ runAfter: 0 }).where(eq(jobs.status, "queued")).run()
    await runner2.runOnce()
    expect(db.select().from(jobs).where(eq(jobs.status, "queued")).all()).toHaveLength(1)
    expect(db.select().from(jobs).where(eq(jobs.status, "failed")).all()).toHaveLength(0)
  })
})

describe("Resend sender", () => {
  it("posts the message with the key and idempotency key", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ id: "re_123" }), { status: 200 }))
    const send = createResendSender({ apiKey: "re_test", from: "Recall <reminders@example.com>", fetch: fetch as unknown as typeof globalThis.fetch })
    expect(await send({ to: "a@example.com", subject: "s", text: "t", html: "<p>t</p>", idempotencyKey: "k1" })).toEqual({ id: "re_123" })
    const [url, init] = fetch.mock.calls[0]! as unknown as [string, RequestInit]
    expect(url).toBe("https://api.resend.com/emails")
    expect(init.headers).toMatchObject({ Authorization: "Bearer re_test", "Idempotency-Key": "k1" })
    expect(JSON.parse(init.body as string)).toEqual({ from: "Recall <reminders@example.com>", to: ["a@example.com"], subject: "s", text: "t", html: "<p>t</p>" })
  })

  it("throws Resend's message on an error", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ name: "validation_error", message: "The domain is not verified" }), { status: 403 }))
    const send = createResendSender({ apiKey: "re_test", from: "x@example.com", fetch: fetch as unknown as typeof globalThis.fetch })
    await expect(send({ to: "a@example.com", subject: "s", text: "t" })).rejects.toThrow("Resend answered 403: The domain is not verified")
  })

  it("is configured only with both env keys", () => {
    delete process.env.RESEND_API_KEY
    delete process.env.RECALL_EMAIL_FROM
    expect(emailConfigured()).toBe(false)
    process.env.RESEND_API_KEY = "re_x"
    expect(emailConfigured()).toBe(false)
    process.env.RECALL_EMAIL_FROM = "Recall <reminders@example.com>"
    expect(emailConfigured()).toBe(true)
  })
})
