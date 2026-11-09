import { test } from "node:test"
import assert from "node:assert/strict"
import { buildContext, relatedFacts } from "../lib/format.mjs"
import { buildMessages, parseTranscript, stats } from "../lib/transcript.mjs"
import { clipSentences, redact } from "../lib/common.mjs"
import { addHooks, removeHooks } from "../install.mjs"
import { t } from "./helpers.mjs"

const profile = {
  static: "Lives in Banjarbaru. Writes code in TypeScript. ".repeat(20),
  dynamic: "Works on Recall, a self-hosted memory engine. Uses pnpm 10.17.0. ".repeat(20),
  searchResults: [
    { type: "memory", text: "Recall runs on the Mac mini in Docker", ftsRank: 1, vecRank: 3 },
    { type: "memory", text: "Recall runs on the Mac mini in Docker", ftsRank: 2, vecRank: 1 },
    { type: "memory", text: "Vector-only noise", ftsRank: null, vecRank: 1 },
    { type: "chunk", text: "a chunk", ftsRank: 1 },
  ],
}

test("buildContext stays under the cap and keeps only keyword hits", () => {
  const ctx = buildContext(profile, { project: "recall", container: "zaki", maxChars: 1500 })
  assert.ok(ctx.length <= 1500, `length ${ctx.length}`)
  assert.match(ctx, /^<recall-memory container="zaki">/)
  assert.match(ctx, /<\/recall-memory>$/)
  assert.match(ctx, /About: Lives in Banjarbaru/)
  assert.match(ctx, /Lately: Works on Recall/)
  assert.match(ctx, /Related to recall:\n- Recall runs on the Mac mini in Docker/)
  assert.doesNotMatch(ctx, /Vector-only noise|a chunk/)
  assert.equal(relatedFacts(profile).length, 1)
})

test("buildContext respects a tiny cap and returns empty for an empty profile", () => {
  assert.ok(buildContext(profile, { project: "recall", container: "zaki", maxChars: 400 }).length <= 400)
  assert.equal(buildContext({ static: "", dynamic: "", searchResults: [] }, { project: "x", container: "zaki" }), "")
  assert.equal(buildContext(null, { project: "x", container: "zaki" }), "")
})

test("clipSentences cuts at a sentence end", () => {
  assert.equal(clipSentences("One two. Three four. Five six seven.", 22), "One two. Three four.")
})

test("redact masks keys, tokens and passwords", () => {
  // Fake tokens are assembled at runtime so secret scanners don't flag this file.
  const letters = "abcdefghijklmnopqrstuvwx"
  const raw = [
    "key rcl_abcdefghijklmnop1234",
    `openai ${["sk", "proj", letters].join("-")}`,
    `gh ${["ghp", letters + "yz0123"].join("_")}`,
    "Authorization: Bearer abc.def.ghijklmnopqrstu",
    "password=hunter2hunter2",
    'API_KEY: "zzzzzzzzzz"',
    `postgres://${["admin", "s3cret"].join(":")}@db.local/app`,
    ["BEGIN", "END"].map((w) => `-----${w} RSA PRIVATE ${"KEY"}-----`).join("\nMIIabc\n"),
    "blob QWxhZGRpbjpvcGVuIHNlc2FtZQ0123456789abcdefghijkl",
  ].join("\n")
  const out = redact(raw)
  for (const leak of ["rcl_abc", "proj-abc", "_abcdefghij","ghijklmnopqrstu", "hunter2", "zzzzzzzzzz", "s3cret", "MIIabc", "QWxhZGRp"]) {
    assert.ok(!out.includes(leak), `leaked ${leak}: ${out}`)
  }
  assert.match(out, /password=\[redacted\]/)
  assert.equal(redact("plain words /home/dev/Code/recall stay"), "plain words /home/dev/Code/recall stay")
})

