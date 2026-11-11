import fs from "node:fs"
import http from "node:http"
import os from "node:os"
import path from "node:path"
import { spawn } from "node:child_process"
import { fileURLToPath } from "node:url"

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
export const FAKE_KEY = "rcl_testkey_0123456789abcdef"

// A stand-in Recall server. handler(req, body) returns { status, json, delayMs }.
export async function fakeServer(handler) {
  const requests = []
  const server = http.createServer(async (req, res) => {
    let body = ""
    for await (const c of req) body += c
    const entry = { method: req.method, url: req.url, headers: req.headers, body }
    requests.push(entry)
    const r = (await handler(entry, requests.length)) ?? { status: 404, json: { error: "not_found" } }
    if (r.delayMs) await new Promise((ok) => setTimeout(ok, r.delayMs))
    if (res.destroyed) return
    res.writeHead(r.status ?? 200, { "content-type": "application/json" })
    res.end(JSON.stringify(r.json ?? {}))
  })
  await new Promise((ok) => server.listen(0, "127.0.0.1", ok))
  const url = `http://127.0.0.1:${server.address().port}`
  return {
    url,
    requests,
    close: () =>
      new Promise((ok) => {
        server.closeAllConnections?.()
        server.close(ok)
      }),
  }
}

// A throwaway HOME with a key file, so nothing touches the real one.
export function tempHome({ key = FAKE_KEY } = {}) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "recall-hooks-test-"))
  if (key) {
    fs.mkdirSync(path.join(home, ".config", "recall"), { recursive: true })
    fs.writeFileSync(path.join(home, ".config", "recall", "hooks-key"), key + "\n", { mode: 0o600 })
  }
  return home
}

export function hookEnv(home, url, extra = {}) {
  const base = { ...process["env"] }
  for (const k of Object.keys(base)) if (k.startsWith("RECALL_HOOKS_")) delete base[k]
  return { ...base, HOME: home, RECALL_HOOKS_URL: url, RECALL_HOOKS_SETTLE_MS: "0", RECALL_HOOKS_RETRY_MS: "50", ...extra }
}

export function run(cmd, args, { env, input = "", cwd } = {}) {
  return new Promise((resolve) => {
    const started = Date.now()
    const child = spawn(cmd, args, { env, cwd })
    let stdout = ""
    let stderr = ""
    child.stdout.on("data", (d) => (stdout += d))
    child.stderr.on("data", (d) => (stderr += d))
    child.on("close", (code) => resolve({ code, stdout, stderr, ms: Date.now() - started }))
    // A hook that exits before reading stdin (kill switch, unknown event) closes the pipe early.
    child.stdin.on("error", (err) => {
      if (err.code !== "EPIPE") throw err
    })
    child.stdin.end(input)
  })
}

export async function waitFor(fn, timeoutMs = 8000) {
  const end = Date.now() + timeoutMs
  while (Date.now() < end) {
    const v = await fn()
    if (v) return v
    await new Promise((ok) => setTimeout(ok, 50))
  }
  return null
}

export function readLog(home) {
  const file = path.join(home, ".cache", "recall-hooks.log")
  if (!fs.existsSync(file)) return []
  return fs
    .readFileSync(file, "utf8")
    .split("\n")
    .filter(Boolean)
    .map((l) => JSON.parse(l))
}

// Transcript lines in the shapes Claude Code writes.
export const t = {
  user: (text) => ({ type: "user", isSidechain: false, message: { role: "user", content: text } }),
  meta: (text) => ({ type: "user", isMeta: true, message: { role: "user", content: [{ type: "text", text }] } }),
  toolResult: (text) => ({ type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "x", content: text }] } }),
  assistant: (text) => ({ type: "assistant", message: { role: "assistant", content: [{ type: "text", text }] } }),
  thinking: (text) => ({ type: "assistant", message: { role: "assistant", content: [{ type: "thinking", thinking: text }] } }),
  toolUse: (cmd) => ({ type: "assistant", message: { role: "assistant", content: [{ type: "tool_use", name: "Bash", input: { command: cmd } }] } }),
  sidechain: (text) => ({ type: "assistant", isSidechain: true, message: { role: "assistant", content: [{ type: "text", text }] } }),
  other: () => ({ type: "attachment", attachment: { type: "hook_success", content: "noise" } }),
}

export function writeTranscript(dir, lines) {
  const file = path.join(dir, "transcript.jsonl")
  fs.writeFileSync(file, lines.map((l) => JSON.stringify(l)).join("\n") + "\n")
  return file
}
