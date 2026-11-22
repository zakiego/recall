package apps_test

// The pull connector against the fake changes feed: the feed client, how a
// connector is stored, syncing, document edits and deletes with their facts,
// and the hourly job. generic_test.go covers the item kinds themselves.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
)

const appKey = "app-key-SECRET-123"

type env struct {
	t    *testing.T
	d    *db.DB
	a, b string
}

func newEnv(t *testing.T) *env {
	t.Setenv("CONNECTOR_SECRET", "test-connector-secret-0123456789abcdefghijklmnop")
	d := testkit.OpenDB(t)
	wa, _ := users.CreateWorkspace(d, "Alpha", "", "")
	wb, _ := users.CreateWorkspace(d, "Beta", "", "")
	return &env{t, d, wa.ID, wb.ID}
}

func obj(kv ...any) *jsonx.Object { return jsonx.ObjectOf(kv...) }

func merge(base *jsonx.Object, extra *jsonx.Object) *jsonx.Object {
	out := base.Clone()
	if extra != nil {
		out.Assign(extra)
	}
	return out
}

func at(iso string) func() int64 {
	t, _ := time.Parse(time.RFC3339, iso)
	return func() int64 { return t.UnixMilli() }
}

func ms(iso string) int64 { return at(iso)() }

func deps(f *apps.FakeFeed, now string) apps.SyncDeps { return apps.SyncDeps{Do: f.Do, Now: at(now)} }

// baseOf is where a test feed lives: https://<provider>.example.com.
func baseOf(provider string) string { return "https://" + provider + ".example.com" }

func (e *env) setup(ws, provider, key string, settings *jsonx.Object, container string) apps.Connector {
	e.t.Helper()
	if container == "" {
		container = "life"
	}
	in := apps.SaveInput{WorkspaceID: ws, Provider: provider, Container: container, BaseURL: baseOf(provider), Enabled: true, Settings: merge(obj("timezone", "Asia/Makassar"), settings)}
	in.APIKey.Set, in.APIKey.Value = true, &key
	c, err := apps.Save(e.d, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) save(ws, baseURL string, enabled bool, key *string, keySet bool) (apps.Connector, error) {
	in := apps.SaveInput{WorkspaceID: ws, Provider: "notes", Container: "life", BaseURL: baseURL, Enabled: enabled}
	in.APIKey.Set, in.APIKey.Value = keySet, key
	return apps.Save(e.d, in)
}

func (e *env) docs(ws string) []model.Document {
	rows, err := e.d.Query(`SELECT `+model.DocumentCols+` FROM documents WHERE workspace_id = ? ORDER BY source_id`, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []model.Document
	for rows.Next() {
		d, _ := model.ScanDocument(rows)
		out = append(out, d)
	}
	return out
}

type stagedRow struct {
	day, key string
	written  bool
}

func (e *env) staged(connectorID string) []stagedRow {
	rows, _ := e.d.Query(`SELECT day, item_key, written_at IS NOT NULL FROM app_connector_staging WHERE connector_id = ? ORDER BY item_key`, connectorID)
	defer rows.Close()
	var out []stagedRow
	for rows.Next() {
		var r stagedRow
		rows.Scan(&r.day, &r.key, &r.written)
		out = append(out, r)
	}
	return out
}

func (e *env) sync(ws, provider string, d apps.SyncDeps) apps.SyncResult {
	e.t.Helper()
	r, err := apps.Sync(context.Background(), e.d, ws, provider, d)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) conn(ws, provider string) apps.Connector {
	c, _, _ := apps.Get(e.d, ws, provider)
	return c
}

func s(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func meta(d model.Document, k string) any {
	v, _ := d.Metadata.Get(k)
	return v
}

func sourceIDs(ds []model.Document) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, s(d.SourceID))
	}
	return out
}

// --- the feed client ------------------------------------------------------------------

