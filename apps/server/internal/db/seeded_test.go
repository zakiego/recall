package db

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdata(parts ...string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(append([]string{filepath.Dir(file), "..", "..", "testdata"}, parts...)...)
}

// copySeeded copies testdata/search/seeded.db into a temp dir.
func copySeeded(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "recall.db")
	in, err := os.Open(testdata("search", "seeded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

type rankedRow struct {
	ID  string  `json:"id"`
	Raw float64 `json:"raw"`
}

type seededResult struct {
	Q     string `json:"q"`
	Scope struct {
		WorkspaceID string `json:"workspaceId"`
		Container   string `json:"container"`
	} `json:"scope"`
	Vector      string      `json:"vector"`
	Match       *string     `json:"match"`
	FtsMemories []rankedRow `json:"ftsMemories"`
	FtsChunks   []rankedRow `json:"ftsChunks"`
	VecMemories []rankedRow `json:"vecMemories"`
	VecChunks   []rankedRow `json:"vecChunks"`
}

type seededFile struct {
	Vec     string         `json:"vec"`
	Results []seededResult `json:"results"`
}

func loadSeeded(t *testing.T) *seededFile {
	t.Helper()
	raw, err := os.ReadFile(testdata("search", "seeded.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f seededFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func ranked(t *testing.T, d *DB, query string, args ...any) []rankedRow {
	t.Helper()
	rows, err := d.R.Query(query, args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	defer rows.Close()
	out := []rankedRow{}
	for rows.Next() {
		var r rankedRow
		if err := rows.Scan(&r.ID, &r.Raw); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameRanked(t *testing.T, what string, got, want []rankedRow, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d rows, want %d", what, len(got), len(want))
		return
	}
	for i := range want {
		if got[i].ID != want[i].ID || math.Abs(got[i].Raw-want[i].Raw) > tol {
			t.Errorf("%s row %d: got %s %.12g, want %s %.12g", what, i, got[i].ID, got[i].Raw, want[i].ID, want[i].Raw)
		}
	}
}

const (
	sqlFtsMemories = `SELECT memory_id AS id, bm25(memories_fts, 1.0, 0.5) AS raw
       FROM memories_fts WHERE memories_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`
	sqlFtsChunks = `SELECT chunk_id AS id, bm25(chunks_fts) AS raw
       FROM chunks_fts WHERE chunks_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`
	sqlVecMemories = `SELECT memory_id AS id, distance AS raw FROM memories_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`
	sqlVecChunks = `SELECT chunk_id AS id, distance AS raw FROM chunks_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`
)

// The Go build reads the database the TS build wrote: sqlite-vec reports the
// same version, and FTS5 and KNN queries return the same rows in the same
// order with the same scores (distances are float32: within 1e-6, since the
// float32 rounding differs between compilers and platforms).
func TestReadsTheTSSeededDatabase(t *testing.T) {
	f := loadSeeded(t)
	d, err := OpenRaw(copySeeded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	v, err := d.VecVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != f.Vec || v != "v0.1.9" {
		t.Fatalf("vec_version %q, want %q", v, f.Vec)
	}
	if len(f.Results) < 30 {
		t.Fatalf("only %d results in the golden", len(f.Results))
	}
	nonEmpty := 0
	for _, r := range f.Results {
		vec, err := base64.StdEncoding.DecodeString(r.Vector)
		if err != nil || len(vec) != 1536 {
			t.Fatalf("bad vector for %q", r.Q)
		}
		name := r.Q + " in " + r.Scope.WorkspaceID + "/" + r.Scope.Container
		ws, c := r.Scope.WorkspaceID, r.Scope.Container
		if r.Match != nil {
			fm := ranked(t, d, sqlFtsMemories, *r.Match, ws, c, 30)
			fc := ranked(t, d, sqlFtsChunks, *r.Match, ws, c, 30)
			sameRanked(t, "fts memories "+name, fm, r.FtsMemories, 1e-9)
			sameRanked(t, "fts chunks "+name, fc, r.FtsChunks, 1e-9)
			nonEmpty += len(fm) + len(fc)
		}
		sameRanked(t, "vec memories "+name, ranked(t, d, sqlVecMemories, vec, 30, ws, c), r.VecMemories, 1e-6)
		sameRanked(t, "vec chunks "+name, ranked(t, d, sqlVecChunks, vec, 30, ws, c), r.VecChunks, 1e-6)
	}
	if nonEmpty == 0 {
		t.Fatal("no FTS query returned rows")
	}
}

// Go writes one row to each indexed table; the rows are found again by
// FTS and KNN. With RECALL_M0_OUT set, the written file is kept there so the
// TS build can open it (testdata/gen/verify-go-writes.ts).
func TestWritesEveryIndexedTable(t *testing.T) {
	path := copySeeded(t)
	d, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	f := loadSeeded(t)
	vec, _ := base64.StdEncoding.DecodeString(f.Results[0].Vector)
	ws, c := "ws_alpha00000000000", "default"
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO documents (id, workspace_id, container, source_type, title, content, status, created_at) VALUES (?, ?, ?, 'text', ?, ?, 'done', ?)`,
			[]any{"doc_gowrite000000000", ws, c, "Written by Go", "Gowritten zebra chunk text", 1700000900000}},
		{`INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES (?, ?, ?, ?, 0, ?)`,
			[]any{"chk_gowrite000000000", "doc_gowrite000000000", ws, c, "Gowritten zebra chunk text"}},
		{`INSERT INTO chunks_fts (text, chunk_id, workspace_id, container) VALUES (?, ?, ?, ?)`,
			[]any{"Gowritten zebra chunk text", "chk_gowrite000000000", ws, c}},
		{`INSERT INTO chunks_vec (chunk_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)`,
			[]any{"chk_gowrite000000000", ws, c, vec}},
		{`INSERT INTO memories (id, workspace_id, container, fact, keywords, kind, valid_from, created_at) VALUES (?, ?, ?, ?, ?, 'dynamic', ?, ?)`,
			[]any{"mem_gowrite000000000", ws, c, "Gowritten zebra memory", "zebra", 1700000900000, 1700000900000}},
		{`INSERT INTO memories_fts (fact, keywords, memory_id, workspace_id, container) VALUES (?, ?, ?, ?, ?)`,
			[]any{"Gowritten zebra memory", "zebra", "mem_gowrite000000000", ws, c}},
		{`INSERT INTO memories_vec (memory_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)`,
			[]any{"mem_gowrite000000000", ws, c, vec}},
	}
	for _, s := range stmts {
		if _, err := d.W.Exec(s.q, s.args...); err != nil {
			t.Fatalf("%v\n%s", err, s.q)
		}
	}
	fm := ranked(t, d, sqlFtsMemories, `"gowritten"*`, ws, c, 30)
	fc := ranked(t, d, sqlFtsChunks, `"gowritten"*`, ws, c, 30)
	if len(fm) != 1 || fm[0].ID != "mem_gowrite000000000" || len(fc) != 1 || fc[0].ID != "chk_gowrite000000000" {
		t.Fatalf("fts: %v %v", fm, fc)
	}
	vm := ranked(t, d, sqlVecMemories, vec, 1, ws, c)
	vc := ranked(t, d, sqlVecChunks, vec, 1, ws, c)
	if len(vm) != 1 || vm[0].Raw > 1e-6 || len(vc) != 1 || vc[0].Raw > 1e-6 {
		t.Fatalf("knn: %v %v", vm, vc)
	}
	var ok string
	if err := d.W.QueryRow("PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("integrity_check: %q %v", ok, err)
	}
	if _, err := d.W.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("RECALL_M0_OUT"); out != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
