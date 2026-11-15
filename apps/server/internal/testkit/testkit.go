// Package testkit holds what the domain tests share: a database in a temp
// dir, the in-process embedder without network, and a scripted gateway.
// Test code only.
package testkit

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

// Setup points the process at the local model files, inline embedding and a
// gateway that is never dialed. Call it from TestMain.
func Setup() {
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
}

// OpenDB opens a migrated database in a temp dir, which is also DATA_DIR.
func OpenDB(t *testing.T) *db.DB {
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

// SSE is a gateway reply stream carrying text.
func SSE(text string) string {
	var b strings.Builder
	for _, c := range []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "text-start", "id": "t0"},
		map[string]any{"type": "text-delta", "id": "t0", "delta": text},
		map[string]any{"type": "text-end", "id": "t0"},
		map[string]any{"type": "finish", "finishReason": "stop"},
	} {
		j, _ := json.Marshal(c)
		b.WriteString("data: " + string(j) + "\n\n")
	}
	return b.String() + "data: [DONE]\n\n"
}

// Gateway plays the model: a profile call (system mentions "profile") gets
// Profile, any other call the next of Replies, then Default. Reply, when
// set, answers first. Every call's system and prompt are recorded.
type Gateway struct {
	mu      sync.Mutex
	Replies []string
	Default string
	Profile string
	Reply   func(system, prompt string) (string, bool)
	Systems []string
	Prompts []string
}

// Do is the gateway.Do the code under test gets.
func (g *Gateway) Do(r *http.Request) (*http.Response, error) {
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
	prompt := ""
	if len(body.Messages) > 0 && len(body.Messages[0].Parts) > 0 {
		prompt = body.Messages[0].Parts[0].Text
	}
	g.mu.Lock()
	g.Systems = append(g.Systems, body.System)
	g.Prompts = append(g.Prompts, prompt)
	reply := g.Reply
	g.mu.Unlock()
	text := ""
	ok := false
	if reply != nil {
		text, ok = reply(body.System, prompt)
	}
	if !ok {
		g.mu.Lock()
		switch {
		case strings.Contains(body.System, "profile"):
			text = g.Profile
			if text == "" {
				text = `{"static":"","dynamic":""}`
			}
		case len(g.Replies) > 0:
			text, g.Replies = g.Replies[0], g.Replies[1:]
		default:
			text = g.Default
			if text == "" {
				text = `{}`
			}
		}
		g.mu.Unlock()
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(SSE(text)))}, nil
}

// Calls is how many calls were made, and how many were not profile calls.
func (g *Gateway) Calls() (all, nonProfile int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.Systems {
		if !strings.Contains(s, "profile") {
			nonProfile++
		}
	}
	return len(g.Systems), nonProfile
}
