// Ask: a plain-language answer grounded in a container's memories, with
// citations. The HTTP request enqueues an `ask` job and waits for it, so the
// gateway call runs in the job loop like every other gateway call (one at a
// time) and asks jump ahead of background work.
import { z } from "zod"
import { dated, toDateString } from "../lib/dates"
import { ASK_TIMEOUT_MS } from "../lib/limits"
import { search } from "./api"
import { config } from "./config"
import type { DB } from "./db"
import { chatJsonWithModel, GatewayError, InvalidJsonError } from "./gateway/client"
import { ASK_SYSTEM, askPrompt } from "./gateway/prompts"
import { askResult } from "./gateway/schemas"
import { containerSchema, HttpError } from "./http"
import { cancelQueued, enqueue, type JobPayloads } from "./jobs/queue"
import { abortJob, JobWaitTimeout, waitForJob } from "./jobs/wait"
import { getProfile } from "./memories"
import { peopleNamedIn, personFacts, type PersonRef } from "./people"

export const ASK_MEMORIES = 12
export const ASK_CHUNKS = 6
export { ASK_TIMEOUT_MS }
// Facts per person named in the question, sent ahead of the search hits.
export const ASK_PERSON_FACTS = 12
const EXCERPT_CHARS = 1_500

export const askInput = z.object({
  container: containerSchema,
  question: z.string().trim().min(1).max(2000),
  include_chunks: z.boolean().optional(),
})
export type AskInput = z.infer<typeof askInput>

// A fact or excerpt the model was shown.
type Source =
  | { type: "memory"; id: string; text: string }
  | { type: "chunk"; id: string; text: string; documentId: string; documentTitle: string | null }

// `ref` is the footnote number: "[2]" in the answer is the citation with ref 2.
export type Citation = Source & { ref: number }

// What the job returns. ask() adds the container, question and total time.
// `people` are the people named in the question whose facts were used.
export type AskAnswer = { answer: string; covered: boolean; citations: Citation[]; model: string | null; people: PersonRef[] }
export type AskResponse = AskAnswer & { container: string; question: string; timingMs: number }

export const NOTHING_STORED = "Nothing is stored in this container yet, so there is nothing to answer from."

// --- citations ---------------------------------------------------------------

const BRACKET = /\[([^[\]\n]{1,80})\]/g
const REF = /^[mc]\d{1,3}$/i

// The model cites the numbered refs it was shown ([m3], [c1], [m1, m2]).
// Each known ref becomes a footnote number in order of first use, mapped to
// the real row it stood for. Refs the model made up are dropped from both the
// text and the list. Brackets that aren't refs ("[sic]") stay as they are.
export function resolveCitations(answer: string, sources: Map<string, Source>): { answer: string; citations: Citation[] } {
  const order = new Map<string, number>()
  const citations: Citation[] = []
  const text = answer.replace(BRACKET, (whole, inner: string) => {
    const tokens = inner.split(/[\s,;]+/).filter(Boolean)
    if (!tokens.length || !tokens.every((t) => REF.test(t))) return whole
    const marks: string[] = []
    for (const t of tokens) {
      const key = t.toLowerCase()
      const source = sources.get(key)
      if (!source) continue
      let n = order.get(key)
      if (n === undefined) {
        n = citations.length + 1
        order.set(key, n)
        citations.push({ ...source, ref: n })
      }
      if (!marks.includes(`[${n}]`)) marks.push(`[${n}]`)
    }
    return marks.join("")
  })
  const tidy = text
    .replace(/(\[\d+\])(?:\s*\1)+/g, "$1")
    .replace(/[ \t]+([.,;:!?])/g, "$1")
    .replace(/[ \t]{2,}/g, " ")
    .trim()
  return { answer: tidy, citations }
}

// --- the job -----------------------------------------------------------------

export type AskDeps = { chat: typeof chatJsonWithModel; now: () => Date }
export const defaultAskDeps: AskDeps = { chat: chatJsonWithModel, now: () => new Date() }

