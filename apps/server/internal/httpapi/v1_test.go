package httpapi_test

// api.test.ts (all 5), add.test.ts (all 13), the REST and MCP halves of
// tenancy.test.ts (8), dated-facts.test.ts's PATCH, upcoming, search and
// Ask, and MCP cases (4), and the REST half of people.test.ts's tenant
// isolation.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

type env struct {
	t   *testing.T
	d   *db.DB
	s   *httpapi.Server
	srv *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testkit.OpenDB(t)
	s := &httpapi.Server{DB: d}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, d: d, s: s, srv: srv}
}

func (e *env) key(ws string) string {
	e.t.Helper()
	key, _, err := auth.CreateAPIKey(e.d, ws, "test", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return key
}

// call sends a JSON request; body nil sends none.
func (e *env) call(method, path, key string, body any) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func obj(v any) map[string]any {
	o, _ := v.(map[string]any)
	return o
}

func vec(t *testing.T, text string) []float32 {
	t.Helper()
	v, err := embed.EmbedOne(text, embed.Passage)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// --- api.test.ts ----------------------------------------------------------------------------

func TestReviewFlagsOverREST(t *testing.T) {
	e := newEnv(t)
	const W = "ws_test"
	key := e.key(W)
	v := vec(t, "salary")
	ins := func(fact, kw, kind string, note *string) string {
		m, err := memories.InsertMemory(e.d, memories.NewMemory{WorkspaceID: W, Container: "c", Fact: fact, Keywords: kw, Kind: kind, ReviewNote: note}, v)
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	note := `Conflicts with "Salary is paid monthly"`
	stale := "stale"
	other := ins("Lives in Banjarbaru", "", "static", nil)
	flagged := ins("Salary is paid weekly", "gaji", "dynamic", &note)
	ins("Old flagged", "", "dynamic", &stale)
	e.d.Exec(`UPDATE memories SET valid_to = 1 WHERE fact = 'Old flagged'`)

	// lists only flagged current facts with review=1
	_, all := e.call("GET", "/v1/memories?container=c", key, nil)
	if n := len(list(all["memories"])); n != 2 {
		t.Fatal(n)
	}
	status, body := e.call("GET", "/v1/memories?container=c&review=1&history=1", key, nil)
	got := list(body["memories"])
	if status != 200 || len(got) != 1 || obj(got[0])["id"] != flagged || obj(got[0])["reviewNote"] != note {
		t.Fatal(status, body)
	}

	// resolves a flag without writing a new version
	status, body = e.call("PATCH", "/v1/memories/"+flagged, key, map[string]any{"resolve": true})
	m := obj(body["memory"])
	if status != 200 || m["id"] != flagged || m["reviewNote"] != nil || m["validTo"] != nil {
		t.Fatal(status, body)
	}
	if chain, _ := memories.Chain(e.d, W, flagged, 500); len(chain) != 1 {
		t.Fatal(len(chain))
	}
	if _, l := e.call("GET", "/v1/memories?container=c&review=true", key, nil); len(list(l["memories"])) != 0 {
		t.Fatal(l)
	}

	// clears the flag when the fact is edited
	e.d.Exec(`UPDATE memories SET review_note = 'check' WHERE id = ?`, other)
	_, body = e.call("PATCH", "/v1/memories/"+other, key, map[string]any{"fact": "Lives in Martapura"})
	m = obj(body["memory"])
	if m["fact"] != "Lives in Martapura" || m["reviewNote"] != nil || m["supersedesId"] != other {
		t.Fatal(body)
	}
	if old, _, _ := memories.GetMemory(e.d, W, other); old.ValidTo == nil {
		t.Fatal("old version still current")
	}

	// rejects an empty patch
	if status, _ := e.call("PATCH", "/v1/memories/"+flagged, key, map[string]any{"resolve": false}); status != 400 {
		t.Fatal(status)
	}
	// refuses to resolve a superseded version
	if status, _ := e.call("PATCH", "/v1/memories/"+other, key, map[string]any{"resolve": true}); status != 409 {
		t.Fatal(status)
	}
}

// --- add.test.ts ----------------------------------------------------------------------------

// endless is a body that never ends, counting what was read.
type endless struct{ pulled int }

func (r *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.pulled += len(p)
	return len(p), nil
}

func TestBodySizeCap(t *testing.T) {
	e := newEnv(t)
	key := e.key("ws_test")
	h := e.s.Handler()
	for _, c := range []struct {
		typ string
		max int64
	}{{"application/json", httpapi.MaxJSONBytes}, {"multipart/form-data; boundary=x", httpapi.MaxMultipartBytes}} {
		for _, declared := range []bool{true, false} {
			body := &endless{}
			req := httptest.NewRequest("POST", "/v1/memories", body)
			req.Header.Set("authorization", "Bearer "+key)
			req.Header.Set("content-type", c.typ)
			req.ContentLength = -1
			if declared {
				req.ContentLength = c.max + 1
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var out map[string]any
			json.Unmarshal(rec.Body.Bytes(), &out)
			if rec.Code != 413 || obj(out["error"])["type"] != "too_large" {
				t.Fatal(c.typ, declared, rec.Code, rec.Body.String())
			}
			limit := int(c.max) + 4*1024*1024
			if declared {
				limit = 2 * 1024 * 1024
			}
			if body.pulled > limit {
				t.Fatalf("%s declared=%v: read %d bytes", c.typ, declared, body.pulled)
			}
		}
	}
}

type addEnv struct {
	*env
	key string
	dir string
}

func newAddEnv(t *testing.T) *addEnv {
	e := newEnv(t)
	return &addEnv{env: e, key: e.key("ws_test"), dir: os.Getenv("DATA_DIR")}
}

// startRunner starts the job loop with a gateway that answers extract calls
// from a script.
func (a *addEnv) startRunner(replies ...string) {
	g := &testkit.Gateway{Replies: replies, Default: `{"add":[]}`}
	r := jobs.NewRunner(a.d, pipeline.Handlers(a.d, pipeline.Deps{Do: g.Do, Now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}), func(string) {})
	r.Start()
	a.t.Cleanup(r.Stop)
}

func (a *addEnv) post(body map[string]any, query string) (int, map[string]any) {
	return a.call("POST", "/v1/memories"+query, a.key, body)
}

func (a *addEnv) postFile(fields map[string]string, query string) (int, map[string]any) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	fw.Write([]byte("Owns a grey cat named Abu."))
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", a.srv.URL+"/v1/memories"+query, &buf)
	req.Header.Set("authorization", "Bearer "+a.key)
	req.Header.Set("content-type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (a *addEnv) count(table string) int {
	var n int
	a.d.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
	return n
}

func TestSourceIDRepeatReturnsExisting(t *testing.T) {
	a := newAddEnv(t)
	status, first := a.post(map[string]any{"container": "zaki", "content": "Flight to Jakarta on Friday", "source_id": "gmail:1a1ab7843db48820"}, "")
	if status != 202 || first["container"] != "zaki" || first["status"] != "queued" || first["sourceId"] != "gmail:1a1ab7843db48820" || first["duplicate"] != false {
		t.Fatal(status, first)
	}
	status, second := a.post(map[string]any{"container": "zaki", "content": "Flight to Jakarta on Friday (re-sent)", "source_id": "gmail:1a1ab7843db48820"}, "")
	if status != 200 || second["id"] != first["id"] || second["duplicate"] != true {
		t.Fatal(status, second)
	}
	if a.count("documents") != 1 || a.count("jobs") != 1 {
		t.Fatal(a.count("documents"), a.count("jobs"))
	}
}

func TestSourceIDOtherContainer(t *testing.T) {
	a := newAddEnv(t)
	_, first := a.post(map[string]any{"container": "zaki", "content": "x", "source_id": "gmail:abc"}, "")
	status, second := a.post(map[string]any{"container": "eva", "content": "x", "source_id": "gmail:abc"}, "")
	if status != 202 || second["duplicate"] != false || second["id"] == first["id"] || a.count("documents") != 2 {
		t.Fatal(status, second)
	}
}

func TestNoSourceIDNeverDuplicate(t *testing.T) {
	a := newAddEnv(t)
	a.post(map[string]any{"container": "zaki", "content": "same text"}, "")
	if status, _ := a.post(map[string]any{"container": "zaki", "content": "same text"}, ""); status != 202 || a.count("documents") != 2 {
		t.Fatal(status)
	}
}

func TestSourceIDTooLong(t *testing.T) {
	a := newAddEnv(t)
	if status, _ := a.post(map[string]any{"container": "zaki", "content": "x", "source_id": strings.Repeat("s", 201)}, ""); status != 400 {
		t.Fatal(status)
	}
}

func TestMultipartDedupeBySourceID(t *testing.T) {
	a := newAddEnv(t)
	status, first := a.postFile(map[string]string{"container": "zaki", "source_id": "drive:notes"}, "")
	if status != 202 {
		t.Fatal(status, first)
	}
	status, second := a.postFile(map[string]string{"container": "zaki", "source_id": "drive:notes"}, "")
	if status != 200 || second["id"] != first["id"] || second["duplicate"] != true {
		t.Fatal(status, second)
	}
	entries, _ := os.ReadDir(filepath.Join(a.dir, "uploads"))
	if len(entries) != 1 || entries[0].Name() != first["id"] {
		t.Fatal(entries)
	}
}

func facts(v any) []string {
	var out []string
	for _, m := range list(v) {
		out = append(out, obj(m)["fact"].(string))
	}
	return out
}

func TestWaitReturnsExtractedFacts(t *testing.T) {
	a := newAddEnv(t)
	a.startRunner(`{"add":[{"fact":"Loves Adidas shoes","keywords":"sneakers sepatu","kind":"dynamic"}]}`, `{"update":[{"ref":1,"fact":"Switched to Puma shoes","keywords":"sneakers sepatu puma"}]}`)
	status, first := a.post(map[string]any{"container": "zaki", "content": "I love my Adidas shoes.", "wait": true}, "")
	if status != 200 || first["status"] != "done" || first["settled"] != true || first["error"] != nil || first["duplicate"] != false || len(list(first["updated"])) != 0 || first["invalidated"] != 0.0 {
		t.Fatal(status, first)
	}
	if got := facts(first["added"]); !reflect.DeepEqual(got, []string{"Loves Adidas shoes"}) {
		t.Fatal(got)
	}
	status, second := a.post(map[string]any{"container": "zaki", "content": "Switched to Puma.", "source_id": "chat:42"}, "?wait=1")
	up := list(second["updated"])
	if status != 200 || len(list(second["added"])) != 0 || len(up) != 1 || obj(up[0])["fact"] != "Switched to Puma shoes" || obj(up[0])["kind"] != "dynamic" || obj(up[0])["supersedesId"] != obj(list(first["added"])[0])["id"] {
		t.Fatal(status, second)
	}
	// A repeated source_id with wait returns the finished original.
	status, again := a.post(map[string]any{"container": "zaki", "content": "Switched to Puma.", "source_id": "chat:42", "wait": true}, "")
	if status != 200 || again["id"] != second["id"] || again["duplicate"] != true || again["settled"] != true || again["status"] != "done" || !reflect.DeepEqual(again["updated"], second["updated"]) {
		t.Fatal(status, again)
	}
}

func TestWaitIncludesReviewNoteOverREST(t *testing.T) {
	a := newAddEnv(t)
	a.startRunner(`{"add":[{"fact":"Salary is paid weekly","keywords":"salary gaji","kind":"dynamic"}]}`, `{"conflict":[{"ref":1,"fact":"Salary is paid on the last Friday of the month","keywords":"salary gaji","note":"Contract says weekly"}]}`)
	a.post(map[string]any{"container": "zaki", "content": "Contract: salary is paid weekly.", "wait": true}, "")
	_, out := a.post(map[string]any{"container": "zaki", "content": "Email: salary goes out on the last Friday.", "wait": true}, "")
	added := list(out["added"])
	if len(added) != 1 || obj(added[0])["fact"] != "Salary is paid on the last Friday of the month" || obj(added[0])["supersedesId"] != nil || obj(added[0])["reviewNote"] == nil || obj(added[0])["reviewNote"] == "" {
		t.Fatal(out)
	}
}

func TestWaitOnMultipart(t *testing.T) {
	a := newAddEnv(t)
	a.startRunner(`{"add":[{"fact":"Owns a grey cat named Abu","keywords":"pet kucing","kind":"static"}]}`)
	status, out := a.postFile(map[string]string{"container": "zaki", "wait": "1", "wait_timeout": "30"}, "")
	if status != 200 || out["status"] != "done" || out["settled"] != true || !reflect.DeepEqual(facts(out["added"]), []string{"Owns a grey cat named Abu"}) {
		t.Fatal(status, out)
	}
}

func TestWaitTimesOutWith202(t *testing.T) {
	a := newAddEnv(t)
	started := time.Now()
	status, out := a.post(map[string]any{"container": "zaki", "content": "Something to remember", "wait": true, "wait_timeout": 0.2}, "")
	if time.Since(started) < 190*time.Millisecond || status != 202 || out["status"] != "queued" || out["settled"] != false || out["duplicate"] != false {
		t.Fatal(time.Since(started), status, out)
	}
	if id, _ := out["id"].(string); !strings.HasPrefix(id, "doc_") {
		t.Fatal(out)
	}
	if _, ok := out["added"]; ok {
		t.Fatal(out)
	}
}

func TestWaitStopsWhenClientGoesAway(t *testing.T) {
	a := newAddEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/memories?wait=1", strings.NewReader(`{"container":"zaki","content":"x"}`)).WithContext(ctx)
	req.Header.Set("authorization", "Bearer "+a.key)
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	time.AfterFunc(50*time.Millisecond, cancel)
	started := time.Now()
	a.s.Handler().ServeHTTP(rec, req)
	if rec.Code != 202 || time.Since(started) > 5*time.Second {
		t.Fatal(rec.Code, time.Since(started))
	}
}

func TestWaitTimeoutOver120(t *testing.T) {
	a := newAddEnv(t)
	if status, _ := a.post(map[string]any{"container": "zaki", "content": "x"}, "?wait=1&wait_timeout=121"); status != 400 {
		t.Fatal(status)
	}
	if status, _ := a.post(map[string]any{"container": "zaki", "content": "x", "wait": true, "wait_timeout": 121}, ""); status != 400 {
		t.Fatal(status)
	}
}

// --- tenancy.test.ts: REST and MCP ----------------------------------------------------------

type tenancy struct {
	*env
	A, B, C            string
	a, b               []string
	docA, docB         string
	keyA, keyB         string
	leakWords          []string
	mcpID              int
	lastMCPStatus      int
	lastMCPBodyForTest string
}

func newTenancy(t *testing.T) *tenancy {
	e := newEnv(t)
	tn := &tenancy{env: e, A: "ws_alpha", B: "ws_beta", C: "shared", leakWords: []string{"Nike", "Puma", "Jakarta", "beta"}}
	now := time.Now().UnixMilli()
	for _, x := range []struct {
		ws     string
		bucket *[]string
		facts  []string
	}{{tn.A, &tn.a, []string{"Prefers Adidas running shoes", "Lives in Banjarbaru"}}, {tn.B, &tn.b, []string{"Prefers Nike running shoes", "Lives in Jakarta"}}} {
		for _, f := range x.facts {
			m, err := memories.AddMemoryDirect(e.d, memories.NewMemory{WorkspaceID: x.ws, Container: tn.C, Fact: f, Keywords: "sepatu shoes kota city", Kind: "dynamic"}, now)
			if err != nil {
				t.Fatal(err)
			}
			*x.bucket = append(*x.bucket, m.ID)
		}
	}
	fact := "Switched to Puma running shoes"
	if _, _, err := memories.EditMemory(e.d, tn.B, tn.b[0], memories.Patch{Fact: &fact}, func() int64 { return time.Now().UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		ws, key, text string
		into          *string
	}{{tn.A, "docA", "The alpha quarterly report covers revenue in Kalimantan.", &tn.docA}, {tn.B, "docB", "The beta quarterly report covers revenue in Java.", &tn.docB}} {
		id := ids.New(ids.Document)
		*x.into = id
		if _, err := e.d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, title, content, status, created_at) VALUES (?, ?, ?, 'text', ?, ?, 'queued', ?)`, id, x.ws, tn.C, x.key+" notes", x.text, now); err != nil {
			t.Fatal(err)
		}
		chunk := ids.New(ids.Chunk)
		if _, err := e.d.Exec(`INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES (?, ?, ?, ?, 0, ?)`, chunk, id, x.ws, tn.C, x.text); err != nil {
			t.Fatal(err)
		}
		v, _ := embed.EmbedOne(x.text, embed.Passage)
		if err := search.IndexChunk(e.d, search.ChunkRef{ID: chunk, WorkspaceID: x.ws, Container: tn.C, Text: x.text}, v); err != nil {
			t.Fatal(err)
		}
	}
	e.d.Exec(`INSERT INTO profiles (workspace_id, container, static_text, dynamic_text, updated_at) VALUES (?, ?, 'alpha profile', '', ?), (?, ?, 'beta profile', '', ?)`, tn.A, tn.C, now, tn.B, tn.C, now)
	tn.keyA, tn.keyB = e.key(tn.A), e.key(tn.B)
	return tn
}

func (tn *tenancy) leaks(v any) []string {
	b, _ := json.Marshal(v)
	var out []string
	for _, w := range tn.leakWords {
		if strings.Contains(string(b), w) {
			out = append(out, w)
		}
	}
	return out
}

func TestTenancyREST(t *testing.T) {
	tn := newTenancy(t)

	// search, list, profile and containers only see the key's workspace
	status, body := tn.call("POST", "/v1/search", tn.keyA, map[string]any{"container": tn.C, "q": "Nike shoes Jakarta", "include": []string{"memories", "chunks"}})
	if status != 200 || len(list(body["results"])) == 0 || len(tn.leaks(body["results"])) > 0 {
		t.Fatal(status, body)
	}
	if _, l := tn.call("GET", "/v1/memories?container="+tn.C+"&history=1", tn.keyA, nil); len(tn.leaks(l)) > 0 {
		t.Fatal(l)
	}
	if _, p := tn.call("GET", "/v1/profile?container="+tn.C+"&q=Nike", tn.keyA, nil); len(tn.leaks(p)) > 0 {
		t.Fatal(p)
	}
	_, c := tn.call("GET", "/v1/containers", tn.keyB, nil)
	cs := list(c["containers"])
	if len(cs) != 1 || obj(cs[0])["container"] != tn.C || obj(cs[0])["memories"] != 2.0 {
		t.Fatal(c)
	}

	// upcoming only lists the key's workspace, in every container
	due := time.Now().UTC().Add(5 * 24 * time.Hour).Format("2006-01-02")
	tn.d.Exec(`UPDATE memories SET due_date = ?, due_precision = 'day' WHERE id = ?`, due, tn.b[1])
	path := "/v1/upcoming?container=" + tn.C + "&all=1&days=366&past_days=366"
	if status, a := tn.call("GET", path, tn.keyA, nil); status != 200 || len(list(a["memories"])) != 0 {
		t.Fatal(status, a)
	}
	_, b := tn.call("GET", path, tn.keyB, nil)
	bm := list(b["memories"])
	if len(bm) != 1 || obj(bm[0])["id"] != tn.b[1] || obj(bm[0])["daysLeft"] != 5.0 {
		t.Fatal(b)
	}
	tn.d.Exec(`UPDATE memories SET due_date = NULL, due_precision = NULL WHERE id = ?`, tn.b[1])

	// another workspace's memory and document ids are 404 for get, patch and delete
	bID := tn.b[1]
	for _, c := range []struct {
		method, path string
		body         any
	}{{"GET", "/v1/memories/" + bID, nil}, {"PATCH", "/v1/memories/" + bID, map[string]any{"fact": "x"}}, {"DELETE", "/v1/memories/" + bID, nil}, {"GET", "/v1/documents/" + tn.docB, nil}, {"DELETE", "/v1/documents/" + tn.docB, nil}} {
		if status, _ := tn.call(c.method, c.path, tn.keyA, c.body); status != 404 {
			t.Fatal(c.method, c.path, status)
		}
	}
	if m, _, _ := memories.GetMemory(tn.d, tn.B, bID); m.ValidTo != nil {
		t.Fatal("B's memory was forgotten")
	}
	if _, ok, _ := memories.GetDocument(tn.d, tn.B, tn.docB); !ok {
		t.Fatal("B's document was deleted")
	}

	// writes land in the key's workspace
	status, res := tn.call("POST", "/v1/memories", tn.keyB, map[string]any{"container": tn.C, "content": "beta only note"})
	if status != 202 {
		t.Fatal(status, res)
	}
	if _, ok, _ := memories.GetDocument(tn.d, tn.B, res["id"].(string)); !ok {
		t.Fatal("not in B")
	}
	if _, ok, _ := memories.GetDocument(tn.d, tn.A, res["id"].(string)); ok {
		t.Fatal("in A")
	}

	// rejects missing and revoked keys
	if status, _ := tn.call("GET", "/v1/containers", "rcl_nope", nil); status != 401 {
		t.Fatal(status)
	}
	key, row, _ := auth.CreateAPIKey(tn.d, tn.A, "temp", nil)
	if k, _ := auth.VerifyAPIKey(tn.d, key, time.Now().UnixMilli()); k == nil || k.WorkspaceID != tn.A {
		t.Fatal(k)
	}
	if ok, _ := auth.RevokeAPIKey(tn.d, tn.B, row.ID, time.Now().UnixMilli()); ok {
		t.Fatal("B revoked A's key")
	}
	if ok, _ := auth.RevokeAPIKey(tn.d, tn.A, row.ID, time.Now().UnixMilli()); !ok {
		t.Fatal("A could not revoke its key")
	}
	if status, _ := tn.call("GET", "/v1/containers", key, nil); status != 401 {
		t.Fatal(status)
	}
}

// mcpCall is a tools/call over POST /mcp; the parsed text of its result.
func (tn *tenancy) mcpCall(key, name string, args map[string]any) any {
	tn.t.Helper()
	tn.mcpID++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": tn.mcpID, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	req, _ := http.NewRequest("POST", tn.srv.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("mcp-protocol-version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		tn.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		tn.t.Fatal(res.StatusCode)
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	var v any
	if err := json.Unmarshal([]byte(out.Result.Content[0].Text), &v); err != nil {
		tn.t.Fatalf("%s: %s", name, out.Result.Content[0].Text)
	}
	return v
}

func TestTenancyMCP(t *testing.T) {
	tn := newTenancy(t)
	hits := tn.mcpCall(tn.keyA, "search_memories", map[string]any{"query": "Nike Puma shoes Jakarta", "container": tn.C, "include_chunks": true})
	if len(list(hits)) == 0 || len(tn.leaks(hits)) > 0 {
		t.Fatal(hits)
	}
	if p := tn.mcpCall(tn.keyA, "get_profile", map[string]any{"container": tn.C, "query": "shoes"}); len(tn.leaks(p)) > 0 {
		t.Fatal(p)
	}
	listed := obj(tn.mcpCall(tn.keyA, "list_memories", map[string]any{"container": tn.C, "history": true}))
	if len(tn.leaks(listed)) > 0 || !reflect.DeepEqual(listed["containers"], []any{tn.C}) {
		t.Fatal(listed)
	}

	// add_memory writes into the key's workspace
	out := obj(tn.mcpCall(tn.keyA, "add_memory", map[string]any{"content": "alpha only note", "container": "fresh-a"}))
	if _, ok, _ := memories.GetDocument(tn.d, tn.A, out["id"].(string)); !ok {
		t.Fatal("not in A")
	}
	if _, ok, _ := memories.GetDocument(tn.d, tn.B, out["id"].(string)); ok {
		t.Fatal("in B")
	}

	// refuses a missing key
	req, _ := http.NewRequest("POST", tn.srv.URL+"/mcp", strings.NewReader("{}"))
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
}

// --- dated-facts.test.ts: PATCH, upcoming, search, Ask, MCP ---------------------------------

func TestPatchShortDatesAndNull(t *testing.T) {
	e := newEnv(t)
	const W, C = "ws_test", "zaki"
	key := e.key(W)
	m, _ := memories.AddMemoryDirect(e.d, memories.NewMemory{WorkspaceID: W, Container: C, Fact: "Contract with Vorneo", Keywords: "contract", Kind: "dynamic"}, time.Now().UnixMilli())
	_, out := e.call("PATCH", "/v1/memories/"+m.ID, key, map[string]any{"event_date": "2026-01", "due_date": "2027-12-31"})
	mem := obj(out["memory"])
	if mem["eventDate"] != "2026-01-01" || mem["eventPrecision"] != "month" || mem["dueDate"] != "2027-12-31" || mem["duePrecision"] != "day" {
		t.Fatal(out)
	}
	_, out = e.call("PATCH", "/v1/memories/"+mem["id"].(string), key, map[string]any{"event_date": nil})
	mem = obj(out["memory"])
	if mem["eventDate"] != nil || mem["eventPrecision"] != nil || mem["dueDate"] != "2027-12-31" {
		t.Fatal(out)
	}
	for _, bad := range []string{"end of 2027", "2027-02-30"} {
		if status, _ := e.call("PATCH", "/v1/memories/"+mem["id"].(string), key, map[string]any{"due_date": bad}); status != 400 {
			t.Fatal(bad, status)
		}
	}
}

func seedDated(t *testing.T, d *db.DB, W, C string) {
	t.Helper()
	day := func(s string) *dates.Dated { return &dates.Dated{Date: s, Precision: "day"} }
	now := time.Now().UnixMilli()
	add := func(ws, c, fact, kw string, event, due *dates.Dated) model.Memory {
		m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: ws, Container: c, Fact: fact, Keywords: kw, Kind: "dynamic", Event: event, Due: due}, now)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	add(W, C, "Contract with Vorneo ends", "contract kontrak vorneo", day("2026-01-02"), day("2027-12-31"))
	add(W, C, "Car insurance renewal", "car insurance asuransi", nil, day("2026-11-16"))
	add(W, C, "Gym membership expired", "gym", nil, day("2026-11-07"))
	add(W, "work", "Tax report due", "tax pajak", nil, day("2026-11-11"))
	old := add(W, C, "Hosting ends", "hosting", nil, day("2026-11-20"))
	if _, _, err := memories.EditMemory(d, W, old.ID, memories.Patch{Due: memories.Value(day("2027-02-01"))}, func() int64 { return time.Now().UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	add("ws_other", C, "Other workspace lease ends", "lease", nil, day("2026-11-12"))
}

func TestUpcomingWindowScoped(t *testing.T) {
	e := newEnv(t)
	const W, C = "ws_test", "zaki"
	seedDated(t, e.d, W, C)
	now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC).UnixMilli()
	q := func(in api.UpcomingInput) []any {
		if in.Days == 0 {
			in.Days = 30
		}
		in.Container, in.TZ = C, "UTC"
		o, err := api.Upcoming(e.d, W, in, now)
		if err != nil {
			t.Fatal(err)
		}
		if today, _ := o.Get("today"); today != "2026-11-10" {
			t.Fatal(today)
		}
		ms, _ := o.Get("memories")
		b, _ := json.Marshal(ms)
		var out []any
		json.Unmarshal(b, &out)
		return out
	}
	pairs := func(l []any) [][2]any {
		var out [][2]any
		for _, m := range l {
			out = append(out, [2]any{obj(m)["fact"], obj(m)["daysLeft"]})
		}
		return out
	}
	if got := pairs(q(api.UpcomingInput{Days: 30})); !reflect.DeepEqual(got, [][2]any{{"Car insurance renewal", 6.0}}) {
		t.Fatal(got)
	}
	if got := facts(q(api.UpcomingInput{Days: 30, All: true})); !reflect.DeepEqual(got, []string{"Tax report due", "Car insurance renewal"}) {
		t.Fatal(got)
	}
	if got := pairs(q(api.UpcomingInput{Days: 30, PastDays: 7})); !reflect.DeepEqual(got, [][2]any{{"Gym membership expired", -3.0}, {"Car insurance renewal", 6.0}}) {
		t.Fatal(got)
	}
	if got := facts(q(api.UpcomingInput{Days: 366})); !reflect.DeepEqual(got, []string{"Car insurance renewal", "Hosting ends"}) {
		t.Fatal(got)
	}
	if b, _ := json.Marshal(q(api.UpcomingInput{Days: 366, All: true, PastDays: 366})); strings.Contains(string(b), "Other workspace") {
		t.Fatal(string(b))
	}
	key := e.key(W)
	if status, _ := e.call("GET", "/v1/upcoming?tz=Mars/Olympus", key, nil); status != 400 {
		t.Fatal(status)
	}
}

func TestSearchHitsCarryDatesAndAskIsTold(t *testing.T) {
	e := newEnv(t)
	const W, C = "ws_test", "zaki"
	seedDated(t, e.d, W, C)
	limit := 3
	res, err := api.Search(e.d, W, api.SearchInput{Container: C, Q: "when does my contract end", Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range res.Results {
		if h.Type == "memory" && strings.HasPrefix(h.Text, "Contract") {
			found = *h.EventDate == "2026-01-02" && *h.DueDate == "2027-12-31" && *h.DuePrecision == "day"
		}
	}
	if !found {
		t.Fatal("no dated contract hit")
	}
	g := &testkit.Gateway{Replies: []string{`{"answer":"It ends on 2027-12-31 [m1].","covered":true}`}}
	out, err := ask.AnswerQuestion(context.Background(), e.d, W, ask.Payload{Container: C, Question: "When does my contract end?"}, ask.Deps{Do: gateway.Do(g.Do), Now: func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	p := g.Prompts[0]
	if !regexp.MustCompile(`\[m\d+\] \(dynamic, [\d-]+, happened 2026-01-02, due 2027-12-31\) Contract with Vorneo ends`).MatchString(p) || !regexp.MustCompile(`\(dynamic, [\d-]+, due 2026-11-07, passed\) Gym membership expired`).MatchString(p) {
		t.Fatal(p)
	}
	if len(out.Citations) == 0 || out.Citations[0].Text != "Contract with Vorneo ends" {
		t.Fatal(out.Citations)
	}
}

func TestMCPShortDates(t *testing.T) {
	e := newEnv(t)
	const W, C = "ws_test", "zaki"
	seedDated(t, e.d, W, C)
	tn := &tenancy{env: e}
	key := e.key(W)
	up := obj(tn.mcpCall(key, "upcoming", map[string]any{"container": C, "days": 366}))
	first := obj(list(up["memories"])[0])
	if first["fact"] != "Car insurance renewal" || first["due_date"] != "2026-11-16" {
		t.Fatal(up)
	}
	if _, ok := first["days_left"].(float64); !ok {
		t.Fatal(first)
	}
	if text := mcpErrorText(t, e, key, "upcoming", map[string]any{"timezone": "Nope/Zone"}); text == "" {
		t.Fatal("no error")
	}
	listed := list(obj(tn.mcpCall(key, "list_memories", map[string]any{"container": C}))["memories"])
	for _, m := range listed {
		mm := obj(m)
		if strings.HasPrefix(mm["fact"].(string), "Contract") && (mm["event_date"] != "2026-01-02" || mm["due_date"] != "2027-12-31") {
			t.Fatal(mm)
		}
		if mm["fact"] == "Gym membership expired" {
			if _, ok := mm["event_date"]; ok {
				t.Fatal(mm)
			}
		}
	}
	hits := list(tn.mcpCall(key, "search_memories", map[string]any{"query": "contract Vorneo", "container": C}))
	if obj(hits[0])["due_date"] != "2027-12-31" {
		t.Fatal(hits)
	}
}

// mcpErrorText is the text of an isError tool result, "" when it isn't one.
func mcpErrorText(t *testing.T, e *env, key, name string, args map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	if !out.Result.IsError {
		return ""
	}
	return out.Result.Content[0].Text
}

// --- people.test.ts: the REST half of tenant isolation --------------------------------------

func TestPeopleTenantIsolationREST(t *testing.T) {
	e := newEnv(t)
	const A, B = "ws_alpha", "ws_beta"
	scopeA, scopeB := search.Scope{WorkspaceID: A, Container: "shared"}, search.Scope{WorkspaceID: B, Container: "shared"}
	now := time.Now().UnixMilli()
	a, _ := memories.AddMemoryDirect(e.d, memories.NewMemory{WorkspaceID: A, Container: "shared", Fact: "Clara leads the redesign", Kind: "dynamic"}, now)
	b, _ := memories.AddMemoryDirect(e.d, memories.NewMemory{WorkspaceID: B, Container: "shared", Fact: "Clara runs payroll", Kind: "dynamic"}, now)
	clara := func(ids ...string) []people.Mention {
		return []people.Mention{{Mention: gateway.PersonMention{Name: "Clara", Aliases: []string{}, Facts: []string{}}, MemoryIDs: ids}}
	}
	if _, err := people.LinkMentions(e.d, scopeA, clara(a.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := people.LinkMentions(e.d, scopeB, clara(b.ID, a.ID)); err != nil {
		t.Fatal(err)
	}
	la, _ := people.List(e.d, people.ListOptions{WorkspaceID: A, Container: "shared"})
	lb, _ := people.List(e.d, people.ListOptions{WorkspaceID: B, Container: "shared"})
	ca, cb := la.People[0], lb.People[0]
	if _, err := api.Person(e.d, B, ca.ID, false); err == nil || !strings.Contains(err.Error(), "no such person") {
		t.Fatal(err)
	}
	if pl, _ := api.People(e.d, B, api.PeopleInput{Container: "shared"}); len(pl.People) != 1 || pl.People[0].ID != cb.ID {
		t.Fatal(pl)
	}
	// Search results only carry the searching workspace's people.
	limit := 5
	res, _ := api.Search(e.d, B, api.SearchInput{Container: "shared", Q: "Clara", Limit: &limit})
	for _, h := range res.Results {
		if h.Type == "memory" && h.ID == b.ID {
			if len(h.People) != 1 || h.People[0].ID != cb.ID {
				t.Fatal(h.People)
			}
		}
	}
	// REST: a key from B gets 404 for A's person and only B's list.
	key := e.key(B)
	if status, _ := e.call("GET", "/v1/people/"+ca.ID, key, nil); status != 404 {
		t.Fatal(status)
	}
	_, mine := e.call("GET", "/v1/people/"+cb.ID, key, nil)
	if fs := list(mine["facts"]); len(fs) != 1 || obj(fs[0])["id"] != b.ID {
		t.Fatal(mine)
	}
	_, all := e.call("GET", "/v1/people?container=shared", key, nil)
	if ps := list(all["people"]); len(ps) != 1 || obj(ps[0])["id"] != cb.ID {
		t.Fatal(all)
	}
	_ = fmt.Sprint
}
