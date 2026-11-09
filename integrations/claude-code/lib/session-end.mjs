// SessionEnd hook: hand the session to a fully detached worker and exit at
// once, so closing Claude Code never waits on the network.
import { spawn } from "node:child_process"
import { fileURLToPath } from "node:url"
import { config, disabled, log, parseJson, readStdin } from "./common.mjs"

const cfg = config()
setTimeout(() => process.exit(0), 1000).unref()

async function main() {
  if (disabled(cfg) || !cfg.capture) return
  const input = parseJson(await readStdin())
  if (!input.session_id || !input.transcript_path) {
    log(cfg, "warn", { event: "session-end", msg: "missing session_id or transcript_path" })
    return
  }
  const job = JSON.stringify({
    session_id: String(input.session_id),
    transcript_path: String(input.transcript_path),
    cwd: typeof input.cwd === "string" ? input.cwd : null,
    reason: typeof input.reason === "string" ? input.reason : null,
  })
  // detached gives the worker its own session (setsid), so it outlives Claude
  // Code's teardown. The job goes in argv, never through a shell.
  const worker = fileURLToPath(new URL("./capture.mjs", import.meta.url))
  const child = spawn(process.execPath, [worker, job], { detached: true, stdio: "ignore" })
  child.unref()
  log(cfg, "info", { event: "session-end", session: input.session_id, reason: input.reason, worker: child.pid })
}

main()
  .catch((err) => log(cfg, "warn", { event: "session-end", msg: String(err?.message ?? err) }))
  .finally(() => process.exit(0))
