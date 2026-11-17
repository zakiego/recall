package mcp_test

// mcp.test.ts, all 11 cases (the TS file runs them in order on one
// database; each Go test builds what it needs), plus list_documents and
// retry_document, which came after the port.

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/mcp"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

const W = "ws_mcp"

type fixture struct {
	t      *testing.T
	d      *db.DB
	nextID int
}

func newFixture(t *testing.T) *fixture {
	t.Setenv("RECALL_DEFAULT_CONTAINER", "me")
	f := &fixture{t: t, d: testkit.OpenDB(t)}
	f.add("me", "Prefers Nike Pegasus running shoes", "shoes sepatu")
	return f
}

func (f *fixture) add(container, fact, kw string) model.Memory {
	f.t.Helper()
	m, err := memories.AddMemoryDirect(f.d, memories.NewMemory{WorkspaceID: W, Container: container, Fact: fact, Keywords: kw, Kind: "dynamic"}, time.Now().UnixMilli())
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

type rpcResult struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError      bool             `json:"isError"`
		ServerInfo   map[string]any   `json:"serverInfo"`
		Instructions string           `json:"instructions"`
		Tools        []map[string]any `json:"tools"`
	} `json:"result"`
	Error map[string]any `json:"error"`
}

func (f *fixture) rpcOn(d *db.DB, s *mcp.Server, method string, params any) rpcResult {
	f.t.Helper()
	f.nextID++
	msg := map[string]any{"jsonrpc": "2.0", "id": f.nextID, "method": method}
	// JSON.stringify leaves an undefined params out.
	if params != nil {
		msg["params"] = params
	}
	body, _ := json.Marshal(msg)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("mcp-protocol-version", "2025-06-18")
	rec := httptest.NewRecorder()
	if s == nil {
		s = &mcp.Server{DB: d, WS: W, Now: func() int64 { return time.Now().UnixMilli() }}
	}
	s.Serve(rec, req)
	if rec.Code != 200 {
		f.t.Fatal(rec.Code, rec.Body.String())
	}
	var out rpcResult
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (f *fixture) rpc(method string, params any) rpcResult { return f.rpcOn(f.d, nil, method, params) }

// tool calls a tool and parses its text as JSON.
func (f *fixture) tool(name string, args map[string]any) any {
	f.t.Helper()
	r := f.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	var v any
	if err := json.Unmarshal([]byte(r.Result.Content[0].Text), &v); err != nil {
		f.t.Fatalf("%s: %s", name, r.Result.Content[0].Text)
	}
	return v
}

func obj(v any) map[string]any { o, _ := v.(map[string]any); return o }
func list(v any) []any         { l, _ := v.([]any); return l }

func TestInitializesAndListsElevenTools(t *testing.T) {
	f := newFixture(t)
	init := f.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init.Result.ServerInfo["name"] != "recall" {
		t.Fatal(init)
	}
	var names []string
	for _, tool := range f.rpc("tools/list", nil).Result.Tools {
		names = append(names, tool["name"].(string))
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "add_memory,ask_memory,get_person,get_playbook,get_profile,list_documents,list_memories,list_people,retry_document,search_memories,upcoming" {
		t.Fatal(names)
	}
}

var playbookVersionLine = regexp.MustCompile(`^Playbook version: ([0-9a-f]{8})\n\n# Recall\n`)

func TestPlaybook(t *testing.T) {
	f := newFixture(t)
	r := f.rpc("tools/call", map[string]any{"name": "get_playbook"})
	if r.Result.IsError || len(r.Result.Content) != 1 || r.Result.Content[0].Type != "text" {
		t.Fatalf("get_playbook should return one text block: %+v", r)
	}
	doc := r.Result.Content[0].Text
	m := playbookVersionLine.FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("playbook starts with %q", doc[:min(len(doc), 80)])
	}
	version := m[1]
	if version != mcp.PlaybookVersion {
		t.Fatalf("served version %s, PlaybookVersion %s", version, mcp.PlaybookVersion)
	}

	// The instructions point at the playbook and carry the same version, so
	// a client can tell its cached copy is stale without calling the tool.
	init := f.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	instructions := init.Result.Instructions
	if !strings.Contains(instructions, "Call get_playbook before your first tool call") ||
		!strings.Contains(instructions, "(playbook version "+version+")") {
		t.Fatalf("instructions don't point at playbook %s: %q", version, instructions)
	}
	// They stay short: they are read in every conversation, and the rules
	// belong in the playbook.
	if len(instructions) > 1200 {
		t.Errorf("instructions are %d characters; move rules into playbook.md", len(instructions))
	}
	// Claude Code saves an MCP result over 50,000 characters to a file and
	// hands the model only a path, which a client without file tools can't
	// open. Trim or split the playbook rather than raising this.
	if len(doc) > 50000 {
		t.Errorf("playbook is %d characters, over 50,000", len(doc))
	}

	// get_playbook is listed first and read-only, and every tool is covered,
	// so a new tool comes with its rules.
	tools := f.rpc("tools/list", nil).Result.Tools
	if tools[0]["name"] != "get_playbook" {
		t.Errorf("first tool is %v", tools[0]["name"])
	}
	if a := obj(tools[0]["annotations"]); a["readOnlyHint"] != true {
		t.Errorf("get_playbook annotations %v", a)
	}
	for _, tl := range tools {
		if name := tl["name"].(string); !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("playbook.md doesn't mention %s", name)
		}
	}

	// It ignores arguments it doesn't take, like every tool here.
	if again := f.rpc("tools/call", map[string]any{"name": "get_playbook", "arguments": map[string]any{"container": "other"}}); again.Result.Content[0].Text != doc {
		t.Fatal("arguments changed the playbook")
	}
}