func TestPagesUntilDone(t *testing.T) {
	var items []apps.ChangeItem
	for i := 0; i < 250; i++ {
		items = append(items, doc(fmt.Sprintf("n%03d", i), fmt.Sprintf("2026-11-01T00:00:%02d.%03dZ", i%60, i), "Note", "Text", false))
	}
	feed := apps.NewFakeFeed(appKey, items...)
	var cursors []*string
	res, err := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://notes.example.com/", APIKey: appKey}},
		func(_ apps.FeedPage, c *string) error { cursors = append(cursors, c); return nil })
	if err != nil || res.Pages != 3 || res.Items != 250 || !res.CaughtUp {
		t.Fatal(res, err)
	}
	for _, c := range feed.Calls {
		if c.Limit != 100 {
			t.Fatal(c.Limit)
		}
	}
	if feed.Calls[0].Cursor != nil || s(feed.Calls[1].Cursor) != s(cursors[0]) || feed.Calls[0].Auth != "Bearer "+appKey || feed.Calls[0].URL != "https://notes.example.com/api/v1/changes?limit=100" {
		t.Fatalf("%+v", feed.Calls[0])
	}
	// An empty page echoes the cursor and ends the run.
	again, _ := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://notes.example.com", APIKey: appKey, Cursor: res.Cursor}},
		func(apps.FeedPage, *string) error { return nil })
	if again.Pages != 1 || again.Items != 0 || s(again.Cursor) != s(res.Cursor) || !again.CaughtUp {
		t.Fatal(again)
	}
}

func TestStopsAtPageCap(t *testing.T) {
	var items []apps.ChangeItem
	for i := 0; i < 30; i++ {
		items = append(items, doc(fmt.Sprintf("n%02d", i), fmt.Sprintf("2026-11-01T00:00:%02dZ", i), "Note", "Text", false))
	}
	feed := apps.NewFakeFeed(appKey, items...)
	res, _ := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://x.test", APIKey: appKey, Limit: 10}, MaxPages: 2},
		func(apps.FeedPage, *string) error { return nil })
	if res.Pages != 2 || res.Items != 20 || res.CaughtUp {
		t.Fatal(res)
	}
}

func feedErr(err error) *apps.FeedError {
	var fe *apps.FeedError
	errors.As(err, &fe)
	return fe
}

func TestFailuresNameTheStatus(t *testing.T) {
	feed := apps.NewFakeFeed(appKey)
	ctx := context.Background()
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: "wrong-key-XYZ"}); feedErr(err) == nil || feedErr(err).Code != "auth" || feedErr(err).Status != 401 {
		t.Fatal(err)
	}
	g := "garbage"
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: appKey, Cursor: &g}); feedErr(err) == nil || feedErr(err).Code != "bad_cursor" {
		t.Fatal(err)
	}
	feed.FailNext("503", 1)
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: appKey}); err == nil || err.Error() != "the app answered HTTP 503: boom" {
		t.Fatal(err)
	}
	feed.FailNext("network", 1)
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: appKey}); feedErr(err) == nil || feedErr(err).Code != "network" {
		t.Fatal(err)
	}
}

