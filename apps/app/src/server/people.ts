// People: the contacts named in a container's facts (colleagues, clients,
// family), each linked to the facts that mention them. Every read and write is
// scoped to one workspace, and people never cross containers.
import { and, eq, inArray, isNull } from "drizzle-orm"
import { config } from "./config"
import type { DB } from "./db"
import { documents, memories, people, peopleScans, personMemories, type Memory, type Person } from "./db/schema"
import { embed } from "./embed"
import { chatJson } from "./gateway/client"
import { peopleBackfillPrompt, peopleBackfillSystem } from "./gateway/prompts"
import { peopleBackfillResult, type PersonMention } from "./gateway/schemas"
import { newId } from "./ids"
import type { Scope } from "./search"
import { sensitiveKinds } from "./sensitive"

// A fuzzy name match ("Clara" for "Clara Simmons", "Budi Santosa" for "Budi
// Santoso") only counts when the mention and the person read alike. Measured on
// e5-small passage vectors of "name. role org. summary. facts": the same person
// scored 0.86 to 0.89, a different person with the same first name 0.82 to 0.84.
export const FUZZY_PERSON_SIMILARITY = 0.855
const MAX_ALIASES = 20
const MAX_CONTACTS = 10
const DESCRIPTOR_FACTS = 3

// --- names --------------------------------------------------------------------

// Case, accents and punctuation don't make a different name.
export function nameKey(s: string) {
  return s
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim()
}

const tokens = (s: string) => nameKey(s).split(" ").filter(Boolean)
const handleKey = (h: string) => h.trim().replace(/^@+/, "").toLowerCase()
const emailKey = (e: string) => e.trim().toLowerCase()

// Words that point at the subject, never at a contact.
const SELF = new Set(["i", "me", "myself", "user", "the user", "subject", "the subject", "you", "he", "she", "they", "him", "her", "them", "someone", "unknown"])

// True when the mention is the container's own subject: facts about "Zaki" in
// container "zaki" (or "zaki-work") are about the subject, not a contact.
// `ownerNames` are other names for the subject (see subjectNames).
export function isContainerOwner(container: string, mention: Pick<PersonMention, "name" | "aliases" | "email" | "handle">, ownerNames: string[] = []) {
  const parts = new Set(tokens(container).filter((t) => t.length >= 3))
  const whole = nameKey(container)
  const owners = new Set(ownerNames.map(nameKey).filter(Boolean))
  const names = [mention.name, ...(mention.aliases ?? [])]
  for (const n of names) {
    const key = nameKey(n)
    if (!key || SELF.has(key) || key === whole || owners.has(key)) return true
    const first = tokens(n)[0]
    if (first && parts.has(first)) return true
  }
  if (mention.handle && (handleKey(mention.handle) === whole || parts.has(handleKey(mention.handle)))) return true
  const local = mention.email?.split("@")[0]
  return !!local && (nameKey(local) === whole || parts.has(nameKey(local)))
}

// Words that make a name a team, company or mailbox rather than a person.
// Left out on purpose: real surnames like Sales, Kantor or Billing, and "tim"
// (Indonesian for team, but also Tim).
const ORG_WORDS = new Set(
  (
    "team support info admin administrator helpdesk newsletter noreply notifications customer service services office department dept " +
    "committee foundation inc ltd llc corp corporation company group official hq staff community institute university association " +
    "ministry agency sekret sekretariat secretariat panitia humas redaksi yayasan kementerian lembaga koperasi komunitas divisi layanan pt cv tbk"
  ).split(" "),
)

// Local parts of team mailboxes. Long words match as a prefix
// ("sekretwargalapor"), short ones only as a whole word ("info.vorneo").
const MAILBOX_PREFIX = /^(sekret|secretariat|support|no-?reply|do-?not-?reply|newsletter|notification|helpdesk|redaksi|panitia|layanan|postmaster|webmaster|mailer-daemon)/
const MAILBOX_WORD = /^(info|admin|hello|halo|team|contact|office|billing|sales|marketing|hr|cs|help|news|humas)(?:$|[._+-]|\d)/

// A mailbox several people write from says nothing about who wrote.
export function isSharedMailbox(email: string) {
  const local = email.trim().toLowerCase().split("@")[0] ?? ""
  return MAILBOX_PREFIX.test(local) || MAILBOX_WORD.test(local)
}

