package httpapi_test

// Cases from the M5 review: what the TS build answers where the goldens
// don't reach (router paths, url values, wire multipart, deep JSON, MCP
// requests the SDK refuses).

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/memories"
)

func (e *env) raw(method, path, key string, header http.Header, body io.Reader) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	for k, v := range header {
		req.Header[k] = v
	}
	if key != "" {
		req.Header.Set("authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestRouterPathsLikeTanStack(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	for _, p := range []string{"/Health", "/health/", "/app/health", "/V1/Containers", "/v1/containers/", "/app/v1/containers"} {
		if status, body := e.raw("GET", p, key, nil, nil); status != 200 {
			t.Fatal(p, status, body)
		}
	}
	mcpHeaders := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}}
	for _, p := range []string{"/mcp/", "/app/mcp", "/MCP"} {
		if status, body := e.raw("POST", p, key, mcpHeaders, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); status != 200 {
			t.Fatal(p, status, body)
		}
	}
	// An id keeps its case.
	if status, _ := e.raw("GET", "/v1/memories/MEM_NOPE/", key, nil, nil); status != 404 {
		t.Fatal(status)
	}
}

func TestURLValuesLikeZod(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	_, out := e.call("POST", "/v1/memories", key, map[string]any{"url": " https://exa\tmple.com/a "})
	doc, _, _ := memories.GetDocument(e.d, "ws_test", out["id"].(string))
	if doc.URL == nil || *doc.URL != "https://example.com/a" {
		t.Fatal(out, doc.URL)
	}
	if status, body := e.call("POST", "/v1/memories", key, map[string]any{"url": "http:example.com"}); status != 400 {
		t.Fatal(status, body)
	}
}

func TestDeepJSONIs400(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	body := strings.Repeat("[", 1_000_000)
	if status, b := e.raw("POST", "/v1/search", key, http.Header{"Content-Type": {"application/json"}}, strings.NewReader(body)); status != 400 || !strings.Contains(b, "invalid_json") {
		t.Fatal(status, b)
	}
}

func TestWireMultipart(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	send := func(disp string, typ *string) map[string]any {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		h := textproto.MIMEHeader{"Content-Disposition": {disp}}
		if typ != nil {
			h.Set("Content-Type", *typ)
		}
		pw, _ := mw.CreatePart(h)
		pw.Write([]byte("Notes in a file."))
		mw.Close()
		status, b := e.raw("POST", "/v1/memories", key, http.Header{"Content-Type": {mw.FormDataContentType()}}, &buf)
		var out map[string]any
		json.Unmarshal([]byte(b), &out)
		out["_status"] = status
		return out
	}
	meta := func(out map[string]any) map[string]any {
		doc, _, _ := memories.GetDocument(e.d, "ws_test", out["id"].(string))
		b, _ := json.Marshal(doc.Metadata)
		var m map[string]any
		json.Unmarshal(b, &m)
		m["_title"] = doc.Title
		return m
	}
	// No Content-Type: text/plain. A path in the filename stays.
	out := send(`form-data; name="file"; filename="reports/q3.txt"`, nil)
	if m := meta(out); out["_status"] != 202 || m["mime"] != "text/plain" || *(m["_title"].(*string)) != "reports/q3.txt" {
		t.Fatal(out, m)
	}
	// The type is lowercased.
	typ := "Text/Plain"
	if m := meta(send(`form-data; name="file"; filename="a.txt"`, &typ)); m["mime"] != "text/plain" {
		t.Fatal(m)
	}
	// An empty filename is still a file.
	if out := send(`form-data; name="file"; filename=""`, nil); out["_status"] != 202 {
		t.Fatal(out)
	}
}

// Malformed tools/call requests come back as tool errors, never as a
// result: go-sdk hands the raw arguments to the tool, which checks them
// against its shape like any other input.
func TestMCPMalformedRequests(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}}
	post := func(body string) map[string]any {
		_, b := e.raw("POST", "/mcp", key, h, strings.NewReader(body))
		var out map[string]any
		json.Unmarshal([]byte(b), &out)
		return out
	}
	for body, want := range map[string]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`:                                          "MCP error -32602: Tool  not found",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_memories","arguments":null}}`: "Invalid input: expected string, received undefined at query",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_memories","arguments":[]}}`:   "Invalid input: expected object, received array",
	} {
		r := obj(post(body)["result"])
		if r["isError"] != true || !strings.Contains(obj(list(r["content"])[0])["text"].(string), want) {
			t.Fatal(body, r)
		}
	}
	if out := post(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":"x"}}`); obj(out["error"])["code"] != -32602.0 {
		t.Fatal(out)
	}
	// A repeated id gets one answer.
	_, b := e.raw("POST", "/mcp", key, h, strings.NewReader(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":1,"method":"ping"}]`))
	var answers []any
	if json.Unmarshal([]byte(b), &answers); len(answers) != 1 {
		t.Fatal(b)
	}
	out := post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"constructor","arguments":{}}}`)
	if text := list(obj(out["result"])["content"]); len(text) != 1 || obj(text[0])["text"] != "MCP error -32602: Tool constructor disabled" {
		t.Fatal(out)
	}
	// A date the MCP regex lets through but parseDate refuses: internal error.
	out = post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_memory","arguments":{"content":"x","document_date":"2026-02-30"}}}`)
	if r := obj(out["result"]); r["isError"] != true || obj(list(r["content"])[0])["text"] != "internal error" {
		t.Fatal(out)
	}
}

func TestInfinityIsNotANumber(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	if _, out := e.call("GET", "/v1/memories?limit=Infinity", key, nil); !strings.Contains(obj(out["error"])["message"].(string), "received Infinity") {
		t.Fatal(out)
	}
}
