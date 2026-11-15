package graph_test

// graph.test.ts: the Graph page's data.

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/graph"
)

const (
	W = "ws_graph"
	C = "zaki"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

func openDB(t *testing.T) *db.DB {
	d, err := db.Open(filepath.Join(t.TempDir(), "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func doc(t *testing.T, d *db.DB, id string, createdAt int64, container string) {
	if _, err := d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, title, status, created_at) VALUES (?, ?, ?, 'email', ?, 'done', ?)`, id, W, container, "Title "+id, createdAt); err != nil {
		t.Fatal(err)
	}
}

type memOpts struct {
	doc        *string
	at         int64
	supersedes *string
	validTo    *int64
	container  string
}

func mem(t *testing.T, d *db.DB, id string, o memOpts) {
	if o.container == "" {
		o.container = C
	}
	if _, err := d.Exec(`INSERT INTO memories (id, workspace_id, container, fact, source_document_id, supersedes_id, valid_from, valid_to, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, W, o.container, "fact "+id, o.doc, o.supersedes, o.at, o.validTo, o.at); err != nil {
		t.Fatal(err)
	}
}

func p[T any](v T) *T { return &v }

// seed: n documents, newest first (doc_0 is the newest), one fact each.
func seed(t *testing.T, d *db.DB, n int) {
	for i := 0; i < n; i++ {
		doc(t, d, fmt.Sprintf("doc_%d", i), t0-int64(i)*1000, C)
		mem(t, d, fmt.Sprintf("mem_%d", i), memOpts{doc: p(fmt.Sprintf("doc_%d", i)), at: t0 - int64(i)*1000 + 1})
	}
}

func node(g graph.Graph, id string) *graph.Document {
	for i := range g.Documents {
		if g.Documents[i].ID == id {
			return &g.Documents[i]
		}
	}
	return nil
}

func ids(n *graph.Document) []string {
	out := []string{}
	for _, m := range n.Memories {
		out = append(out, m.ID)
	}
	return out
}

func build(t *testing.T, d *db.DB, ws string) graph.Graph {
	g, err := graph.Build(d, ws, C)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDrawsEverythingUnderTheCap(t *testing.T) {
	d := openDB(t)
	seed(t, d, 3)
	mem(t, d, "mem_hand", memOpts{at: t0 - 5000})
	g := build(t, d, W)
	if g.Capped || g.ShownDocuments != 3 || g.TotalDocuments != 3 || g.HiddenFacts != 0 {
		t.Fatalf("%+v", g)
	}
	if fmt.Sprint(ids(node(g, "doc_0"))) != "[mem_0]" || fmt.Sprint(ids(node(g, "manual:"+C))) != "[mem_hand]" {
		t.Fatal("nodes")
	}
}

func TestLeavesOutFactsPastTheCap(t *testing.T) {
	d := openDB(t)
	n := graph.MaxDocuments + 50
	seed(t, d, n)
	mem(t, d, "mem_hand_1", memOpts{at: t0 - 10})
	mem(t, d, "mem_hand_2", memOpts{at: t0 - 20})
	g := build(t, d, W)
	if !g.Capped || g.ShownDocuments != graph.MaxDocuments || g.TotalDocuments != int64(n) || g.HiddenFacts != 50 {
		t.Fatalf("%+v", g.HiddenFacts)
	}
	orphan := node(g, "manual:"+C)
	got := ids(orphan)
	sort.Strings(got)
	if orphan.Title != "No source" || fmt.Sprint(got) != "[mem_hand_1 mem_hand_2]" || orphan.CreatedAt != graph.ISO(t0-20) || orphan.UpdatedAt != graph.ISO(t0-10) {
		t.Fatalf("%+v", orphan)
	}
	if node(g, "doc_0") == nil || len(node(g, fmt.Sprintf("doc_%d", graph.MaxDocuments-1)).Memories) != 1 || node(g, fmt.Sprintf("doc_%d", graph.MaxDocuments)) != nil {
		t.Fatal("window")
	}
	var drawn []string
	for i := range g.Documents {
		drawn = append(drawn, ids(&g.Documents[i])...)
	}
	if len(drawn) != graph.MaxDocuments+2 {
		t.Fatal(len(drawn))
	}
	for _, id := range drawn {
		if id == fmt.Sprintf("mem_%d", n-1) {
			t.Fatal("oldest drawn")
		}
	}
	if graph.ISO(t0-20) != "2025-12-31T23:59:59.980Z" {
		t.Fatal(graph.ISO(t0 - 20))
	}
}

func TestNoSyntheticNodeWhenAllHaveSources(t *testing.T) {
	d := openDB(t)
	seed(t, d, graph.MaxDocuments+1)
	g := build(t, d, W)
	if node(g, "manual:"+C) != nil || len(g.Documents) != graph.MaxDocuments {
		t.Fatal("synthetic node")
	}
}

func TestVersionsAcrossTheWindowEdge(t *testing.T) {
	d := openDB(t)
	seed(t, d, graph.MaxDocuments+2)
	old := fmt.Sprintf("doc_%d", graph.MaxDocuments+1)
	// v1 from an old document, v2 from a drawn one, and a drawn v1 replaced by a fact from an old document.
	mem(t, d, "v1", memOpts{doc: &old, at: t0 - 9_000_000, validTo: p(t0)})
	mem(t, d, "v2", memOpts{doc: p("doc_0"), at: t0, supersedes: p("v1")})
	mem(t, d, "w1", memOpts{doc: p("doc_1"), at: t0 - 5000, validTo: p(t0)})
	mem(t, d, "w2", memOpts{doc: &old, at: t0, supersedes: p("w1")})
	g := build(t, d, W)
	var v2, w1 graph.Memory
	for _, m := range node(g, "doc_0").Memories {
		if m.ID == "v2" {
			v2 = m
		}
	}
	for _, m := range node(g, "doc_1").Memories {
		if m.ID == "w1" {
			w1 = m
		}
	}
	if v2.Version != 2 || v2.RootMemoryID != "v1" || *v2.ParentMemoryID != "v1" || !v2.IsLatest {
		t.Fatalf("%+v", v2)
	}
	if w1.IsLatest || w1.IsForgotten || *w1.NextVersionID != "w2" {
		t.Fatalf("%+v", w1)
	}
	if g.HiddenFacts != 4 {
		t.Fatal(g.HiddenFacts)
	}
}

func TestOnlyItsOwnWorkspaceAndContainer(t *testing.T) {
	d := openDB(t)
	seed(t, d, 2)
	doc(t, d, "doc_other", t0, "other")
	mem(t, d, "mem_other", memOpts{doc: p("doc_other"), at: t0, container: "other"})
	mem(t, d, "mem_other_hand", memOpts{at: t0, container: "other"})
	g := build(t, d, W)
	if g.TotalDocuments != 2 || g.HiddenFacts != 0 || node(g, "manual:"+C) != nil {
		t.Fatalf("%+v", g)
	}
	if len(build(t, d, "ws_else").Documents) != 0 {
		t.Fatal("other workspace")
	}
}