func TestPeopleToolsAndPeopleOnHits(t *testing.T) {
	f := newFixture(t)
	m := f.add("team", "Clara Simmons designs the Vorneo listing page", "")
	role := "Designer"
	if _, err := people.LinkMentions(f.d, search.Scope{WorkspaceID: W, Container: "team"}, []people.Mention{{Mention: gateway.PersonMention{Name: "Clara Simmons", Aliases: []string{"Clara"}, Role: &role, Facts: []string{}}, MemoryIDs: []string{m.ID}}}); err != nil {
		t.Fatal(err)
	}
	ps := list(obj(f.tool("list_people", map[string]any{"container": "team"}))["people"])
	if len(ps) != 1 || obj(ps[0])["name"] != "Clara Simmons" || obj(ps[0])["role"] != "Designer" || obj(ps[0])["facts"] != 1.0 {
		t.Fatal(ps)
	}
	one := obj(f.tool("get_person", map[string]any{"name": "clara", "container": "team"}))
	fs := list(one["facts"])
	if one["name"] != "Clara Simmons" || len(fs) != 1 || obj(fs[0])["fact"] != "Clara Simmons designs the Vorneo listing page" || obj(fs[0])["current"] != true {
		t.Fatal(one)
	}
	if !f.rpc("tools/call", map[string]any{"name": "get_person", "arguments": map[string]any{"name": "Nobody", "container": "team"}}).Result.IsError {
		t.Fatal("found Nobody")
	}
	if !f.rpc("tools/call", map[string]any{"name": "get_person", "arguments": map[string]any{"id": "per_missing"}}).Result.IsError {
		t.Fatal("found per_missing")
	}
	for _, h := range list(f.tool("search_memories", map[string]any{"query": "Vorneo listing designer", "container": "team"})) {
		if obj(h)["id"] == m.ID {
			if p := list(obj(h)["people"]); len(p) != 1 || p[0] != "Clara Simmons" {
				t.Fatal(h)
			}
			return
		}
	}
	t.Fatal("the fact was not a hit")
}

func TestSearchesDefaultContainer(t *testing.T) {
	f := newFixture(t)
	hits := list(f.tool("search_memories", map[string]any{"query": "what shoes"}))
	if obj(hits[0])["text"] != "Prefers Nike Pegasus running shoes" {
		t.Fatal(hits)
	}
}

func docIDs(t *testing.T, d *db.DB, where string) []string {
	rows, err := d.Query(`SELECT id FROM documents ` + where)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

func TestAddsQueuedDocument(t *testing.T) {
	f := newFixture(t)
	out := obj(f.tool("add_memory", map[string]any{"content": "Drinks dark roast coffee", "container": "other"}))
	if out["container"] != "other" || out["status"] != "queued" || !strings.Contains(strings.Join(docIDs(t, f.d, ""), ","), out["id"].(string)) {
		t.Fatal(out)
	}
}

func TestRejectsEmptyAdd(t *testing.T) {
	f := newFixture(t)
	if !f.rpc("tools/call", map[string]any{"name": "add_memory", "arguments": map[string]any{}}).Result.IsError {
		t.Fatal("empty add accepted")
	}
}

func TestProfileAndList(t *testing.T) {
	f := newFixture(t)
	p := obj(f.tool("get_profile", map[string]any{}))
	if !strings.Contains(strings.Join(strs(p["dynamicFacts"]), "|"), "Prefers Nike Pegasus running shoes") {
		t.Fatal(p)
	}
	l := obj(f.tool("list_memories", map[string]any{}))
	if len(list(l["memories"])) != 1 || !strings.Contains(strings.Join(strs(l["containers"]), "|"), "me") {
		t.Fatal(l)
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, x.(string))
	}
	return out
}