const personalEmail = (e: string | null | undefined) => (e && !isSharedMailbox(e) ? e.trim() : undefined)

// Email display names put teams and mailboxes where a person's name goes
// ("Sekret WargaLapor" for sekretwargalapor@example.com).
export function looksLikeOrganization(m: Pick<PersonMention, "name" | "email">) {
  const words = tokens(m.name)
  if (words.some((t) => ORG_WORDS.has(t))) return true
  const local = m.email?.split("@")[0]
  return !!local && isSharedMailbox(m.email!) && words.join("") === tokens(local).join("")
}

// The default container has no person's name in it, so there the subject is
// the workspace's owner(s): their display name and username count as the subject.
export function subjectNames(db: DB, scope: Scope): string[] {
  if (scope.container !== config().defaultContainer) return []
  const rows = db.$client
    .prepare(
      `SELECT u.name, u.username, u.email FROM member m JOIN user u ON u.id = m.user_id
       WHERE m.organization_id = ? AND m.role = 'owner'`,
    )
    .all(scope.workspaceId) as { name: string; username: string | null; email: string }[]
  return rows.flatMap((r) => [r.name, r.username ?? ""]).filter(Boolean)
}

function levenshtein(a: string, b: string) {
  if (Math.abs(a.length - b.length) > 2) return 3
  let prev = Array.from({ length: b.length + 1 }, (_, i) => i)
  for (let i = 1; i <= a.length; i++) {
    const cur = [i]
    for (let j = 1; j <= b.length; j++) cur[j] = Math.min(prev[j]! + 1, cur[j - 1]! + 1, prev[j - 1]! + (a[i - 1] === b[j - 1] ? 0 : 1))
    prev = cur
  }
  return prev[b.length]!
}

export type NameMatch = "same" | "strong" | "weak"

// How alike two names are, or null when they name different people.
// "same": equal ignoring case, accents and punctuation.
// "strong": two spelled-out words agree ("Maya Hartono" ~ "Maya Lee Hartono",
// "Rizky Kurn" ~ "Rizky Kurniawan"), or a one-letter typo in a name of two
// or more words ("Budi Santosa" ~ "Budi Santoso").
// "weak": only the first name does ("Clara" or "Clara S." ~ "Clara Simmons").
// Never "Clara Wong" ~ "Clara Simmons" or "Contractor 1" ~ "Contractor 2".
export function nameMatch(a: string, b: string): NameMatch | null {
  const x = tokens(a)
  const y = tokens(b)
  if (!x.length || !y.length) return null
  const ka = x.join(" ")
  const kb = y.join(" ")
  if (ka === kb) return "same"
  // A one-letter typo, but never a different number.
  if (Math.min(ka.length, kb.length) >= 5 && !/\d/.test(ka + kb) && levenshtein(ka, kb) <= 1) return x.length > 1 && y.length > 1 ? "strong" : "weak"
  if (x[0] !== y[0]) return null
  const [short, long] = x.length <= y.length ? [x, y] : [y, x]
  let spelled = 0
  for (const t of short) {
    if (long.includes(t)) spelled += t.length > 1 ? 1 : 0
    else if (t.length === 1 && long.some((l) => l.startsWith(t))) continue
    // A word cut short, as email display names do ("Ferd" for "Ferdiansyah").
    else if (t.length >= 3 && !/\d/.test(t) && long.some((l) => l.length > t.length && l.startsWith(t))) spelled++
    else return null
  }
  return spelled >= 2 ? "strong" : "weak"
}

export const namesLookAlike = (a: string, b: string) => nameMatch(a, b) !== null

// "Clara" or "Clara S." is a partial of "Clara Simmons".
function isPartialOf(short: string, full: string) {
  return tokens(short).length < tokens(full).length && namesLookAlike(short, full)
}

// --- reads --------------------------------------------------------------------

// latestFact: the newest current fact that mentions them, for list rows.
export type PersonRow = Person & { lastMentionedAt: number | null; factCount: number; latestFact: string | null }

const STATS = `(SELECT max(m.created_at) FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id) AS lastMentionedAt,
  (SELECT count(*) FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id AND m.valid_to IS NULL) AS factCount,
  (SELECT m.fact FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id AND m.valid_to IS NULL
    ORDER BY m.created_at DESC, m.id DESC LIMIT 1) AS latestFact`

type RawPerson = Omit<PersonRow, "aliases" | "emails" | "handles" | "manual"> & { aliases: string; emails: string; handles: string; manual: number }

function parseList(s: string | string[] | null | undefined): string[] {
  if (Array.isArray(s)) return s
  try {
    const v = JSON.parse(s ?? "[]")
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []
  } catch {
    return []
  }
}

function fromRaw(r: RawPerson): PersonRow {
  return { ...r, aliases: parseList(r.aliases), emails: parseList(r.emails), handles: parseList(r.handles), manual: !!r.manual }
}

const SELECT_PERSON = `SELECT p.id, p.workspace_id AS workspaceId, p.container, p.name, p.aliases, p.role, p.organization, p.emails, p.handles,
  p.summary, p.manual, p.created_at AS createdAt, p.updated_at AS updatedAt, ${STATS} FROM people p`

function peopleInScope(db: DB, scope: Scope): PersonRow[] {
  const rows = db.$client.prepare(`${SELECT_PERSON} WHERE p.workspace_id = ? AND p.container = ?`).all(scope.workspaceId, scope.container) as RawPerson[]
  return rows.map(fromRaw)
}

export function getPersonRow(db: DB, workspaceId: string, id: string): PersonRow | undefined {
  const row = db.$client.prepare(`${SELECT_PERSON} WHERE p.id = ? AND p.workspace_id = ?`).get(id, workspaceId) as RawPerson | undefined
  return row ? fromRaw(row) : undefined
}

export type PeopleSort = "recent" | "name"

export function listPeople(
  db: DB,
  opts: { workspaceId: string; container: string; q?: string; sort?: PeopleSort; limit?: number; offset?: number },
): { people: PersonRow[]; total: number; nextOffset: number | null } {
  const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200)
  const offset = Math.max(opts.offset ?? 0, 0)
  // Filtered in JS with nameKey, so "elodie" finds "Élodie" the way the
  // resolver does (SQLite's lower() and LIKE only fold ASCII). A container
  // holds hundreds of people at most.
  const q = nameKey(opts.q ?? "")
  const order = opts.sort === "name" ? "lower(p.name) ASC, p.id ASC" : "lastMentionedAt IS NULL, lastMentionedAt DESC, lower(p.name) ASC, p.id ASC"
  const all = (
    db.$client.prepare(`${SELECT_PERSON} WHERE p.workspace_id = ? AND p.container = ? ORDER BY ${order}`).all(opts.workspaceId, opts.container) as RawPerson[]
  ).map(fromRaw)
  const found = q
    ? all.filter((p) =>
        [p.name, ...p.aliases, p.role ?? "", p.organization ?? "", ...p.emails, ...p.handles].some((v) => nameKey(v).includes(q)),
      )
    : all
  const rows = found.slice(offset, offset + limit)
  return { people: rows, total: found.length, nextOffset: offset + rows.length < found.length ? offset + rows.length : null }
}

export type PersonFact = {
  id: string
  fact: string
  kind: "static" | "dynamic"
  createdAt: number
  validTo: number | null
  supersedesId: string | null
  reviewNote: string | null
  // When it happened and when it is due (migration 0007).
  eventDate: string | null
  eventPrecision: Memory["eventPrecision"]
  dueDate: string | null
  duePrecision: Memory["duePrecision"]
  source: { id: string; title: string | null; type: string; url: string | null } | null
}

// The facts that mention a person, newest first. Current ones only, unless history.
export function personFacts(db: DB, workspaceId: string, personId: string, opts: { history?: boolean; limit?: number } = {}): PersonFact[] {
  const rows = db
    .select({
      id: memories.id,
      fact: memories.fact,
      kind: memories.kind,
      createdAt: memories.createdAt,
      validTo: memories.validTo,
      supersedesId: memories.supersedesId,
      reviewNote: memories.reviewNote,
      eventDate: memories.eventDate,
      eventPrecision: memories.eventPrecision,
      dueDate: memories.dueDate,
      duePrecision: memories.duePrecision,
      sourceId: documents.id,
      sourceTitle: documents.title,
      sourceType: documents.sourceType,
      sourceUrl: documents.url,
    })
    .from(personMemories)
    .innerJoin(memories, and(eq(memories.id, personMemories.memoryId), eq(memories.workspaceId, workspaceId)))
    .leftJoin(documents, and(eq(documents.id, memories.sourceDocumentId), eq(documents.workspaceId, workspaceId)))
    .where(and(eq(personMemories.personId, personId), eq(personMemories.workspaceId, workspaceId), opts.history ? undefined : isNull(memories.validTo)))
    .all()
    .sort((a, b) => b.createdAt - a.createdAt || (a.id < b.id ? 1 : -1))
    .slice(0, opts.limit ?? 500)
  return rows.map(({ sourceId, sourceTitle, sourceType, sourceUrl, ...m }) => ({
    ...m,
    source: sourceId ? { id: sourceId, title: sourceTitle, type: sourceType ?? "text", url: sourceUrl } : null,
  }))
}

