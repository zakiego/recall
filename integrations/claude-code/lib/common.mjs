// Shared bits for the Recall Claude Code hooks: config, the API key, the kill
// switch, the log, redaction and the project name. No dependencies, Node 18+.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { execFileSync } from "node:child_process"

const ENV = process["env"]

export function config() {
  const home = ENV.HOME || os.homedir()
  const confDir = path.join(home, ".config", "recall")
  const url = (ENV.RECALL_HOOKS_URL || "https://recall.zakiego.com").replace(/\/+$/, "")
  return {
    home,
    url,
    urlError: checkUrl(url),
    container: ENV.RECALL_HOOKS_CONTAINER || "zaki",
    keyFiles: ENV.RECALL_HOOKS_KEY_FILE
      ? [ENV.RECALL_HOOKS_KEY_FILE]
      : [path.join(confDir, "hooks-key"), path.join(confDir, "api-key")],
    disableFile: ENV.RECALL_HOOKS_DISABLE_FILE || path.join(confDir, "hooks-disabled"),
    logFile: ENV.RECALL_HOOKS_LOG || path.join(home, ".cache", "recall-hooks.log"),
    logMaxBytes: num(ENV.RECALL_HOOKS_LOG_MAX_BYTES, 512 * 1024),
    startTimeoutMs: num(ENV.RECALL_HOOKS_START_TIMEOUT_MS, 1800),
    maxContextChars: num(ENV.RECALL_HOOKS_MAX_CONTEXT_CHARS, 1500),
    minPrompts: num(ENV.RECALL_HOOKS_MIN_PROMPTS, 2),
    maxSummaryChars: num(ENV.RECALL_HOOKS_MAX_SUMMARY_CHARS, 12000),
    captureTimeoutMs: num(ENV.RECALL_HOOKS_CAPTURE_TIMEOUT_MS, 20000),
    inject: !isOff(ENV.RECALL_HOOKS_INJECT),
    capture: !isOff(ENV.RECALL_HOOKS_CAPTURE),
  }
}

const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"])

// The bearer key rides on every request, so plain http is only allowed to this
// machine. Returns why the URL is refused, or null when it's fine.
export function checkUrl(raw) {
  let u
  try {
    u = new URL(raw)
  } catch {
    return "RECALL_HOOKS_URL is not a valid URL"
  }
  if (u.protocol === "https:") return null
  if (u.protocol === "http:" && LOCAL_HOSTS.has(u.hostname)) return null
  if (u.protocol === "http:") return `refused http://${u.host}: the API key would go in cleartext. Use https://, or http:// only for localhost or 127.0.0.1`
  return `RECALL_HOOKS_URL must be https://, got ${u.protocol}`
}

function num(v, fallback) {
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? n : fallback
}

const isOn = (v) => /^(1|true|yes|on)$/i.test(String(v ?? "").trim())
const isOff = (v) => /^(0|false|no|off)$/i.test(String(v ?? "").trim())

// Kill switch: RECALL_HOOKS_DISABLED=1 or the file ~/.config/recall/hooks-disabled.
export function disabled(cfg) {
  return isOn(ENV.RECALL_HOOKS_DISABLED) || fs.existsSync(cfg.disableFile)
}

// First readable key file wins. Returns null when there's none, or when that
// file is open to group or others: a key others can read may already be out,
// so the hooks stop until it's fixed rather than keep using it.
export function readKey(cfg) {
  for (const file of cfg.keyFiles) {
    try {
      const key = fs.readFileSync(file, "utf8").trim()
      if (!key) continue
      if (fs.statSync(file).mode & 0o077) {
        const fix = `chmod 600 '${file.replace(/'/g, "'\\''")}'`
        log(cfg, "warn", { msg: `key file is readable by group or others, refused. Fix it with: ${fix}`, file, fix })
        return null
      }
      return key
    } catch {
      // try the next one
    }
  }
  return null
}

