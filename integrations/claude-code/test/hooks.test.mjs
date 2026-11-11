// Runs the real hook scripts as subprocesses against a fake Recall server.
import { test } from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import path from "node:path"
import { execFileSync } from "node:child_process"
import { FAKE_KEY, ROOT, fakeServer, hookEnv, readLog, run, t, tempHome, waitFor, writeTranscript } from "./helpers.mjs"

const START = path.join(ROOT, "lib", "session-start.mjs")
const END = path.join(ROOT, "lib", "session-end.mjs")
const LAUNCHER = path.join(ROOT, "bin", "recall-hook")

const PROFILE = {
  container: "zaki",
  static: "Lives in Banjarbaru.",
  dynamic: "Works on Recall.",
  searchResults: [{ type: "memory", text: "Recall uses pnpm 10.17.0", ftsRank: 1 }],
}

const startInput = (cwd) => JSON.stringify({ session_id: "s-start", transcript_path: "/nope", cwd, hook_event_name: "SessionStart", source: "startup" })

test("session-start injects the profile as additionalContext", async () => {
  const srv = await fakeServer((req) => (req.url.startsWith("/v1/profile") ? { json: PROFILE } : null))
  const home = tempHome()
  try {
    const r = await run(process.execPath, [START], { env: hookEnv(home, srv.url), input: startInput(home) })
    assert.equal(r.code, 0)
    const out = JSON.parse(r.stdout)
    assert.equal(out.hookSpecificOutput.hookEventName, "SessionStart")
    const ctx = out.hookSpecificOutput.additionalContext
    assert.match(ctx, /About: Lives in Banjarbaru\./)
    assert.match(ctx, /- Recall uses pnpm 10\.17\.0/)
    assert.ok(ctx.length <= 1500)
    const req = srv.requests[0]
    assert.equal(req.headers.authorization, `Bearer ${FAKE_KEY}`)
    const u = new URL(req.url, srv.url)
    assert.equal(u.searchParams.get("container"), "zaki")
    assert.equal(u.searchParams.get("q"), path.basename(home))
    assert.ok(!fs.readFileSync(path.join(home, ".cache", "recall-hooks.log"), "utf8").includes(FAKE_KEY), "key in log")
  } finally {
    await srv.close()
  }
})

test("session-start prints nothing on a server error", async () => {
  const srv = await fakeServer(() => ({ status: 500, json: { error: "boom" } }))
  const home = tempHome()
  try {
    const r = await run(process.execPath, [START], { env: hookEnv(home, srv.url), input: startInput(home) })
    assert.equal(r.code, 0)
    assert.equal(r.stdout, "")
  } finally {
    await srv.close()
  }
})

test("session-start gives up within the hard timeout on a hanging server", async () => {
  const srv = await fakeServer(() => ({ delayMs: 5000, json: PROFILE }))
  const home = tempHome()
  try {
    const r = await run(process.execPath, [START], { env: hookEnv(home, srv.url, { RECALL_HOOKS_START_TIMEOUT_MS: "600" }), input: startInput(home) })
    assert.equal(r.code, 0)
    assert.equal(r.stdout, "")
    assert.ok(r.ms < 1800, `took ${r.ms} ms`)
  } finally {
    await srv.close()
  }
})

test("session-start does nothing without a key, when unreachable, or with the kill switch", async () => {
  const srv = await fakeServer(() => ({ json: PROFILE }))
  try {
    const noKey = tempHome({ key: null })
    let r = await run(process.execPath, [START], { env: hookEnv(noKey, srv.url), input: startInput(noKey) })
    assert.equal(r.stdout, "")
    assert.match(readLog(noKey).at(-1).msg, /no usable API key/)

    const home = tempHome()
    r = await run(process.execPath, [START], { env: hookEnv(home, "http://127.0.0.1:9"), input: startInput(home) })
    assert.equal(r.code, 0)
    assert.equal(r.stdout, "")

    fs.writeFileSync(path.join(home, ".config", "recall", "hooks-disabled"), "")
    r = await run(process.execPath, [START], { env: hookEnv(home, srv.url), input: startInput(home) })
    assert.equal(r.stdout, "")
    fs.rmSync(path.join(home, ".config", "recall", "hooks-disabled"))
    r = await run(process.execPath, [START], { env: hookEnv(home, srv.url, { RECALL_HOOKS_DISABLED: "1" }), input: startInput(home) })
    assert.equal(r.stdout, "")
    assert.equal(srv.requests.length, 0)
  } finally {
    await srv.close()
  }
})

