// Snippets for the Connect page: the exact config each client needs for this
// server, a key and a container. Pure functions, so the page and the tests
// build the same strings.

export const KEY_PLACEHOLDER = "rcl_YOUR_KEY"

export const CLIENTS = [
  { id: "claude-code", label: "Claude Code" },
  { id: "claude-desktop", label: "Claude Desktop" },
  { id: "mcp", label: "Other MCP clients" },
  { id: "rest", label: "REST" },
] as const

export type ClientId = (typeof CLIENTS)[number]["id"]
export const CLIENT_IDS = CLIENTS.map((c) => c.id) as [ClientId, ...ClientId[]]

export function clientLabel(id: ClientId) {
  return CLIENTS.find((c) => c.id === id)!.label
}

// "agent-zaki-1110": a general name, since one key works for every client on
// the page, plus who made it and the month and day so the key list tells keys
// apart.
export function keyBase(username: string) {
  return `agent-${username.slice(0, 60)}`
}

export function keyNameFor(username: string, now = new Date()) {
  const day = `${String(now.getMonth() + 1).padStart(2, "0")}${String(now.getDate()).padStart(2, "0")}`
  return `${keyBase(username)}-${day}`
}

export function mcpUrl(origin: string) {
  return `${origin.replace(/\/+$/, "")}/mcp`
}

// Wrap a value in single quotes for a POSIX shell.
export function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

export function claudeCodeCommand(origin: string, key: string) {
  return `claude mcp add --transport http recall ${mcpUrl(origin)} --header "Authorization: Bearer ${key}"`
}

// Claude Desktop's config file only starts local (stdio) programs, so
// mcp-remote bridges it to the HTTP endpoint. The header value goes through an
// env var because Desktop on Windows splits arguments that contain spaces.
export function claudeDesktopConfig(origin: string, key: string) {
  const url = mcpUrl(origin)
  const args = ["-y", "mcp-remote", url, "--header", "Authorization:${AUTH_HEADER}"]
  // mcp-remote refuses plain http unless the host is localhost.
  if (url.startsWith("http://") && !/^http:\/\/(localhost|127\.0\.0\.1)[:/]/.test(url)) args.push("--allow-http")
  return JSON.stringify({ mcpServers: { recall: { command: "npx", args, env: { AUTH_HEADER: `Bearer ${key}` } } } }, null, 2)
}

// The shape Claude Code's .mcp.json and Cursor's mcp.json read. Other clients
// name the same fields differently, so the page also lists the raw values.
export function genericMcpConfig(origin: string, key: string) {
  return JSON.stringify(
    { mcpServers: { recall: { type: "http", url: mcpUrl(origin), headers: { Authorization: `Bearer ${key}` } } } },
    null,
    2,
  )
}

export function restExamples(origin: string, key: string, container: string) {
  const c = JSON.stringify(container)
  const json = (body: string) => shellQuote(body)
  // `!` stays encoded too: inside double quotes an interactive shell expands it.
  const q = encodeURIComponent(container).replace(/!/g, "%21")
  const auth = `-H "Authorization: Bearer $RECALL_KEY"`
  const type = `-H "Content-Type: application/json"`
  return [
    `export RECALL_URL=${shellQuote(origin.replace(/\/+$/, ""))}`,
    `export RECALL_KEY=${shellQuote(key)}`,
    "",
    "# What Recall knows about this container",
    `curl "$RECALL_URL/v1/profile?container=${q}" ${auth}`,
    "",
    "# Search facts (local, no LLM call)",
    `curl -X POST "$RECALL_URL/v1/search" ${auth} ${type} \\`,
    `  -d ${json(`{"container":${c},"q":"running shoes"}`)}`,
    "",
    "# Add something to remember (facts appear a few seconds later)",
    `curl -X POST "$RECALL_URL/v1/memories" ${auth} ${type} \\`,
    `  -d ${json(`{"container":${c},"content":"I switched to Puma running shoes."}`)}`,
    "",
    "# Ask a question, answered only from stored facts",
    `curl -X POST "$RECALL_URL/v1/ask" ${auth} ${type} \\`,
    `  -d ${json(`{"container":${c},"question":"Which running shoes do I wear?"}`)}`,
  ].join("\n")
}

export type InstructionFlavor = "mcp" | "rest"