// `workspaceId` is the asking job's workspace; search and profile never leave it.
export async function answerQuestion(
  db: DB,
  workspaceId: string,
  input: JobPayloads["ask"],
  deps: AskDeps = defaultAskDeps,
  signal?: AbortSignal,
): Promise<AskAnswer> {
  const { results: memoryHits } = await search(db, workspaceId, { container: input.container, q: input.question, limit: ASK_MEMORIES })
  const chunkHits = input.includeChunks
    ? (await search(db, workspaceId, { container: input.container, q: input.question, limit: ASK_CHUNKS, include: ["chunks"] })).results
    : []
  // "What do I know about Clara?": the facts on Clara's person page come first,
  // whether or not search ranks them, and her card goes in as background.
  const named = peopleNamedIn(db, { workspaceId, container: input.container }, input.question)
  const personHits = named.flatMap((p) => personFacts(db, workspaceId, p.id, { limit: ASK_PERSON_FACTS }))
  if (memoryHits.length === 0 && chunkHits.length === 0 && personHits.length === 0) {
    return { answer: NOTHING_STORED, covered: false, citations: [], model: null, people: [] }
  }

  const sources = new Map<string, Source>()
  const seen = new Set<string>()
  const candidates = [
    ...personHits.map((f) => ({ ...f, text: f.fact })),
    ...memoryHits.flatMap((h) => (h.type === "memory" ? [h] : [])),
  ].filter((f) => !seen.has(f.id) && !!seen.add(f.id))
  const today = deps.now().toISOString().slice(0, 10)
  const facts = candidates.map((h, i) => {
    const ref = `m${i + 1}`
    sources.set(ref, { type: "memory", id: h.id, text: h.text })
    const happened = dated(h.eventDate, h.eventPrecision)
    const due = dated(h.dueDate, h.duePrecision)
    return {
      ref,
      text: h.text,
      kind: h.kind,
      date: new Date(h.createdAt).toISOString().slice(0, 10),
      ...(happened ? { happened: toDateString(happened) } : {}),
      // "2026-11" passes once the month is over, so compare at the date's own precision.
      ...(due ? { due: toDateString(due), duePassed: toDateString(due) < today.slice(0, toDateString(due).length) } : {}),
    }
  })
  const excerpts = chunkHits.flatMap((h, i) => {
    if (h.type !== "chunk") return []
    const ref = `c${i + 1}`
    sources.set(ref, { type: "chunk", id: h.id, text: h.text, documentId: h.documentId, documentTitle: h.documentTitle })
    const text = h.text.length > EXCERPT_CHARS ? `${h.text.slice(0, EXCERPT_CHARS)}...` : h.text
    return [{ ref, text, title: h.documentTitle }]
  })

  const profile = getProfile(db, workspaceId, input.container)
  const { data, model } = await deps.chat({
    system: ASK_SYSTEM,
    prompt: askPrompt({
      question: input.question,
      facts,
      excerpts,
      profile,
      now: deps.now(),
      people: named.map((p) => ({
        name: p.name,
        aliases: p.aliases,
        role: p.role,
        organization: p.organization,
        summary: p.summary,
        contact: [...p.emails, ...p.handles],
      })),
    }),
    schema: askResult,
    signal,
  })
  const { answer, citations } = resolveCitations(data.answer, sources)
  return { answer, covered: data.covered, citations, model, people: named.map((p) => ({ id: p.id, name: p.name })) }
}

// --- the request -------------------------------------------------------------

export async function ask(
  db: DB,
  workspaceId: string,
  input: AskInput,
  opts: { timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<AskResponse> {
  if (!config().jobsEnabled) throw new HttpError(503, "jobs_disabled", "The job loop is off on this server (RECALL_JOBS=off), so nothing can answer.")
  const timeoutMs = opts.timeoutMs ?? ASK_TIMEOUT_MS
  const started = performance.now()
  const id = enqueue(db, workspaceId, "ask", { container: input.container, question: input.question, includeChunks: input.include_chunks ?? false })!
  try {
    const result = await waitForJob<AskAnswer>(id, { timeoutMs, signal: opts.signal })
    return { container: input.container, question: input.question, ...result, timingMs: Math.round(performance.now() - started) }
  } catch (err) {
    if (err instanceof JobWaitTimeout) {
      // Still queued: drop it. Already running: stop its gateway call.
      if (!cancelQueued(db, id, "timed out waiting for a free job slot")) abortJob(id, "the caller timed out")
      throw new HttpError(
        504,
        "timeout",
        `No answer within ${Math.round(timeoutMs / 1000)} s. The gateway is slow or Recall is busy extracting a large document. Try again in a minute.`,
      )
    }
    if (opts.signal?.aborted) {
      if (!cancelQueued(db, id, "the caller went away")) abortJob(id, "the caller went away")
      throw err
    }
    if (err instanceof GatewayError) throw new HttpError(502, "gateway_error", `The gateway failed: ${err.message}`)
    if (err instanceof InvalidJsonError) throw new HttpError(502, "bad_answer", "The model's reply couldn't be read. Try asking again.")
    // fetch() itself failed: the gateway is down or unreachable.
    if (err instanceof TypeError && /fetch failed/i.test(err.message)) {
      throw new HttpError(502, "gateway_unreachable", "Couldn't reach the gateway. Check that it's running and GATEWAY_URL is right.")
    }
    throw err
  }
}
