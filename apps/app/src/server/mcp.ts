import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js"
import { z } from "zod"
import { addContent, dateView, DEFAULT_WAIT_MS, list, MAX_SOURCE_ID_LEN, MAX_UPCOMING_DAYS, people, person, profile, search, upcoming, waitForAdd } from "./api"
import { isTimezone } from "../lib/dates"
import { ask } from "./ask"
import { HttpError } from "./http"
import { config } from "./config"
import type { DB } from "./db"
import { listContainers, MAX_CONTAINER_LEN } from "./memories"
import { getPersonRow, peopleNamedIn } from "./people"

const container = z
  .string()
  .trim()
  .min(1)
  .max(MAX_CONTAINER_LEN)
  .optional()
  .describe("Memory namespace: a user id, project or team. Defaults to the server's default container.")

// Only the dates a fact has, in short form ("2027-12" for a month).
function dates(m: Parameters<typeof dateView>[0]) {
  const v = dateView(m)
  return { ...(v.event_date ? { event_date: v.event_date } : {}), ...(v.due_date ? { due_date: v.due_date } : {}) }
}

function text(value: unknown) {
  return { content: [{ type: "text" as const, text: typeof value === "string" ? value : JSON.stringify(value, null, 2) }] }
}

// `ws` is the API key's workspace. No tool can reach another one.
export function buildMcpServer(db: DB, ws: string) {
  const def = () => config().defaultContainer
  const server = new McpServer(
    { name: "recall", version: "1.0.0" },
    {
      instructions:
        "Recall is a long-term memory. Call search_memories or get_profile before answering questions about the user, their preferences or past work, or ask_memory for a short cited answer. For someone the user knows (a colleague, client or relative), call get_person with their name. Call add_memory when the user shares something worth remembering. Newer facts automatically supersede older ones. Facts can carry an event_date (when it happened) and a due_date (when it ends or is due); call upcoming to see what is due soon.",
    },
  )

  server.registerTool(
    "add_memory",
    {
      title: "Add memory",
      description:
        "Save text, a conversation excerpt or a URL. Facts are extracted in the background (a few seconds) and contradicting older facts are superseded. Returns the document id, or with wait=true the extracted facts.",
      inputSchema: {
        content: z.string().max(1_000_000).optional().describe("Text to remember. Send this or url."),
        url: z.url({ protocol: /^https?$/ }).optional().describe("A web page or PDF to fetch and remember."),
        title: z.string().max(300).optional(),
        source_id: z
          .string()
          .trim()
          .min(1)
          .max(MAX_SOURCE_ID_LEN)
          .optional()
          .describe("Your own id for the source, e.g. gmail:<message id>. A repeat in the same container returns the existing document instead of adding it again."),
        wait: z.boolean().optional().describe("Wait (up to 60 s) for extraction to finish and return the facts it added or updated."),
        document_date: z
          .string()
          .regex(/^\d{4}-\d{2}-\d{2}/)
          .optional()
          .describe("The day the content was written (YYYY-MM-DD), e.g. an email's sent date. Relative dates in it (\"next Friday\") resolve against this. Defaults to today."),
        container,
      },
      annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: true },
    },
    async (args, extra) => {
      if (!args.content?.trim() && !args.url) return { ...text("Send content or url."), isError: true }
      const res = addContent(db, ws, {
        container: args.container || def(),
        content: args.url ? undefined : args.content,
        url: args.url,
        title: args.title,
        source_id: args.source_id,
        document_date: args.document_date,
      })
      if (!args.wait) {
        return text({ ...res, note: res.duplicate ? "Already saved under this source_id." : "Queued. Facts appear within a few seconds." })
      }
      const out = await waitForAdd(db, ws, res, DEFAULT_WAIT_MS, extra.signal)
      if (!out.settled) return text({ ...res, status: out.status, note: "Still processing after 60 s. Facts will appear when it finishes." })
      return text({
        id: out.id,
        container: out.container,
        status: out.status,
        duplicate: out.duplicate,
        ...(out.error ? { error: out.error } : {}),
        added: out.added?.map((m) => ({ id: m.id, fact: m.fact, kind: m.kind, ...dates(m), ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}) })),
        updated: out.updated?.map((m) => ({
          id: m.id,
          fact: m.fact,
          kind: m.kind,
          replaces: m.supersedesId,
          ...dates(m),
          ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}),
        })),
        invalidated: out.invalidated,
      })
    },
  )

  server.registerTool(
    "search_memories",
    {
      title: "Search memories",
      description: "Hybrid keyword + semantic search over current memories (English or Indonesian). Optionally include raw document chunks.",
      inputSchema: {
        query: z.string().min(1).max(2000),
        limit: z.number().int().min(1).max(50).optional(),
        include_chunks: z.boolean().optional().describe("Also search the raw text of saved documents."),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const res = await search(db, ws, {
        container: args.container || def(),
        q: args.query,
        limit: args.limit ?? 10,
        include: args.include_chunks ? ["memories", "chunks"] : ["memories"],
      })
      return text(
        res.results.map((r) => ({
          type: r.type,
          id: r.id,
          text: r.text,
          score: Number(r.score.toFixed(4)),
          ...(r.type === "memory" ? { kind: r.kind, ...dates(r), ...(r.people.length ? { people: r.people.map((p) => p.name) } : {}) } : { document: r.documentTitle }),
        })),
      )
    },
  )

  server.registerTool(
    "ask_memory",
    {
      title: "Ask memory",
      description:
        "Ask a question in English or Indonesian and get a short answer written only from the stored facts, with citations ([1], [2]) that map to memory or document ids. Says plainly when memory doesn't cover it. Takes a few seconds (one LLM call).",
      inputSchema: {
        question: z.string().trim().min(1).max(2000),
        include_chunks: z.boolean().optional().describe("Also use the raw text of saved documents."),
        container,
      },
      annotations: { readOnlyHint: true, openWorldHint: false },
    },
    async (args, extra) => {
      try {
        const res = await ask(
          db,
          ws,
          { container: args.container || def(), question: args.question, include_chunks: args.include_chunks },
          { signal: extra.signal },
        )
        return text({
          answer: res.answer,
          covered: res.covered,
          citations: res.citations.map((c) => ({ ref: c.ref, type: c.type, id: c.id, text: c.text })),
          model: res.model,
        })
      } catch (err) {
        if (err instanceof HttpError) return { ...text(err.message), isError: true }
        throw err
      }
    },
  )

  server.registerTool(
    "get_profile",
    {
      title: "Get profile",
      description: "A short summary of who the subject is (static) and what is going on now (dynamic), plus the facts behind it. Pass query to also get matching memories.",
      inputSchema: { query: z.string().max(2000).optional(), container },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const p = await profile(db, ws, { container: args.container || def(), q: args.query })
      return text({
        container: p.container,
        static: p.static,
        dynamic: p.dynamic,
        updatedAt: p.updatedAt ? new Date(p.updatedAt).toISOString() : null,
        staticFacts: p.staticMemories.map((m) => m.fact),
        dynamicFacts: p.dynamicMemories.map((m) => m.fact),
        ...("searchResults" in p ? { searchResults: p.searchResults.map((r) => r.text) } : {}),
      })
    },
  )

  server.registerTool(
    "list_memories",
    {
      title: "List memories",
      description:
        "List current memories, newest first. Pass cursor from the previous page to continue. history=true includes superseded versions. needs_review=true lists only current facts flagged as conflicting with another fact; reviewNote says why.",
      inputSchema: {
        cursor: z.string().optional(),
        limit: z.number().int().min(1).max(200).optional(),
        history: z.boolean().optional(),
        needs_review: z.boolean().optional().describe("Only facts flagged for human review because they conflict with another fact."),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const res = list(db, ws, {
        container: args.container || def(),
        cursor: args.cursor,
        limit: args.limit ?? 50,
        history: args.history ?? false,
        review: args.needs_review ?? false,
      })
      return text({
        memories: res.memories.map((m) => ({
          id: m.id,
          fact: m.fact,
          kind: m.kind,
          current: m.validTo === null,
          ...dates(m),
          ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}),
          createdAt: new Date(m.createdAt).toISOString(),
        })),
        nextCursor: res.nextCursor,
        containers: args.cursor ? undefined : listContainers(db, ws).map((c) => c.container),
      })
    },
  )

  server.registerTool(
    "upcoming",
    {
      title: "Upcoming",
      description:
        "Current facts with a due date in the next N days (contracts ending, renewals, deadlines), soonest first, with days_left. Use it for \"what's due soon\" or before planning. past_days also includes facts that came due recently.",
      inputSchema: {
        days: z.number().int().min(1).max(MAX_UPCOMING_DAYS).optional().describe("How far ahead to look. Default 30."),
        past_days: z.number().int().min(0).max(MAX_UPCOMING_DAYS).optional().describe("Also include facts due in the last N days. Default 0."),
        all_containers: z.boolean().optional().describe("Every container in the workspace instead of one."),
        timezone: z.string().max(64).optional().describe("IANA time zone for \"today\", e.g. Asia/Makassar. Default UTC."),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      if (args.timezone && !isTimezone(args.timezone)) return { ...text(`Unknown time zone: ${args.timezone}`), isError: true }
      const res = upcoming(db, ws, {
        container: args.container || def(),
        all: args.all_containers ?? false,
        days: args.days ?? 30,
        past_days: args.past_days ?? 0,
        tz: args.timezone ?? "UTC",
      })
      return text({
        today: res.today,
        container: res.container,
        memories: res.memories.map((m) => ({ id: m.id, fact: m.fact, container: m.container, ...dates(m), days_left: m.daysLeft })),
      })
    },
  )

  server.registerTool(
    "list_people",
    {
      title: "List people",
      description:
        "The people named in this container's facts (colleagues, clients, family), most recently mentioned first, with role, organization, contact details and how many current facts mention them. Pass query to filter by name, alias, role, organization, email or handle.",
      inputSchema: {
        query: z.string().trim().max(200).optional(),
        sort: z.enum(["recent", "name"]).optional().describe("recent (default): last mentioned first. name: A to Z."),
        limit: z.number().int().min(1).max(200).optional(),
        offset: z.number().int().min(0).optional().describe("nextOffset from the previous page."),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const res = people(db, ws, { container: args.container || def(), q: args.query, sort: args.sort, limit: args.limit ?? 50, offset: args.offset })
      return text({
        people: res.people.map((p) => ({
          id: p.id,
          name: p.name,
          ...(p.aliases.length ? { aliases: p.aliases } : {}),
          ...(p.role ? { role: p.role } : {}),
          ...(p.organization ? { organization: p.organization } : {}),
          ...(p.emails.length ? { emails: p.emails } : {}),
          ...(p.handles.length ? { handles: p.handles } : {}),
          facts: p.factCount,
          lastMentionedAt: p.lastMentionedAt ? new Date(p.lastMentionedAt).toISOString() : null,
        })),
        total: res.total,
        nextOffset: res.nextOffset,
      })
    },
  )

  server.registerTool(
    "get_person",
    {
      title: "Get person",
      description:
        "Everything stored about one person the user knows: name, aliases, role, organization, contact details, a short summary and the current facts that mention them, newest first, with their sources. Pass id from list_people, or name (matches names and aliases, ignoring case).",
      inputSchema: {
        id: z.string().trim().min(1).max(64).optional(),
        name: z.string().trim().min(1).max(200).optional(),
        history: z.boolean().optional().describe("Also include older versions of those facts."),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const c = args.container || def()
      const id = args.id ?? (args.name ? peopleNamedIn(db, { workspaceId: ws, container: c }, args.name)[0]?.id : undefined)
      if (!id || !getPersonRow(db, ws, id)) {
        return { ...text(args.id || args.name ? `No person matches ${JSON.stringify(args.id ?? args.name)} in ${c}.` : "Send id or name."), isError: true }
      }
      const res = person(db, ws, id, { history: args.history })
      return text({
        ...res.person,
        lastMentionedAt: res.person.lastMentionedAt ? new Date(res.person.lastMentionedAt).toISOString() : null,
        createdAt: new Date(res.person.createdAt).toISOString(),
        updatedAt: new Date(res.person.updatedAt).toISOString(),
        facts: res.facts.map((f) => ({ ...f, createdAt: new Date(f.createdAt).toISOString(), source: f.source?.title ?? f.source?.url ?? null })),
      })
    },
  )

  return server
}

// Stateless: a fresh server and transport per request, JSON responses.
export async function handleMcp(db: DB, ws: string, request: Request): Promise<Response> {
  const server = buildMcpServer(db, ws)
  const transport = new WebStandardStreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
  await server.connect(transport)
  try {
    return await transport.handleRequest(request)
  } finally {
    void server.close()
  }
}
