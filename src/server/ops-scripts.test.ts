// Runs scripts/pre-deploy.sh and scripts/backup.sh against stub docker, cf and
// installer binaries. The container's /data is a temp dir, nothing real is
// touched (no Docker, no launchd, no R2).
import { spawnSync } from "node:child_process"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

const repo = path.resolve(import.meta.dirname, "../..")
let tmp: string
let data: string
let home: string
let bin: string

// docker compose, played by a script. /data inside "the container" is $FAKE_DATA.
const DOCKER = `#!/bin/bash
echo "$*" >> "$FAKE_LOG"
[ "$1" = compose ] || exit 1
shift
case "$1" in
  ps) echo "$FAKE_RUNNING"; exit 0 ;;
  cp) src="\${2#app:}"; cp "$FAKE_DATA\${src#/data}" "$3"; exit $? ;;
  exec) shift; [ "$1" = -T ] && shift; shift ;;  # drop "-T app"
  *) exit 1 ;;
esac
case "$1 $2" in
  "test -f") exit "\${FAKE_NO_CLI:-0}" ;;
  "node .output/server/cli.mjs") ;;
  "node -e") ;;
  "sh -c") script="\${3//\\/data/$FAKE_DATA}"; shift 3; exec sh -c "$script" "$@" ;;
  *) exit 1 ;;
esac
# A snapshot: count it, fail the first $FAKE_FAILS, then write a gzip.
n=$(( $(cat "$FAKE_DATA/.attempts" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$FAKE_DATA/.attempts"
[ "$n" -le "\${FAKE_FAILS:-0}" ] && exit 1
dest="\${@: -1}"
mkdir -p "$(dirname "$FAKE_DATA\${dest#/data}")"
echo snapshot | gzip > "$FAKE_DATA\${dest#/data}"
`

// The cf-zakiego-lab wrapper: remembers uploads, lists them back.
const CF = `#!/bin/bash
echo "cf $*" >> "$FAKE_LOG"
if [ "$3" = put ]; then
  while [ $# -gt 0 ]; do [ "$1" = --file ] && stat -f %z "$2" > "$FAKE_DATA/.uploaded"; shift; done
  exit 0
fi
while [ $# -gt 0 ]; do [ "$1" = --prefix ] && key="$2"; shift; done
printf '[{"key":"%s","size":%s}]' "$key" "$(cat "$FAKE_DATA/.uploaded")"
`

function stub(file: string, body: string) {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, body, { mode: 0o755 })
}

function run(script: string, vars: Record<string, string>) {
  const res = spawnSync("bash", [path.join(repo, "scripts", script)], {
    cwd: repo,
    encoding: "utf8",
    env: { PATH: `${bin}:/usr/bin:/bin:/usr/sbin:/sbin`, HOME: home, FAKE_LOG: path.join(tmp, "calls.log"), FAKE_DATA: data, ...vars },
  })
  const calls = fs.existsSync(path.join(tmp, "calls.log")) ? fs.readFileSync(path.join(tmp, "calls.log"), "utf8") : ""
  return { status: res.status, out: res.stdout + res.stderr, calls }
}

const backups = () => (fs.existsSync(path.join(data, "backups")) ? fs.readdirSync(path.join(data, "backups")).sort() : [])

beforeEach(() => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), "recall-ops-"))
  data = path.join(tmp, "data")
  home = path.join(tmp, "home")
  bin = path.join(tmp, "bin")
  fs.mkdirSync(data, { recursive: true })
  stub(path.join(bin, "docker"), DOCKER)
  stub(path.join(bin, "launchctl"), "#!/bin/bash\nexit 0\n")
  stub(path.join(bin, "install-backup"), '#!/bin/bash\necho "install-backup" >> "$FAKE_LOG"\n')
  // Already installed and identical, unless a test changes it.
  stub(path.join(home, "selfhost/bin/recall-backup.sh"), fs.readFileSync(path.join(repo, "scripts/backup.sh"), "utf8"))
})
afterEach(() => fs.rmSync(tmp, { recursive: true, force: true }))

