package pipeline_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

const W = "ws_test"

func TestMain(m *testing.M) {
	embed.HubURL = "http://127.0.0.1:1/"
	os.Setenv("GATEWAY_KEY", "zkg_test")
	os.Setenv("GATEWAY_URL", "http://gateway.test")
	os.Setenv("RECALL_EMBED", "inline")
	os.Setenv("MODELS_DIR", testutil.ModelsDir())
	if os.Getenv("ONNXRUNTIME_LIB") == "" {
		p, err := embed.DefaultLibPath(testutil.Root())
		if err != nil {
			panic(err)
		}
		os.Setenv("ONNXRUNTIME_LIB", p)
	}
	os.Exit(m.Run())
}

func openDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	d, err := db.Open(filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// sse is a gateway reply carrying text.
func sse(text string) string {
	chunks := []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "text-start", "id": "t0"},
		map[string]any{"type": "text-delta", "id": "t0", "delta": text},
		map[string]any{"type": "text-end", "id": "t0"},
		map[string]any{"type": "finish", "finishReason": "stop"},
	}
	var b strings.Builder
	for _, c := range chunks {
		j, _ := json.Marshal(c)
		b.WriteString("data: " + string(j) + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// scripted plays the model: each extract call gets the next reply (JSON
// text), a profile call gets a fixed summary; prompts are recorded.
type scripted struct {
	mu      sync.Mutex
	replies []string
	prompts []string
	systems []string
	calls   int
	// hook runs before an extract reply (tests that delete mid-call).
	hook  func()
	fail  error
	reply func(system, prompt string) string
}

func (s *scripted) do(r *http.Request) (*http.Response, error) {
	var body struct {
		System   string `json:"system"`
		Messages []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"messages"`
	}
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &body)
	prompt := body.Messages[0].Parts[0].Text
	s.mu.Lock()
	s.prompts = append(s.prompts, prompt)
	s.systems = append(s.systems, body.System)
	s.calls++
	var text string
	switch {
	case s.fail != nil:
		s.mu.Unlock()
		return nil, s.fail
	case s.reply != nil:
		text = s.reply(body.System, prompt)
	case strings.Contains(body.System, "profile"):
		text = `{"static":"Static summary.","dynamic":"Dynamic summary."}`
	default:
		text = `{"add":[]}`
		if len(s.replies) > 0 {
			text = s.replies[0]
			s.replies = s.replies[1:]
		}
	}
	hook := s.hook
	s.mu.Unlock()
	if hook != nil && !strings.Contains(body.System, "profile") {
		hook()
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse(text)))}, nil
}

func (s *scripted) deps(now time.Time) pipeline.Deps {
	return pipeline.Deps{Do: s.do, Now: func() time.Time { return now }}
}

func newRunner(d *db.DB, deps pipeline.Deps) *jobs.Runner {
	return jobs.NewRunner(d, pipeline.Handlers(d, deps), func(string) {})
}

func drain(t *testing.T, r *jobs.Runner) {
	t.Helper()
	for i := 0; i < 50; i++ {
		j, err := r.RunOnce(time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			return
		}
	}
}

func str(s string) *string { return &s }

func addText(t *testing.T, d *db.DB, container, content string) api.AddResult {
	t.Helper()
	r, err := api.AddContent(d, W, api.AddInput{Container: container, Content: &content}, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func addChat(t *testing.T, d *db.DB, container, content string, sourceID *string) api.AddResult {
	t.Helper()
	r, err := api.AddContent(d, W, api.AddInput{Container: container, Messages: []api.ChatMessage{{Role: "user", Content: content}}, SourceID: sourceID}, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func memoriesWhere(t *testing.T, d *db.DB, where string, args ...any) []model.Memory {
	t.Helper()
	rows, err := d.Query(`SELECT `+model.MemoryCols+` FROM memories `+where, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []model.Memory
	for rows.Next() {
		m, err := model.ScanMemory(rows)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func current(t *testing.T, d *db.DB) []model.Memory {
	return memoriesWhere(t, d, "WHERE valid_to IS NULL")
}

func currentFacts(t *testing.T, d *db.DB, container string) []string {
	var out []string
	for _, m := range memoriesWhere(t, d, "WHERE valid_to IS NULL AND container = ?", container) {
		out = append(out, m.Fact)
	}
	sort.Strings(out)
	return out
}

func documentsAll(t *testing.T, d *db.DB) []model.Document {
	t.Helper()
	rows, err := d.Query(`SELECT ` + model.DocumentCols + ` FROM documents`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []model.Document
	for rows.Next() {
		doc, err := model.ScanDocument(rows)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, doc)
	}
	return out
}

func docByID(t *testing.T, d *db.DB, id string) model.Document {
	for _, doc := range documentsAll(t, d) {
		if doc.ID == id {
			return doc
		}
	}
	t.Fatalf("no document %s", id)
	return model.Document{}
}

func extractStats(t *testing.T, doc model.Document) map[string]any {
	t.Helper()
	v, _ := doc.Metadata.Get("extract")
	b, _ := jsonx.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func expectStats(t *testing.T, got map[string]any, want map[string]float64) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("stats %s = %v, want %v (all %v)", k, got[k], v, got)
		}
	}
}

func jobsAll(t *testing.T, d *db.DB) []jobs.Job {
	rows, _ := d.Query("SELECT " + jobs.Cols + " FROM jobs ORDER BY id")
	defer rows.Close()
	var out []jobs.Job
	for rows.Next() {
		j, _ := jobs.ScanJob(rows)
		out = append(out, j)
	}
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %#v\nwant %#v", got, want)
	}
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }
