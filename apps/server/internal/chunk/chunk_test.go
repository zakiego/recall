package chunk_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/chunk"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

func words(n int, w string) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", w, i)
	}
	return strings.Join(out, " ")
}

func ptr(n int) *int { return &n }

// chunk.test.ts, all four cases.
func TestChunkTextCases(t *testing.T) {
	if got := chunk.Text("  \n ", chunk.Options{}); len(got) != 0 {
		t.Fatalf("%q", got)
	}
	if got := chunk.Text("hello there\n\nsecond paragraph", chunk.Options{}); len(got) != 1 || got[0] != "hello there\n\nsecond paragraph" {
		t.Fatalf("%q", got)
	}
	text := strings.Join([]string{words(500, "a"), words(500, "b"), words(500, "c")}, "\n\n")
	chunks := chunk.Text(text, chunk.Options{Size: ptr(800), Overlap: ptr(100)})
	if len(chunks) < 2 {
		t.Fatal("not split")
	}
	for _, c := range chunks {
		if len(strings.Fields(c)) > 900 {
			t.Fatal("chunk too long")
		}
	}
	first := strings.Fields(chunks[0])
	if !strings.HasPrefix(chunks[1], strings.Join(first[len(first)-100:], " ")) {
		t.Fatal("no overlap")
	}
	if got := chunk.Text(words(2000, "word"), chunk.Options{Size: ptr(800), Overlap: ptr(100)}); len(got) != 3 {
		t.Fatalf("%d chunks", len(got))
	}
}

func TestChunkTextMatchesTS(t *testing.T) {
	var g struct {
		Chunks []struct {
			Text string `json:"text"`
			Opts *struct {
				Size    *int `json:"size"`
				Overlap *int `json:"overlap"`
			} `json:"opts"`
			Chunks []string `json:"chunks"`
		} `json:"chunks"`
	}
	testutil.LoadJSON(t, "text.json", &g)
	if len(g.Chunks) < 100 {
		t.Fatal("golden incomplete")
	}
	for i, c := range g.Chunks {
		var o chunk.Options
		if c.Opts != nil {
			o = chunk.Options{Size: c.Opts.Size, Overlap: c.Opts.Overlap}
		}
		got := chunk.Text(c.Text, o)
		if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", c.Chunks) {
			t.Errorf("case %d: got %d chunks, want %d\n got %q\nwant %q", i, len(got), len(c.Chunks), trunc(got), trunc(c.Chunks))
		}
	}
}

func trunc(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		if len(x) > 80 {
			x = x[:80] + "..."
		}
		out[i] = x
	}
	return out
}
