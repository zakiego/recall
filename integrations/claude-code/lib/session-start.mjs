// SessionStart hook: fetch the profile (plus a search on the project name) and
// print it as additionalContext. Any failure or timeout prints nothing and
// exits 0, so the session always starts.
import { buildContext } from "./format.mjs"
import { config, disabled, log, parseJson, projectInfo, readKey, readStdin } from "./common.mjs"

const cfg = config()
const started = Date.now()
// Hard stop, whatever is still pending (stdin, DNS, a slow server).
setTimeout(() => {
  log(cfg, "warn", { event: "session-start", msg: "hard timeout, injected nothing", ms: Date.now() - started })
  process.exit(0)
}, cfg.startTimeoutMs + 150).unref()

async function main() {
  if (disabled(cfg) || !cfg.inject) return
  const input = parseJson(await readStdin())
  const key = readKey(cfg)
  if (!key) {
    log(cfg, "warn", { event: "session-start", msg: "no API key file", files: cfg.keyFiles })
    return
  }
  const cwd = typeof input.cwd === "string" ? input.cwd : process.cwd()
  const project = projectInfo(cwd)
  const url = new URL("/v1/profile", cfg.url)
  url.searchParams.set("container", cfg.container)
  url.searchParams.set("q", project.name)

  const res = await fetch(url, {
    headers: { authorization: `Bearer ${key}`, "user-agent": "recall-claude-code-hooks" },
    signal: AbortSignal.timeout(Math.max(100, cfg.startTimeoutMs - (Date.now() - started))),
  })
  if (!res.ok) {
    log(cfg, "warn", { event: "session-start", session: input.session_id, status: res.status, ms: Date.now() - started })
    return
  }
  const context = buildContext(await res.json(), { project: project.name, container: cfg.container, maxChars: cfg.maxContextChars })
  if (!context) {
    log(cfg, "info", { event: "session-start", session: input.session_id, msg: "empty profile", ms: Date.now() - started })
    return
  }
  // Pipes are async on macOS: wait for the flush before process.exit.
  const out = JSON.stringify({ hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: context } })
  await new Promise((resolve) => process.stdout.write(out, resolve))
  log(cfg, "info", {
    event: "session-start",
    session: input.session_id,
    source: input.source,
    project: project.name,
    chars: context.length,
    ms: Date.now() - started,
  })
}

main()
  .catch((err) => log(cfg, "warn", { event: "session-start", msg: String(err?.message ?? err), ms: Date.now() - started }))
  .finally(() => process.exit(0))
