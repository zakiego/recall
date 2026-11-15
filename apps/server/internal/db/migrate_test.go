package db_test

import (
	"os"
	"path/filepath"
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
	if strings.Join(applied, ",") != strings.Join(g.Migrations, ",") || len(applied) != 13 {
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
		t.Fatalf("%d schema objects, want %d", len(got), len(g.Schema))
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

// The embedded copies equal apps/app/drizzle/*.sql byte for byte, and no file
// is missing on either side (db.test.ts "hand-written migrations").
func TestEmbeddedMigrationsMatchTheTSCopies(t *testing.T) {
	files, err := filepath.Glob(testutil.AppDir("drizzle", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ms := db.Migrations()
	if len(files) != len(ms) {
		t.Fatalf("%d TS migrations, %d embedded", len(files), len(ms))
	}
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSuffix(filepath.Base(f), ".sql") != ms[i].Name || string(b) != ms[i].SQL {
			t.Errorf("%s differs from the embedded %s", f, ms[i].Name)
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