export function getPerson(db: DB, workspaceId: string, id: string, opts: { history?: boolean } = {}) {
  const person = getPersonRow(db, workspaceId, id)
  if (!person) return null
  return { person, facts: personFacts(db, workspaceId, id, opts) }
}

export type PersonRef = { id: string; name: string }

// The people each memory mentions, for memory rows and search results.
export function peopleForMemories(db: DB, workspaceId: string, memoryIds: string[]): Record<string, PersonRef[]> {
  const out: Record<string, PersonRef[]> = {}
  if (!memoryIds.length) return out
  const rows = db
    .select({ memoryId: personMemories.memoryId, id: people.id, name: people.name })
    .from(personMemories)
    .innerJoin(people, and(eq(people.id, personMemories.personId), eq(people.workspaceId, workspaceId)))
    .where(and(eq(personMemories.workspaceId, workspaceId), inArray(personMemories.memoryId, memoryIds)))
    .orderBy(people.name)
    .all()
  for (const r of rows) (out[r.memoryId] ??= []).push({ id: r.id, name: r.name })
  return out
}

// --- resolve ------------------------------------------------------------------

type Candidate = { person: PersonRow; exact: boolean }

function candidates(all: PersonRow[], m: PersonMention): Candidate[] {
  const out = new Map<string, Candidate>()
  const add = (person: PersonRow, exact: boolean) => {
    const cur = out.get(person.id)
    if (!cur || (exact && !cur.exact)) out.set(person.id, { person, exact })
  }
  const mine = personalEmail(m.email)
  const email = mine ? emailKey(mine) : null
  const handle = m.handle ? handleKey(m.handle) : null
  const key = nameKey(m.name)
  const aliasKeys = m.aliases.map(nameKey).filter(Boolean)
  for (const p of all) {
    const names = [p.name, ...p.aliases]
    const keys = names.map(nameKey)
    // A shortened form of the full name ("Clara" for "Clara Simmons") is shared
    // by many people, so matching on it alone needs the embedding check too.
    const firm = names.filter((n) => n === p.name || !isPartialOf(n, p.name)).map(nameKey)
    // A new email rules out a first-name match ("Clara" at gmail.com is not
    // "Clara Simmons" at vorneo.id). People do write from several addresses,
    // so a full name that matches still goes to the embedding check.
    const theirs = p.emails.filter((e) => !isSharedMailbox(e)).map(emailKey)
    const otherEmail = !!email && theirs.length > 0 && !theirs.includes(email)
    if (email && theirs.includes(email)) add(p, true)
    else if (handle && p.handles.some((h) => handleKey(h) === handle)) add(p, true)
    else if (firm.includes(key)) add(p, true)
    else if (otherEmail && !names.some((n) => nameMatch(m.name, n) === "strong")) continue
    else if (keys.includes(key) || aliasKeys.some((a) => keys.includes(a)) || names.some((n) => namesLookAlike(m.name, n))) add(p, false)
  }
  return [...out.values()]
}

const cosine = (a: Float32Array, b: Float32Array) => a.reduce((s, x, i) => s + x * b[i]!, 0)

function describe(name: string, role: string | null | undefined, org: string | null | undefined, summary: string | null | undefined, facts: string[]) {
  return [name, [role, org].filter(Boolean).join(" "), summary, ...facts.slice(0, DESCRIPTOR_FACTS)].filter((s) => s && s.trim()).join(". ")
}

type Resolved = { person: PersonRow; similarity: number | null } | null