func reply(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

func TestContractAndBadItems(t *testing.T) {
	ctx := context.Background()
	if _, err := apps.FetchPage(ctx, reply(`{"rows":[]}`), apps.FetchOptions{BaseURL: "https://x.test", APIKey: "k"}); feedErr(err) == nil || feedErr(err).Code != "bad_response" {
		t.Fatal(err)
	}
	ok := jsonx.Stringify(doc("ok", "2026-11-01T00:00:00Z", "Note", "Text", false))
	bad := jsonx.Stringify(doc("x", "not a date", "Note", "Text", false))
	page, err := apps.FetchPage(ctx, reply(`{"items":[`+ok+`,{"kind":"document"},`+bad+`],"nextCursor":"c1","hasMore":false}`), apps.FetchOptions{BaseURL: "https://x.test", APIKey: "k"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "ok" || page.Skipped != 2 {
		t.Fatal(page, err)
	}
}

func TestTimesOut(t *testing.T) {
	hang := func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	if _, err := apps.FetchPage(context.Background(), hang, apps.FetchOptions{BaseURL: "https://x.test", APIKey: "k", TimeoutMs: 20}); feedErr(err) == nil || feedErr(err).Code != "timeout" {
		t.Fatal(err)
	}
}

// --- storage ---------------------------------------------------------------------------

func TestSealsKeyAndNeverShowsIt(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "notes", appKey, nil, "")
	if strings.Contains(s(row.APIKey), appKey) || !strings.HasPrefix(s(row.APIKey), "v1.") {
		t.Fatal(s(row.APIKey))
	}
	view, _ := apps.ConnectorView(e.d, e.a, "notes")
	if !view.HasKey || !view.Enabled || view.BaseURL != "https://notes.example.com" || s(view.Container) != "life" || strings.Contains(jsonx.Stringify(view), "v1.") {
		t.Fatalf("%+v", view)
	}
	// Saving without a key keeps it; a new address starts the feed over.
	e.d.Exec(`UPDATE app_connectors SET cursor = 'c9', caught_up_at = 1 WHERE id = ?`, row.ID)
	kept, err := e.save(e.a, "https://notes.example.com/", false, nil, false)
	if err != nil || s(kept.APIKey) != s(row.APIKey) || s(kept.Cursor) != "c9" || kept.Enabled {
		t.Fatal(kept, err)
	}
	moved, _ := e.save(e.a, "https://notes.example.com/v2", true, nil, false)
	if moved.Cursor != nil || moved.CaughtUpAt != nil || s(moved.APIKey) != s(row.APIKey) {
		t.Fatalf("%+v", moved)
	}
}

func TestNewHostDropsTheKey(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "notes", appKey, nil, "")
	if _, err := e.save(e.a, "https://evil.example.com", true, nil, false); err == nil || !strings.Contains(err.Error(), "paste the API key again") {
		t.Fatal(err)
	}
	if c := e.conn(e.a, "notes"); s(c.APIKey) != s(row.APIKey) || c.BaseURL != "https://notes.example.com" {
		t.Fatalf("%+v", c)
	}
	if c, _ := e.save(e.a, "https://evil.example.com", false, nil, false); c.APIKey != nil || c.BaseURL != "https://evil.example.com" {
		t.Fatalf("%+v", c)
	}
	// Moving back doesn't bring the old key back either.
	if c, _ := e.save(e.a, "https://notes.example.com", false, nil, false); c.APIKey != nil {
		t.Fatal("key came back")
	}
	k := appKey
	if c, _ := e.save(e.a, "https://notes.example.com:8443", true, &k, true); !strings.HasPrefix(s(c.APIKey), "v1.") {
		t.Fatal(s(c.APIKey))
	}
}

func TestDefaultsBeforeSetup(t *testing.T) {
	e := newEnv(t)
	o, _ := apps.ConnectorView(e.d, e.a, "notes")
	if o.Configured || o.Name != "notes" || o.BaseURL != "" || o.HasKey || o.Enabled {
		t.Fatalf("%+v", o)
	}
}

func TestHTTPSOnlyExceptLocal(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"ftp://notes", "http://notes.example.com", "http://192.168.1.5:3000"} {
		if _, err := e.save(e.a, u, false, nil, false); err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatal(u, err)
		}
	}
	for _, u := range []string{"http://localhost:3000", "http://127.0.0.1:3000"} {
		if c, err := e.save(e.a, u, false, nil, false); err != nil || c.BaseURL != u {
			t.Fatal(u, err)
		}
	}
	if _, err := e.save(e.a, "not a url", false, nil, false); err == nil || !strings.Contains(err.Error(), "address") {
		t.Fatal(err)
	}
}

// --- sync ------------------------------------------------------------------------------