const sessionLines = [
  t.user("Set up the Recall hooks for Claude Code on both Macs."),
  t.toolUse("cat ~/.config/recall/hooks-key"),
  t.toolResult("rcl_leaked_tool_output_0123456789"),
  t.assistant("I'll use SessionStart and SessionEnd. Your key rcl_abcdefghijklmnop999 stays local."),
  t.user("Good. Always keep the hooks under 2 seconds."),
  t.assistant("Agreed, the start hook has a 1.8 s budget."),
]

test("a key file readable by group or others is refused, with the chmod fix in the log", async () => {
  const srv = await fakeServer(() => ({ json: PROFILE }))
  const home = tempHome()
  const keyFile = path.join(home, ".config", "recall", "hooks-key")
  // a working fallback must not be used instead
  fs.writeFileSync(path.join(home, ".config", "recall", "api-key"), FAKE_KEY + "\n", { mode: 0o600 })
  try {
    for (const mode of [0o644, 0o640, 0o604]) {
      fs.chmodSync(keyFile, mode)
      const r = await run(process.execPath, [START], { env: hookEnv(home, srv.url), input: startInput(home) })
      assert.equal(r.code, 0)
      assert.equal(r.stdout, "")
      const refused = readLog(home).find((l) => l.fix)
      assert.equal(refused.fix, `chmod 600 '${keyFile}'`)
      assert.match(refused.msg, /readable by group or others, refused\. Fix it with: chmod 600 /)
      assert.match(readLog(home).at(-1).msg, /no usable API key/)
    }
    assert.equal(srv.requests.length, 0)
    assert.ok(!fs.readFileSync(path.join(home, ".cache", "recall-hooks.log"), "utf8").includes(FAKE_KEY), "key in log")

    fs.chmodSync(keyFile, 0o600)
    const r = await run(process.execPath, [START], { env: hookEnv(home, srv.url), input: startInput(home) })
    assert.match(JSON.parse(r.stdout).hookSpecificOutput.additionalContext, /Lives in Banjarbaru/)
  } finally {
    await srv.close()
  }
})

test("session-start and capture refuse a non-local http URL and still exit 0", async () => {
  const home = tempHome()
  const env = hookEnv(home, "http://recall.example.test")
  const r = await run(process.execPath, [START], { env, input: startInput(home) })
  assert.equal(r.code, 0)
  assert.equal(r.stdout, "")
  assert.match(readLog(home).at(-1).msg, /refused http:\/\/recall\.example\.test: the API key would go in cleartext/)

  const transcript = writeTranscript(home, sessionLines)
  const e = await run(process.execPath, [END], { env, input: endInput(transcript, home, "plain-http") })
  assert.equal(e.code, 0)
  const refused = await waitFor(() => readLog(home).find((l) => l.event === "capture"))
  assert.match(refused.msg, /cleartext/)
})

function endInput(transcript, cwd, id = "abc-123") {
  return JSON.stringify({ session_id: id, transcript_path: transcript, cwd, hook_event_name: "SessionEnd", reason: "prompt_input_exit" })
}

test("session-end returns at once and the detached worker stores one document", async () => {
  const srv = await fakeServer((req) => (req.method === "POST" ? { status: 202, json: { id: "doc_1", duplicate: false } } : null))
  const home = tempHome()
  try {
    const transcript = writeTranscript(home, sessionLines)
    const r = await run(process.execPath, [END], { env: hookEnv(home, srv.url), input: endInput(transcript, home) })
    assert.equal(r.code, 0)
    assert.equal(r.stdout, "")
    assert.ok(r.ms < 1000, `session-end took ${r.ms} ms`)
    const req = await waitFor(() => srv.requests.find((x) => x.method === "POST"))
    assert.ok(req, "worker never posted")
    assert.equal(req.url, "/v1/memories")
    assert.equal(req.headers.authorization, `Bearer ${FAKE_KEY}`)
    const body = JSON.parse(req.body)
    assert.equal(body.container, "zaki")
    assert.equal(body.source_id, "claude-code:abc-123")
    assert.equal(body.metadata.source, "claude-code")
    assert.equal(body.metadata.prompts, 2)
    assert.equal(body.wait, undefined)
    assert.deepEqual(
      body.messages.map((m) => m.role),
      ["note", "user", "assistant", "user", "assistant"],
    )
    assert.ok(!req.body.includes("rcl_"), "a key reached the server")
    assert.ok(!req.body.includes("hooks-key"), "tool input reached the server")
    const done = await waitFor(() => readLog(home).find((l) => l.event === "capture" && l.msg === "stored"))
    assert.equal(done.document, "doc_1")
  } finally {
    await srv.close()
  }
})

