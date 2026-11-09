import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js"
import { z } from "zod"
import { addContent, DEFAULT_WAIT_MS, list, MAX_SOURCE_ID_LEN, profile, search, waitForAdd } from "./api"
import { ask } from "./ask"
import { HttpError } from "./http"
import { config } from "./config"
import type { DB } from "./db"
import { listContainers, MAX_CONTAINER_LEN } from "./memories"

const container = z
  .string()
  .trim()
  .min(1)
  .max(MAX_CONTAINER_LEN)
  .optional()
  .describe("Memory namespace: a user id, project or team. Defaults to the server's default container.")

function text(value: unknown) {
  return { content: [{ type: "text" as const, text: typeof value === "string" ? value : JSON.stringify(value, null, 2) }] }
}

export function buildMcpServer(db: DB) {
  const def = () => config().defaultContainer
  const server = new McpServer(
    { name: "recall", version: "1.0.0" },
    {
      instructions:
        "Recall is a long-term memory. Call search_memories or get_profile before answering questions about the user, their preferences or past work, or ask_memory for a short cited answer. Call add_memory when the user shares something worth remembering. Newer facts automatically supersede older ones.",
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
        container,
      },
      annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: true },
    },
    async (args, extra) => {
      if (!args.content?.trim() && !args.url) return { ...text("Send content or url."), isError: true }
      const res = addContent(db, {
        container: args.container || def(),
        content: args.url ? undefined : args.content,
        url: args.url,
        title: args.title,
        source_id: args.source_id,
      })
      if (!args.wait) {
        return text({ ...res, note: res.duplicate ? "Already saved under this source_id." : "Queued. Facts appear within a few seconds." })
      }
      const out = await waitForAdd(db, res, DEFAULT_WAIT_MS, extra.signal)
      if (!out.settled) return text({ ...res, status: out.status, note: "Still processing after 60 s. Facts will appear when it finishes." })
      return text({
        id: out.id,
        container: out.container,
        status: out.status,
        duplicate: out.duplicate,
        ...(out.error ? { error: out.error } : {}),
        added: out.added?.map((m) => ({ id: m.id, fact: m.fact, kind: m.kind, ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}) })),
        updated: out.updated?.map((m) => ({ id: m.id, fact: m.fact, kind: m.kind, replaces: m.supersedesId, ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}) })),
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
      const res = await search(db, {
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
          ...(r.type === "memory" ? { kind: r.kind } : { document: r.documentTitle }),
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
      const p = await profile(db, { container: args.container || def(), q: args.query })
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
      const res = list(db, {
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
          ...(m.reviewNote ? { reviewNote: m.reviewNote } : {}),
          createdAt: new Date(m.createdAt).toISOString(),
        })),
        nextCursor: res.nextCursor,
        containers: args.cursor ? undefined : listContainers(db).map((c) => c.container),
      })
    },
  )

  return server
}

// Stateless: a fresh server and transport per request, JSON responses.
export async function handleMcp(db: DB, request: Request): Promise<Response> {
  const server = buildMcpServer(db)
  const transport = new WebStandardStreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
  await server.connect(transport)
  try {
    return await transport.handleRequest(request)
  } finally {
    void server.close()
  }
}