describe.runIf(process.platform === "darwin")("scripts/pre-deploy.sh", () => {
  const vars = () => ({ RECALL_INSTALL_BACKUP: path.join(bin, "install-backup") })

  it("is executable, so deploy.sh runs it", () => {
    expect(fs.statSync(path.join(repo, "scripts/pre-deploy.sh")).mode & 0o111).toBeTruthy()
  })

  it("skips the backup on a first deploy", () => {
    const r = run("pre-deploy.sh", { ...vars(), FAKE_RUNNING: "" })
    expect(r.status).toBe(0)
    expect(r.calls).not.toMatch(/cli\.mjs backup/)
    expect(backups()).toEqual([])
  })

  it("backs up a running app and keeps the newest 3", () => {
    fs.mkdirSync(path.join(data, "backups"))
    for (const t of ["20261101T000000Z", "20261102T000000Z", "20261103T000000Z", "20261104T000000Z"]) {
      fs.writeFileSync(path.join(data, "backups", `pre-deploy-${t}.db.gz`), "old")
    }
    fs.writeFileSync(path.join(data, "backups", "manual.db.gz"), "keep me")
    const r = run("pre-deploy.sh", { ...vars(), FAKE_RUNNING: "abc123" })
    expect(r.status, r.out).toBe(0)
    expect(r.calls).toMatch(/compose exec -T app node \.output\/server\/cli\.mjs backup \/data\/backups\/pre-deploy-\d{8}T\d{6}Z\.db\.gz/)
    const left = backups()
    expect(left).toHaveLength(4)
    expect(left).toContain("manual.db.gz")
    expect(left.filter((f) => f.startsWith("pre-deploy-")).slice(0, 2)).toEqual(["pre-deploy-20261103T000000Z.db.gz", "pre-deploy-20261104T000000Z.db.gz"])
  })

  it("stops the deploy when a running app can't be backed up", () => {
    const r = run("pre-deploy.sh", { ...vars(), FAKE_RUNNING: "abc123", FAKE_FAILS: "1" })
    expect(r.status).toBe(1)
    expect(r.out).toMatch(/backup FAILED/)
  })

  it("reinstalls the nightly backup only when scripts/backup.sh changed", () => {
    expect(run("pre-deploy.sh", { ...vars(), FAKE_RUNNING: "" }).calls).not.toMatch(/install-backup/)
    fs.appendFileSync(path.join(home, "selfhost/bin/recall-backup.sh"), "\n# older copy\n")
    expect(run("pre-deploy.sh", { ...vars(), FAKE_RUNNING: "" }).calls).toMatch(/install-backup/)
  })
})

describe.runIf(process.platform === "darwin")("scripts/backup.sh", () => {
  function backupVars(extra: Record<string, string>) {
    // backup.sh sets its own PATH and adds the nvm dir holding `cf`, so the stubs live there.
    const nvm = path.join(home, ".nvm/versions/node/v0/bin")
    stub(path.join(nvm, "cf"), "#!/bin/bash\nexit 0\n")
    stub(path.join(nvm, "docker"), DOCKER)
    stub(path.join(tmp, "cf-wrapper"), CF)
    const recallDir = path.join(tmp, "recall")
    fs.mkdirSync(recallDir, { recursive: true })
    return {
      RECALL_DIR: recallDir,
      BACKUP_LOG: path.join(tmp, "backup.log"),
      CF_WRAPPER: path.join(tmp, "cf-wrapper"),
      SNAPSHOT_RETRY_DELAY: "0",
      FAKE_NO_CLI: "1",
      ...extra,
    }
  }
  const log = () => fs.readFileSync(path.join(tmp, "backup.log"), "utf8")

  it("retries the snapshot and uploads once it works", () => {
    const r = run("backup.sh", backupVars({ FAKE_FAILS: "2" }))
    expect(r.status, log()).toBe(0)
    expect(log()).toMatch(/snapshot attempt 1 failed/)
    expect(log()).toMatch(/snapshot attempt 2 failed/)
    expect(log()).toMatch(/uploaded r2:\/\/recall-backups\/recall\//)
    expect(log().trimEnd()).toMatch(/done$/)
  })

  it("gives up after 3 failed snapshots", () => {
    const r = run("backup.sh", backupVars({ FAKE_FAILS: "5" }))
    expect(r.status).not.toBe(0)
    expect(fs.readFileSync(path.join(data, ".attempts"), "utf8").trim()).toBe("3")
    expect(log()).toMatch(/snapshot failed 3 times/)
    expect(r.calls).not.toMatch(/cf r2 objects put/)
  })
})