test("parseTranscript keeps prompts and prose, drops tools, meta, thinking and sidechains", () => {
  const lines = [
    t.other(),
    t.user("Add a SessionStart hook <system-reminder>secret context</system-reminder>"),
    t.thinking("private thoughts"),
    t.toolUse("cat ~/.config/recall/api-key"),
    t.toolResult("rcl_realkey_should_never_leave"),
    t.assistant("Done. I added the hook."),
    t.assistant("Here's the code:\n```js\nconst x = 1\n```\nIt works."),
    t.sidechain("subagent chatter"),
    t.meta("Caveat: meta stuff"),
    t.user("<command-name>/clear</command-name>"),
    t.user("Use pnpm 10.17.0 from now on."),
    "not json at all",
  ]
  const turns = parseTranscript(lines.map((l) => (typeof l === "string" ? l : JSON.stringify(l))).join("\n"))
  assert.deepEqual(
    turns.map((x) => x.role),
    ["user", "assistant", "user"],
  )
  assert.equal(turns[0].text, "Add a SessionStart hook")
  assert.match(turns[1].text, /Done\. I added the hook\.\n\nHere's the code:\n\[code omitted\]\nIt works\./)
  const all = JSON.stringify(turns)
  for (const leak of ["private thoughts", "rcl_realkey", "api-key", "subagent chatter", "secret context", "const x"]) assert.ok(!all.includes(leak), leak)
  assert.deepEqual(stats(turns), { prompts: 2, promptChars: turns[0].text.length + turns[2].text.length, turns: 3 })
})

test("buildMessages keeps the first prompt and the newest tail within budget", () => {
  const turns = [{ role: "user", text: "TASK: build hooks" }]
  for (let i = 0; i < 40; i++) turns.push({ role: "assistant", text: `reply ${i} `.repeat(30) }, { role: "user", text: `prompt ${i}` })
  const msgs = buildMessages(turns, { maxChars: 3000, header: "HEADER" })
  const size = msgs.reduce((n, m) => n + m.content.length, 0)
  assert.ok(size <= 3000 + 60, `size ${size}`)
  assert.equal(msgs[0].content, "HEADER")
  assert.equal(msgs[1].content, "TASK: build hooks")
  assert.match(msgs[2].content, /earlier messages left out/)
  assert.equal(msgs.at(-1).content, "prompt 39")
})

const existing = {
  env: { FOO: "1" },
  hooks: {
    SessionStart: [{ hooks: [{ type: "command", command: "other-start" }] }],
    Stop: [{ hooks: [{ type: "command", command: "other-stop" }] }],
  },
}

test("addHooks appends ours, keeps other hooks and is idempotent", () => {
  const home = "/Users/someone"
  const dir = "/Users/someone/.local/share/recall-hooks"
  const once = addHooks(existing, { dir, home })
  const twice = addHooks(once, { dir, home })
  assert.deepEqual(twice, once)
  assert.equal(once.hooks.SessionStart.length, 2)
  assert.equal(once.hooks.SessionStart[0].hooks[0].command, "other-start")
  assert.equal(once.hooks.SessionStart[1].hooks[0].command, '"$HOME/.local/share/recall-hooks/bin/recall-hook" session-start')
  assert.equal(once.hooks.SessionStart[1].hooks[0].timeout, 3)
  assert.equal(once.hooks.SessionEnd[0].hooks[0].command, '"$HOME/.local/share/recall-hooks/bin/recall-hook" session-end')
  assert.deepEqual(once.hooks.Stop, existing.hooks.Stop)
  assert.deepEqual(once.env, existing.env)
})

test("removeHooks restores the original settings exactly", () => {
  const added = addHooks(existing, { dir: "/h/.local/share/recall-hooks", home: "/h" })
  assert.deepEqual(removeHooks(added), existing)
  assert.deepEqual(removeHooks(addHooks({}, { dir: "/h/x", home: "/h" })), {})
  // a group shared with another hook keeps the other hook
  const shared = { hooks: { SessionEnd: [{ hooks: [{ type: "command", command: "keep-me" }, { type: "command", command: '"/x/bin/recall-hook" session-end' }] }] } }
  assert.deepEqual(removeHooks(shared), { hooks: { SessionEnd: [{ hooks: [{ type: "command", command: "keep-me" }] }] } })
})