func TestKeepsCursorOnFailure(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, doc("a", "2026-11-09T01:00:00Z", "A", "First note.", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	cursor := e.conn(e.a, "notes").Cursor
	if cursor == nil {
		t.Fatal("no cursor")
	}
	feed.Add(doc("b", "2026-11-09T02:00:00Z", "B", "Second note.", false))
	feed.FailNext("500", 1)
	failed := e.sync(e.a, "notes", deps(feed, "2026-11-10T03:00:00Z"))
	if failed.OK || s(failed.Error) != "the app answered HTTP 500: boom" {
		t.Fatal(failed)
	}
	after := e.conn(e.a, "notes")
	if s(after.Cursor) != s(cursor) || s(after.LastError) != "the app answered HTTP 500: boom" || *after.LastSyncAt != ms("2026-11-10T03:00:00Z") || *after.LastSuccessAt != ms("2026-11-10T02:00:00Z") {
		t.Fatalf("%+v", after)
	}
	// The next run picks up from the same place and clears the error.
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	if e.conn(e.a, "notes").LastError != nil {
		t.Fatal("error kept")
	}
	if ids := sourceIDs(e.docs(e.a)); strings.Join(ids, ",") != "feed:notes:a,feed:notes:b" || row.ID != after.ID {
		t.Fatal(ids)
	}
}

func TestKeepsPagesBeforeFailure(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	var items []apps.ChangeItem
	for i := 0; i < 5; i++ {
		items = append(items, doc(fmt.Sprintf("n%d", i), fmt.Sprintf("2026-11-09T0%d:00:00Z", i), "Note", fmt.Sprintf("Note number %d.", i), false))
	}
	feed := apps.NewFakeFeed(appKey, items...)
	n := 0
	flaky := func(r *http.Request) (*http.Response, error) {
		n++
		if n == 2 {
			return &http.Response{StatusCode: 502, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		return feed.Do(r)
	}
	res := e.sync(e.a, "notes", apps.SyncDeps{Do: flaky, Now: at("2026-11-10T02:00:00Z"), PageLimit: 2})
	if res.OK || res.Documents != 2 {
		t.Fatal(res)
	}
	if s(e.conn(e.a, "notes").Cursor) != base64.RawURLEncoding.EncodeToString([]byte("o:2")) {
		t.Fatal(s(e.conn(e.a, "notes").Cursor))
	}
	rest := e.sync(e.a, "notes", apps.SyncDeps{Do: feed.Do, Now: at("2026-11-10T03:00:00Z"), PageLimit: 2})
	if !rest.OK || rest.Documents != 3 || !rest.CaughtUp {
		t.Fatal(rest)
	}
}

func TestAsksForBigPages(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey)
	e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z"))
	if len(feed.Calls) != 1 || feed.Calls[0].Limit != apps.SyncPageLimit {
		t.Fatalf("%+v", feed.Calls)
	}
}

func TestRefusedKey(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", "a-wrong-key-SECRET", nil, "")
	res := e.sync(e.a, "notes", deps(apps.NewFakeFeed(appKey), "2026-11-10T02:00:00Z"))
	if s(res.Error) != "the app refused the API key (HTTP 401: unauthorized)" || strings.Contains(s(e.conn(e.a, "notes").LastError), "SECRET") {
		t.Fatal(res)
	}
}

func TestOneSyncAtATime(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-gate
		return feed.Do(r)
	}
	var wg sync.WaitGroup
	var first apps.SyncResult
	wg.Add(1)
	go func() {
		defer wg.Done()
		first, _ = apps.Sync(context.Background(), e.d, e.a, "notes", apps.SyncDeps{Do: slow, Now: at("2026-11-10T02:00:00Z")})
	}()
	<-started
	var ae *apps.Error
	if _, err := apps.Sync(context.Background(), e.d, e.a, "notes", deps(feed, "2026-11-10T02:00:00Z")); !errors.As(err, &ae) || ae.Code != "busy" {
		t.Fatal(err)
	}
	close(gate)
	wg.Wait()
	if !first.OK {
		t.Fatal(first)
	}
}

func TestBucketsByLocalDay(t *testing.T) {
	for _, c := range [][3]string{{"2026-11-09T17:30:00Z", "Asia/Makassar", "2026-11-10"}, {"2026-11-09T15:59:00Z", "Asia/Makassar", "2026-11-09"}, {"2026-11-09T17:30:00Z", "UTC", "2026-11-09"}} {
		if d, _ := apps.LocalDay(c[0], c[1]); d != c[2] {
			t.Fatal(c, d)
		}
	}
}

func TestNoCloseWhileMoreToRead(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "chat", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey,
		event("m1", "rina", "Chat with Rina", "2026-11-09T01:00:00Z", "Rina", "One"),
		event("m2", "rina", "Chat with Rina", "2026-11-09T01:01:00Z", "Rina", "Two"),
		event("m3", "rina", "Chat with Rina", "2026-11-09T01:02:00Z", "Rina", "Three"))
	capped := e.sync(e.a, "chat", apps.SyncDeps{Do: feed.Do, Now: at("2026-11-10T02:00:00Z"), MaxPages: 1, PageLimit: 2})
	if !capped.OK || capped.CaughtUp || capped.Documents != 0 || len(e.staged(row.ID)) != 2 {
		t.Fatal(capped)
	}
	rest := e.sync(e.a, "chat", deps(feed, "2026-11-10T03:00:00Z"))
	if !rest.OK || !rest.CaughtUp || rest.Documents != 1 || !strings.Contains(s(e.docs(e.a)[0].Content), "3 entries") {
		t.Fatal(rest)
	}
}