func TestRepeatedSourceID(t *testing.T) {
	f := newFixture(t)
	call := func() map[string]any {
		return obj(f.tool("add_memory", map[string]any{"content": "Dentist on Monday", "source_id": "gmail:dup", "container": "mcp-dup"}))
	}
	a, b := call(), call()
	if a["duplicate"] != false || b["id"] != a["id"] || b["duplicate"] != true || len(docIDs(t, f.d, "WHERE container = 'mcp-dup'")) != 1 {
		t.Fatal(a, b)
	}
}

func TestWaitReturnsExtractedFacts(t *testing.T) {
	f := newFixture(t)
	g := &testkit.Gateway{Default: `{"add":[{"fact":"Drinks oat milk lattes","keywords":"coffee kopi","kind":"dynamic"}]}`}
	r := jobs.NewRunner(f.d, pipeline.Handlers(f.d, pipeline.Deps{Do: g.Do, Now: time.Now}), func(string) {})
	r.Start()
	defer r.Stop()
	out := obj(f.tool("add_memory", map[string]any{"content": "I drink oat milk lattes", "container": "mcp-wait", "wait": true}))
	added := list(out["added"])
	if out["container"] != "mcp-wait" || out["status"] != "done" || len(added) != 1 || obj(added[0])["fact"] != "Drinks oat milk lattes" || obj(added[0])["kind"] != "dynamic" || len(list(out["updated"])) != 0 {
		t.Fatal(out)
	}
}

func TestUnexpectedToolErrorIsGeneric(t *testing.T) {
	f := newFixture(t)
	broken := testkit.OpenDB(t)
	broken.Close()
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	r := f.rpcOn(broken, nil, "tools/call", map[string]any{"name": "search_memories", "arguments": map[string]any{"query": "shoes"}})
	if !r.Result.IsError || r.Result.Content[0].Type != "text" || r.Result.Content[0].Text != "internal error" {
		t.Fatal(r)
	}
	if !strings.Contains(logged.String(), "[recall] mcp search_memories error:") {
		t.Fatal(logged.String())
	}
}

func TestAskLimitBeforeGateway(t *testing.T) {
	f := newFixture(t)
	limiter := ratelimit.New(1, 60_000)
	limiter.Take("key_busy", time.Now().UnixMilli())
	s := &mcp.Server{DB: f.d, WS: W, KeyID: "key_busy", AskLimiter: limiter, Now: func() int64 { return time.Now().UnixMilli() }}
	r := f.rpcOn(f.d, s, "tools/call", map[string]any{"name": "ask_memory", "arguments": map[string]any{"question": "what shoes?"}})
	if !r.Result.IsError || !regexp.MustCompile(`Too many asks with this API key\. Try again in \d+ s\.`).MatchString(r.Result.Content[0].Text) {
		t.Fatal(r)
	}
}

func TestNeedsReview(t *testing.T) {
	f := newFixture(t)
	call := func() []any {
		return list(obj(f.tool("list_memories", map[string]any{"needs_review": true}))["memories"])
	}
	if len(call()) != 0 {
		t.Fatal("flagged before any flag")
	}
	f.d.Exec(`UPDATE memories SET review_note = 'Conflicts with "Prefers Adidas"' WHERE container = 'me'`)
	flagged := call()
	if len(flagged) != 1 || obj(flagged[0])["fact"] != "Prefers Nike Pegasus running shoes" || obj(flagged[0])["reviewNote"] != `Conflicts with "Prefers Adidas"` {
		t.Fatal(flagged)
	}
}