// Exact (email, handle, name or alias, ignoring case and accents) wins. Several
// exact matches are told apart by the embedding. A fuzzy match needs the
// embedding to agree (FUZZY_PERSON_SIMILARITY); otherwise it's a new person.
export async function matchPerson(db: DB, scope: Scope, m: PersonMention, factTexts: string[], all = peopleInScope(db, scope)): Promise<Resolved> {
  const found = candidates(all, m)
  if (!found.length) return null
  const exact = found.filter((c) => c.exact)
  if (exact.length === 1) return { person: exact[0]!.person, similarity: null }
  const pool = exact.length ? exact : found
  const descriptors = pool.map(({ person: p }) =>
    describe(p.name, p.role, p.organization, p.summary, personFacts(db, scope.workspaceId, p.id, { limit: DESCRIPTOR_FACTS }).map((f) => f.fact)),
  )
  const [mine, ...theirs] = await embed([describe(m.name, m.role, m.organization, m.summary, factTexts), ...descriptors], "passage")
  const scored = pool.map((c, i) => ({ person: c.person, similarity: cosine(mine!, theirs[i]!) })).sort((a, b) => b.similarity - a.similarity)
  const best = scored[0]!
  if (exact.length) return best
  return best.similarity >= FUZZY_PERSON_SIMILARITY ? best : null
}

const uniq = (list: string[], key: (s: string) => string, max: number) => {
  const seen = new Set<string>()
  const out: string[] = []
  for (const s of list.map((x) => x.trim()).filter(Boolean)) {
    const k = key(s)
    if (!k || seen.has(k)) continue
    seen.add(k)
    out.push(s)
  }
  return out.slice(0, max)
}

const clean = (s: string | undefined) => (s && !sensitiveKinds(s).length ? s.trim() : undefined)

// Folds a mention into an existing person, or returns the row for a new one.
// A hand-edited person keeps its name, role, organization and summary.
function merged(existing: PersonRow | null, m: PersonMention) {
  const role = clean(m.role)
  const organization = clean(m.organization)
  const summary = clean(m.summary)
  const email = personalEmail(m.email)
  if (!existing) {
    return {
      name: m.name.trim(),
      aliases: uniq(m.aliases, nameKey, MAX_ALIASES).filter((a) => nameKey(a) !== nameKey(m.name)),
      role: role ?? null,
      organization: organization ?? null,
      emails: email ? [email] : [],
      handles: m.handle ? [m.handle.trim()] : [],
      summary: summary ?? "",
    }
  }
  // A fuller name than the one on file ("Clara Simmons" for "Clara") becomes the name.
  const fuller = !existing.manual && tokens(m.name).length > tokens(existing.name).length && namesLookAlike(m.name, existing.name)
  const name = fuller ? m.name.trim() : existing.name
  return {
    name,
    aliases: uniq([...existing.aliases, existing.name, m.name, ...m.aliases], nameKey, MAX_ALIASES).filter((a) => nameKey(a) !== nameKey(name)),
    role: existing.manual ? existing.role : (role ?? existing.role),
    organization: existing.manual ? existing.organization : (organization ?? existing.organization),
    emails: uniq([...existing.emails, ...(email ? [email] : [])], emailKey, MAX_CONTACTS),
    handles: uniq([...existing.handles, ...(m.handle ? [m.handle] : [])], handleKey, MAX_CONTACTS),
    summary: existing.manual ? existing.summary : summary || existing.summary,
  }
}

export type LinkStats = { linked: number; created: number; skipped: number }

// Resolves each mention to a person (creating one when nothing matches) and
// links it to the given memories. Mentions of the container's own subject and
// memories outside the scope are skipped. Safe to repeat: links are unique.
export async function linkMentions(db: DB, scope: Scope, mentions: { mention: PersonMention; memoryIds: string[] }[]): Promise<LinkStats> {
  const stats: LinkStats = { linked: 0, created: 0, skipped: 0 }
  const allIds = [...new Set(mentions.flatMap((x) => x.memoryIds))]
  if (!allIds.length) return stats
  const owners = subjectNames(db, scope)
  const facts = new Map(
    db
      .select({ id: memories.id, fact: memories.fact })
      .from(memories)
      .where(and(eq(memories.workspaceId, scope.workspaceId), eq(memories.container, scope.container), inArray(memories.id, allIds)))
      .all()
      .map((m) => [m.id, m.fact]),
  )
  for (const { mention, memoryIds } of mentions) {
    const ids = [...new Set(memoryIds)].filter((id) => facts.has(id))
    if (!ids.length || isContainerOwner(scope.container, mention, owners) || looksLikeOrganization(mention) || sensitiveKinds(mention.name).length) {
      stats.skipped++
      continue
    }
    const match = await matchPerson(
      db,
      scope,
      mention,
      ids.map((id) => facts.get(id)!),
    )
    const next = merged(match?.person ?? null, mention)
    db.transaction(() => {
      let personId = match?.person.id
      if (personId) {
        db.update(people)
          .set({ ...next, updatedAt: Date.now() })
          .where(and(eq(people.id, personId), eq(people.workspaceId, scope.workspaceId)))
          .run()
      } else {
        personId = newId("per")
        db.insert(people)
          .values({ id: personId, workspaceId: scope.workspaceId, container: scope.container, ...next })
          .run()
        stats.created++
      }
      for (const memoryId of ids) {
        stats.linked += db.insert(personMemories).values({ personId, memoryId, workspaceId: scope.workspaceId }).onConflictDoNothing().run().changes
      }
    })
  }
  return stats
}

