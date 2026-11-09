import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js"
import { z } from "zod"
import { addContent, list, profile, search } from "./api"
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
        "Recall is a long-term memory. Call search_memories or get_profile before answering questions about the user, their preferences or past work. Call add_memory when the user shares something worth remembering. Newer facts automatically supersede older ones.",
    },
  )

  server.registerTool(
    "add_memory",
    {
      title: "Add memory",
      description:
        "Save text, a conversation excerpt or a URL. Facts are extracted in the background (a few seconds) and contradicting older facts are superseded. Returns the document id.",
      inputSchema: {
        content: z.string().max(1_000_000).optional().describe("Text to remember. Send this or url."),
        url: z.url({ protocol: /^https?$/ }).optional().describe("A web page or PDF to fetch and remember."),
        title: z.string().max(300).optional(),
        container,
      },
      annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: true },
    },
    async (args) => {
      if (!args.content?.trim() && !args.url) return { ...text("Send content or url."), isError: true }
      const res = addContent(db, {
        container: args.container || def(),
        content: args.url ? undefined : args.content,
        url: args.url,
        title: args.title,
      })
      return text({ ...res, note: "Queued. Facts appear within a few seconds." })
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
      description: "List current memories, newest first. Pass cursor from the previous page to continue. history=true includes superseded versions.",
      inputSchema: {
        cursor: z.string().optional(),
        limit: z.number().int().min(1).max(200).optional(),
        history: z.boolean().optional(),
        container,
      },
      annotations: { readOnlyHint: true },
    },
    async (args) => {
      const res = list(db, { container: args.container || def(), cursor: args.cursor, limit: args.limit ?? 50, history: args.history ?? false })
      return text({
        memories: res.memories.map((m) => ({
          id: m.id,
          fact: m.fact,
          kind: m.kind,
          current: m.validTo === null,
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
