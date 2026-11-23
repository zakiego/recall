package cli_test

// admin-cli.test.ts (all 10), backup.test.ts (2), people.test.ts's
// "backfill-people queues jobs, or runs them with --now" and the CLI half of
// dated-facts.test.ts's "queues the next batch with a cursor".

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/backup"
	"github.com/zakiego/recall/apps/server/internal/cli"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

type harness struct {
	d        *db.DB
	out, err []string
	io       cli.IO
}

func newHarness(t *testing.T, password string) *harness {
	h := &harness{d: testkit.OpenDB(t)}
	h.io = cli.IO{
		DB:           func() *db.DB { return h.d },
		Out:          func(l string) { h.out = append(h.out, l) },
		Err:          func(l string) { h.err = append(h.err, l) },
		ReadPassword: func() (string, error) { return password, nil },
	}
	return h
}

func (h *harness) run(argv ...string) int { return cli.Run(argv, h.io) }

func last(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return l[len(l)-1]
}

func parseJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("%v: %q", err, s)
	}
	return v
}

func col(t *testing.T, d *db.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := d.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func one(t *testing.T, d *db.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := d.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatal(q, err)
	}
	return s
}

func TestCreateUserOwnerOrJoins(t *testing.T) {
	h := newHarness(t, "a long enough password")
	if c := h.run("create-user", "zane", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	zane := parseJSON(t, h.out[0]).(map[string]any)
	if zane["username"] != "zane" || zane["workspace"] != "zane" || zane["role"] != "owner" {
		t.Fatal(zane)
	}
	if c := h.run("create-user", "ida", "--workspace", "zane", "--role=member"); c != 0 {
		t.Fatal(c, h.err)
	}
	ida := one(t, h.d, `SELECT id FROM user WHERE username = 'ida'`)
	ws := one(t, h.d, `SELECT id FROM organization WHERE slug = 'zane'`)
	if r, _ := users.GetRole(h.d, ida, ws); r != "member" {
		t.Fatal(r)
	}
	ms, _ := users.ListMembers(h.d, ws)
	var names []string
	for _, m := range ms {
		names = append(names, *m.Username)
	}
	if !reflect.DeepEqual(names, []string{"zane", "ida"}) {
		t.Fatal(names)
	}
	if c := h.run("create-user", "ida"); c != 1 || !strings.Contains(last(h.err), "taken") {
		t.Fatal(c, h.err)
	}
	if c := h.run("create-user", "x", "--role", "boss"); c != 1 {
		t.Fatal(c)
	}
}

func TestCreateUserRefusesUnknownWorkspace(t *testing.T) {
	h := newHarness(t, "a long enough password")
	h.run("create-user", "zane")
	workspaces := func() []string { return col(t, h.d, `SELECT slug FROM organization ORDER BY slug`) }
	usernames := func() []string { return col(t, h.d, `SELECT username FROM user ORDER BY username`) }
	check := func(code int, re string, argv ...string) {
		t.Helper()
		if c := h.run(argv...); c != code || !regexp.MustCompile(re).MatchString(last(h.err)) {
			t.Fatal(argv, c, h.err)
		}
	}
	// A typo fails and leaves nothing behind.
	check(1, `no workspace "zanei".*--new-workspace`, "create-user", "ida", "--workspace", "zanei", "--role", "member")
	// A new workspace can't start without an owner, with or without --workspace.
	check(1, `needs an owner`, "create-user", "ida", "--workspace", "team", "--new-workspace", "--role", "member")
	check(1, `needs an owner`, "create-user", "ida", "--role", "admin")
	// --new-workspace never joins an existing one.
	check(1, `already exists`, "create-user", "ida", "--workspace", "zane", "--new-workspace")
	if w, u := workspaces(), usernames(); !reflect.DeepEqual(w, []string{"zane"}) || !reflect.DeepEqual(u, []string{"zane"}) {
		t.Fatal(w, u)
	}
	if c := h.run("create-user", "ida", "--workspace", "team", "--new-workspace", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	got := parseJSON(t, last(h.out)).(map[string]any)
	if got["username"] != "ida" || got["workspace"] != "team" || got["role"] != "owner" {
		t.Fatal(got)
	}
	if w := workspaces(); !reflect.DeepEqual(w, []string{"team", "zane"}) {
		t.Fatal(w)
	}
}

func TestCreateUserShortPasswordLeavesNothing(t *testing.T) {
	h := newHarness(t, "short")
	if c := h.run("create-user", "zane"); c != 1 || !strings.Contains(last(h.err), "at least 12") {
		t.Fatal(c, h.err)
	}
	if n := one(t, h.d, `SELECT count(*) FROM user`); n != "0" {
		t.Fatal(n)
	}
	if n := one(t, h.d, `SELECT count(*) FROM organization`); n != "0" {
		t.Fatal(n)
	}
}

func TestCreateKeyQuiet(t *testing.T) {
	h := newHarness(t, "a long enough password")
	h.run("create-user", "zane")
	if c := h.run("create-key", "ci", "--quiet"); c != 0 {
		t.Fatal(c, h.err)
	}
	key := last(h.out)
	if !regexp.MustCompile(`^rcl_[0-9A-Za-z]{32}$`).MatchString(key) {
		t.Fatal(key)
	}
	if k, err := auth.VerifyAPIKey(h.d, key, time.Now().UnixMilli()); err != nil || k == nil || k.Name != "ci" {
		t.Fatal(k, err)
	}
}

func TestNeedsWorkspaceWhenSeveral(t *testing.T) {
	h := newHarness(t, "a long enough password")
	if c := h.run("create-key", "ci"); c != 1 || !strings.Contains(last(h.err), "no workspaces yet") {
		t.Fatal(c, h.err)
	}
	h.run("create-user", "zane")
	h.run("create-user", "rina")
	if c := h.run("create-key", "ci"); c != 1 || !strings.Contains(last(h.err), "pass --workspace") {
		t.Fatal(c, h.err)
	}
	if c := h.run("create-key", "ci", "--workspace", "rina", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	if got := parseJSON(t, last(h.out)).(map[string]any); got["workspace"] != "rina" {
		t.Fatal(got)
	}
}

func TestCreatesListsRevokes(t *testing.T) {
	h := newHarness(t, "a long enough password")
	h.run("create-user", "zane")
	h.run("create-key", "my", "agent", "--json")
	created := parseJSON(t, last(h.out)).(map[string]any)
	if created["name"] != "my agent" || !strings.HasPrefix(created["key"].(string), "rcl_") {
		t.Fatal(created)
	}
	// The JSON keys in TS order.
	if !regexp.MustCompile(`^\{"id":"[^"]+","name":"my agent","prefix":"[^"]+","workspace":"zane","key":"rcl_\w+"\}$`).MatchString(last(h.out)) {
		t.Fatal(last(h.out))
	}
	h.run("list-keys", "--json")
	list := parseJSON(t, last(h.out)).([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != created["id"] || list[0].(map[string]any)["revokedAt"] != nil {
		t.Fatal(list)
	}
	id := created["id"].(string)
	if c := h.run("revoke-key", id, "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	if got := last(h.out); got != `{"id":"`+id+`","revoked":true}` {
		t.Fatal(got)
	}
	if k, _ := auth.VerifyAPIKey(h.d, created["key"].(string), time.Now().UnixMilli()); k != nil {
		t.Fatal(k)
	}
	if c := h.run("revoke-key", id); c != 1 {
		t.Fatal(c)
	}
	if !reflect.DeepEqual(h.err, []string{"no such active key"}) {
		t.Fatal(h.err)
	}
}

func TestListKeysAndUsersText(t *testing.T) {
	h := newHarness(t, "a long enough password")
	h.run("list-users")
	if last(h.out) != "no users" {
		t.Fatal(h.out)
	}
	h.run("create-user", "zane")
	h.run("list-keys")
	if last(h.out) != "no keys" {
		t.Fatal(h.out)
	}
	h.run("create-key", "ci", "--json")
	id := parseJSON(t, last(h.out)).(map[string]any)["id"].(string)
	h.run("list-keys")
	if !regexp.MustCompile(`^` + id + `  rcl_\w+…  ci  created \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z  never used  active$`).MatchString(last(h.out)) {
		t.Fatal(last(h.out))
	}
	h.run("list-users")
	if last(h.out) != "zane  zane:owner" {
		t.Fatal(h.out)
	}
	h.run("list-users", "--workspace", "zane")
	if !regexp.MustCompile(`^zane  owner  joined \d{4}-.*Z$`).MatchString(last(h.out)) {
		t.Fatal(h.out)
	}
}

func TestHashPassword(t *testing.T) {
	ok := newHarness(t, "correct horse battery")
	if c := ok.run("hash-password"); c != 0 || !strings.HasPrefix(ok.out[0], "pbkdf2$") {
		t.Fatal(c, ok.out, ok.err)
	}
	if !auth.VerifyPassword(ok.out[0], "correct horse battery") {
		t.Fatal("hash does not verify")
	}
	if c := newHarness(t, "short").run("hash-password"); c != 1 {
		t.Fatal(c)
	}
}

func TestBackupCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "recall.db")
	d, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	t.Setenv("RECALL_DB_PATH", file)
	h := newHarness(t, "")
	if c := h.run("backup"); c != 1 {
		t.Fatal(c)
	}
	dest := filepath.Join(dir, "backups", "snap.db.gz")
	if c := h.run("backup", dest, "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	st, _ := os.Stat(dest)
	got := parseJSON(t, h.out[0]).(map[string]any)
	if got["dest"] != dest || got["bytes"] != float64(st.Size()) {
		t.Fatal(got)
	}
}

func TestUnknownCommandsAndFlags(t *testing.T) {
	if c := newHarness(t, "").run("nope"); c != 1 {
		t.Fatal(c)
	}
	if c := newHarness(t, "").run("list-keys", "--bogus"); c != 1 {
		t.Fatal(c)
	}
	h := newHarness(t, "")
	if c := h.run(); c != 0 || !reflect.DeepEqual(h.out, []string{cli.Usage}) {
		t.Fatal(c, h.out)
	}
	h = newHarness(t, "")
	if c := h.run("--workspace"); c != 1 || !strings.HasPrefix(last(h.err), "unknown flag: --workspace needs a value\nusage:") {
		t.Fatal(c, h.err)
	}
}

func TestSuggestPeopleMerges(t *testing.T) {
	h := newHarness(t, "a long enough password")
	h.run("create-user", "zane")
	ws := one(t, h.d, `SELECT id FROM organization WHERE slug = 'zane'`)
	add := func(id, container, name, email string) {
		if _, err := h.d.Exec(`INSERT INTO people (id, workspace_id, container, name, emails) VALUES (?, ?, ?, ?, ?)`, id, ws, container, name, jsonx.Stringify([]string{email})); err != nil {
			t.Fatal(err)
		}
	}
	add("per_a", "zane", "Rizky Kurniawan", "rizky@wargalapor.example")
	add("per_b", "zane", "Rizky Kurn", "RIZKY@wargalapor.example")
	add("per_c", "vortek", "Budi Santoso", "budi@vortek.id")
	add("per_d", "vortek", "Pak Budi", "budi@vortek.id")
	h.out = nil

	if c := h.run("suggest-people-merges", "--container", "zane"); c != 0 {
		t.Fatal(c, h.err)
	}
	want := []string{`zane/zane  keep "Rizky Kurniawan" (per_a, 0 fact(s))  merge in "Rizky Kurn" (per_b, 0 fact(s))  same email rizky@wargalapor.example`}
	if !reflect.DeepEqual(h.out, want) {
		t.Fatal(h.out)
	}
	if c := h.run("suggest-people-merges", "--workspace", "zane", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	var got [][]any
	for _, r := range parseJSON(t, last(h.out)).([]any) {
		m := r.(map[string]any)
		got = append(got, []any{m["container"], m["keep"].(map[string]any)["id"], m["drop"].(map[string]any)["id"]})
	}
	if !reflect.DeepEqual(got, [][]any{{"vortek", "per_c", "per_d"}, {"zane", "per_a", "per_b"}}) {
		t.Fatal(got)
	}
	if c := h.run("suggest-people-merges", "--container", "nobody"); c != 0 || last(h.out) != "no likely duplicates" {
		t.Fatal(c, h.out)
	}
	if c := h.run("suggest-people-merges", "--workspace", "nope"); c != 1 {
		t.Fatal(c)
	}
	if n := one(t, h.d, `SELECT count(*) FROM people`); n != "4" {
		t.Fatal(n)
	}
}

// people.test.ts "backfill-people queues jobs, or runs them with --now".
func TestBackfillPeopleQueuesOrRunsNow(t *testing.T) {
	h := newHarness(t, "a long enough password")
	calls := 0
	h.io.Backfill = func(*db.DB, search.Scope) (people.BackfillStats, error) {
		calls++
		return people.BackfillStats{Scanned: 1, Linked: 1, Created: 1, Calls: 1}, nil
	}
	h.run("create-user", "zane")
	ws := one(t, h.d, `SELECT id FROM organization WHERE slug = 'zane'`)
	for _, f := range [][2]string{{"zane", "Works with Clara"}, {"vortek", "Budi owns the brief"}} {
		if _, err := memories.AddMemoryDirect(h.d, memories.NewMemory{WorkspaceID: ws, Container: f[0], Fact: f[1], Kind: "dynamic"}, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	h.out = nil

	if c := h.run("backfill-people", "--container", "zane"); c != 0 {
		t.Fatal(c, h.err)
	}
	if len(h.out) != 1 || !regexp.MustCompile(`^zane/zane {2}1 fact\(s\) to check {2}queued job \d+$`).MatchString(h.out[0]) {
		t.Fatal(h.out)
	}
	if c := h.run("backfill-people", "--workspace", "zane", "--now", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	rows := parseJSON(t, last(h.out)).([]any)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for i, c := range []string{"vortek", "zane"} {
		r := rows[i].(map[string]any)
		if r["container"] != c || r["stats"].(map[string]any)["linked"] != float64(1) {
			t.Fatal(rows)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	if c := h.run("backfill-people", "--workspace", "nope"); c != 1 {
		t.Fatal(c)
	}
	// A second queue run finds the job already queued.
	h.run("backfill-people", "--container", "zane")
	if !strings.HasSuffix(last(h.out), "  already queued") {
		t.Fatal(h.out)
	}
}

// dated-facts.test.ts "queues the next batch with a cursor, and the CLI
// queues one job per workspace": the CLI half.
func TestBackfillDatesQueuesOneJobPerWorkspace(t *testing.T) {
	h := newHarness(t, "")
	const W, C = "ws_test", "zane"
	for i := 0; i < 41; i++ {
		if _, err := h.d.Exec(`INSERT INTO memories (id, workspace_id, container, fact, created_at) VALUES (?, ?, ?, ?, ?)`, fmt.Sprintf("mem_%03d", i), W, C, fmt.Sprintf("Fact %d", i), 1_000+i); err != nil {
			t.Fatal(err)
		}
	}
	g := &testkit.Gateway{Replies: []string{`{"dates":[]}`, `{"dates":[{"ref":1,"due_date":"2027-05"}]}`}}
	r := jobs.NewRunner(h.d, pipeline.Handlers(h.d, pipeline.Deps{Do: g.Do, Now: time.Now}), func(string) {})
	if _, err := users.CreateWorkspace(h.d, "Test", W, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateWorkspace(h.d, "Empty", "ws_empty", "empty"); err != nil {
		t.Fatal(err)
	}
	if c := h.run("backfill-dates", "--json"); c != 0 {
		t.Fatal(c, h.err)
	}
	if !regexp.MustCompile(`^\[\{"workspace":"test","undated":41,"job":\d+\},\{"workspace":"empty","undated":0,"job":null\}\]$`).MatchString(h.out[0]) {
		t.Fatal(h.out)
	}
	for i := 0; i < 10; i++ {
		if j, _ := r.RunOnce(time.Now().UnixMilli()); j == nil {
			break
		}
	}
	if len(g.Prompts) != 2 || !strings.Contains(g.Prompts[1], "1. (stored 1970-01-01) Fact 40") {
		t.Fatal(g.Prompts)
	}
	m, _, _ := memories.GetMemory(h.d, W, "mem_040")
	if m.DueDate == nil || *m.DueDate != "2027-05-01" || *m.DuePrecision != "month" {
		t.Fatalf("%+v", m)
	}
	if st := col(t, h.d, `SELECT status FROM jobs WHERE type = 'backfill_dates' ORDER BY id`); !reflect.DeepEqual(st, []string{"done", "done"}) {
		t.Fatal(st)
	}
	// The text form: 40 facts the model gave no date are still undated.
	h.out = nil
	h.run("backfill-dates")
	if len(h.out) != 3 || !regexp.MustCompile(`^test: queued job #\d+ for 40 undated fact\(s\)$`).MatchString(h.out[0]) || h.out[1] != "empty: nothing to date" ||
		h.out[2] != "The running server's job loop picks these up (RECALL_JOBS must not be off). Watch its log for [recall:backfill]." {
		t.Fatal(h.out)
	}
}

// backup.test.ts "writes a gzipped snapshot that includes uncheckpointed WAL writes".
func TestBackupIncludesWAL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "recall.db")
	d, err := db.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Keep the rows in the WAL.
	if _, err := d.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Exec(`INSERT INTO memories (id, workspace_id, container, fact, keywords, kind, valid_from, created_at) VALUES (?, 'ws_test', 'c', ?, '', 'static', 0, 0)`, fmt.Sprintf("mem_%d", i), fmt.Sprintf("fact %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := os.Stat(src + "-wal"); err != nil || st.Size() == 0 {
		t.Fatal(st, err)
	}
	dest := filepath.Join(dir, "backups", "snap.db.gz")
	res, err := backup.DB(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dest)
	if res.Dest != dest || res.Bytes != st.Size() {
		t.Fatal(res)
	}
	if ents, _ := os.ReadDir(filepath.Dir(dest)); len(ents) != 1 || ents[0].Name() != "snap.db.gz" {
		t.Fatal(ents) // temp file removed
	}
	restored := filepath.Join(dir, "restored.db")
	f, _ := os.Open(dest)
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	f.Close()
	os.WriteFile(restored, b, 0o644)
	if int64(len(b)) != res.DBBytes {
		t.Fatal(len(b), res.DBBytes)
	}
	// File format bytes 18 and 19 are 1 in rollback-journal mode, 2 in WAL:
	// the snapshot is one self-contained file, as the TS build leaves it.
	if b[18] != 1 || b[19] != 1 {
		t.Fatalf("header write/read versions %d %d, want 1 1", b[18], b[19])
	}
	check, err := sql.Open("sqlite3", "file:"+restored+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var n int
	if err := check.QueryRow(`SELECT count(*) FROM memories`).Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}

func TestBackupMissingSource(t *testing.T) {
	dir := t.TempDir()
	if _, err := backup.DB(filepath.Join(dir, "missing.db"), filepath.Join(dir, "x.db.gz")); err == nil {
		t.Fatal("no error")
	}
}
