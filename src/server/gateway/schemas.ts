import { z } from "zod"

const fact = z.string().trim().min(1).max(500)
const keywords = z
  .union([z.string(), z.array(z.string())])
  .transform((k) => (Array.isArray(k) ? k.join(" ") : k).trim().slice(0, 500))
  .default("")

// The model sees memories numbered 1..n, never our ids, so a hallucinated id
// can't touch a row outside the set we sent. `ref` is that number.
export const extractResult = z.object({
  add: z
    .array(z.object({ fact, keywords, kind: z.enum(["static", "dynamic"]).catch("dynamic") }))
    .default([]),
  update: z.array(z.object({ ref: z.coerce.number().int().positive(), fact, keywords })).default([]),
  invalidate: z.array(z.coerce.number().int().positive()).default([]),
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
