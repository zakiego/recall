package search_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type seeded struct {
	Results []struct {
		Q     string `json:"q"`
		Scope struct {
			WorkspaceID string `json:"workspaceId"`
			Container   string `json:"container"`
		} `json:"scope"`
		Vector         string          `json:"vector"`
		Hybrid         json.RawMessage `json:"hybrid"`
		HybridVariants []struct {
			Params struct {
				Limit   *int      `json:"limit"`
				Include *[]string `json:"include"`
			} `json:"params"`
			JSON string `json:"json"`
		} `json:"hybridVariants"`
	} `json:"results"`
	FtsQueries []struct {
		Q     string  `json:"q"`
		Match *string `json:"match"`
	} `json:"ftsQueries"`
	RRF []struct {
		Fts   []rankedJSON    `json:"fts"`
		Vec   []rankedJSON    `json:"vec"`
		Fused json.RawMessage `json:"fused"`
	} `json:"rrf"`
}

type rankedJSON struct {
	ID   string  `json:"id"`
	Rank int     `json:"rank"`
	Raw  float64 `json:"raw"`
}

func loadSeeded(t *testing.T) *seeded {
	var s seeded
	testutil.LoadJSON(t, "search/seeded.json", &s)
	return &s
}

func copySeededDB(t *testing.T) *db.DB {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "recall.db")
	in, err := os.Open(testutil.Testdata("search", "seeded.db"))
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
	out.Close()
	d, err := db.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func decodeVec(t *testing.T, s string) []float32 {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// SimilarityTolerance applies to the one field computed from a KNN distance
// (similarity = 1 - distance): vec0's float32 rounding differs by up to about
// 1.5e-7 between macOS and Linux builds. Every other field, ids and order are
// exact.
const SimilarityTolerance = 1e-4

// sameJSON compares two JSON documents with key order, exactly except for
// "similarity".
func sameJSON(got, want any, path string) error {
	switch w := want.(type) {
	case *jsonx.Object:
		g, ok := got.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("%s: got %T", path, got)
		}
		gk, wk := g.Keys(), w.Keys()
		if fmt.Sprint(gk) != fmt.Sprint(wk) {
			return fmt.Errorf("%s: keys %v, want %v", path, gk, wk)
		}
		for _, k := range wk {
			gv, _ := g.Get(k)
			wv, _ := w.Get(k)
			if k == "similarity" {
				gf, gok := gv.(float64)
				wf, wok := wv.(float64)
				if !gok || !wok || math.Abs(gf-wf) > SimilarityTolerance {
					return fmt.Errorf("%s.similarity: %v, want %v", path, gv, wv)
				}
				continue
			}
			if err := sameJSON(gv, wv, path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf("%s: got %v, want %d items", path, jsonx.Stringify(got), len(w))
		}
		for i := range w {
			if err := sameJSON(g[i], w[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		if jsonx.Stringify(got) != jsonx.Stringify(want) {
			return fmt.Errorf("%s: %s, want %s", path, jsonx.Stringify(got), jsonx.Stringify(want))
		}
		return nil
	}
}

func compareHits(t *testing.T, name string, hits []search.Hit, want []byte) {
	t.Helper()
	gotB, err := jsonx.Marshal(hits)
	if err != nil {
		t.Fatal(err)
	}
	// On the platform the golden was made on, the JSON text must be equal byte
	// for byte (escaping, number text); elsewhere similarity may differ.
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, want); err != nil {
			t.Fatal(err)
		}
		if string(gotB) != compact.String() {
			t.Errorf("%s: JSON text differs\n got %s\nwant %s", name, gotB, compact.String())
		}
	}
	g, err := jsonx.Parse(gotB)
	if err != nil {
		t.Fatal(err)
	}
	w, err := jsonx.Parse(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := sameJSON(g, w, name); err != nil {
		t.Error(err)
	}
}

// hybridSearch on the TS-seeded database gives the TS results: ids, order,
// scores, ranks and row fields, for every query, scope, limit and include.
func TestHybridSearchMatchesTS(t *testing.T) {
	s := loadSeeded(t)
	d := copySeededDB(t)
	n := 0
	for _, r := range s.Results {
		v := decodeVec(t, r.Vector)
		ten := 10
		hits, err := search.HybridSearch(d, search.Input{WorkspaceID: r.Scope.WorkspaceID, Container: r.Scope.Container, Q: r.Q, Vector: v, Limit: &ten, Include: []string{"memories", "chunks"}})
		if err != nil {
			t.Fatal(err)
		}
		compareHits(t, r.Q+" in "+r.Scope.Container, hits, r.Hybrid)
		n++
		for _, hv := range r.HybridVariants {
			in := search.Input{WorkspaceID: r.Scope.WorkspaceID, Container: r.Scope.Container, Q: r.Q, Vector: v, Limit: hv.Params.Limit}
			if hv.Params.Include != nil {
				in.Include = *hv.Params.Include
			}
			hits, err := search.HybridSearch(d, in)
			if err != nil {
				t.Fatal(err)
			}
			compareHits(t, fmt.Sprintf("%s in %s %+v", r.Q, r.Scope.Container, hv.Params), hits, []byte(hv.JSON))
			n++
		}
	}
	if n < 250 {
		t.Fatalf("only %d searches compared", n)
	}
}

func TestFtsQueryMatchesTS(t *testing.T) {
	s := loadSeeded(t)
	if len(s.FtsQueries) < 20 {
		t.Fatal("golden incomplete")
	}
	for _, c := range s.FtsQueries {
		got, ok := search.FtsQuery(c.Q)
		if (c.Match == nil) == ok || (c.Match != nil && got != *c.Match) {
			t.Errorf("FtsQuery(%q) = %q %v, want %v", c.Q, got, ok, c.Match)
		}
	}
}

func TestRRFMatchesTS(t *testing.T) {
	s := loadSeeded(t)
	for i, c := range s.RRF {
		conv := func(rs []rankedJSON) []search.Ranked {
			out := []search.Ranked{}
			for _, r := range rs {
				out = append(out, search.Ranked{ID: r.ID, Rank: r.Rank, Raw: r.Raw})
			}
			return out
		}
		fused := search.RRF(conv(c.Fts), conv(c.Vec))
		var list []any
		for _, f := range fused {
			o := jsonx.NewMap[any]()
			o.Set("id", f.ID)
			o.Set("score", f.Score)
			if f.FtsRank != nil {
				o.Set("ftsRank", *f.FtsRank)
			}
			if f.VecRank != nil {
				o.Set("vecRank", *f.VecRank)
			}
			if f.Distance != nil {
				o.Set("distance", *f.Distance)
			}
			list = append(list, o)
		}
		if got := jsonx.Stringify(list); got != jsonx.Stringify(mustParse(t, c.Fused)) {
			t.Errorf("rrf %d: %s, want %s", i, got, c.Fused)
		}
	}
}

func mustParse(t *testing.T, b []byte) any {
	v, err := jsonx.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
