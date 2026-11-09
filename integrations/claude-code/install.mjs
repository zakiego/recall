#!/usr/bin/env node
// Installs, removes or checks the Recall Claude Code hooks on this machine.
//
//   node integrations/claude-code/install.mjs              install or update (idempotent)
//   node integrations/claude-code/install.mjs --uninstall  remove the hooks and the installed files
//   node integrations/claude-code/install.mjs --status     show what's installed
//
// Options: --no-key (don't try to create ~/.config/recall/hooks-key),
// --settings <file> and --dir <dir> (override the targets, used by the tests).
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { execFileSync, spawnSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const HERE = path.dirname(fileURLToPath(import.meta.url))
const HOOK_RE = /recall-hook["']?\s+session-(start|end)\b/

export function hookCommand(dir, home, event) {
  // The $HOME form keeps the command identical on both Macs.
  const shown = dir === defaultDir(home) ? '"$HOME/.local/share/recall-hooks/bin/recall-hook"' : JSON.stringify(path.join(dir, "bin", "recall-hook"))
  return `${shown} ${event}`
}

export function defaultDir(home) {
  return path.join(home, ".local", "share", "recall-hooks")
}

const isOurs = (h) => typeof h?.command === "string" && HOOK_RE.test(h.command)

// Drops our hook entries and leaves every other hook untouched. Groups that end
// up empty because only our hook was in them are removed.
export function removeHooks(settings) {
  const out = structuredClone(settings ?? {})
  if (!out.hooks || typeof out.hooks !== "object") return out
  for (const event of Object.keys(out.hooks)) {
    const groups = Array.isArray(out.hooks[event]) ? out.hooks[event] : []
    const kept = []
    for (const g of groups) {
      const hooks = Array.isArray(g?.hooks) ? g.hooks : []
      const rest = hooks.filter((h) => !isOurs(h))
      if (rest.length === hooks.length) kept.push(g)
      else if (rest.length) kept.push({ ...g, hooks: rest })
    }
    if (kept.length) out.hooks[event] = kept
    else delete out.hooks[event]
  }
  if (!Object.keys(out.hooks).length) delete out.hooks
  return out
}

export function addHooks(settings, { dir, home }) {
  const out = removeHooks(settings)
  out.hooks ??= {}
  out.hooks.SessionStart = [
    ...(out.hooks.SessionStart ?? []),
    { hooks: [{ type: "command", command: hookCommand(dir, home, "session-start"), timeout: 3 }] },
  ]
  out.hooks.SessionEnd = [...(out.hooks.SessionEnd ?? []), { hooks: [{ type: "command", command: hookCommand(dir, home, "session-end") }] }]
  return out
}

function readSettings(file) {
  if (!fs.existsSync(file)) return {}
  const text = fs.readFileSync(file, "utf8")
  if (!text.trim()) return {}
  return JSON.parse(text) // a parse error stops the install before anything is written
}

function backup(file) {
  if (!fs.existsSync(file)) return null
  const stamp = new Date().toISOString().replace(/[:.]/g, "-")
  const dest = `${file}.recall-backup-${stamp}`
  fs.copyFileSync(file, dest)
  return dest
}

function writeAtomic(file, data) {
  const mode = fs.existsSync(file) ? fs.statSync(file).mode & 0o777 : 0o644
  fs.mkdirSync(path.dirname(file), { recursive: true })
  const tmp = `${file}.recall-tmp-${process.pid}`
  fs.writeFileSync(tmp, data, { mode })
  fs.renameSync(tmp, file)
}

function copyFiles(dir) {
  fs.mkdirSync(path.join(dir, "bin"), { recursive: true })
  fs.mkdirSync(path.join(dir, "lib"), { recursive: true })
  fs.copyFileSync(path.join(HERE, "bin", "recall-hook"), path.join(dir, "bin", "recall-hook"))
  fs.chmodSync(path.join(dir, "bin", "recall-hook"), 0o755)
  const libs = fs.readdirSync(path.join(HERE, "lib")).filter((f) => f.endsWith(".mjs"))
  for (const f of fs.readdirSync(path.join(dir, "lib"))) if (!libs.includes(f)) fs.rmSync(path.join(dir, "lib", f))
  for (const f of libs) fs.copyFileSync(path.join(HERE, "lib", f), path.join(dir, "lib", f))
  fs.writeFileSync(path.join(dir, "node-path"), process.execPath + "\n")
  let version = "unknown"
  try {
    version = execFileSync("git", ["-C", HERE, "rev-parse", "--short", "HEAD"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim()
  } catch {
    // not a git checkout
  }
  fs.writeFileSync(path.join(dir, "VERSION"), version + "\n")
  return version
}

// Creates a dedicated workspace key with the bundled CLI in the production
// container. The key goes straight to a 600 file and is never printed.
function createKey(home, keyFile) {
  const selfhost = path.join(home, "selfhost", "recall")
  if (!fs.existsSync(path.join(selfhost, "compose.yaml")) && !fs.existsSync(path.join(selfhost, "docker-compose.yml"))) {
    return "skipped: no ~/selfhost/recall on this machine (see the second-Mac steps in the README)"
  }
  const r = spawnSync(
    "docker",
    ["compose", "exec", "-T", "app", "node", ".output/server/cli.mjs", "create-key", "claude-code-hooks", "--workspace", "zaki", "--quiet"],
    { cwd: selfhost, encoding: "utf8", timeout: 60000 },
  )
  const key = (r.stdout ?? "").trim()
  if (r.status !== 0 || !/^rcl_\S+$/.test(key)) return `failed: create-key exited ${r.status} (${(r.stderr ?? "").trim().slice(0, 200)})`
  fs.mkdirSync(path.dirname(keyFile), { recursive: true, mode: 0o700 })
  fs.writeFileSync(keyFile, key + "\n", { mode: 0o600, flag: "wx" })
  fs.chmodSync(keyFile, 0o600)
  return "created key claude-code-hooks (workspace zaki)"
}

function parseArgs(argv) {
  const a = { mode: "install", key: true, settings: null, dir: null }
  for (let i = 0; i < argv.length; i++) {
    const v = argv[i]
    if (v === "--uninstall") a.mode = "uninstall"
    else if (v === "--status") a.mode = "status"
    else if (v === "--no-key") a.key = false
    else if (v === "--settings") a.settings = argv[++i]
    else if (v === "--dir") a.dir = argv[++i]
    else if (v === "-h" || v === "--help") a.mode = "help"
    else throw new Error(`unknown option ${v}`)
  }
  return a
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  const home = process["env"].HOME || os.homedir()
  const settingsFile = args.settings ?? path.join(home, ".claude", "settings.json")
  const dir = args.dir ?? defaultDir(home)
  const keyFile = path.join(home, ".config", "recall", "hooks-key")
  const fallbackKey = path.join(home, ".config", "recall", "api-key")
  const say = (s) => process.stdout.write(s + "\n")

  if (args.mode === "help") {
    say(fs.readFileSync(fileURLToPath(import.meta.url), "utf8").split("\n").slice(1, 9).join("\n").replace(/^\/\/ ?/gm, ""))
    return
  }

  if (args.mode === "status") {
    const s = readSettings(settingsFile)
    const ours = Object.entries(s.hooks ?? {}).flatMap(([ev, gs]) => (gs ?? []).flatMap((g) => (g.hooks ?? []).filter(isOurs).map(() => ev)))
    say(`settings: ${settingsFile}: ${ours.length ? ours.join(", ") : "no Recall hooks"}`)
    say(`files: ${dir}: ${fs.existsSync(path.join(dir, "bin", "recall-hook")) ? `installed (${fs.readFileSync(path.join(dir, "VERSION"), "utf8").trim()})` : "not installed"}`)
    say(`key: ${fs.existsSync(keyFile) ? keyFile : fs.existsSync(fallbackKey) ? `${fallbackKey} (fallback, no hooks-key)` : "none"}`)
    const off = fs.existsSync(path.join(home, ".config", "recall", "hooks-disabled"))
    say(`kill switch: ${off ? "ON (~/.config/recall/hooks-disabled exists)" : "off"}`)
    say(`log: ${path.join(home, ".cache", "recall-hooks.log")}`)
    return
  }

  const before = readSettings(settingsFile)
  if (args.mode === "uninstall") {
    const after = removeHooks(before)
    if (JSON.stringify(after) !== JSON.stringify(before)) {
      const saved = backup(settingsFile)
      writeAtomic(settingsFile, JSON.stringify(after, null, 2) + "\n")
      say(`removed the Recall hooks from ${settingsFile} (backup: ${saved})`)
    } else {
      say(`no Recall hooks in ${settingsFile}`)
    }
    if (fs.existsSync(dir)) {
      fs.rmSync(dir, { recursive: true, force: true })
      say(`deleted ${dir}`)
    }
    say(`kept ${keyFile} and the log. Revoke the key with: pnpm cli list-keys --workspace zaki, then pnpm cli revoke-key <id>`)
    return
  }

  const version = copyFiles(dir)
  say(`installed hook files to ${dir} (${version})`)
  if (fs.existsSync(keyFile)) say(`key: ${keyFile} exists, kept`)
  else if (args.key) say(`key: ${createKey(home, keyFile)}`)
  else say("key: --no-key given")
  if (!fs.existsSync(keyFile)) say(`key: hooks will fall back to ${fallbackKey}${fs.existsSync(fallbackKey) ? "" : " (missing too, hooks will do nothing)"}`)

  const after = addHooks(before, { dir, home })
  if (JSON.stringify(after) === JSON.stringify(before)) {
    say(`settings: ${settingsFile} already up to date`)
  } else {
    const saved = backup(settingsFile)
    writeAtomic(settingsFile, JSON.stringify(after, null, 2) + "\n")
    say(`settings: added SessionStart and SessionEnd hooks to ${settingsFile}${saved ? ` (backup: ${saved})` : ""}`)
  }
  say("kill switch: touch ~/.config/recall/hooks-disabled (or RECALL_HOOKS_DISABLED=1)")
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main()
  } catch (err) {
    process.stderr.write(`recall hooks: ${err.message}\n`)
    process.exit(1)
  }
}