// Facts already checked for people, so the backfill skips them.
export function markScanned(db: DB, workspaceId: string, memoryIds: string[]) {
  for (const memoryId of memoryIds) db.insert(peopleScans).values({ memoryId, workspaceId }).onConflictDoNothing().run()
}

// Extract answers name facts as "a1" (first add), "u2" (second update), "c1"
// (first conflict). `written` maps those refs to the rows actually written.
// The facts are marked checked only after their links are written.
export async function linkExtractedPeople(db: DB, scope: Scope, mentions: PersonMention[], written: Map<string, Memory>) {
  const resolved = mentions.map((mention) => ({
    mention,
    memoryIds: mention.facts.map((f) => written.get(f.trim().toLowerCase())?.id).filter((id): id is string => !!id),
  }))
  const stats = await linkMentions(db, scope, resolved)
  markScanned(
    db,
    scope.workspaceId,
    [...written.values()].map((m) => m.id),
  )
  return stats
}

// --- backfill -----------------------------------------------------------------

export const BACKFILL_BATCH = 40

export type BackfillStats = { scanned: number; linked: number; created: number; calls: number }

type BackfillDeps = { chat: typeof chatJson }

// Links current facts that were never checked for people. Each batch is marked
// scanned once its links are written, so a rerun (or a retry after a failure)
// only sends facts the model hasn't seen.
export async function backfillPeople(db: DB, scope: Scope, deps: BackfillDeps = { chat: chatJson }, opts: { maxBatches?: number } = {}): Promise<BackfillStats> {
  const stats: BackfillStats = { scanned: 0, linked: 0, created: 0, calls: 0 }
  for (let batch = 0; batch < (opts.maxBatches ?? Infinity); batch++) {
    const rows = db.$client
      .prepare(
        `SELECT m.id, m.fact FROM memories m
         WHERE m.workspace_id = ? AND m.container = ? AND m.valid_to IS NULL
           AND NOT EXISTS (SELECT 1 FROM people_scans s WHERE s.memory_id = m.id)
         ORDER BY m.created_at, m.id LIMIT ?`,
      )
      .all(scope.workspaceId, scope.container, BACKFILL_BATCH) as { id: string; fact: string }[]
    if (!rows.length) break
    const result = await deps.chat({
      system: peopleBackfillSystem(scope.container),
      prompt: peopleBackfillPrompt(rows.map((r, i) => ({ ref: i + 1, fact: r.fact }))),
      schema: peopleBackfillResult,
    })
    stats.calls++
    const s = await linkMentions(
      db,
      scope,
      result.people.map((mention) => ({
        mention,
        memoryIds: mention.facts.map((f) => rows[Number(f) - 1]?.id).filter((id): id is string => !!id),
      })),
    )
    markScanned(
      db,
      scope.workspaceId,
      rows.map((r) => r.id),
    )
    stats.scanned += rows.length
    stats.linked += s.linked
    stats.created += s.created
  }
  return stats
}

// Containers in a workspace with current facts nobody has checked for people.
export function containersToBackfill(db: DB, workspaceId: string): { container: string; pending: number }[] {
  return db.$client
    .prepare(
      `SELECT m.container, count(*) AS pending FROM memories m
       WHERE m.workspace_id = ? AND m.valid_to IS NULL AND NOT EXISTS (SELECT 1 FROM people_scans s WHERE s.memory_id = m.id)
       GROUP BY m.container ORDER BY m.container`,
    )
    .all(workspaceId) as { container: string; pending: number }[]
}