// One JSON line per event. Never logs the key or session text.
export function log(cfg, level, fields) {
  try {
    fs.mkdirSync(path.dirname(cfg.logFile), { recursive: true })
    try {
      if (fs.statSync(cfg.logFile).size > cfg.logMaxBytes) fs.renameSync(cfg.logFile, `${cfg.logFile}.1`)
    } catch {
      // no log yet
    }
    fs.appendFileSync(cfg.logFile, JSON.stringify({ t: new Date().toISOString(), level, ...fields }) + "\n", { mode: 0o600 })
  } catch {
    // logging must never break a hook
  }
}

export async function readStdin(limitBytes = 1024 * 1024) {
  if (process.stdin.isTTY) return ""
  const parts = []
  let size = 0
  for await (const chunk of process.stdin) {
    size += chunk.length
    if (size > limitBytes) break
    parts.push(chunk)
  }
  return Buffer.concat(parts).toString("utf8")
}

export function parseJson(text) {
  try {
    const v = JSON.parse(text)
    return v && typeof v === "object" ? v : {}
  } catch {
    return {}
  }
}

// Project name for search and titles: the git remote's repo name, then the
// repo folder, then the cwd folder. Worktrees resolve to the real repo name.
export function projectInfo(cwd) {
  const info = { name: path.basename(cwd || "") || "unknown", branch: null }
  if (!cwd) return info
  const git = (...args) => {
    try {
      return execFileSync("git", ["-C", cwd, ...args], { encoding: "utf8", timeout: 300, stdio: ["ignore", "pipe", "ignore"] }).trim()
    } catch {
      return ""
    }
  }
  const remote = git("config", "--get", "remote.origin.url")
  const fromRemote = remote.replace(/\.git$/, "").split(/[/:]/).filter(Boolean).pop()
  const top = git("rev-parse", "--show-toplevel")
  info.name = fromRemote || (top && path.basename(top)) || info.name
  info.branch = git("rev-parse", "--abbrev-ref", "HEAD") || null
  return info
}

export function tildify(p, home) {
  return p && home && p.startsWith(home) ? "~" + p.slice(home.length) : p
}

// Best-effort secret scrub before anything leaves the machine. Recall masks
// secrets again on ingest; this is the first of two layers.
const SECRET_PATTERNS = [
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
  /\b(?:rcl|zkg|sk|pk|rk)_[A-Za-z0-9_-]{12,}/g,
  /\bsk-[A-Za-z0-9_-]{16,}/g,
  /\b(?:ghp|gho|ghs|ghu|ghr)_[A-Za-z0-9]{20,}/g,
  /\bgithub_pat_[A-Za-z0-9_]{20,}/g,
  /\bxox[abprs]-[A-Za-z0-9-]{10,}/g,
  /\bAKIA[0-9A-Z]{16}\b/g,
  /\bAIza[0-9A-Za-z_-]{35}\b/g,
  /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g,
  /\b[a-z][a-z0-9+.-]*:\/\/[^\s:@/]+:[^\s@/]+@/gi, // credentials in URLs
]

export function redact(text) {
  let out = String(text ?? "")
  for (const re of SECRET_PATTERNS) out = out.replace(re, "[redacted]")
  out = out.replace(/\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{12,}/g, "$1 [redacted]")
  out = out.replace(
    /\b([A-Za-z0-9_]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_]*)(["']?\s*[:=]\s*["']?)[^\s"',;]{4,}/gi,
    "$1$2[redacted]",
  )
  // Long opaque strings with letters and digits (tokens, hashes, base64 blobs).
  out = out.replace(/[A-Za-z0-9+/_=-]{40,}/g, (m) => (/\d/.test(m) && /[A-Za-z]/.test(m) ? "[redacted]" : m))
  return out
}

export function truncate(text, max) {
  if (text.length <= max) return text
  return text.slice(0, Math.max(0, max - 1)).trimEnd() + "…"
}

// Cut at the last sentence end that fits, else hard truncate.
export function clipSentences(text, max) {
  const clean = String(text ?? "").replace(/\s+/g, " ").trim()
  if (clean.length <= max) return clean
  const head = clean.slice(0, max)
  const end = Math.max(head.lastIndexOf(". "), head.lastIndexOf("! "), head.lastIndexOf("? "))
  return end > max * 0.4 ? head.slice(0, end + 1) : truncate(clean, max)
}
