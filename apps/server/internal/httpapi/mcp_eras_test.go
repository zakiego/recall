package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/recall/apps/server/internal/memories"
)

// methodLog is an http.RoundTripper that adds the API key and records the
// JSON-RPC method of every request, so a test can tell which handshake a
// client used.
type methodLog struct {
	key     string
	mu      sync.Mutex
	methods []string
}

func (m *methodLog) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("authorization", "Bearer "+m.key)
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		var msg struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(b, &msg) == nil && msg.Method != "" {
			m.mu.Lock()
			m.methods = append(m.methods, msg.Method)
			m.mu.Unlock()
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (m *methodLog) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.methods)
}

// /mcp serves both protocol eras. go-sdk's client probes server/discover and
// stays on 2026-07-28; a client pinned to 2025-11-25 goes through initialize,
// and so does a 2025-06-18 client speaking raw JSON-RPC the way the TS SDK in
// mcporter and Claude Code does. All of them see the same tools in the same
// order and get the same results, through the same API key check.
func TestMCPServesBothProtocolEras(t *testing.T) {
	e := newEnv(t)
	const W, C = "ws_eras", "me"
	key := e.key(W)
	if _, err := memories.AddMemoryDirect(e.d, memories.NewMemory{WorkspaceID: W, Container: C, Fact: "Prefers Nike Pegasus running shoes", Keywords: "shoes sepatu", Kind: "dynamic"}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	connect := func(opts *sdk.ClientSessionOptions) (*sdk.ClientSession, *methodLog) {
		t.Helper()
		log := &methodLog{key: key}
		client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
		s, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
			Endpoint:             e.srv.URL + "/mcp",
			HTTPClient:           &http.Client{Transport: log},
			DisableStandaloneSSE: true,
			MaxRetries:           -1,
		}, opts)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s, log
	}
	modern, modernLog := connect(nil)
	legacy, legacyLog := connect(&sdk.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if v := modern.InitializeResult().ProtocolVersion; v != "2026-07-28" {
		t.Fatalf("modern client negotiated %q", v)
	}
	if v := legacy.InitializeResult().ProtocolVersion; v != "2025-11-25" {
		t.Fatalf("legacy client negotiated %q", v)
	}
	if m := modernLog.seen(); !slices.Contains(m, "server/discover") || slices.Contains(m, "initialize") {
		t.Fatalf("modern handshake: %v", m)
	}
	if m := legacyLog.seen(); !slices.Contains(m, "initialize") {
		t.Fatalf("legacy handshake: %v", m)
	}
	for _, s := range []*sdk.ClientSession{modern, legacy} {
		if r := s.InitializeResult(); r.ServerInfo.Name != "recall" || !strings.Contains(r.Instructions, "Call get_playbook before your first tool call") {
			t.Fatalf("server info %+v", r)
		}
	}

	names := func(s *sdk.ClientSession) []string {
		t.Helper()
		res, err := s.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
		}
		return out
	}
	m, l := names(modern), names(legacy)
	if !slices.Equal(m, l) || len(m) != 11 || m[0] != "get_playbook" {
		t.Fatalf("tools differ:\nmodern: %v\nlegacy: %v", m, l)
	}

	call := func(s *sdk.ClientSession, name string, args map[string]any) string {
		t.Helper()
		r, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.IsError || len(r.Content) != 1 {
			t.Fatalf("%s: %+v", name, r)
		}
		return r.Content[0].(*sdk.TextContent).Text
	}
	search := map[string]any{"query": "what shoes", "container": C}
	if m, l := call(modern, "search_memories", search), call(legacy, "search_memories", search); m != l || !strings.Contains(m, "Prefers Nike Pegasus running shoes") {
		t.Fatalf("search_memories differs:\nmodern: %s\nlegacy: %s", m, l)
	}
	if m, l := call(modern, "get_playbook", nil), call(legacy, "get_playbook", nil); m != l || !strings.HasPrefix(m, "Playbook version: ") {
		t.Fatal("get_playbook differs")
	}

	// The raw 2025-06-18 flow: initialize, notifications/initialized,
	// tools/list, tools/call, each a POST of its own with no session id.
	raw := func(body, version string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("authorization", "Bearer "+key)
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "application/json, text/event-stream")
		if version != "" {
			req.Header.Set("mcp-protocol-version", version)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if sid := res.Header.Get("mcp-session-id"); sid != "" {
			t.Fatalf("stateless server sent session id %q", sid)
		}
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	status, init := raw(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mcporter","version":"1"}}}`, "")
	if status != 200 || obj(init["result"])["protocolVersion"] != "2025-06-18" || obj(obj(init["result"])["serverInfo"])["name"] != "recall" {
		t.Fatal(status, init)
	}
	if status, _ := raw(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, "2025-06-18"); status != 202 {
		t.Fatal(status)
	}
	if _, out := raw(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "2025-06-18"); len(list(obj(out["result"])["tools"])) != 11 {
		t.Fatal(out)
	}
	_, out := raw(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_memories","arguments":{"query":"what shoes","container":"me"}}}`, "2025-06-18")
	if text := obj(list(obj(out["result"])["content"])[0])["text"]; text != call(modern, "search_memories", search) {
		t.Fatalf("raw tools/call: %v", out)
	}

	// No key, no handshake in either era.
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`,
	} {
		res, err := http.Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 || !strings.Contains(res.Header.Get("www-authenticate"), "Bearer") {
			t.Fatalf("%s: %d %q", body, res.StatusCode, res.Header.Get("www-authenticate"))
		}
	}
}
