// Detached worker started by session-end.mjs. Reads the transcript, skips
// trivial sessions, and sends one chat document to Recall with
// source_id claude-code:<session_id>, so a retry or a second SessionEnd for the
// same session never creates a duplicate. Recall's extractor decides what is
// worth keeping; this script makes no LLM call.
import fs from "node:fs"
import { config, disabled, log, projectInfo, readKey, tildify } from "./common.mjs"
import { buildMessages, parseTranscript, stats } from "./transcript.mjs"

const MAX_TRANSCRIPT_BYTES = 64 * 1024 * 1024
const cfg = config()
const started = Date.now()
setTimeout(() => process.exit(0), cfg.captureTimeoutMs * 2 + 10000).unref()

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function capture(job) {
  const base = { event: "capture", session: job.session_id }
  if (disabled(cfg) || !cfg.capture) return log(cfg, "info", { ...base, msg: "disabled" })
  if (cfg.urlError) return log(cfg, "warn", { ...base, msg: cfg.urlError })
  // The transcript is written asynchronously and can lag the last turn.
  await sleep(Number(process["env"].RECALL_HOOKS_SETTLE_MS ?? 1500))

  let size
  try {
    size = fs.statSync(job.transcript_path).size
  } catch {
    return log(cfg, "warn", { ...base, msg: "transcript not found" })
  }
  if (size > MAX_TRANSCRIPT_BYTES) return log(cfg, "warn", { ...base, msg: "transcript too large, skipped", bytes: size })

  const turns = parseTranscript(fs.readFileSync(job.transcript_path, "utf8"))
  const s = stats(turns)
  if (s.prompts < Math.max(1, cfg.minPrompts) || s.promptChars < 20) {
    return log(cfg, "info", { ...base, msg: "trivial session, skipped", prompts: s.prompts, minPrompts: cfg.minPrompts })
  }

  const key = readKey(cfg)
  if (!key) return log(cfg, "warn", { ...base, msg: "no usable API key file" })

  const project = job.cwd ? projectInfo(job.cwd) : { name: "unknown", branch: null }
  // Local date, so a late-night session gets the day the user saw.
  const now = new Date()
  const day = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`
  // The branch and path stay in metadata, out of the text the extractor reads,
  // so they don't come back as facts.
  const header =
    `Claude Code coding session in project "${project.name}", ended ${day}. ` +
    `"user" is the person this memory is about; "assistant" is Claude Code. Tool calls, tool output and code are left out. ` +
    `Keep only lasting decisions, preferences and project facts. ` +
    `Do not keep transient work status: branch names, PR or issue numbers, what is committed, pushed, merged or still a draft, ` +
    `merge order, or what was fixed locally but not committed. It goes stale within days.`
  const messages = buildMessages(turns, { maxChars: cfg.maxSummaryChars, header })

  const body = JSON.stringify({
    container: cfg.container,
    messages,
    title: `Claude Code: ${project.name} (${day})`,
    source_id: `claude-code:${job.session_id}`,
    metadata: {
      source: "claude-code",
      session_id: job.session_id,
      project: project.name,
      branch: project.branch,
      cwd: tildify(job.cwd, cfg.home),
      end_reason: job.reason,
      prompts: s.prompts,
    },
  })

  for (let attempt = 1; attempt <= 3; attempt++) {
    try {
      const res = await fetch(new URL("/v1/memories", cfg.url), {
        method: "POST",
        headers: { authorization: `Bearer ${key}`, "content-type": "application/json", "user-agent": "recall-claude-code-hooks" },
        body,
        signal: AbortSignal.timeout(cfg.captureTimeoutMs),
      })
      if (res.ok) {
        const out = await res.json().catch(() => ({}))
        return log(cfg, "info", {
          ...base,
          msg: out.duplicate ? "already stored" : "stored",
          document: out.id,
          status: res.status,
          prompts: s.prompts,
          messages: messages.length,
          chars: body.length,
          attempt,
          ms: Date.now() - started,
        })
      }
      // 4xx other than 429 won't get better on retry.
      if (res.status < 500 && res.status !== 429) return log(cfg, "warn", { ...base, msg: "rejected", status: res.status })
      log(cfg, "warn", { ...base, msg: "server error, retrying", status: res.status, attempt })
    } catch (err) {
      log(cfg, "warn", { ...base, msg: "request failed", error: String(err?.message ?? err), attempt })
    }
    if (attempt < 3) await sleep(attempt * Number(process["env"].RECALL_HOOKS_RETRY_MS ?? 2000))
  }
  log(cfg, "warn", { ...base, msg: "gave up" })
}

let job = null
try {
  job = JSON.parse(process.argv[2] ?? "")
} catch {
  // fall through
}
if (job?.session_id && job?.transcript_path) {
  capture(job)
    .catch((err) => log(cfg, "warn", { event: "capture", session: job.session_id, msg: String(err?.message ?? err) }))
    .finally(() => process.exit(0))
} else {
  log(cfg, "warn", { event: "capture", msg: "bad job argument" })
}
