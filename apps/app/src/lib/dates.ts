// Calendar dates on facts: when something happened (event) and when it ends or
// is due (due). Pure helpers, shared by the server and the dashboard.
//
// A date is stored as a full ISO date plus a precision. "2027-12" becomes
// { date: "2027-12-01", precision: "month" } and "2027" becomes
// { date: "2027-01-01", precision: "year" }. Counting from the first day of the
// period makes a reminder early rather than late.

export type DatePrecision = "day" | "month" | "year"
export type Dated = { date: string; precision: DatePrecision }

const MIN_YEAR = 1900
const MAX_YEAR = 2200

function validDay(y: number, m: number, d: number) {
  const t = new Date(Date.UTC(y, m - 1, d))
  return t.getUTCFullYear() === y && t.getUTCMonth() === m - 1 && t.getUTCDate() === d
}

// "2026-11-05", "2026-11" or "2026", optionally with a time part that is
// ignored ("2026-11-05T10:00:00Z" is 2026-11-05). Anything else is null,
// including impossible dates like 2026-02-30.
export function parseDate(input: string | null | undefined): Dated | null {
  if (typeof input !== "string") return null
  const s = input.trim()
  const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2})(?:[T ][\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)?)?)?$/.exec(s)
  if (!m) return null
  const y = Number(m[1])
  if (y < MIN_YEAR || y > MAX_YEAR) return null
  if (m[2] === undefined) return { date: `${m[1]}-01-01`, precision: "year" }
  const mo = Number(m[2])
  if (mo < 1 || mo > 12) return null
  if (m[3] === undefined) return { date: `${m[1]}-${m[2]}-01`, precision: "month" }
  if (!validDay(y, mo, Number(m[3]))) return null
  return { date: `${m[1]}-${m[2]}-${m[3]}`, precision: "day" }
}

// Back to the short form the extractor and the API use: "2027-12" for a month.
export function toDateString(d: Dated): string {
  if (d.precision === "year") return d.date.slice(0, 4)
  if (d.precision === "month") return d.date.slice(0, 7)
  return d.date
}

const RANK: Record<DatePrecision, number> = { day: 0, month: 1, year: 2 }

// Reads the two stored columns. The date comes as a full date or in short form
// ("2027-01" with precision month). Null when either is missing or broken.
export function dated(date: string | null | undefined, precision: string | null | undefined): Dated | null {
  if (precision !== "day" && precision !== "month" && precision !== "year") return null
  const d = parseDate(date)
  if (!d) return null
  return { date: d.date, precision: RANK[d.precision] > RANK[precision] ? d.precision : precision }
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

// "Nov 5, 2026", "Dec 2027" or "2027". Calendar dates have no time zone, so
// this formats the parts directly instead of going through Date.
export function formatDate(d: Dated): string {
  const [y, m, day] = d.date.split("-")
  if (d.precision === "year") return y!
  const month = MONTHS[Number(m) - 1]
  if (d.precision === "month") return `${month} ${y}`
  return `${month} ${Number(day)}, ${y}`
}

function utc(iso: string) {
  const [y, m, d] = iso.split("-").map(Number)
  return Date.UTC(y!, m! - 1, d!)
}

export function addDays(iso: string, days: number): string {
  return new Date(utc(iso) + days * 86_400_000).toISOString().slice(0, 10)
}

// Whole days from a to b (b later is positive).
export function daysBetween(a: string, b: string): number {
  return Math.round((utc(b) - utc(a)) / 86_400_000)
}

export function isTimezone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat("en", { timeZone: tz })
    return true
  } catch {
    return false
  }
}

// Today's calendar date in a time zone, as YYYY-MM-DD.
export function todayIn(timezone: string, now: Date | number = Date.now()): string {
  const tz = isTimezone(timezone) ? timezone : "UTC"
  return new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(now)
}

// The hour (0 to 23) in a time zone.
export function hourIn(timezone: string, now: Date | number = Date.now()): number {
  const tz = isTimezone(timezone) ? timezone : "UTC"
  return Number(new Intl.DateTimeFormat("en-US", { timeZone: tz, hour: "numeric", hourCycle: "h23" }).format(now))
}

// For due dates: "today", "tomorrow", "in 12 days", "1 day overdue", "3 days overdue".
export function relativeDays(days: number): string {
  if (days === 0) return "today"
  if (days === 1) return "tomorrow"
  if (days > 0) return `in ${days} days`
  return `${-days} ${days === -1 ? "day" : "days"} overdue`
}