// doc creates a document in ws and sets its status; failed ones get an error.
func (f *fixture) doc(ws, title, status string) string {
	f.t.Helper()
	content := title + " body"
	d, _, err := memories.CreateDocument(f.d, memories.NewDocument{WorkspaceID: ws, Container: "me", SourceType: "text", Content: &content, Title: &title}, time.Now().UnixMilli())
	if err != nil {
		f.t.Fatal(err)
	}
	var errText any
	if status == "failed" {
		errText = "gateway 502 after 4 attempts"
	}
	if _, err := f.d.Exec(`UPDATE documents SET status = ?, error = ? WHERE id = ?`, status, errText, d.ID); err != nil {
		f.t.Fatal(err)
	}
	return d.ID
}

// docState is the document's status, its error and how many ingest jobs name it.
func (f *fixture) docState(id string) (string, *string, int) {
	f.t.Helper()
	var status string
	var errText *string
	if err := f.d.QueryRow(`SELECT status, error FROM documents WHERE id = ?`, id).Scan(&status, &errText); err != nil {
		f.t.Fatal(err)
	}
	var jobsN int
	if err := f.d.QueryRow(`SELECT count(*) FROM jobs WHERE type = 'ingest' AND json_extract(payload, '$.documentId') = ?`, id).Scan(&jobsN); err != nil {
		f.t.Fatal(err)
	}
	return status, errText, jobsN
}

func TestListDocumentsFailedOnly(t *testing.T) {
	f := newFixture(t)
	f.doc(W, "Queued note", "queued")
	f.doc(W, "Done note", "done")
	failed := f.doc(W, "Failed note", "failed")
	f.doc("ws_other", "Someone else's failure", "failed")
	docs := list(obj(f.tool("list_documents", map[string]any{"status": "failed"}))["documents"])
	if len(docs) != 1 {
		t.Fatal(docs)
	}
	d := obj(docs[0])
	if d["id"] != failed || d["title"] != "Failed note" || d["status"] != "failed" || d["error"] != "gateway 502 after 4 attempts" || !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`).MatchString(d["createdAt"].(string)) {
		t.Fatal(d)
	}
	if all := list(obj(f.tool("list_documents", map[string]any{}))["documents"]); len(all) != 3 {
		t.Fatal(all)
	}
	if !f.rpc("tools/call", map[string]any{"name": "list_documents", "arguments": map[string]any{"status": "broken"}}).Result.IsError {
		t.Fatal("unknown status accepted")
	}
}

func TestRetryDocumentRequeuesFailed(t *testing.T) {
	f := newFixture(t)
	id := f.doc(W, "Failed note", "failed")
	_, _, before := f.docState(id)
	out := obj(f.tool("retry_document", map[string]any{"id": id}))
	if out["id"] != id || out["status"] != "queued" {
		t.Fatal(out)
	}
	status, errText, after := f.docState(id)
	if status != "queued" || errText != nil || after != before+1 {
		t.Fatal(status, errText, before, after)
	}
	// A second retry finds it queued, not failed.
	if r := f.rpc("tools/call", map[string]any{"name": "retry_document", "arguments": map[string]any{"id": id}}); !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "This one is queued.") {
		t.Fatal(r)
	}
}

func TestRetryDocumentRefusals(t *testing.T) {
	f := newFixture(t)
	email := "" // an email whose body has to be fetched again with the importer's Gmail access
	d, _, err := memories.CreateDocument(f.d, memories.NewDocument{WorkspaceID: W, Container: "me", SourceType: "email", Content: &email, Metadata: jsonx.ObjectOf("userId", "usr_zaki")}, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	f.d.Exec(`UPDATE documents SET status = 'failed', error = 'x' WHERE id = ?`, d.ID)
	cases := []struct{ name, id, want string }{
		{"done", f.doc(W, "Done note", "done"), "Only failed documents can be retried. This one is done."},
		{"foreign workspace", f.doc("ws_other", "Someone else's failure", "failed"), "in this workspace."},
		{"missing", "doc_missing", "in this workspace."},
		{"email needing the importer", d.ID, "Retry it from the dashboard."},
	}
	for _, c := range cases {
		var status string
		var jobsBefore int
		if c.id != "doc_missing" {
			status, _, jobsBefore = f.docState(c.id)
		}
		r := f.rpc("tools/call", map[string]any{"name": "retry_document", "arguments": map[string]any{"id": c.id}})
		if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, c.want) {
			t.Fatal(c.name, r)
		}
		if c.id != "doc_missing" {
			if s, _, n := f.docState(c.id); s != status || n != jobsBefore {
				t.Fatal(c.name, "changed:", s, n)
			}
		}
	}
}
