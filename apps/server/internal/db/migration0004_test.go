package db_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
)

// vec is a fixed unit vector for seed i (the TS test uses real embeddings;
// the search assertions are ported with search in M2).
func vec(i int) []byte {
	b := make([]byte, 1536)
	var norm float64
	vals := make([]float32, 384)
	for k := range vals {
		vals[k] = float32(math.Sin(float64(i*384+k) + 1))
		norm += float64(vals[k]) * float64(vals[k])
	}
	for k, v := range vals {
		binary.LittleEndian.PutUint32(b[k*4:], math.Float32bits(float32(float64(v)/math.Sqrt(norm))))
	}
	return b
}

var mainKeys = map[string]string{
	"laptop": "rcl_laptopKEY000000000000000000000000",
	"beacon": "rcl_beaconKEY000000000000000000000000",
	"old":    "rcl_oldKEY0000000000000000000000000000",
}

func seedMain(t *testing.T, d *db.DB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec("INSERT INTO documents (id, container, source_type, title, content, status, created_at) VALUES (?, ?, 'text', ?, ?, 'done', ?)", "doc_1", "zane", "Planning call", "Notes from the planning call about the Q4 roadmap.", 900)
	exec("INSERT INTO documents (id, container, source_type, title, url, status, created_at) VALUES (?, ?, 'url', ?, ?, 'queued', ?)", "doc_2", "vortek", "Brief", "https://example.com/brief", 950)
	exec("INSERT INTO chunks (id, document_id, container, idx, text) VALUES ('chk_1', 'doc_1', 'zane', 0, ?)", "Notes from the planning call about the Q4 roadmap.")
	exec("INSERT INTO chunks_fts (text, chunk_id, container) VALUES (?, 'chk_1', 'zane')", "Notes from the planning call about the Q4 roadmap.")
	exec("INSERT INTO chunks_vec (chunk_id, container, embedding) VALUES ('chk_1', 'zane', ?)", vec(99))
	type fact struct {
		id, container, fact, keywords, kind string
		created                             int64
		validTo                             any
		supersedes                          any
	}
	facts := []fact{
		{"mem_a1", "zane", "Prefers Adidas running shoes", "sepatu shoes", "dynamic", 1000, int64(2000), nil},
		{"mem_a2", "zane", "Switched to Puma running shoes", "sepatu shoes puma", "dynamic", 2000, nil, "mem_a1"},
		{"mem_a3", "zane", "Lives in Bandarjaya", "kota city", "static", 1100, nil, nil},
		{"mem_v1", "vortek", "Vortek sells property listings", "properti rumah", "static", 1200, nil, nil},
		{"mem_e1", "ida", "Drinks oat milk lattes", "kopi coffee", "dynamic", 1300, nil, nil},
	}
	for i, f := range facts {
		var src any
		if f.container == "zane" {
			src = "doc_1"
		}
		exec("INSERT INTO memories (id, container, fact, keywords, kind, source_document_id, supersedes_id, valid_from, valid_to, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			f.id, f.container, f.fact, f.keywords, f.kind, src, f.supersedes, f.created, f.validTo, f.created)
		if f.validTo == nil {
			exec("INSERT INTO memories_fts (fact, keywords, memory_id, container) VALUES (?, ?, ?, ?)", f.fact, f.keywords, f.id, f.container)
			exec("INSERT INTO memories_vec (memory_id, container, embedding) VALUES (?, ?, ?)", f.id, f.container, vec(i))
		}
	}
	exec("INSERT INTO profiles (container, static_text, dynamic_text) VALUES ('zane', 'Engineer in Bandarjaya.', 'Running in Puma.')")
	exec("INSERT INTO profiles (container, static_text, dynamic_text) VALUES ('vortek', 'Property site.', '')")
	exec(`INSERT INTO jobs (type, payload, status) VALUES ('profile', '{"container":"ida"}', 'queued')`)
	exec(`INSERT INTO jobs (type, payload, status) VALUES ('ingest', '{"documentId":"doc_2"}', 'queued')`)
	exec(`INSERT INTO jobs (type, payload, status) VALUES ('extract', '{"documentId":"doc_1"}', 'done')`)
	for _, name := range []string{"laptop", "beacon", "old"} {
		key := mainKeys[name]
		var revoked any
		if name == "old" {
			revoked = 5000
		}
		exec("INSERT INTO api_keys (id, name, key_hash, prefix, revoked_at) VALUES (?, ?, ?, ?, ?)", "key_"+name, name, auth.Sha256(key), key[:10], revoked)
	}
}

// migration.test.ts "migration 0004 on main's schema": every existing row ends
// up in workspace Zaki, indexes included, and the database checks clean. The
// first-boot user, sign-in and search halves of that test are ported with
// users (M6) and search (M2).
func TestMigration0004MovesRowsIntoZaki(t *testing.T) {
	d, err := db.OpenRaw(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var before []db.Migration
	for _, m := range db.Migrations() {
		if m.Name < "0004" {
			before = append(before, m)
		}
	}
	if err := db.Migrate(d, before); err != nil {
		t.Fatal(err)
	}
	seedMain(t, d)
	tables := []string{"documents", "chunks", "memories", "jobs", "api_keys", "profiles", "memories_fts", "chunks_fts", "memories_vec", "chunks_vec"}
	counts := map[string]int{}
	for _, tb := range tables {
		var n int
		if err := d.QueryRow("SELECT count(*) FROM " + tb).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[tb] = n
	}
	if err := db.Migrate(d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		rows, err := d.Query("SELECT workspace_id, count(*) FROM " + tb + " GROUP BY workspace_id")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var ws string
			var n int
			if err := rows.Scan(&ws, &n); err != nil {
				t.Fatal(err)
			}
			if ws != "ws_zaki" || n != counts[tb] {
				got = append(got, ws)
			}
		}
		rows.Close()
		if len(got) > 0 {
			t.Errorf("%s: rows outside ws_zaki or lost: %v", tb, got)
		}
	}
	var id, name, slug string
	if err := d.QueryRow("SELECT id, name, slug FROM organization").Scan(&id, &name, &slug); err != nil || id != "ws_zaki" || name != "Zaki" || slug != "zaki" {
		t.Fatalf("organization %s %s %s %v", id, name, slug, err)
	}
	idx := queryStrings(t, d, "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'documents'")
	has := func(n string) bool {
		for _, x := range idx {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("documents_workspace_source_id_unique") || has("documents_container_source_id_unique") {
		t.Fatalf("indexes %v", idx)
	}
	rows, err := d.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign_key_check reports rows")
	}
	rows.Close()
	var ok string
	if err := d.QueryRow("PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("integrity_check %q %v", ok, err)
	}
	// Old keys work, scoped to Zaki. The revoked one stays revoked.
	for _, n := range []string{"laptop", "beacon"} {
		k, err := auth.VerifyAPIKey(d, mainKeys[n], 10_000_000)
		if err != nil || k == nil || k.WorkspaceID != "ws_zaki" {
			t.Fatalf("%s: %+v %v", n, k, err)
		}
	}
	if k, _ := auth.VerifyAPIKey(d, mainKeys["old"], 10_000_000); k != nil {
		t.Fatal("revoked key verified")
	}
	// The indexes still answer in the new scope.
	var hit string
	if err := d.QueryRow(`SELECT memory_id FROM memories_vec WHERE embedding MATCH ? AND k = 1 AND workspace_id = 'ws_zaki' AND container = 'zane'`, vec(1)).Scan(&hit); err != nil || hit != "mem_a2" {
		t.Fatalf("knn %q %v", hit, err)
	}
	if err := d.QueryRow(`SELECT chunk_id FROM chunks_fts WHERE chunks_fts MATCH '"roadmap"*' AND workspace_id = 'ws_zaki' AND container = 'zane'`).Scan(&hit); err != nil || hit != "chk_1" {
		t.Fatalf("fts %q %v", hit, err)
	}
	profiles := queryStrings(t, d, "SELECT container || ':' || static_text FROM profiles ORDER BY container")
	if len(profiles) != 2 || profiles[0] != "vortek:Property site." || profiles[1] != "zane:Engineer in Bandarjaya." {
		t.Fatalf("profiles %v", profiles)
	}
}