test("capture tells the extractor to drop work status and keeps the branch out of the text", async () => {
  const srv = await fakeServer(() => ({ status: 202, json: { id: "doc_3" } }))
  const home = tempHome()
  const git = (...args) => execFileSync("git", ["-C", home, ...args], { stdio: "ignore" })
  git("init", "-q", "-b", "feat/pr-42-wip")
  git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
  try {
    const transcript = writeTranscript(home, sessionLines)
    await run(process.execPath, [END], { env: hookEnv(home, srv.url), input: endInput(transcript, home, "status-1") })
    const req = await waitFor(() => srv.requests.find((x) => x.method === "POST"))
    assert.ok(req, "worker never posted")
    const body = JSON.parse(req.body)
    const header = body.messages[0].content
    assert.match(header, /Do not keep transient work status: branch names, PR or issue numbers, what is committed, pushed, merged/)
    assert.match(header, /merge order/)
    assert.ok(!header.includes("feat/pr-42-wip"), header)
    assert.ok(!header.includes(home), header)
    assert.equal(body.metadata.branch, "feat/pr-42-wip")
  } finally {
    await srv.close()
  }
})

test("capture retries a 5xx with the same source_id", async () => {
  const srv = await fakeServer((req, n) => (n === 1 ? { status: 503, json: {} } : { status: 200, json: { id: "doc_1", duplicate: true } }))
  const home = tempHome()
  try {
    const transcript = writeTranscript(home, sessionLines)
    await run(process.execPath, [END], { env: hookEnv(home, srv.url), input: endInput(transcript, home, "retry-1") })
    const log = await waitFor(() => readLog(home).find((l) => l.event === "capture" && l.msg === "already stored"))
    assert.ok(log, JSON.stringify(readLog(home)))
    assert.equal(srv.requests.length, 2)
    assert.equal(new Set(srv.requests.map((x) => JSON.parse(x.body).source_id)).size, 1)
    assert.equal(log.attempt, 2)
  } finally {
    await srv.close()
  }
})

test("capture skips trivial sessions and respects RECALL_HOOKS_MIN_PROMPTS", async () => {
  const srv = await fakeServer(() => ({ status: 202, json: { id: "doc_2" } }))
  const home = tempHome()
  try {
    const transcript = writeTranscript(home, [t.user("What does this regex do? ^a+$"), t.assistant("Matches one or more a's.")])
    await run(process.execPath, [END], { env: hookEnv(home, srv.url), input: endInput(transcript, home, "tiny") })
    const skipped = await waitFor(() => readLog(home).find((l) => l.event === "capture"))
    assert.match(skipped.msg, /trivial/)
    assert.equal(srv.requests.length, 0)

    await run(process.execPath, [END], { env: hookEnv(home, srv.url, { RECALL_HOOKS_MIN_PROMPTS: "1" }), input: endInput(transcript, home, "tiny-2") })
    assert.ok(await waitFor(() => srv.requests.length === 1))
  } finally {
    await srv.close()
  }
})

test("launcher exits 0 for the kill switch and unknown events", async () => {
  const home = tempHome()
  fs.writeFileSync(path.join(home, ".config", "recall", "hooks-disabled"), "")
  let r = await run("/bin/sh", [LAUNCHER, "session-start"], { env: hookEnv(home, "http://127.0.0.1:9"), input: startInput(home) })
  assert.equal(r.code, 0)
  assert.equal(r.stdout, "")
  r = await run("/bin/sh", [LAUNCHER, "rm-rf"], { env: hookEnv(tempHome(), "http://127.0.0.1:9") })
  assert.equal(r.code, 0)
})

test("launcher runs the hook through node", async () => {
  const srv = await fakeServer(() => ({ json: PROFILE }))
  const home = tempHome()
  try {
    const r = await run("/bin/sh", [LAUNCHER, "session-start"], { env: hookEnv(home, srv.url), input: startInput(home) })
    assert.equal(r.code, 0)
    assert.match(JSON.parse(r.stdout).hookSpecificOutput.additionalContext, /Lives in Banjarbaru/)
  } finally {
    await srv.close()
  }
})

test("log rotates past the size cap", async () => {
  const home = tempHome({ key: null })
  const env = hookEnv(home, "http://127.0.0.1:9", { RECALL_HOOKS_LOG_MAX_BYTES: "200" })
  for (let i = 0; i < 4; i++) await run(process.execPath, [START], { env, input: startInput(home) })
  assert.ok(fs.existsSync(path.join(home, ".cache", "recall-hooks.log.1")))
  assert.ok(fs.statSync(path.join(home, ".cache", "recall-hooks.log")).size < 600)
})