// --- edits --------------------------------------------------------------------

export class PersonError extends Error {}

export type PersonPatch = {
  name?: string
  aliases?: string[]
  role?: string | null
  organization?: string | null
  summary?: string
  emails?: string[]
  handles?: string[]
}

export function updatePerson(db: DB, workspaceId: string, id: string, patch: PersonPatch): PersonRow | null {
  const cur = getPersonRow(db, workspaceId, id)
  if (!cur) return null
  const name = patch.name?.trim() || cur.name
  db.update(people)
    .set({
      name,
      aliases: uniq(patch.aliases ?? cur.aliases, nameKey, MAX_ALIASES).filter((a) => nameKey(a) !== nameKey(name)),
      role: patch.role === undefined ? cur.role : patch.role?.trim() || null,
      organization: patch.organization === undefined ? cur.organization : patch.organization?.trim() || null,
      summary: patch.summary === undefined ? cur.summary : patch.summary.trim(),
      emails: uniq(patch.emails ?? cur.emails, emailKey, MAX_CONTACTS),
      handles: uniq(patch.handles ?? cur.handles, handleKey, MAX_CONTACTS),
      manual: true,
      updatedAt: Date.now(),
    })
    .where(and(eq(people.id, id), eq(people.workspaceId, workspaceId)))
    .run()
  return getPersonRow(db, workspaceId, id)!
}

// Moves everything from `dropId` into `keepId` and deletes `dropId`. Both must
// be in the same workspace and container.
export function mergePeople(db: DB, workspaceId: string, keepId: string, dropId: string): PersonRow {
  if (keepId === dropId) throw new PersonError("Pick two different people to merge.")
  const keep = getPersonRow(db, workspaceId, keepId)
  const drop = getPersonRow(db, workspaceId, dropId)
  if (!keep || !drop) throw new PersonError("No such person.")
  if (keep.container !== drop.container) throw new PersonError("Only people in the same container can be merged.")
  db.transaction(() => {
    db.$client
      .prepare(
        `INSERT OR IGNORE INTO person_memories (person_id, memory_id, workspace_id, created_at)
         SELECT ?, memory_id, workspace_id, created_at FROM person_memories WHERE person_id = ? AND workspace_id = ?`,
      )
      .run(keepId, dropId, workspaceId)
    db.update(people)
      .set({
        aliases: uniq([...keep.aliases, drop.name, ...drop.aliases], nameKey, MAX_ALIASES).filter((a) => nameKey(a) !== nameKey(keep.name)),
        role: keep.role ?? drop.role,
        organization: keep.organization ?? drop.organization,
        emails: uniq([...keep.emails, ...drop.emails], emailKey, MAX_CONTACTS),
        handles: uniq([...keep.handles, ...drop.handles], handleKey, MAX_CONTACTS),
        summary: keep.summary || drop.summary,
        manual: keep.manual || drop.manual,
        updatedAt: Date.now(),
      })
      .where(and(eq(people.id, keepId), eq(people.workspaceId, workspaceId)))
      .run()
    db.delete(people)
      .where(and(eq(people.id, dropId), eq(people.workspaceId, workspaceId)))
      .run()
  })
  return getPersonRow(db, workspaceId, keepId)!
}

// --- merge suggestions --------------------------------------------------------

export type MergeSuggestion = { keep: PersonRow; drop: PersonRow; reasons: string[]; similarity: number | null }

const NAME_RANK: Record<NameMatch, number> = { same: 3, strong: 2, weak: 1 }

// Hand-edited, more facts, then the fuller name, then the older row.
function keepFirst(a: PersonRow, b: PersonRow): [PersonRow, PersonRow] {
  const score = (p: PersonRow) => [Number(p.manual), p.factCount, tokens(p.name).length, -p.createdAt]
  const [x, y] = [score(a), score(b)]
  const i = x.findIndex((v, k) => v !== y[k])
  return i === -1 || x[i]! > y[i]! ? [a, b] : [b, a]
}

