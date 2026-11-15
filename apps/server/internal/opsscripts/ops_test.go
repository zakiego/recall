// Package opsscripts tests the repo's ops scripts (scripts/pre-deploy.sh and
// scripts/backup.sh) against stub docker, cf and installer binaries: the
// container's /data is a temp dir, nothing real is touched (no Docker, no
// launchd, no R2). Ported from ops-scripts.test.ts. The image has no shell,
// so the scripts only run `recall <command>` in it.
package opsscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/testutil"
)

// docker compose, played by a script. /data inside "the container" is
// $FAKE_DATA. Only `recall ...` runs in it (the image has no shell).
// FAKE_FAILS fails that many snapshots, FAKE_DOWN that many execs of any kind.
const dockerStub = `#!/bin/bash
echo "$*" >> "$FAKE_LOG"
[ "$1" = compose ] || exit 1
shift
case "$1" in
  ps) echo "$FAKE_RUNNING"; exit 0 ;;
  exec) shift; [ "$1" = -T ] && shift; shift ;;  # drop "-T app"
  *) exit 1 ;;
esac
# FAKE_DOWN=n: the first n execs fail, as while the container restarts.
execs=$(( $(cat "$FAKE_DATA/.execs" 2>/dev/null || echo 0) + 1 ))
echo "$execs" > "$FAKE_DATA/.execs"
[ "$execs" -le "${FAKE_DOWN:-0}" ] && exit 1
[ "$1" = recall ] || exit 127
snap() {
  n=$(( $(cat "$FAKE_DATA/.attempts" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$FAKE_DATA/.attempts"
  [ "$n" -le "${FAKE_FAILS:-0}" ] && exit 1
  return 0
}
case "$2" in
  version) echo "recall dev"; exit 0 ;;
  backup)
    snap
    if [ "$3" = - ]; then echo snapshot | gzip; exit 0; fi
    mkdir -p "$(dirname "$FAKE_DATA${3#/data}")"
    echo snapshot | gzip > "$FAKE_DATA${3#/data}"; exit 0 ;;
  prune-backups)
    dir="$FAKE_DATA${3#/data}"
    ls -1 "$dir" | grep "^$4" | grep -v '\.tmp-' | sort -r | tail -n +$(($5 + 1)) | while read -r f; do rm -f "$dir/$f"; echo "removed $f"; done
    exit 0 ;;
esac
exit 1
`

// The cf-zakiego-lab wrapper: remembers uploads, lists them back.
const cfStub = `#!/bin/bash
echo "cf $*" >> "$FAKE_LOG"
if [ "$3" = put ]; then
  while [ $# -gt 0 ]; do [ "$1" = --file ] && stat -f %z "$2" > "$FAKE_DATA/.uploaded"; shift; done
  exit 0
fi
while [ $# -gt 0 ]; do [ "$1" = --prefix ] && key="$2"; shift; done
printf '[{"key":"%s","size":%s}]' "$key" "$(cat "$FAKE_DATA/.uploaded")"
`

type env struct {
	t                    *testing.T
	repo, tmp, data, bin string
	home                 string
}

