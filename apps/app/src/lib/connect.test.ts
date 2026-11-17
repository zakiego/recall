import { execFileSync } from "node:child_process"
import { describe, expect, it } from "vitest"
import {
  agentInstructions,
  claudeCodeCommand,
  claudeDesktopConfig,
  genericMcpConfig,
  KEY_PLACEHOLDER,
  keyNameFor,
  mcpUrl,
  restExamples,
  setupPrompt,
  shellQuote,
} from "./connect"

const ORIGIN = "https://recall.zakiego.com"

describe("connect snippets", () => {
  it("builds the Claude Code command from the origin and key", () => {
    expect(claudeCodeCommand(`${ORIGIN}/`, "rcl_abc")).toBe(
      'claude mcp add --transport http recall https://recall.zakiego.com/mcp --header "Authorization: Bearer rcl_abc"',
    )
  })

  it("bridges Claude Desktop through mcp-remote with the header in an env var", () => {
    const cfg = JSON.parse(claudeDesktopConfig(ORIGIN, KEY_PLACEHOLDER))
    expect(cfg.mcpServers.recall).toEqual({
      command: "npx",
      args: ["-y", "mcp-remote", `${ORIGIN}/mcp`, "--header", "Authorization:${AUTH_HEADER}"],
      env: { AUTH_HEADER: `Bearer ${KEY_PLACEHOLDER}` },
    })
  })

  it("adds --allow-http only for plain http on a non-local host", () => {
    expect(JSON.parse(claudeDesktopConfig("http://127.0.0.1:4800", "k")).mcpServers.recall.args).not.toContain("--allow-http")
    expect(JSON.parse(claudeDesktopConfig("http://localhost:4800", "k")).mcpServers.recall.args).not.toContain("--allow-http")
    expect(JSON.parse(claudeDesktopConfig("http://recall.lan:4800", "k")).mcpServers.recall.args).toContain("--allow-http")
  })

  it("writes the generic MCP config with a bearer header", () => {
    expect(JSON.parse(genericMcpConfig(ORIGIN, "rcl_x")).mcpServers.recall).toEqual({
      type: "http",
      url: `${ORIGIN}/mcp`,
      headers: { Authorization: "Bearer rcl_x" },
    })
  })

  it("strips trailing slashes from the origin", () => {
    expect(mcpUrl("http://localhost:4800///")).toBe("http://localhost:4800/mcp")
  })

  it("names keys generally, after the person and the day", () => {
    const day = new Date(2026, 10, 10)
    expect(keyNameFor("zaki", day)).toBe("agent-zaki-1110")
    expect(keyNameFor("x".repeat(100), day).length).toBeLessThanOrEqual(80)
  })
})

describe("shell quoting", () => {
  it("survives a container with quotes and spaces", () => {
    const value = `it's "odd" $HOME`
    const out = execFileSync("sh", ["-c", `printf %s ${shellQuote(value)}`], { encoding: "utf8" })
    expect(out).toBe(value)
  })

  it("puts the container into valid JSON bodies", () => {
    const text = restExamples(ORIGIN, "rcl_k", `team "a"`)
    expect(text).toContain("profile?container=team%20%22a%22")
    expect(restExamples(ORIGIN, "k", "hi!")).toContain("container=hi%21")
    const body = text.split("\n").find((l) => l.includes("/v1/search"))!
    expect(body).toBeTruthy()
    const dLine = text.split("\n").find((l) => l.trim().startsWith("-d") && l.includes('"q"'))!
    const json = execFileSync("sh", ["-c", `printf %s ${dLine.trim().slice(3)}`], { encoding: "utf8" })
    expect(JSON.parse(json)).toEqual({ container: `team "a"`, q: "running shoes" })
  })
})

describe("agent instructions", () => {
  it("names the container and the MCP tools", () => {
    const text = agentInstructions("mcp", "zaki", ORIGIN)
    for (const tool of ["get_playbook", "get_profile", "search_memories", "ask_memory", "add_memory"]) expect(text).toContain(tool)
    expect(text).toContain('container "zaki"')
    expect(text).toMatch(/Never store secrets/)
  })

  it("points the REST flavor at the origin and RECALL_KEY, never at a key", () => {
    const text = agentInstructions("rest", "project-recall", `${ORIGIN}/`)
    expect(text).toContain(`${ORIGIN}/v1/search`)
    expect(text).toContain("$RECALL_KEY")
    expect(text).not.toMatch(/rcl_/)
  })

  it("has no em dashes", () => {
    expect(agentInstructions("mcp", "a", ORIGIN) + agentInstructions("rest", "a", ORIGIN) + restExamples(ORIGIN, "k", "a")).not.toContain("\u2014")
  })
})

describe("setup prompt", () => {
  it("has Claude Code connect itself, then save the MCP rules", () => {
    const text = setupPrompt("claude-code", ORIGIN, "rcl_abc", "zaki")
    expect(text).toContain(`claude mcp add -s user --transport http recall ${ORIGIN}/mcp --header "Authorization: Bearer rcl_abc"`)
    expect(text).toContain("~/.claude/CLAUDE.md")
    expect(text).toContain('Pass container "zaki"')
    expect(text).toMatch(/Never write the API key into a file/)
  })

  it("keeps the key out of the rules block", () => {
    for (const client of ["claude-code", "claude-desktop", "mcp", "rest"] as const) {
      const text = setupPrompt(client, ORIGIN, "rcl_secret", "zaki")
      const rules = text.slice(text.indexOf("Memory rules:"))
      expect(text).toContain("rcl_secret")
      expect(rules).not.toContain("rcl_secret")
      expect(text).not.toContain("\u2014")
    }
  })

  it("gives REST agents the REST rules and the Desktop prompt the bridge config", () => {
    expect(setupPrompt("rest", ORIGIN, "k", "a")).toContain(`${ORIGIN}/v1/search`)
    expect(setupPrompt("claude-desktop", ORIGIN, "k", "a")).toContain('"mcp-remote"')
  })

  it("tells the agent to stop when the key is still the placeholder", () => {
    expect(setupPrompt("claude-code", ORIGIN, KEY_PLACEHOLDER, "a")).toMatch(/Stop and ask me for a real key/)
    expect(setupPrompt("claude-code", ORIGIN, "rcl_real", "a")).not.toMatch(/Stop and ask me/)
  })
})
