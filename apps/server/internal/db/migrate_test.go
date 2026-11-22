package db_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type schemaRow struct {
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	TblName string  `json:"tblName"`
	SQL     *string `json:"sql"`
}

type m1Golden struct {
	Migrations []string    `json:"migrations"`
	Schema     []schemaRow `json:"schema"`
}

func queryStrings(t *testing.T, q db.Q, query string, args ...any) []string {
	t.Helper()
	rows, err := q.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// db.test.ts "loads sqlite-vec and applies every migration", and the schema
// Go builds is exactly the one the TS migrations build (every table, index,
// trigger and virtual table, with its SQL).
func TestOpenAppliesEveryMigration(t *testing.T) {
	var g m1Golden
	testutil.LoadJSON(t, "m1.json", &g)
	d := db.Memory()
	defer d.Close()
	v, err := d.VecVersion()
	if err != nil || !strings.HasPrefix(v, "v0.") {
		t.Fatalf("vec_version %q %v", v, err)
	}
	applied := queryStrings(t, d, "SELECT name FROM _migrations ORDER BY name")
	if strings.Join(applied, ",") != strings.Join(g.Migrations, ",") {
		t.Fatalf("applied %v, want %v", applied, g.Migrations)
	}
	var names []string
	for _, m := range db.Migrations() {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != strings.Join(g.Migrations, ",") {
		t.Fatalf("bundled %v, want %v", names, g.Migrations)
	}
	rows, err := d.Query("SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	var got []schemaRow
	for rows.Next() {
		var r schemaRow
		if err := rows.Scan(&r.Type, &r.Name, &r.TblName, &r.SQL); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != len(g.Schema) {
		have, want := map[string]bool{}, map[string]bool{}
		for _, r := range got {
			have[r.Type+" "+r.Name] = true
		}
		for _, r := range g.Schema {
			want[r.Type+" "+r.Name] = true
		}
		var extra, missing []string
		for k := range have {
			if !want[k] {
				extra = append(extra, k)
			}
		}
		for k := range want {
			if !have[k] {
				missing = append(missing, k)
			}
		}
		t.Fatalf("%d schema objects, want %d (testdata/m1.json); not in the golden: %v; missing: %v", len(got), len(g.Schema), extra, missing)
	}
	for i := range got {
		a, b := got[i], g.Schema[i]
		if a.Type != b.Type || a.Name != b.Name || a.TblName != b.TblName || (a.SQL == nil) != (b.SQL == nil) || (a.SQL != nil && *a.SQL != *b.SQL) {
			t.Errorf("schema object %d differs: %+v vs %+v", i, a, b)
		}
	}
	for _, tb := range []string{"documents", "chunks", "memories", "profiles", "jobs", "api_keys", "memories_fts", "memories_vec", "chunks_fts", "chunks_vec",
		"user", "session", "account", "verification", "passkey", "organization", "member", "invitation", "workspace_invites"} {
		found := false
		for _, r := range got {
			if r.Type == "table" && r.Name == tb {
				found = true
			}
		}
		if !found {
			t.Errorf("table %s missing", tb)
		}
	}
}

// db.test.ts "is idempotent".
func TestMigrateIsIdempotent(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
}

// db.test.ts "workspace guard".
func TestWorkspaceGuard(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	for _, q := range []string{
		"INSERT INTO memories (id, container, fact) VALUES ('m', 'c', 'f')",
		"INSERT INTO documents (id, container, source_type) VALUES ('d', 'c', 'text')",
		"INSERT INTO jobs (type, payload) VALUES ('ingest', '{}')",
		"INSERT INTO api_keys (id, name, key_hash, prefix) VALUES ('k', 'n', 'h', 'p')",
		"INSERT INTO profiles (workspace_id, container) VALUES ('', 'c')",
	} {
		if _, err := d.Exec(q); err == nil || !strings.Contains(err.Error(), "workspace_id is required") {
			t.Errorf("%s: %v", q, err)
		}
	}
	if _, err := d.Exec("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('m', 'ws', 'c', 'f')"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE memories SET workspace_id = '' WHERE id = 'm'"); err == nil || !strings.Contains(err.Error(), "workspace_id is required") {
		t.Fatalf("update: %v", err)
	}
}

// db.test.ts "migration names never reuse a number".
func TestMigrationNamesNeverReuseANumber(t *testing.T) {
	seen := map[string][]string{}
	for _, m := range db.Migrations() {
		seen[m.Name[:4]] = append(seen[m.Name[:4]], m.Name)
	}
	for n, names := range seen {
		if len(names) > 1 && n != "0006" {
			t.Errorf("number %s used by %v", n, names)
		}
	}
}

// Migrations never change once shipped: migrate.go records applied ones by
// name, and production has every file below applied (both 0006 files
// included). A renamed or edited file fails here, and so does a file with no
// entry: a new migration adds its name and SHA-256 here (AGENTS.md,
// Migrations). The first 13 are the files the TS build shipped
// (apps/app/drizzle until the Go port removed it), byte for byte.
var shippedMigrations = []struct{ name, sha256 string }{
	{"0000_init", "4bdd26a814d153eb83a8b21d76ecdec5109a2cfed19781fe6ebc8f5c507818e8"},
	{"0001_search_indexes", "8bde540e039a44cb3b211f20bee430decf725b2481bc7d9e8730a364a370c364"},
	{"0002_review_flags", "e7e6f8a09297d3d85369ed8403d46547ca9d4d27e5efebde2164dc0cb6820584"},
	{"0003_document_source_id", "d51bd1753b70f3217a9a7e6896f41a8722f35f6a4b37f28de6e036c4c4e1d20b"},
	{"0004_workspaces", "83f0f54304826947cc467f00de2b08293d82b0815274de2ffe116b3def4e7b58"},
	{"0005_source_id_per_workspace", "fbcf5f00f168b690d72b33007ebd940e4b2eaa03cc94b27119726fe21ef5c481"},
	{"0006_conflicts_with", "9ccdb2e0c8e2021edc072be34e5031394644ec6ed906aac3ca4f05defed253b4"},
	{"0006_connector_accounts", "a07f3ad813e6e96ce5d8f98e039001e7428cc01a0141bab22996aa560e4a76b2"},
	{"0007_dated_facts_reminders", "3cec5f08bce907956ad3efaa1b788c76a0122a409b4122903856c2bbb5e9079d"},
	{"0008_people", "387514fcf3ecbe2726fbfd7add56daf517393cb6c937c786f90b6a5776ecf7b4"},
	{"0009_app_connectors", "a35fb51ca2eea6dacdf43c003710a3d33a39a46ef1c0ce17235b4f88aef05fe5"},
	{"0010_job_timing", "5b655a5cfb1aa99994e1182a2200f144c698b05064f0c98fd7d9753d0e4ffe75"},
	{"0011_oauth_provider", "8b09acd495b7b1aadf784ae0e0c2915a7cc7682e96da1cb9c35b2780cfa88728"},
	{"0012_drop_legacy_feed_staging", "4c29b98ea6d31f9694a4415dc336b33bbc0ee98dd89289f2e5be80851bc83b99"},
}

func TestShippedMigrationsAreUnchanged(t *testing.T) {
	ms := db.Migrations()
	if len(ms) != len(shippedMigrations) {
		t.Fatalf("%d embedded migrations, %d in shippedMigrations: add the new file's name and sha256 there", len(ms), len(shippedMigrations))
	}
	for i, want := range shippedMigrations {
		sum := sha256.Sum256([]byte(ms[i].SQL))
		if ms[i].Name != want.name || hex.EncodeToString(sum[:]) != want.sha256 {
			t.Errorf("migration %d is %s (sha256 %x), shipped as %s (%s)", i, ms[i].Name, sum, want.name, want.sha256)
		}
	}
}

// migration.test.ts "migration 0010": an older database migrates forward and
// keeps its rows.
func TestMigration0010KeepsOldJobRows(t *testing.T) {
	d, err := db.OpenRaw(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var before []db.Migration
	for _, m := range db.Migrations() {
		if m.Name < "0010" {
			before = append(before, m)
		}
	}
	if err := db.Migrate(d, before); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO jobs (workspace_id, type, payload, status) VALUES ('ws_a', 'extract', '{"documentId":"d"}', 'done')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	var status string
	var started, finished *int64
	if err := d.QueryRow("SELECT status, started_at, finished_at FROM jobs").Scan(&status, &started, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "done" || started != nil || finished != nil {
		t.Fatalf("%s %v %v", status, started, finished)
	}
	rows, err := d.Query("EXPLAIN QUERY PLAN SELECT count(*) FROM memories m WHERE m.source_document_id = ? AND m.valid_to IS NULL", "doc_1")
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	rows.Close()
	if !strings.Contains(strings.Join(details, " "), "memories_source_document_idx") {
		t.Fatalf("plan %v", details)
	}
}

// migration.test.ts "migration 0011": the OAuth tables arrive next to existing
// users, and deleting a user takes their tokens.
func TestMigration0011OAuthTables(t *testing.T) {
	d, err := db.OpenRaw(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var before []db.Migration
	for _, m := range db.Migrations() {
		if m.Name < "0011" {
			before = append(before, m)
		}
	}
	if err := db.Migrate(d, before); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO user (id, name, email, username, created_at, updated_at) VALUES ('u1', 'zaki', 'zaki@x', 'zaki', 0, 0)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	if got := queryStrings(t, d, "SELECT username FROM user"); len(got) != 1 || got[0] != "zaki" {
		t.Fatalf("%v", got)
	}
	for _, q := range []string{
		`INSERT INTO oauth_client (id, client_id, redirect_uris) VALUES ('c1', 'claude', '["https://claude.ai/cb"]')`,
		`INSERT INTO oauth_access_token (id, token, client_id, user_id, reference_id, scopes) VALUES ('t1', 'h', 'claude', 'u1', 'ws', '["mcp"]')`,
		"DELETE FROM user WHERE id = 'u1'",
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var n int
	if err := d.QueryRow("SELECT count(*) FROM oauth_access_token").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d tokens left %v", n, err)
	}
}

// Migration 0012 drops what the removed Oslo, Finansial and WhatsApp adapters
// staged and keeps event lines.
func TestMigration0012DropsLegacyStaging(t *testing.T) {
	d, err := db.OpenRaw(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var before []db.Migration
	for _, m := range db.Migrations() {
		if m.Name < "0012" {
			before = append(before, m)
		}
	}
	if err := db.Migrate(d, before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO organization (id, name, slug, created_at) VALUES ('ws_a', 'A', 'a', 0)",
		"INSERT INTO app_connectors (id, workspace_id, provider, container, base_url) VALUES ('con_1', 'ws_a', 'chat', 'life', 'https://chat.example.com')",
		`INSERT INTO app_connector_staging (connector_id, day, item_key, payload, written_at) VALUES
			('con_1', '2026-11-22', 'ev:m1', '{}', NULL),
			('con_1', '2026-11-22', 'task:t1', '{}', NULL),
			('con_1', '2026-11-01', 'project-created:p1', '{}', 1),
			('con_1', '2026-11-22', 'wa:personal:x', '{}', NULL)`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	if got := queryStrings(t, d, "SELECT item_key FROM app_connector_staging"); len(got) != 1 || got[0] != "ev:m1" {
		t.Fatalf("%v", got)
	}
}

// A fresh database has no workspace (migration.test.ts).
func TestFreshDatabaseHasNoWorkspace(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	var n int
	if err := d.QueryRow("SELECT count(*) FROM organization").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

// Nested Tx is a savepoint: an inner error rolls back only the inner part.
func TestNestedTxIsASavepoint(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	err := d.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('a', 'ws', 'c', 'f')"); err != nil {
			return err
		}
		inner := tx.Tx(func(tx db.Q) error {
			if _, err := tx.Exec("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('b', 'ws', 'c', 'f')"); err != nil {
				return err
			}
			return os.ErrInvalid
		})
		if inner != os.ErrInvalid {
			t.Fatalf("inner: %v", inner)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := queryStrings(t, d, "SELECT id FROM memories ORDER BY id"); strings.Join(got, ",") != "a" {
		t.Fatalf("%v", got)
	}
}