// Likely duplicates in one container, for someone to confirm. Never merges.
// A shared email or handle is enough. A look-alike name also needs the
// embedding to agree, the same bar the resolver uses, and a first-name match
// between people with different emails doesn't count.
export async function suggestPeopleMerges(db: DB, scope: Scope, opts: { limit?: number } = {}): Promise<MergeSuggestion[]> {
  // Rows that read as a team or mailbox get deleted, not merged into someone.
  const all = peopleInScope(db, scope).filter((p) => !looksLikeOrganization({ name: p.name, email: p.emails[0] }))
  const info = all.map((p) => ({
    // A shortened alias ("Muhammad" for "Muhammad Rizki") is shared by many people.
    names: [p.name, ...p.aliases.filter((a) => !isPartialOf(a, p.name))],
    emails: p.emails.filter((e) => !isSharedMailbox(e)).map(emailKey),
    handles: p.handles.map(handleKey),
  }))
  const found: (MergeSuggestion & { check: boolean })[] = []
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) {
      const [a, b] = [info[i]!, info[j]!]
      const reasons = [
        ...a.emails.filter((e) => b.emails.includes(e)).map((e) => `same email ${e}`),
        ...a.handles.filter((h) => b.handles.includes(h)).map((h) => `same handle @${h}`),
      ]
      let best: NameMatch | null = null
      for (const x of a.names)
        for (const y of b.names) {
          const m = nameMatch(x, y)
          if (m && (!best || NAME_RANK[m] > NAME_RANK[best])) best = m
        }
      // Without a shared contact, the embedding has to agree.
      const check = !reasons.length
      if (check) {
        if (!best || (best === "weak" && a.emails.length && b.emails.length)) continue
        reasons.push(best === "same" ? "same name" : "names look alike")
      }
      const [keep, drop] = keepFirst(all[i]!, all[j]!)
      found.push({ keep, drop, reasons, similarity: null, check })
    }
  }
  const checked = [...new Set(found.filter((f) => f.check).flatMap((f) => [f.keep, f.drop]))]
  if (checked.length) {
    const vectors = await embed(
      checked.map((p) => describe(p.name, p.role, p.organization, p.summary, personFacts(db, scope.workspaceId, p.id, { limit: DESCRIPTOR_FACTS }).map((f) => f.fact))),
      "passage",
    )
    const byId = new Map(checked.map((p, i) => [p.id, vectors[i]!]))
    for (const f of found) if (f.check) f.similarity = cosine(byId.get(f.keep.id)!, byId.get(f.drop.id)!)
  }
  return found
    .filter((f) => !f.check || f.similarity! >= FUZZY_PERSON_SIMILARITY)
    .sort((x, y) => Number(x.check) - Number(y.check) || (y.similarity ?? 0) - (x.similarity ?? 0))
    .slice(0, opts.limit ?? 50)
    .map(({ check: _, ...s }) => s)
}

// Removes the person and their links. The facts stay.
export function deletePerson(db: DB, workspaceId: string, id: string) {
  return (
    db
      .delete(people)
      .where(and(eq(people.id, id), eq(people.workspaceId, workspaceId)))
      .run().changes > 0
  )
}

// --- ask ------------------------------------------------------------------------

const MAX_ASK_PEOPLE = 2

// People in the container whose name or alias appears in the text as whole words.
// A one-word name that is also a common word ("Will", "Mark", "Grace") must be
// capitalized in the text, unless the whole text is typed in lowercase.
export function peopleNamedIn(db: DB, scope: Scope, text: string): PersonRow[] {
  const hay = ` ${nameKey(text)} `
  const casual = text.replace(/\bI\b/g, "i") === text.toLowerCase()
  const capitalized = new Set(
    text
      .normalize("NFKD")
      .replace(/\p{M}/gu, "")
      .split(/[^\p{L}\p{N}]+/u)
      .filter((w) => /^\p{Lu}/u.test(w))
      .map((w) => w.toLowerCase()),
  )
  const named = (name: string) => {
    const key = nameKey(name)
    if (key.length < 2 || !hay.includes(` ${key} `)) return false
    return casual || key.includes(" ") || capitalized.has(key)
  }
  const hits: { person: PersonRow; len: number }[] = []
  for (const p of peopleInScope(db, scope)) {
    const len = Math.max(0, ...[p.name, ...p.aliases].filter(named).map((n) => nameKey(n).length))
    if (len) hits.push({ person: p, len })
  }
  return hits
    .sort((a, b) => b.len - a.len || (b.person.lastMentionedAt ?? 0) - (a.person.lastMentionedAt ?? 0))
    .slice(0, MAX_ASK_PEOPLE)
    .map((h) => h.person)
}