func TestFirstSyncReaches30Days(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	old := doc("old", "2026-09-01T01:00:00Z", "Old", "From September.", false)
	old.Data.Set("date", "2026-09-01")
	undated := doc("undated", "2026-09-01T02:00:00Z", "Undated", "No date.", false)
	undated.Data.Delete("date")
	feed := apps.NewFakeFeed(appKey, old, undated,
		event("e1", "log", "Log", "2026-09-01T03:00:00Z", "", "Too old"),
		doc("new", "2026-11-09T01:00:00Z", "New", "From November.", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 2 || r.Staged != 0 {
		t.Fatal(r)
	}
	if ids := sourceIDs(e.docs(e.a)); strings.Join(ids, ",") != "feed:notes:new,feed:notes:undated" {
		t.Fatal(ids)
	}
}

// --- document edits and deletes with their facts -----------------------------------------

var idrAmount = regexp.MustCompile(`IDR [\d,]+`)

// factModel plays the model: one fact per document naming the amount;
// onExtract runs while the job waits on the "network".
func factModel(onExtract func()) *testkit.Gateway {
	g := &testkit.Gateway{}
	g.Reply = func(system, prompt string) (string, bool) {
		if strings.Contains(system, "profile") {
			return "", false
		}
		if onExtract != nil {
			onExtract()
		}
		money := idrAmount.FindString(prompt)
		if money == "" {
			money = "an amount"
		}
		return `{"add":[{"fact":"Spent ` + money + ` at Tokopedia on 2026-11-09","keywords":"expense belanja","kind":"dynamic"}]}`, true
	}
	return g
}

func (e *env) drain(onExtract func()) {
	g := factModel(onExtract)
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Do: g.Do, Now: func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }}), func(string) {})
	for i := 0; i < 50; i++ {
		if j, _ := r.RunOnce(time.Now().UnixMilli()); j == nil {
			return
		}
	}
}

func (e *env) current() []string {
	var out []string
	rows, _ := e.d.Query(`SELECT fact FROM memories WHERE workspace_id = ? AND valid_to IS NULL`, e.a)
	defer rows.Close()
	for rows.Next() {
		var f string
		rows.Scan(&f)
		out = append(out, f)
	}
	return out
}

func (e *env) unfinished() int {
	var n int
	e.d.QueryRow(`SELECT count(*) FROM jobs WHERE status <> 'done'`).Scan(&n)
	return n
}

func (e *env) count(q string, args ...any) int {
	var n int
	e.d.QueryRow(q, args...).Scan(&n)
	return n
}

func purchase(id, updatedAt, amount string) apps.ChangeItem {
	return doc(id, updatedAt, "New monitor", "Paid IDR "+amount+" at Tokopedia for a new monitor.", false)
}

func deletedDoc(id, updatedAt string) apps.ChangeItem {
	return apps.ChangeItem{Kind: "document", ID: id, UpdatedAt: updatedAt, Deleted: true, Data: jsonx.NewMap[any]()}
}

func (e *env) ingested(feed *apps.FakeFeed) model.Document {
	e.t.Helper()
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 0 {
		e.t.Fatal(r)
	}
	e.drain(nil)
	if c := e.current(); len(c) != 1 || c[0] != "Spent IDR 5,000,000 at Tokopedia on 2026-11-09" {
		e.t.Fatal(c)
	}
	return e.docs(e.a)[0]
}

func TestEditReplacesDocAndFacts(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	old := e.ingested(feed)
	fp, _ := meta(old, "fingerprint").(string)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(fp) {
		t.Fatal(fp)
	}
	feed.Add(purchase("t1", "2026-11-10T03:00:00Z", "4,500,000"))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 1 {
		t.Fatal(r)
	}
	ds := e.docs(e.a)
	if len(ds) != 1 || s(ds[0].SourceID) != "feed:notes:t1" || !strings.Contains(s(ds[0].Content), "IDR 4,500,000") || ds[0].ID == old.ID || meta(ds[0], "fingerprint") == fp {
		t.Fatalf("%+v", ds)
	}
	// The old fact is forgotten right away and the old chunks are gone.
	if len(e.current()) != 0 || e.count(`SELECT count(*) FROM chunks WHERE document_id = ?`, old.ID) != 0 {
		t.Fatal("old facts or chunks")
	}
	e.drain(nil)
	if c := e.current(); len(c) != 1 || c[0] != "Spent IDR 4,500,000 at Tokopedia on 2026-11-09" || e.unfinished() != 0 {
		t.Fatal(c)
	}
}

