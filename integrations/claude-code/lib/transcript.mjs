// Reads a Claude Code transcript (JSONL) and builds the compact chat that the
// SessionEnd worker sends to Recall. Only the user's prompts and Claude's prose
// replies go in: no tool calls, no tool output, no thinking, no code blocks.
// The transcript format is internal to Claude Code, so parsing is defensive.
import { redact, truncate } from "./common.mjs"

const NOISE_PREFIXES = ["<command-name>", "<command-message>", "<local-command", "<bash-input>", "<bash-stdout>", "Caveat:", "[Request interrupted"]

function cleanText(text) {
  return String(text ?? "")
    .replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, "")
    .replace(/<recall-memory[\s\S]*?<\/recall-memory>/g, "")
    .replace(/```[\s\S]*?```/g, "[code omitted]")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}

function textBlocks(content) {
  if (typeof content === "string") return [content]
  if (!Array.isArray(content)) return []
  return content.filter((b) => b && b.type === "text" && typeof b.text === "string").map((b) => b.text)
}

// [{ role: "user" | "assistant", text }] in order, consecutive assistant texts merged.
export function parseTranscript(jsonl) {
  const turns = []
  for (const line of String(jsonl ?? "").split("\n")) {
    if (!line.trim()) continue
    let e
    try {
      e = JSON.parse(line)
    } catch {
      continue
    }
    if (!e || e.isSidechain || e.isMeta || (e.type !== "user" && e.type !== "assistant")) continue
    const content = e.message?.content
    // A user entry made only of tool results is tool output, not a prompt.
    if (e.type === "user" && Array.isArray(content) && content.some((b) => b?.type === "tool_result")) continue
    const text = cleanText(textBlocks(content).join("\n"))
    if (!text || NOISE_PREFIXES.some((p) => text.startsWith(p))) continue
    const last = turns[turns.length - 1]
    if (e.type === "assistant" && last?.role === "assistant") last.text += `\n\n${text}`
    else turns.push({ role: e.type, text })
  }
  return turns
}

export function stats(turns) {
  const prompts = turns.filter((t) => t.role === "user")
  return { prompts: prompts.length, promptChars: prompts.reduce((n, t) => n + t.text.length, 0), turns: turns.length }
}

// Keeps the first prompt (it usually states the task) and as much of the tail
// as fits in maxChars. Every message is redacted and capped.
export function buildMessages(turns, { maxChars = 12000, header, userRole = "user", assistantRole = "assistant" } = {}) {
  const capped = turns.map((t) => ({
    role: t.role === "user" ? userRole : assistantRole,
    content: truncate(redact(t.text), t.role === "user" ? 1500 : 1200),
  }))
  if (!capped.length) return []
  const headMsg = header ? [{ role: "note", content: header }] : []
  let used = headMsg.reduce((n, m) => n + m.content.length, 0)
  const firstIdx = turns.findIndex((t) => t.role === "user")
  const first = firstIdx >= 0 ? capped[firstIdx] : null
  if (first) used += first.content.length
  const tail = []
  for (let i = capped.length - 1; i > firstIdx; i--) {
    const m = capped[i]
    if (used + m.content.length > maxChars) break
    tail.unshift(m)
    used += m.content.length
  }
  // Anything before the first prompt is assistant noise and is dropped.
  const skipped = capped.length - (first ? 1 : 0) - tail.length - Math.max(0, firstIdx)
  const gap = skipped > 0 ? [{ role: "note", content: `(${skipped} earlier messages left out)` }] : []
  return [...headMsg, ...(first ? [first] : []), ...gap, ...tail]
}
