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

// SSE is a chat-completions reply stream carrying text.
func SSE(text string) string {
	var b strings.Builder
	for _, delta := range []map[string]any{{"role": "assistant", "content": ""}, {"content": text}} {
		j, _ := json.Marshal(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
		b.WriteString("data: " + string(j) + "\n\n")
	}
	return b.String() + `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
}

// ChatRequest is the system and user prompt of a /v1/chat/completions body.
func ChatRequest(b []byte) (system, prompt string) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	json.Unmarshal(b, &body)
	for _, m := range body.Messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			if prompt == "" {
				prompt = m.Content
			}
		}
	}
	return system, prompt
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
	b, _ := io.ReadAll(r.Body)
	system, prompt := ChatRequest(b)
	g.mu.Lock()
	g.Systems = append(g.Systems, system)
	g.Prompts = append(g.Prompts, prompt)
	reply := g.Reply
	g.mu.Unlock()
	text := ""
	ok := false
	if reply != nil {
		text, ok = reply(system, prompt)
	}
	if !ok {
		g.mu.Lock()
		switch {
		case strings.Contains(system, "profile"):
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
