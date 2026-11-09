// Runs install.mjs end to end against a temp HOME and settings file.
import { test } from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import path from "node:path"
import { ROOT, run, tempHome } from "./helpers.mjs"

const INSTALL = path.join(ROOT, "install.mjs")

test("install, reinstall and uninstall keep the user's settings intact", async () => {
  const home = tempHome({ key: null })
  const settings = path.join(home, ".claude", "settings.json")
  fs.mkdirSync(path.dirname(settings), { recursive: true })
  const original = {
    env: { A: "1" },
    hooks: { SessionEnd: [{ hooks: [{ type: "command", command: "failproofai --hook SessionEnd", timeout: 60000 }] }] },
  }
  fs.writeFileSync(settings, JSON.stringify(original, null, 2) + "\n", { mode: 0o600 })
  const env = { ...process["env"], HOME: home }

  let r = await run(process.execPath, [INSTALL, "--no-key"], { env })
  assert.equal(r.code, 0, r.stderr)
  const installed = JSON.parse(fs.readFileSync(settings, "utf8"))
  assert.equal(installed.hooks.SessionEnd.length, 2)
  assert.equal(installed.hooks.SessionEnd[0].hooks[0].command, "failproofai --hook SessionEnd")
  assert.match(installed.hooks.SessionStart[0].hooks[0].command, /recall-hook" session-start$/)
  assert.equal(fs.statSync(settings).mode & 0o777, 0o600, "file mode changed")
  const dir = path.join(home, ".local", "share", "recall-hooks")
  assert.ok(fs.statSync(path.join(dir, "bin", "recall-hook")).mode & 0o100)
  assert.ok(fs.existsSync(path.join(dir, "lib", "capture.mjs")))
  assert.equal(fs.readFileSync(path.join(dir, "node-path"), "utf8").trim(), process.execPath)
  assert.equal(fs.readdirSync(path.dirname(settings)).filter((f) => f.includes("recall-backup")).length, 1)

  r = await run(process.execPath, [INSTALL, "--no-key"], { env })
  assert.match(r.stdout, /already up to date/)
  assert.deepEqual(JSON.parse(fs.readFileSync(settings, "utf8")), installed)

  r = await run(process.execPath, [INSTALL, "--status"], { env })
  assert.match(r.stdout, /SessionEnd, SessionStart/)

  r = await run(process.execPath, [INSTALL, "--uninstall"], { env })
  assert.equal(r.code, 0, r.stderr)
  assert.deepEqual(JSON.parse(fs.readFileSync(settings, "utf8")), original)
  assert.ok(!fs.existsSync(dir))
})

test("install refuses to touch a settings file that isn't valid JSON", async () => {
  const home = tempHome({ key: null })
  const settings = path.join(home, ".claude", "settings.json")
  fs.mkdirSync(path.dirname(settings), { recursive: true })
  fs.writeFileSync(settings, "{ broken")
  const r = await run(process.execPath, [INSTALL, "--no-key"], { env: { ...process["env"], HOME: home } })
  assert.equal(r.code, 1)
  assert.equal(fs.readFileSync(settings, "utf8"), "{ broken")
})