func stub(t *testing.T, file, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(file), 0o755)
	if err := os.WriteFile(file, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newEnv(t *testing.T) *env {
	if runtime.GOOS != "darwin" {
		t.Skip("the scripts use BSD stat (the Mac mini)")
	}
	tmp := t.TempDir()
	e := &env{t: t, repo: filepath.Dir(filepath.Dir(testutil.Root())), tmp: tmp, data: filepath.Join(tmp, "data"), bin: filepath.Join(tmp, "bin"), home: filepath.Join(tmp, "home")}
	os.MkdirAll(e.data, 0o755)
	stub(t, filepath.Join(e.bin, "docker"), dockerStub)
	stub(t, filepath.Join(e.bin, "launchctl"), "#!/bin/bash\nexit 0\n")
	stub(t, filepath.Join(e.bin, "install-backup"), "#!/bin/bash\necho \"install-backup\" >> \"$FAKE_LOG\"\n")
	installed, err := os.ReadFile(filepath.Join(e.repo, "scripts", "backup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	stub(t, filepath.Join(e.home, "selfhost/bin/recall-backup.sh"), string(installed))
	return e
}

type result struct {
	code       int
	out, calls string
}

func (e *env) run(script string, vars map[string]string) result {
	e.t.Helper()
	cmd := exec.Command("bash", filepath.Join(e.repo, "scripts", script))
	cmd.Dir = e.repo
	cmd.Env = []string{"PATH=" + e.bin + ":/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + e.home, "FAKE_LOG=" + filepath.Join(e.tmp, "calls.log"), "FAKE_DATA=" + e.data}
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, _ := cmd.CombinedOutput()
	calls, _ := os.ReadFile(filepath.Join(e.tmp, "calls.log"))
	return result{code: cmd.ProcessState.ExitCode(), out: string(out), calls: string(calls)}
}

func (e *env) backups() []string {
	entries, _ := os.ReadDir(filepath.Join(e.data, "backups"))
	var out []string
	for _, x := range entries {
		out = append(out, x.Name())
	}
	sort.Strings(out)
	return out
}

func (e *env) preDeployVars(extra map[string]string) map[string]string {
	v := map[string]string{"RECALL_INSTALL_BACKUP": filepath.Join(e.bin, "install-backup")}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

func TestPreDeployIsExecutable(t *testing.T) {
	e := newEnv(t)
	st, err := os.Stat(filepath.Join(e.repo, "scripts", "pre-deploy.sh"))
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatal(st, err)
	}
}

func TestPreDeploySkipsFirstDeploy(t *testing.T) {
	e := newEnv(t)
	r := e.run("pre-deploy.sh", e.preDeployVars(map[string]string{"FAKE_RUNNING": ""}))
	if r.code != 0 || strings.Contains(r.calls, "backup") || len(e.backups()) != 0 {
		t.Fatal(r)
	}
}

func seedOld(e *env) {
	os.MkdirAll(filepath.Join(e.data, "backups"), 0o755)
	for _, ts := range []string{"20261101T000000Z", "20261102T000000Z", "20261103T000000Z", "20261104T000000Z"} {
		os.WriteFile(filepath.Join(e.data, "backups", "pre-deploy-"+ts+".db.gz"), []byte("old"), 0o644)
	}
	os.WriteFile(filepath.Join(e.data, "backups", "manual.db.gz"), []byte("keep me"), 0o644)
}

func checkKept(t *testing.T, e *env) {
	t.Helper()
	left := e.backups()
	if len(left) != 4 || left[0] != "manual.db.gz" || left[1] != "pre-deploy-20261103T000000Z.db.gz" || left[2] != "pre-deploy-20261104T000000Z.db.gz" || !strings.HasPrefix(left[3], "pre-deploy-2") {
		t.Fatal(left)
	}
}

var backupDest = regexp.MustCompile(`/data/backups/pre-deploy-\d{8}T\d{6}Z\.db\.gz`)

func TestPreDeployBacksUpAndPrunes(t *testing.T) {
	e := newEnv(t)
	seedOld(e)
	r := e.run("pre-deploy.sh", e.preDeployVars(map[string]string{"FAKE_RUNNING": "abc123"}))
	if r.code != 0 || !regexp.MustCompile(`compose exec -T app recall backup `+backupDest.String()).MatchString(r.calls) || !strings.Contains(r.calls, "recall prune-backups /data/backups pre-deploy- 3") {
		t.Fatal(r)
	}
	checkKept(t, e)
}

func TestPreDeployStopsWhenTheBackupFails(t *testing.T) {
	e := newEnv(t)
	r := e.run("pre-deploy.sh", e.preDeployVars(map[string]string{"FAKE_RUNNING": "abc123", "FAKE_FAILS": "1"}))
	if r.code != 1 || !strings.Contains(r.out, "backup FAILED") || strings.Contains(r.calls, "prune-backups") {
		t.Fatal(r)
	}
}

func TestPreDeployReinstallsTheNightlyBackupOnlyWhenChanged(t *testing.T) {
	e := newEnv(t)
	if r := e.run("pre-deploy.sh", e.preDeployVars(map[string]string{"FAKE_RUNNING": ""})); strings.Contains(r.calls, "install-backup") {
		t.Fatal(r)
	}
	f, _ := os.OpenFile(filepath.Join(e.home, "selfhost/bin/recall-backup.sh"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\n# older copy\n")
	f.Close()
	if r := e.run("pre-deploy.sh", e.preDeployVars(map[string]string{"FAKE_RUNNING": ""})); !strings.Contains(r.calls, "install-backup") {
		t.Fatal(r)
	}
}

func (e *env) backupVars(extra map[string]string) map[string]string {
	// backup.sh sets its own PATH and adds the nvm dir holding `cf`, so the stubs live there.
	nvm := filepath.Join(e.home, ".nvm/versions/node/v0/bin")
	stub(e.t, filepath.Join(nvm, "cf"), "#!/bin/bash\nexit 0\n")
	stub(e.t, filepath.Join(nvm, "docker"), dockerStub)
	stub(e.t, filepath.Join(e.tmp, "cf-wrapper"), cfStub)
	recallDir := filepath.Join(e.tmp, "recall")
	os.MkdirAll(recallDir, 0o755)
	v := map[string]string{"RECALL_DIR": recallDir, "BACKUP_LOG": filepath.Join(e.tmp, "backup.log"), "CF_WRAPPER": filepath.Join(e.tmp, "cf-wrapper"), "SNAPSHOT_RETRY_DELAY": "0"}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

func (e *env) log() string {
	b, _ := os.ReadFile(filepath.Join(e.tmp, "backup.log"))
	return string(b)
}

var uploaded = regexp.MustCompile(`uploaded r2://recall-backups/recall/\d{4}-\d{2}-\d{2}/recall-\d{8}-\d{6}\.db\.gz \(\d+ bytes\)`)

func TestBackupRetriesThenUploads(t *testing.T) {
	e := newEnv(t)
	r := e.run("backup.sh", e.backupVars(map[string]string{"FAKE_FAILS": "2"}))
	l := e.log()
	if r.code != 0 || !strings.Contains(l, "snapshot attempt 1 failed") || !strings.Contains(l, "snapshot attempt 2 failed") || !uploaded.MatchString(l) || !strings.HasSuffix(strings.TrimRight(l, "\n"), "done") {
		t.Fatal(r, l)
	}
	if !strings.Contains(r.calls, "recall backup -") || strings.Contains(r.calls, "compose cp") {
		t.Fatal("the snapshot is streamed out:", r.calls)
	}
}

func TestBackupGivesUpAfterThreeFailures(t *testing.T) {
	e := newEnv(t)
	r := e.run("backup.sh", e.backupVars(map[string]string{"FAKE_FAILS": "5"}))
	attempts, _ := os.ReadFile(filepath.Join(e.data, ".attempts"))
	if r.code == 0 || strings.TrimSpace(string(attempts)) != "3" || !strings.Contains(e.log(), "snapshot failed 3 times") || strings.Contains(r.calls, "cf r2 objects put") {
		t.Fatal(r, e.log())
	}
}

// A container that is mid-restart refuses the exec; the next attempt works.
func TestBackupWaitsOutARestart(t *testing.T) {
	e := newEnv(t)
	r := e.run("backup.sh", e.backupVars(map[string]string{"FAKE_DOWN": "1"}))
	l := e.log()
	if r.code != 0 || !strings.Contains(l, "snapshot attempt 1 failed") || !uploaded.MatchString(l) {
		t.Fatal(r, l)
	}
}