func TestNoopEdit(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	old := e.ingested(feed)
	jobsBefore := e.count(`SELECT count(*) FROM jobs`)
	// A newer updatedAt and a field Recall doesn't use.
	again := purchase("t1", "2026-11-10T03:00:00Z", "5,000,000")
	again.Data.Set("reconciled", true)
	feed.Add(again)
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 0 {
		t.Fatal(r)
	}
	if ds := e.docs(e.a); len(ds) != 1 || ds[0].ID != old.ID || e.count(`SELECT count(*) FROM jobs`) != jobsBefore || len(e.current()) != 1 {
		t.Fatal(ds)
	}
}

func TestDeleteRemovesDocAndFacts(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	e.ingested(feed)
	feed.Add(deletedDoc("t1", "2026-11-10T03:00:00Z"))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 1 {
		t.Fatal(r)
	}
	if len(e.docs(e.a)) != 0 || len(e.current()) != 0 {
		t.Fatal("left over")
	}
	// The forgotten fact keeps its history, now without a document.
	var fact string
	var validTo, src any
	e.d.QueryRow(`SELECT fact, valid_to, source_document_id FROM memories`).Scan(&fact, &validTo, &src)
	if fact != "Spent IDR 5,000,000 at Tokopedia on 2026-11-09" || validTo == nil || src != nil || e.count(`SELECT count(*) FROM memories`) != 1 {
		t.Fatal(fact, validTo, src)
	}
}

func TestEmptiedDocumentRemoves(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	e.ingested(feed)
	feed.Add(doc("t1", "2026-11-10T03:00:00Z", "New monitor", "", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 1 || len(e.docs(e.a)) != 0 || len(e.current()) != 0 {
		t.Fatal(r)
	}
}

func TestIgnoresUnknownDeletedDocument(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, deletedDoc("t9", "2026-11-09T01:00:00Z"))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 0 || len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM jobs`) != 0 {
		t.Fatal(r)
	}
}

func TestQueuedIngestDoesntResurrect(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z"))
	feed.Add(deletedDoc("t1", "2026-11-10T03:00:00Z"))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); r.Removed != 1 {
		t.Fatal(r)
	}
	e.drain(nil)
	if len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM chunks`) != 0 || len(e.current()) != 0 || e.unfinished() != 0 {
		t.Fatal("resurrected")
	}
}

func TestExtractInFlightWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	feed := apps.NewFakeFeed(appKey, purchase("t1", "2026-11-09T01:00:00Z", "5,000,000"))
	e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z"))
	d := e.docs(e.a)[0]
	// The delete lands while the model is answering.
	e.drain(func() { memories.DeleteDocumentAndMemories(e.d, e.a, d.ID, time.Now().UnixMilli()) })
	if len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM memories`) != 0 || e.unfinished() != 0 {
		t.Fatal("facts written")
	}
}

// --- the job and isolation ------------------------------------------------------------------

func TestHourlyRunOneFailureDoesntBlock(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", appKey, nil, "")
	e.setup(e.a, "chat", notesKey, nil, "")
	notes := apps.NewFakeFeed(appKey)
	chat := apps.NewFakeFeed(notesKey, doc("d1", "2026-11-09T01:00:00Z", "Note", "Text", false))
	notes.FailNext("503", 1)
	route := func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.String(), baseOf("notes")) {
			return notes.Do(r)
		}
		return chat.Do(r)
	}
	run := apps.RunSync(context.Background(), e.d, apps.SyncDeps{Do: route, Now: at("2026-11-10T02:00:00Z")}, func(string) {})
	if run.Connectors != 2 || run.OK != 1 || run.Failed != 1 || run.Documents != 1 {
		t.Fatal(run)
	}
	if !strings.Contains(s(e.conn(e.a, "notes").LastError), "503") || e.conn(e.a, "chat").LastError != nil {
		t.Fatal("errors")
	}
}

func TestSkipsOffOrKeyless(t *testing.T) {
	e := newEnv(t)
	k := appKey
	e.save(e.a, "https://notes.example.com", false, &k, true)
	e.save(e.b, "https://notes.example.com", true, nil, false)
	feed := apps.NewFakeFeed(appKey)
	if run := apps.RunSync(context.Background(), e.d, deps(feed, "2026-11-10T02:00:00Z"), func(string) {}); run.Connectors != 0 || feed.CallCount() != 0 {
		t.Fatal(run)
	}
}

func TestOneHourlyJob(t *testing.T) {
	e := newEnv(t)
	now := ms("2026-11-10T02:20:00Z")
	e.setup(e.a, "notes", appKey, nil, "")
	if _, ok, _ := apps.Schedule(e.d, now); !ok {
		t.Fatal("not queued")
	}
	if _, ok, _ := apps.Schedule(e.d, now); ok {
		t.Fatal("queued twice")
	}
	var runAfter int64
	e.d.QueryRow(`SELECT run_after FROM jobs WHERE type = 'app_sync'`).Scan(&runAfter)
	if e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync'`) != 1 || runAfter != ms("2026-11-10T03:00:00Z") {
		t.Fatal(runAfter)
	}
	feed := apps.NewFakeFeed(appKey, doc("j1", "2026-11-09T01:00:00Z", "Note", "Text", false))
	g := &testkit.Gateway{}
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Do: g.Do, Now: time.Now, AppSync: deps(feed, "2026-11-10T03:00:00Z")}), func(string) {})
	j, err := r.RunOnce(ms("2026-11-10T03:00:01Z"))
	if err != nil || j == nil || j.Type != "app_sync" {
		t.Fatal(j, err)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'done'`) != 1 || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'queued'`) != 1 {
		t.Fatal("job states")
	}
	if ids := sourceIDs(e.docs(e.a)); strings.Join(ids, ",") != "feed:notes:j1" {
		t.Fatal(ids)
	}
}

