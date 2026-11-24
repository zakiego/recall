package api_test

// add.test.ts "wait": waitForAdd with the real job loop and a scripted model.
// The HTTP half (status codes, query and form fields, body caps) is
// handleAdd's and is ported with the routes in M5.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testkit"
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

func sse(text string) string { return testkit.SSE(text) }

// startRunner starts the real job loop with a model that answers extract
// calls from a script.
func startRunner(t *testing.T, d *db.DB, replies ...string) {
	var mu sync.Mutex
	do := func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		text := `{"add":[]}`
		mu.Lock()
		if profileCall(b) {
			text = `{"static":"","dynamic":""}`
		} else if len(replies) > 0 {
			text, replies = replies[0], replies[1:]
		}
		mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse(text)))}, nil
	}
	r := jobs.NewRunner(d, pipeline.Handlers(d, pipeline.Deps{Do: do, Now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}), func(string) {})
	r.Start()
	t.Cleanup(r.Stop)
}

func profileCall(body []byte) bool {
	system, _ := testkit.ChatRequest(body)
	return strings.Contains(system, "profile")
}

func add(t *testing.T, d *db.DB, in api.AddInput, timeoutMs float64) api.WaitResult {
	t.Helper()
	res, err := api.AddContent(d, W, in, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	out, err := api.WaitForAdd(context.Background(), d, W, res, timeoutMs)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func text(container, content string) api.AddInput {
	return api.AddInput{Container: container, Content: &content}
}

func asJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func TestWaitReturnsExtractedFacts(t *testing.T) {
	d := openDB(t)
	startRunner(t, d,
		`{"add":[{"fact":"Loves Adidas shoes","keywords":"sneakers sepatu","kind":"dynamic"}]}`,
		`{"update":[{"ref":1,"fact":"Switched to Puma shoes","keywords":"sneakers sepatu puma"}]}`)
	a := add(t, d, text("zane", "I love my Adidas shoes."), 60_000)
	ja := asJSON(t, a)
	for k, v := range map[string]any{"container": "zane", "status": "done", "settled": true, "error": nil, "duplicate": false, "invalidated": float64(0)} {
		if ja[k] != v {
			t.Fatalf("%s = %v in %v", k, ja[k], ja)
		}
	}
	if len(a.Updated) != 0 || len(a.Added) != 1 || a.Added[0].Fact != "Loves Adidas shoes" {
		t.Fatalf("first: %+v", a)
	}
	in := text("zane", "Switched to Puma.")
	in.SourceID = strPtr("chat:42")
	b := add(t, d, in, 60_000)
	if len(b.Added) != 0 || len(b.Updated) != 1 || b.Updated[0].Fact != "Switched to Puma shoes" || b.Updated[0].Kind != "dynamic" || *b.Updated[0].SupersedesID != a.Added[0].ID {
		t.Fatalf("second: %+v", b)
	}
	// A repeated source_id with wait returns the finished original.
	again := add(t, d, in, 60_000)
	if again.ID != b.ID || !again.Duplicate || !again.Settled || again.Status != "done" || len(again.Updated) != 1 || again.Updated[0].ID != b.Updated[0].ID {
		t.Fatalf("again: %+v", again)
	}
}

func strPtr(s string) *string { return &s }

func TestWaitIncludesReviewNote(t *testing.T) {
	d := openDB(t)
	startRunner(t, d,
		`{"add":[{"fact":"Salary is paid weekly","keywords":"salary gaji","kind":"dynamic"}]}`,
		`{"conflict":[{"ref":1,"fact":"Salary is paid on the last Friday of the month","keywords":"salary gaji","note":"Contract says weekly"}]}`)
	add(t, d, text("zane", "Contract: salary is paid weekly."), 60_000)
	out := add(t, d, text("zane", "Email: salary goes out on the last Friday."), 60_000)
	if len(out.Added) != 1 || out.Added[0].Fact != "Salary is paid on the last Friday of the month" || out.Added[0].SupersedesID != nil || out.Added[0].ReviewNote == nil || *out.Added[0].ReviewNote == "" {
		t.Fatalf("%+v", out)
	}
}

func TestWaitTimesOut(t *testing.T) {
	d := openDB(t)
	// No job loop running, so the document stays queued.
	started := time.Now()
	out := add(t, d, text("zane", "Something to remember"), 200)
	if time.Since(started) < 190*time.Millisecond {
		t.Fatal("returned early")
	}
	j := asJSON(t, out)
	if j["status"] != "queued" || j["settled"] != false || j["duplicate"] != false || !strings.HasPrefix(out.ID, "doc_") {
		t.Fatalf("%v", j)
	}
	if _, ok := j["added"]; ok {
		t.Fatal("added on an unsettled wait")
	}
}

func TestWaitStopsWhenCallerGoesAway(t *testing.T) {
	d := openDB(t)
	res, err := api.AddContent(d, W, text("zane", "x"), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	started := time.Now()
	out, err := api.WaitForAdd(ctx, d, W, res, 60_000)
	if err != nil || out.Settled || time.Since(started) > 5*time.Second {
		t.Fatalf("%+v %v", out, err)
	}
}

// A job that finishes between WaitForDocument's first read and its
// subscription must still wake it: the listener is registered first.
func TestWaitForDocumentHearsAFinishRightAway(t *testing.T) {
	d := openDB(t)
	res, err := api.AddContent(d, W, text("zane", "x"), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE documents SET status = 'done' WHERE id = ?`, res.ID); err != nil {
		t.Fatal(err)
	}
	doc, ok, err := api.WaitForDocument(context.Background(), d, W, res.ID, 1_000)
	if err != nil || !ok || doc.Status != "done" {
		t.Fatalf("%v %v %v", doc.Status, ok, err)
	}
}
