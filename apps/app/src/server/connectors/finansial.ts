// Maps Finansial's changes feed to Recall documents. Only notable items become
// documents: recurring rules and loans (one per version of the fields that
// matter), and transactions at or above a per-currency threshold (one per
// transaction, replaced when it changes and removed when it goes). Each is one
// short plain-English document. Masking of keys and card numbers happens in
// ingest, like every other source.
import { createHash } from "node:crypto"
import type { ChangeItem } from "./changes-feed"

// Major units per currency code. A currency without an entry is skipped.
export const DEFAULT_THRESHOLDS: Record<string, number> = { IDR: 1_000_000 }

export type FinansialSettings = { thresholds: Record<string, number> }

export function finansialSettings(raw: Record<string, unknown> | null | undefined): FinansialSettings {
  const t = raw?.thresholds
  if (!t || typeof t !== "object" || Array.isArray(t)) return { thresholds: { ...DEFAULT_THRESHOLDS } }
  const thresholds: Record<string, number> = {}
  for (const [code, value] of Object.entries(t)) {
    if (/^[A-Z]{3}$/.test(code) && typeof value === "number" && Number.isFinite(value) && value >= 0) thresholds[code] = value
  }
  return { thresholds }
}

// fingerprint: transactions only. A hash of what the document says, stored in
// its metadata, so an edit that changes nothing in it costs nothing.
export type FinansialDoc = { sourceId: string; title: string; content: string; documentDate: string | null; fingerprint?: string }

// --- field readers ---------------------------------------------------------------

const str = (v: unknown) => (typeof v === "string" ? v.trim() : typeof v === "number" ? String(v) : "")
const day = (v: unknown) => {
  const s = str(v)
  return /^\d{4}-\d{2}-\d{2}/.test(s) ? s.slice(0, 10) : null
}
function int(v: unknown): number | null {
  const n = typeof v === "number" ? v : typeof v === "string" && /^-?\d+$/.test(v.trim()) ? Number(v) : NaN
  return Number.isSafeInteger(n) ? n : null
}
function digitsOf(v: unknown) {
  const n = int(v)
  return n !== null && n >= 0 && n <= 6 ? n : 2
}
const currencyOf = (v: unknown) => str(v).toUpperCase()
const tagsOf = (v: unknown) => (Array.isArray(v) ? v.map(str).filter(Boolean) : [])

// sha256 of the values in a fixed order, so the same content always hashes the
// same no matter how the feed ordered its keys.
export function contentHash(values: Record<string, unknown>, length = 64): string {
  const ordered = Object.keys(values)
    .sort()
    .map((k) => [k, values[k] ?? null])
  return createHash("sha256").update(JSON.stringify(ordered)).digest("hex").slice(0, length)
}

// "IDR 1,500,000" or "USD 1,234.50". Integer math, so no float rounding.
export function formatMoney(amountMinor: number, currency: string, minorUnitDigits: number): string {
  const digits = Math.max(0, Math.min(6, Math.trunc(minorUnitDigits)))
  const negative = amountMinor < 0
  const abs = Math.abs(Math.trunc(amountMinor))
  const scale = 10 ** digits
  const major = Math.floor(abs / scale)
  const minor = abs % scale
  const grouped = major.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",")
  const fraction = digits ? `.${minor.toString().padStart(digits, "0")}` : ""
  return `${negative ? "-" : ""}${currency || "?"} ${grouped}${fraction}`
}

// Whether a transaction amount reaches the configured threshold for its currency.
export function meetsThreshold(amountMinor: number, currency: string, minorUnitDigits: number, settings: FinansialSettings) {
  const threshold = settings.thresholds[currency]
  if (threshold === undefined) return false
  return Math.abs(amountMinor) >= Math.round(threshold * 10 ** minorUnitDigits)
}

const sentence = (parts: (string | false | null | undefined)[]) => parts.filter(Boolean).join(" ")
const linkLine = (url: string) => (url ? `\nLink: ${url}` : "")
const asOf = (item: ChangeItem) => item.updatedAt.slice(0, 10)

// --- per kind --------------------------------------------------------------------

// One document per transaction, whatever its version: an edit replaces it and a
// delete removes it (see apps.ts).
export const transactionSourceId = (id: string) => `fin:txn:${id}`

function transactionDoc(item: ChangeItem, settings: FinansialSettings): FinansialDoc | null {
  if (item.deleted) return null
  const d = item.data
  const amount = int(d.amountMinor)
  const currency = currencyOf(d.currency)
  const digits = digitsOf(d.minorUnitDigits)
  if (amount === null || !currency || !meetsThreshold(amount, currency, digits, settings)) return null
  const money = formatMoney(Math.abs(amount), currency, digits)
  const kind = str(d.kind) || "transaction"
  const date = day(d.date) ?? asOf(item)
  const payee = str(d.payee)
  const account = str(d.account)
  const category = str(d.category)
  const note = str(d.note)
  const tags = tagsOf(d.tags)
  const what =
    kind === "income"
      ? sentence([`an income of ${money}`, payee && `from ${payee}`, account && `into the account ${account}`])
      : kind === "transfer"
        ? sentence([`a transfer of ${money}`, account && `from the account ${account}`, payee && `to ${payee}`])
        : sentence([`an expense of ${money}`, payee && `paid to ${payee}`, account && `from the account ${account}`])
  const lines = [
    `Finansial transaction on ${date}: ${what}.`,
    category && `Category: ${category}.`,
    note && `Note: ${note}`,
    tags.length > 0 && `Tags: ${tags.join(", ")}.`,
  ].filter(Boolean)
  const label = kind === "income" ? "Income" : kind === "transfer" ? "Transfer" : "Expense"
  return {
    sourceId: transactionSourceId(item.id),
    title: `${label} of ${money}${payee ? ` ${kind === "income" ? "from" : "to"} ${payee}` : ""} on ${date}`,
    content: lines.join("\n") + linkLine(item.url),
    documentDate: date,
    fingerprint: contentHash({ date, kind, amountMinor: amount, currency, minorUnitDigits: digits, account, payee, category, note, tags: [...tags].sort() }),
  }
}

function ruleActive(v: unknown): string {
  if (v === true || v === "active") return "It is active."
  if (v === false) return "It is no longer active."
  if (v === "paused") return "It is paused."
  if (v === "ended") return "It has ended."
  return ""
}

function activeOf(v: unknown): boolean | string | null {
  return typeof v === "boolean" ? v : str(v) || null
}

// A rule's version: Finansial bumps updated_at every time the rule posts (its
// next due date moves on), so the version is a hash of the fields that matter
// instead. A rule posting every month stays one document.
export function ruleVersion(item: ChangeItem): string {
  if (item.deleted) return "deleted"
  const d = item.data
  return contentHash(
    {
      name: str(d.name),
      kind: str(d.kind),
      amountMinor: int(d.amountMinor),
      currency: currencyOf(d.currency),
      minorUnitDigits: digitsOf(d.minorUnitDigits),
      frequency: str(d.frequency),
      payee: str(d.payee),
      category: str(d.category),
      active: activeOf(d.active ?? d.status),
    },
    16,
  )
}

function ruleDoc(item: ChangeItem): FinansialDoc {
  const d = item.data
  const name = str(d.name) || `rule ${item.id}`
  const at = asOf(item)
  const sourceId = `fin:rule:${item.id}:${ruleVersion(item)}`
  if (item.deleted) {
    return {
      sourceId,
      title: `Recurring rule removed: ${name}`,
      content: `Finansial recurring rule "${name}" was deleted on ${at}. It no longer applies.` + linkLine(item.url),
      documentDate: at,
    }
  }
  const amount = int(d.amountMinor)
  const currency = currencyOf(d.currency)
  const money = amount !== null && currency ? formatMoney(Math.abs(amount), currency, digitsOf(d.minorUnitDigits)) : null
  const kind = str(d.kind) || "recurring"
  const frequency = str(d.frequency)
  const payee = str(d.payee)
  const category = str(d.category)
  // No next due date: it moves on every posting while the source_id stays the
  // same, so a stored date would go stale and fire reminders on an old day.
  const lines = [
    `Finansial recurring rule "${name}", as of ${at}: ${sentence([`a ${frequency || "recurring"} ${kind}`, money && `of ${money}`, payee && (kind === "income" ? `from ${payee}` : `to ${payee}`)])}.`,
    category && `Category: ${category}.`,
    ruleActive(d.active ?? d.status),
  ].filter(Boolean)
  return {
    sourceId,
    title: `Recurring ${kind}: ${name}${money ? `, ${money}${frequency ? ` ${frequency}` : ""}` : ""}`,
    content: lines.join("\n") + linkLine(item.url),
    documentDate: at,
  }
}

// A loan's version, hashed the same way as a rule's.
export function loanVersion(item: ChangeItem): string {
  if (item.deleted) return "deleted"
  const d = item.data
  return contentHash(
    {
      name: str(d.name),
      principalMinor: int(d.principalMinor),
      currency: currencyOf(d.currency),
      minorUnitDigits: digitsOf(d.minorUnitDigits),
      lender: str(d.lender),
      status: str(d.status),
    },
    16,
  )
}

function loanDoc(item: ChangeItem): FinansialDoc {
  const d = item.data
  const name = str(d.name) || `loan ${item.id}`
  const at = asOf(item)
  const sourceId = `fin:loan:${item.id}:${loanVersion(item)}`
  if (item.deleted) {
    return {
      sourceId,
      title: `Loan removed: ${name}`,
      content: `Finansial loan "${name}" was deleted on ${at}.` + linkLine(item.url),
      documentDate: at,
    }
  }
  const principal = int(d.principalMinor)
  const currency = currencyOf(d.currency)
  const money = principal !== null && currency ? formatMoney(Math.abs(principal), currency, digitsOf(d.minorUnitDigits)) : null
  const lender = str(d.lender)
  const status = str(d.status)
  const lines = [
    `Finansial loan "${name}", as of ${at}: ${sentence([money ? `a principal of ${money}` : "a loan", lender && `from ${lender}`])}.`,
    status && `Status: ${status}.`,
  ].filter(Boolean)
  return {
    sourceId,
    title: `Loan: ${name}${money ? `, ${money}` : ""}`,
    content: lines.join("\n") + linkLine(item.url),
    documentDate: at,
  }
}

// The document for one feed item, or null when it isn't notable.
export function finansialDocument(item: ChangeItem, settings: FinansialSettings): FinansialDoc | null {
  if (item.kind === "transaction") return transactionDoc(item, settings)
  if (item.kind === "recurring_rule") return ruleDoc(item)
  if (item.kind === "loan") return loanDoc(item)
  return null
}

// The date a transaction happened, for the backfill window. Null for other kinds.
export function finansialEventDate(item: ChangeItem): string | null {
  return item.kind === "transaction" ? day(item.data.date) : null
}