func TestNoJobWithoutSyncableConnector(t *testing.T) {
	e := newEnv(t)
	now := ms("2026-11-10T02:20:00Z")
	if _, ok, _ := apps.Schedule(e.d, now); ok {
		t.Fatal("queued with nothing to sync")
	}
	k := appKey
	e.save(e.a, "https://notes.example.com", false, &k, true)
	e.save(e.b, "https://notes.example.com", true, nil, false)
	if _, ok, _ := apps.Schedule(e.d, now); ok || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync'`) != 0 {
		t.Fatal("queued for off or keyless")
	}
	e.save(e.a, "https://notes.example.com", true, nil, false)
	if _, ok, _ := apps.Schedule(e.d, now); !ok {
		t.Fatal("not queued")
	}
	// Turned off before the hour: the queued run finds nothing and doesn't queue another.
	e.save(e.a, "https://notes.example.com", false, nil, false)
	g := &testkit.Gateway{}
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Do: g.Do, Now: time.Now, AppSync: deps(apps.NewFakeFeed(appKey), "2026-11-10T03:00:00Z")}), func(string) {})
	if j, _ := r.RunOnce(ms("2026-11-10T03:00:01Z")); j == nil || j.Type != "app_sync" {
		t.Fatal(j)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'done'`) != 1 || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'queued'`) != 0 {
		t.Fatal("job states")
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.setup(e.a, "notes", appKey, nil, "")
	b := e.setup(e.b, "notes", "beta-key-SECRET", nil, "")
	feedA := apps.NewFakeFeed(appKey, doc("a1", "2026-11-09T03:00:00Z", "Alpha", "Alpha work", false))
	feedB := apps.NewFakeFeed("beta-key-SECRET", doc("a1", "2026-11-09T03:00:00Z", "Beta", "Beta work", false))
	e.sync(e.a, "notes", deps(feedA, "2026-11-10T02:00:00Z"))
	e.sync(e.b, "notes", deps(feedB, "2026-11-10T02:00:00Z"))
	da, dbs := e.docs(e.a), e.docs(e.b)
	if feedA.Calls[0].Auth != "Bearer "+appKey || feedB.Calls[0].Auth != "Bearer beta-key-SECRET" || len(da) != 1 || len(dbs) != 1 ||
		!strings.Contains(s(da[0].Content), "Alpha work") || !strings.Contains(s(dbs[0].Content), "Beta work") || strings.Contains(s(da[0].Content), "Beta") ||
		s(da[0].SourceID) != s(dbs[0].SourceID) {
		t.Fatal(da, dbs)
	}
	if v, _ := apps.ConnectorView(e.d, e.b, "notes"); v.LastSyncAt == nil {
		t.Fatal("no sync time")
	}
	// A sealed key copied into the other workspace's row doesn't open.
	e.d.Exec(`UPDATE app_connectors SET api_key = ? WHERE id = ?`, *a.APIKey, b.ID)
	res := e.sync(e.b, "notes", deps(feedA, "2026-11-10T03:00:00Z"))
	if !regexp.MustCompile(`can't be read`).MatchString(s(res.Error)) || feedA.CallCount() != 1 {
		t.Fatal(res)
	}
}
