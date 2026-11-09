import { z } from "zod"
import { parseDate, type Dated } from "../../lib/dates"

const fact = z.string().trim().min(1).max(500)
const keywords = z
  .union([z.string(), z.array(z.string())])
  .transform((k) => (Array.isArray(k) ? k.join(" ") : k).trim().slice(0, 500))
  .default("")

// Dated facts: optional "event_date" and "due_date" on add, update and conflict
// items ("2026-11-05", "2026-11" or "2026"). A missing or unreadable value is
// undefined (an update keeps the memory's date); null clears it.
const dateValue = z
  .unknown()
  .transform((v): Dated | null | undefined => (v === null ? null : typeof v === "string" ? (parseDate(v) ?? undefined) : undefined))
  .optional()
const dates = { event_date: dateValue, due_date: dateValue }

// The model sees memories numbered 1..n, never our ids, so a hallucinated id
// can't touch a row outside the set we sent. `ref` is that number.
export const extractResult = z.object({
  add: z
    .array(z.object({ fact, keywords, kind: z.enum(["static", "dynamic"]).catch("dynamic"), ...dates }))
    .default([]),
  update: z.array(z.object({ ref: z.coerce.number().int().positive(), fact, keywords, ...dates })).default([]),
  invalidate: z.array(z.coerce.number().int().positive()).default([]),
  // New content disagrees with memory `ref` and it isn't clear the new one
  // replaces it. Both are kept as current and flagged for a human.
  conflict: z
    .array(z.object({ ref: z.coerce.number().int().positive(), fact, keywords, note: z.string().default("").catch("").transform((s) => s.trim().slice(0, 300)), ...dates }))
    .default([]),
})
export type ExtractResult = z.infer<typeof extractResult>

export const profileResult = z.object({
  static: z.string().trim().max(4000).default(""),
  dynamic: z.string().trim().max(4000).default(""),
})
export type ProfileResult = z.infer<typeof profileResult>

export const askResult = z.object({
  answer: z.string().trim().min(1).max(6000),
  covered: z
    .union([z.boolean(), z.enum(["true", "false"])])
    .transform((v) => v === true || v === "true")
    .default(true),
})
export type AskResult = z.infer<typeof askResult>

// --- people ---------------------------------------------------------------------
// The "people" block of an extract answer, and the backfill answer. A bad
// entry is dropped on its own instead of failing the whole answer.

const optional = (max: number) =>
  z
    .string()
    .trim()
    .max(max)
    .optional()
    .catch(undefined)
    .transform((s) => s || undefined)

const personMention = z.object({
  name: z.string().trim().min(1).max(120),
  aliases: z.array(z.string().trim().min(1).max(120)).max(10).default([]).catch([]),
  role: optional(120),
  organization: optional(120),
  email: z.email().max(254).optional().catch(undefined),
  handle: optional(80),
  summary: optional(300),
  // Extract: "a1", "u2", "c1". Backfill: fact numbers.
  facts: z.array(z.union([z.string().trim(), z.number()]).transform(String)).max(100).default([]).catch([]),
})
export type PersonMention = z.infer<typeof personMention>

export const peopleBlock = z
  .array(z.unknown())
  .default([])
  .catch([])
  .transform((items) => items.flatMap((i) => {
    const r = personMention.safeParse(i)
    return r.success ? [r.data] : []
  }))

export const extractWithPeople = extractResult.extend({ people: peopleBlock })
export type ExtractWithPeople = z.infer<typeof extractWithPeople>

export const peopleBackfillResult = z.object({ people: peopleBlock })
