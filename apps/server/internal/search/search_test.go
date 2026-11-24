package search_test

import (
	"os"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

func TestMain(m *testing.M) {
	embed.HubURL = "http://127.0.0.1:1/" // never the real Hub
	os.Exit(m.Run())
}

// search.test.ts "ftsQuery".
func TestFtsQueryCases(t *testing.T) {
	if got, ok := search.FtsQuery(`Adidas OR "shoes" NEAR(x)`); !ok || got != `"adidas"* OR "or"* OR "shoes"* OR "near"*` {
		t.Fatalf("%q", got)
	}
	if _, ok := search.FtsQuery("!! ?"); ok {
		t.Fatal("expected nothing searchable")
	}
}

// search.test.ts "rrf rewards items found by both lists".
func TestRRFRewardsBoth(t *testing.T) {
	fused := search.RRF(
		[]search.Ranked{{ID: "a", Rank: 1, Raw: -3}, {ID: "b", Rank: 2, Raw: -2}},
		[]search.Ranked{{ID: "b", Rank: 1, Raw: 0.1}, {ID: "c", Rank: 2, Raw: 0.2}},
	)
	if fused[0].ID != "b" || fused[1].ID != "a" || fused[2].ID != "c" {
		t.Fatalf("%v %v %v", fused[0].ID, fused[1].ID, fused[2].ID)
	}
	if *fused[0].FtsRank != 2 || *fused[0].VecRank != 1 || *fused[0].Distance != 0.1 {
		t.Fatalf("%+v", fused[0])
	}
}

func setupEmbed(t *testing.T) {
	t.Setenv("MODELS_DIR", testutil.ModelsDir())
	t.Setenv("RECALL_EMBED", "inline")
	if os.Getenv("ONNXRUNTIME_LIB") == "" {
		p, err := embed.DefaultLibPath(testutil.Root())
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("ONNXRUNTIME_LIB", p)
	}
}

// search.test.ts "embed returns normalized 384-dim vectors".
func TestEmbedNormalized(t *testing.T) {
	setupEmbed(t)
	v, err := embed.EmbedOne("Zane suka sepatu Adidas", embed.Passage)
	if err != nil {
		t.Fatal(err)
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if len(v) != embed.Dims || n < 0.999 || n > 1.001 {
		t.Fatalf("%d %f", len(v), n)
	}
}

// search.test.ts "hybridSearch", with the real model.
func TestHybridSearchWithTheModel(t *testing.T) {
	setupEmbed(t)
	d := db.Memory()
	defer d.Close()
	const C, W = "test-user", "ws_search"
	facts := []struct{ key, fact, keywords string }{
		{"shoes", "Prefers Adidas running shoes", "sneakers sepatu lari footwear brand"},
		{"food", "Is allergic to peanuts", "alergi kacang food allergy"},
		{"city", "Lives in Bandarjaya, South Lembahraya", "tinggal kota domisili home city"},
	}
	var texts []string
	for _, f := range facts {
		texts = append(texts, f.fact+". "+f.keywords)
	}
	vectors, err := embed.Embed(texts, embed.Passage)
	if err != nil {
		t.Fatal(err)
	}
	memIDs := map[string]string{}
	for i, f := range facts {
		id := ids.New(ids.Memory)
		memIDs[f.key] = id
		if _, err := d.Exec("INSERT INTO memories (id, workspace_id, container, fact, keywords) VALUES (?, ?, ?, ?, ?)", id, W, C, f.fact, f.keywords); err != nil {
			t.Fatal(err)
		}
		if err := search.IndexMemory(d, search.MemoryRef{ID: id, WorkspaceID: W, Container: C, Fact: f.fact, Keywords: f.keywords}, vectors[i]); err != nil {
			t.Fatal(err)
		}
	}
	// A memory in another container must never leak.
	other := ids.New(ids.Memory)
	d.Exec("INSERT INTO memories (id, workspace_id, container, fact, keywords) VALUES (?, ?, 'someone-else', 'Prefers Nike shoes', '')", other, W)
	search.IndexMemory(d, search.MemoryRef{ID: other, WorkspaceID: W, Container: "someone-else", Fact: "Prefers Nike shoes"}, vectors[0])
	docID := ids.New(ids.Document)
	d.Exec("INSERT INTO documents (id, workspace_id, container, source_type, title, content) VALUES (?, ?, ?, 'text', 'Notes', 'x')", docID, W, C)
	chunkText := "The quarterly report covers revenue growth in Lembahraya."
	d.Exec("INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES ('chk_1', ?, ?, ?, 0, ?)", docID, W, C, chunkText)
	cv, _ := embed.EmbedOne(chunkText, embed.Passage)
	if err := search.IndexChunk(d, search.ChunkRef{ID: "chk_1", WorkspaceID: W, Container: C, Text: chunkText}, cv); err != nil {
		t.Fatal(err)
	}

	run := func(q string, include ...string) []search.Hit {
		v, err := embed.EmbedOne(q, embed.Query)
		if err != nil {
			t.Fatal(err)
		}
		hits, err := search.HybridSearch(d, search.Input{WorkspaceID: W, Container: C, Q: q, Vector: v, Include: include})
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	if h := run("sepatu apa yang dia suka?"); h[0].Type != "memory" || h[0].ID != memIDs["shoes"] {
		t.Errorf("by meaning: %+v", h[0])
	}
	if h := run("peanuts"); h[0].ID != memIDs["food"] || *h[0].FtsRank != 1 {
		t.Errorf("by keyword: %+v", h[0])
	}
	for _, h := range run("Nike shoes") {
		if h.Type != "memory" || (h.ID != memIDs["shoes"] && h.ID != memIDs["food"] && h.ID != memIDs["city"]) {
			t.Errorf("hit outside the container's memories: %+v", h)
		}
	}
	search.UnindexMemory(d, memIDs["city"])
	for _, h := range run("where does he live") {
		if h.ID == memIDs["city"] {
			t.Error("unindexed memory came back")
		}
	}
	if h := run("revenue report", "memories", "chunks"); h[0].Type != "chunk" || h[0].ID != "chk_1" || *h[0].DocumentTitle != "Notes" {
		t.Errorf("chunks: %+v", h[0])
	}
	start := time.Now()
	run("what shoes does he wear")
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Errorf("warm search took %s", el)
	}
}