// Paste-ready instructions for a system prompt, CLAUDE.md or project
// instructions. They never contain the key: those files get committed and
// shared, so the REST flavor reads it from RECALL_KEY.
export function agentInstructions(flavor: InstructionFlavor, container: string, origin: string) {
  const c = JSON.stringify(container)
  const base = origin.replace(/\/+$/, "")
  const intro =
    flavor === "mcp"
      ? [
          "## Memory (Recall)",
          "",
          `You have long-term memory through the Recall MCP server. Pass container ${c} on every Recall tool call.`,
        ]
      : [
          "## Memory (Recall)",
          "",
          `You have long-term memory through the Recall REST API at ${base}. Send \`Authorization: Bearer $RECALL_KEY\` on every request (the key is in the RECALL_KEY environment variable) and use container ${c}.`,
        ]

  const read =
    flavor === "mcp"
      ? [
          `- At the start of a task about the user, their work or their preferences, call get_profile (container ${c}). Pass a query to focus it.`,
          "- Before answering anything that depends on what you know about the user, call search_memories with a short query.",
          '- For a direct question memory should answer ("what did I decide about X?"), call ask_memory. It answers only from stored facts and cites them. covered: false means memory doesn\'t know, so say so instead of guessing.',
        ]
      : [
          `- At the start of a task about the user, their work or their preferences, call GET ${base}/v1/profile?container=${encodeURIComponent(container)} (add &q=... to focus it).`,
          `- Before answering anything that depends on what you know about the user, call POST ${base}/v1/search with {"container":${c},"q":"<short query>"}.`,
          `- For a direct question memory should answer ("what did I decide about X?"), call POST ${base}/v1/ask with {"container":${c},"question":"..."}. It answers only from stored facts and cites them. covered: false means memory doesn't know, so say so instead of guessing.`,
        ]

  const write =
    flavor === "mcp"
      ? `- When the user shares something lasting (a preference, a decision, a fact about them, their work or the people they mention), call add_memory once with a short plain statement. Recall extracts the facts and replaces outdated ones itself.`
      : `- When the user shares something lasting (a preference, a decision, a fact about them, their work or the people they mention), call POST ${base}/v1/memories once with {"container":${c},"content":"<short plain statement>"}. Recall extracts the facts and replaces outdated ones itself.`

  return [
    ...intro,
    "",
    "When to read:",
    ...read,
    "- Prefer what memory says over your own assumptions. If the user contradicts it, the user wins: store the correction.",
    "",
    "When to write:",
    write,
    "- Don't store small talk, guesses you weren't told, or things memory already has.",
    "- Never store secrets: passwords, API keys, tokens, private keys, one-time codes, card or bank numbers. If the user shares one, leave it out.",
    "",
    "Use memory quietly. Don't narrate these calls unless the user asks.",
  ].join("\n")
}

// What to paste into the agent itself: connect to Recall, then save the memory
// rules into its own instructions. It carries the key once one is created, so
// it tells the agent to keep the key out of every file it writes.
export function setupPrompt(client: ClientId, origin: string, key: string, container: string) {
  const base = origin.replace(/\/+$/, "")
  const rules = agentInstructions(client === "rest" ? "rest" : "mcp", container, origin)
  const keep = "Never write the API key into a file that gets committed or shared."
  const indent = (text: string) => text.split("\n").map((l) => `   ${l}`)

  const steps: Record<ClientId, string[]> = {
    "claude-code": [
      "1. Connect to Recall. Run:",
      "",
      `   claude mcp add -s user --transport http recall ${mcpUrl(origin)} --header "Authorization: Bearer ${key}"`,
      "",
      "   Then run `claude mcp list` and check that recall shows Connected. The tools load in the next session.",
      `2. Append the text inside the md fence below (not the fence lines) to ~/.claude/CLAUDE.md. Create the file if it's missing and keep what's there. ${keep}`,
    ],
    "claude-desktop": [
      "1. Connect Claude Desktop to Recall. Its config file only starts local programs, so mcp-remote bridges to the server (it needs Node.js). Merge this `recall` entry into `mcpServers` in claude_desktop_config.json (macOS: ~/Library/Application Support/Claude/claude_desktop_config.json, Windows: %APPDATA%\\Claude\\claude_desktop_config.json) and keep the other servers:",
      "",
      ...indent(claudeDesktopConfig(origin, key)),
      "",
      "   Then tell me to restart Claude Desktop.",
      `2. Add the text inside the md fence below (not the fence lines) to my personal preferences or to this project's instructions. ${keep}`,
    ],
    mcp: [
      "1. Connect to Recall. Add a remote MCP server named `recall` to your user-level MCP config (for example ~/.cursor/mcp.json) over streamable HTTP. If you only have a project file like .mcp.json, read the key from an environment variable there instead of writing it in:",
      "",
      `   URL: ${mcpUrl(origin)}`,
      `   Header: Authorization: Bearer ${key}`,
      "",
      "   Reload your MCP servers and check that the recall tools are listed.",
      `2. Append the text inside the md fence below (not the fence lines) to your instructions file (AGENTS.md, CLAUDE.md or your rules file) and keep what's there. ${keep}`,
    ],
    rest: [
      `1. Put this API key in the RECALL_KEY environment variable, somewhere private like a secret store or a gitignored env file: ${key}`,
      `   Check it with: curl -s "${base}/v1/containers" -H "Authorization: Bearer $RECALL_KEY"`,
      `2. Append the text inside the md fence below (not the fence lines) to your instructions file (AGENTS.md, CLAUDE.md or your system prompt) and keep what's there. ${keep}`,
    ],
  }

  return [
    "Set up Recall as your long-term memory. Do each step yourself. If you can't do one (no shell or file access), tell me exactly what to paste where. Then tell me what you did.",
    "",
    ...(key === KEY_PLACEHOLDER ? [`The API key below is still the placeholder ${KEY_PLACEHOLDER}. Stop and ask me for a real key before step 1.`, ""] : []),
    ...steps[client],
    "",
    "Memory rules:",
    "",
    "```md",
    rules,
    "```",
  ].join("\n")
}
