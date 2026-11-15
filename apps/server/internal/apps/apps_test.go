package apps_test

// apps.test.ts: the Oslo and Finansial pull connectors against the fake
// changes feed.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
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

const (
	osloKey = "oslo-key-SECRET-123"
	finKey  = "fin-key-SECRET-456"
)

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

var seq = 0

func task(id, updatedAt string, data *jsonx.Object, deleted bool) apps.ChangeItem {
	base := obj("title", "Task "+id, "status", "done", "projectId", "p1", "projectName", "Recall", "dueDate", nil, "completedAt", updatedAt, "tags", []any{}, "notes", "")
	return apps.ChangeItem{Kind: "task", ID: id, UpdatedAt: updatedAt, URL: "https://todo.zakiego.com/tasks/" + id, Deleted: deleted, Data: merge(base, data)}
}

func project(id, updatedAt string, data *jsonx.Object) apps.ChangeItem {
	base := obj("name", "Project "+id, "status", "active", "completedAt", nil)
	return apps.ChangeItem{Kind: "project", ID: id, UpdatedAt: updatedAt, URL: "https://todo.zakiego.com/projects/" + id, Data: merge(base, data)}
}

func txn(id string, amountMinor float64, data *jsonx.Object, updatedAt string) apps.ChangeItem {
	if updatedAt == "" {
		updatedAt = fmt.Sprintf("2026-11-09T0%d:00:00.000Z", seq%10)
		seq++
	}
	base := obj("date", "2026-11-09", "kind", "expense", "amountMinor", amountMinor, "currency", "IDR", "minorUnitDigits", 0.0, "account", "BCA", "payee", "Tokopedia",
		"category", "Electronics", "note", "new monitor", "tags", []any{"work"})
	return apps.ChangeItem{Kind: "transaction", ID: id, UpdatedAt: updatedAt, URL: "https://finansial.zakiego.com/transactions/" + id, Data: merge(base, data)}
}

func rule(id, updatedAt string, data *jsonx.Object) apps.ChangeItem {
	base := obj("name", "Rent", "kind", "expense", "amountMinor", 5_000_000.0, "currency", "IDR", "minorUnitDigits", 0.0, "frequency", "monthly", "nextDueDate", "2026-12-01",
		"payee", "Pak Budi", "category", "Housing", "active", true)
	return apps.ChangeItem{Kind: "recurring_rule", ID: id, UpdatedAt: updatedAt, URL: "https://finansial.zakiego.com/recurring/" + id, Data: merge(base, data)}
}

func at(iso string) func() int64 {
	t, _ := time.Parse(time.RFC3339, iso)
	return func() int64 { return t.UnixMilli() }
}

func ms(iso string) int64 { return at(iso)() }

func deps(f *apps.FakeFeed, now string) apps.SyncDeps { return apps.SyncDeps{Do: f.Do, Now: at(now)} }

func (e *env) setup(ws, provider, key string, settings *jsonx.Object, container string) apps.Connector {
	e.t.Helper()
	if container == "" {
		container = "life"
	}
	base := "https://finansial.zakiego.com"
	if provider == "oslo" {
		base = "https://todo.zakiego.com"
	}
	in := apps.SaveInput{WorkspaceID: ws, Provider: provider, Container: container, BaseURL: base, Enabled: true, Settings: merge(obj("timezone", "Asia/Makassar"), settings)}
	in.APIKey.Set, in.APIKey.Value = true, &key
	c, err := apps.Save(e.d, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) save(ws, baseURL string, enabled bool, key *string, keySet bool) (apps.Connector, error) {
	in := apps.SaveInput{WorkspaceID: ws, Provider: "oslo", Container: "life", BaseURL: baseURL, Enabled: enabled}
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

// --- the feed client ------------------------------------------------------------------

func TestPagesUntilDone(t *testing.T) {
	var items []apps.ChangeItem
	for i := 0; i < 250; i++ {
		items = append(items, task(fmt.Sprintf("t%03d", i), fmt.Sprintf("2026-11-01T00:00:%02d.%03dZ", i%60, i), nil, false))
	}
	feed := apps.NewFakeFeed(osloKey, items...)
	var cursors []*string
	res, err := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://todo.zakiego.com/", APIKey: osloKey}},
		func(_ apps.FeedPage, c *string) error { cursors = append(cursors, c); return nil })
	if err != nil || res.Pages != 3 || res.Items != 250 || !res.CaughtUp {
		t.Fatal(res, err)
	}
	for _, c := range feed.Calls {
		if c.Limit != 100 {
			t.Fatal(c.Limit)
		}
	}
	if feed.Calls[0].Cursor != nil || s(feed.Calls[1].Cursor) != s(cursors[0]) || feed.Calls[0].Auth != "Bearer "+osloKey || feed.Calls[0].URL != "https://todo.zakiego.com/api/v1/changes?limit=100" {
		t.Fatalf("%+v", feed.Calls[0])
	}
	// An empty page echoes the cursor and ends the run.
	again, _ := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://todo.zakiego.com", APIKey: osloKey, Cursor: res.Cursor}},
		func(apps.FeedPage, *string) error { return nil })
	if again.Pages != 1 || again.Items != 0 || s(again.Cursor) != s(res.Cursor) || !again.CaughtUp {
		t.Fatal(again)
	}
}

func TestStopsAtPageCap(t *testing.T) {
	var items []apps.ChangeItem
	for i := 0; i < 30; i++ {
		items = append(items, task(fmt.Sprintf("t%02d", i), fmt.Sprintf("2026-11-01T00:00:%02dZ", i), nil, false))
	}
	feed := apps.NewFakeFeed(osloKey, items...)
	res, _ := apps.PullChanges(context.Background(), feed.Do, apps.PullOptions{FetchOptions: apps.FetchOptions{BaseURL: "https://x.test", APIKey: osloKey, Limit: 10}, MaxPages: 2},
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
	feed := apps.NewFakeFeed(osloKey)
	ctx := context.Background()
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: "wrong-key-XYZ"}); feedErr(err) == nil || feedErr(err).Code != "auth" || feedErr(err).Status != 401 {
		t.Fatal(err)
	}
	g := "garbage"
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: osloKey, Cursor: &g}); feedErr(err) == nil || feedErr(err).Code != "bad_cursor" {
		t.Fatal(err)
	}
	feed.FailNext("503", 1)
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: osloKey}); err == nil || err.Error() != "the app answered HTTP 503: boom" {
		t.Fatal(err)
	}
	feed.FailNext("network", 1)
	if _, err := apps.FetchPage(ctx, feed.Do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: osloKey}); feedErr(err) == nil || feedErr(err).Code != "network" {
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
	ok := jsonx.Stringify(task("ok", "2026-11-01T00:00:00Z", nil, false))
	bad := jsonx.Stringify(task("x", "not a date", nil, false))
	page, err := apps.FetchPage(ctx, reply(`{"items":[`+ok+`,{"kind":"task"},`+bad+`],"nextCursor":"c1","hasMore":false}`), apps.FetchOptions{BaseURL: "https://x.test", APIKey: "k"})
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
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	if strings.Contains(s(row.APIKey), osloKey) || !strings.HasPrefix(s(row.APIKey), "v1.") {
		t.Fatal(s(row.APIKey))
	}
	view, _ := apps.ConnectorView(e.d, e.a, "oslo")
	if !view.HasKey || !view.Enabled || view.BaseURL != "https://todo.zakiego.com" || s(view.Container) != "life" || strings.Contains(jsonx.Stringify(view), "v1.") {
		t.Fatalf("%+v", view)
	}
	// Saving without a key keeps it; a new address starts the feed over.
	e.d.Exec(`UPDATE app_connectors SET cursor = 'c9', caught_up_at = 1 WHERE id = ?`, row.ID)
	kept, err := e.save(e.a, "https://todo.zakiego.com/", false, nil, false)
	if err != nil || s(kept.APIKey) != s(row.APIKey) || s(kept.Cursor) != "c9" || kept.Enabled {
		t.Fatal(kept, err)
	}
	moved, _ := e.save(e.a, "https://todo.zakiego.com/v2", true, nil, false)
	if moved.Cursor != nil || moved.CaughtUpAt != nil || s(moved.APIKey) != s(row.APIKey) {
		t.Fatalf("%+v", moved)
	}
}

func TestNewHostDropsTheKey(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	if _, err := e.save(e.a, "https://evil.example.com", true, nil, false); err == nil || !strings.Contains(err.Error(), "paste the API key again") {
		t.Fatal(err)
	}
	if c := e.conn(e.a, "oslo"); s(c.APIKey) != s(row.APIKey) || c.BaseURL != "https://todo.zakiego.com" {
		t.Fatalf("%+v", c)
	}
	if c, _ := e.save(e.a, "https://evil.example.com", false, nil, false); c.APIKey != nil || c.BaseURL != "https://evil.example.com" {
		t.Fatalf("%+v", c)
	}
	// Moving back doesn't bring the old key back either.
	if c, _ := e.save(e.a, "https://todo.zakiego.com", false, nil, false); c.APIKey != nil {
		t.Fatal("key came back")
	}
	k := osloKey
	if c, _ := e.save(e.a, "https://todo.zakiego.com:8443", true, &k, true); !strings.HasPrefix(s(c.APIKey), "v1.") {
		t.Fatal(s(c.APIKey))
	}
}

func TestDefaultsBeforeSetup(t *testing.T) {
	e := newEnv(t)
	o, _ := apps.ConnectorView(e.d, e.a, "oslo")
	if o.Configured || o.BaseURL != "https://todo.zakiego.com" || o.HasKey || o.Enabled {
		t.Fatalf("%+v", o)
	}
	f, _ := apps.ConnectorView(e.d, e.a, "finansial")
	if f.BaseURL != "https://finansial.zakiego.com" || jsonx.Stringify(f.Thresholds) != `{"IDR":1000000}` {
		t.Fatalf("%+v", f)
	}
}

func TestHTTPSOnlyExceptLocal(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"ftp://todo", "http://todo.zakiego.com", "http://192.168.1.5:3000"} {
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
	row := e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("a", 2_000_000, nil, "2026-11-09T01:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	cursor := e.conn(e.a, "finansial").Cursor
	if cursor == nil {
		t.Fatal("no cursor")
	}
	feed.Add(txn("b", 3_000_000, nil, "2026-11-09T02:00:00Z"))
	feed.FailNext("500", 1)
	failed := e.sync(e.a, "finansial", deps(feed, "2026-11-10T03:00:00Z"))
	if failed.OK || s(failed.Error) != "the app answered HTTP 500: boom" {
		t.Fatal(failed)
	}
	after := e.conn(e.a, "finansial")
	if s(after.Cursor) != s(cursor) || s(after.LastError) != "the app answered HTTP 500: boom" || *after.LastSyncAt != ms("2026-11-10T03:00:00Z") || *after.LastSuccessAt != ms("2026-11-10T02:00:00Z") {
		t.Fatalf("%+v", after)
	}
	// The next run picks up from the same place and clears the error.
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	if e.conn(e.a, "finansial").LastError != nil {
		t.Fatal("error kept")
	}
	var sids []string
	for _, d := range e.docs(e.a) {
		sids = append(sids, s(d.SourceID))
	}
	if strings.Join(sids, ",") != "fin:txn:a,fin:txn:b" || row.ID != after.ID {
		t.Fatal(sids)
	}
}

func TestKeepsPagesBeforeFailure(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	var items []apps.ChangeItem
	for i := 0; i < 5; i++ {
		items = append(items, txn(fmt.Sprintf("t%d", i), 2_000_000, nil, fmt.Sprintf("2026-11-09T0%d:00:00Z", i)))
	}
	feed := apps.NewFakeFeed(finKey, items...)
	n := 0
	flaky := func(r *http.Request) (*http.Response, error) {
		n++
		if n == 2 {
			return &http.Response{StatusCode: 502, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		return feed.Do(r)
	}
	res := e.sync(e.a, "finansial", apps.SyncDeps{Do: flaky, Now: at("2026-11-10T02:00:00Z"), PageLimit: 2})
	if res.OK || res.Documents != 2 {
		t.Fatal(res)
	}
	if s(e.conn(e.a, "finansial").Cursor) != base64.RawURLEncoding.EncodeToString([]byte("o:2")) {
		t.Fatal(s(e.conn(e.a, "finansial").Cursor))
	}
	rest := e.sync(e.a, "finansial", apps.SyncDeps{Do: feed.Do, Now: at("2026-11-10T03:00:00Z"), PageLimit: 2})
	if !rest.OK || rest.Documents != 3 || !rest.CaughtUp {
		t.Fatal(rest)
	}
}

func TestRefusedKey(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "oslo", "a-wrong-key-SECRET", nil, "")
	res := e.sync(e.a, "oslo", deps(apps.NewFakeFeed(osloKey), "2026-11-10T02:00:00Z"))
	if s(res.Error) != "the app refused the API key (HTTP 401: unauthorized)" || strings.Contains(s(e.conn(e.a, "oslo").LastError), "SECRET") {
		t.Fatal(res)
	}
}

func TestOneSyncAtATime(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey)
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
		first, _ = apps.Sync(context.Background(), e.d, e.a, "oslo", apps.SyncDeps{Do: slow, Now: at("2026-11-10T02:00:00Z")})
	}()
	<-started
	var ae *apps.Error
	if _, err := apps.Sync(context.Background(), e.d, e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z")); !errors.As(err, &ae) || ae.Code != "busy" {
		t.Fatal(err)
	}
	close(gate)
	wg.Wait()
	if !first.OK {
		t.Fatal(first)
	}
}

// --- Oslo ------------------------------------------------------------------------------

func TestBucketsByLocalDay(t *testing.T) {
	for _, c := range [][3]string{{"2026-11-09T17:30:00Z", "Asia/Makassar", "2026-11-10"}, {"2026-11-09T15:59:00Z", "Asia/Makassar", "2026-11-09"}, {"2026-11-09T17:30:00Z", "UTC", "2026-11-09"}} {
		if d, _ := apps.LocalDay(c[0], c[1]); d != c[2] {
			t.Fatal(c, d)
		}
	}
}

func TestWritesDayOnceOver(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey,
		task("y1", "2026-11-09T03:00:00Z", obj("title", "Ship the Gmail connector"), false),
		// 23:30 local on Nov 9.
		task("y2", "2026-11-09T15:30:00Z", obj("title", "Write the release notes", "projectName", "Docs"), false),
		task("open", "2026-11-09T04:00:00Z", obj("status", "open", "completedAt", nil), false),
		task("cancel", "2026-11-09T04:30:00Z", obj("status", "cancelled", "completedAt", "2026-11-09T04:30:00Z"), false),
		task("gone", "2026-11-09T05:00:00Z", nil, true),
		// 01:00 local on Nov 10: today.
		task("t1", "2026-11-09T17:00:00Z", obj("title", "Plan the week"), false))
	first := e.sync(e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
	if !first.OK || !first.CaughtUp || first.Documents != 1 {
		t.Fatal(first)
	}
	day9 := e.docs(e.a)[0]
	if s(day9.SourceID) != "oslo:day:2026-11-09" || day9.SourceType != "text" || day9.Container != "life" ||
		meta(day9, "source") != "oslo" || meta(day9, "day") != "2026-11-09" || meta(day9, "documentDate") != "2026-11-09" || meta(day9, "events") != float64(2) {
		t.Fatalf("%+v", day9)
	}
	c := s(day9.Content)
	if !strings.Contains(c, "Tasks completed (2):") || !strings.Contains(c, "- Ship the Gmail connector (project: Recall), at 11:00. https://todo.zakiego.com/tasks/y1") ||
		!strings.Contains(c, "- Write the release notes (project: Docs), at 23:30. https://todo.zakiego.com/tasks/y2") || regexp.MustCompile(`open|cancel|gone`).MatchString(c) ||
		s(day9.Title) != "Oslo on Nov 9, 2026: 2 tasks done" {
		t.Fatal(c, s(day9.Title))
	}
	// Today's task waits, past the cursor.
	if st := e.staged(row.ID); len(st) != 1 || st[0].day != "2026-11-10" || st[0].key != "task:t1:2026-11-10" {
		t.Fatal(st)
	}
	// The same task done again today, a new project, and a project finished.
	feed.Add(task("t1", "2026-11-10T03:00:00Z", obj("title", "Plan the week (v2)", "completedAt", "2026-11-09T17:00:00Z"), false),
		project("p9", "2026-11-10T04:00:00Z", obj("name", "Garden")),
		task("t2", "2026-11-10T05:00:00Z", obj("title", "Water plants"), false))
	if r := e.sync(e.a, "oslo", deps(feed, "2026-11-10T06:00:00Z")); !r.OK || r.Documents != 0 {
		t.Fatal(r)
	}
	// Next morning the day closes.
	feed.Add(project("p9", "2026-11-10T10:00:00Z", obj("name", "Garden", "status", "done", "completedAt", "2026-11-10T10:00:00Z")))
	if r := e.sync(e.a, "oslo", deps(feed, "2026-11-10T17:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	var day10 model.Document
	for _, d := range e.docs(e.a) {
		if s(d.SourceID) == "oslo:day:2026-11-10" {
			day10 = d
		}
	}
	c = s(day10.Content)
	if strings.Count(c, "Plan the week") != 1 || !strings.Contains(c, "Plan the week (v2)") || !strings.Contains(c, "Projects created (1):\n- Garden, at 12:00. https://todo.zakiego.com/projects/p9") ||
		!strings.Contains(c, "Projects completed (1):\n- Garden, at 18:00.") || s(day10.Title) != "Oslo on Nov 10, 2026: 2 tasks done, 1 project started, 1 project finished" {
		t.Fatal(c, s(day10.Title))
	}
	// Only the "already reported" marker for the project is left.
	if st := e.staged(row.ID); len(st) != 1 || st[0].key != "project-created:p9" {
		t.Fatal(st)
	}
}

func TestReopenedTaskDropped(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey, task("r1", "2026-11-10T01:00:00Z", nil, false))
	e.sync(e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
	if len(e.staged(row.ID)) != 1 {
		t.Fatal("not staged")
	}
	feed.Add(task("r1", "2026-11-10T03:00:00Z", obj("status", "open", "completedAt", nil), false))
	e.sync(e.a, "oslo", deps(feed, "2026-11-10T04:00:00Z"))
	if len(e.staged(row.ID)) != 0 {
		t.Fatal("still staged")
	}
	e.sync(e.a, "oslo", deps(feed, "2026-11-11T04:00:00Z"))
	if len(e.docs(e.a)) != 0 {
		t.Fatal("day written")
	}
}

func TestNoCloseWhileMoreToRead(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "oslo", osloKey, nil, "")
	var items []apps.ChangeItem
	for i := 0; i < 6; i++ {
		items = append(items, task(fmt.Sprintf("m%d", i), fmt.Sprintf("2026-11-09T0%d:00:00Z", i), nil, false))
	}
	feed := apps.NewFakeFeed(osloKey, items...)
	capped := e.sync(e.a, "oslo", apps.SyncDeps{Do: feed.Do, Now: at("2026-11-10T02:00:00Z"), MaxPages: 1, PageLimit: 2})
	if !capped.OK || capped.CaughtUp || capped.Documents != 0 {
		t.Fatal(capped)
	}
	done := e.sync(e.a, "oslo", deps(feed, "2026-11-10T03:00:00Z"))
	if !done.CaughtUp || done.Documents != 1 || meta(e.docs(e.a)[0], "events") != float64(6) {
		t.Fatal(done)
	}
}

func TestOldProjectsNotNew(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey, project("old", "2026-11-09T01:00:00Z", nil))
	e.sync(e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
	feed.Add(project("old", "2026-11-10T03:00:00Z", obj("name", "Renamed")))
	e.sync(e.a, "oslo", deps(feed, "2026-11-11T02:00:00Z"))
	if st := e.staged(row.ID); len(e.docs(e.a)) != 0 || len(st) != 1 || st[0].key != "project-created:old" || !st[0].written {
		t.Fatal(st)
	}
}

func TestNeverRewritesDay(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey, task("d1", "2026-11-09T03:00:00Z", nil, false))
	e.sync(e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
	// Read the whole feed again from the start.
	e.d.Exec(`UPDATE app_connectors SET cursor = NULL WHERE id = ?`, row.ID)
	again := e.sync(e.a, "oslo", deps(feed, "2026-11-10T03:00:00Z"))
	if !again.OK || again.Staged != 0 || again.Documents != 0 || len(e.docs(e.a)) != 1 || len(e.staged(row.ID)) != 0 {
		t.Fatal(again)
	}
}

func TestFirstSyncReaches30Days(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "oslo", osloKey, nil, "")
	feed := apps.NewFakeFeed(osloKey, task("ancient", "2026-08-01T03:00:00Z", nil, false), task("recent", "2026-11-01T03:00:00Z", nil, false))
	e.sync(e.a, "oslo", deps(feed, "2026-11-10T02:00:00Z"))
	if d := e.docs(e.a); len(d) != 1 || s(d[0].SourceID) != "oslo:day:2026-11-01" {
		t.Fatal(d)
	}
}

func TestRendersDigest(t *testing.T) {
	_, content := apps.OsloDigest("2026-11-10", []apps.OsloEvent{
		{Type: "task", ID: "b", Title: "Second", URL: "https://t/b", At: "2026-11-10T05:00:00Z"},
		{Type: "task", ID: "a", Title: "First", At: "2026-11-10T01:00:00Z"},
	}, "Asia/Makassar")
	if content != "Activity in Oslo, the todo app, on Tuesday, Nov 10, 2026 (Asia/Makassar time).\n\nTasks completed (2):\n- First, at 09:00.\n- Second, at 13:00. https://t/b" {
		t.Fatal(content)
	}
}

// --- Finansial --------------------------------------------------------------------------

func TestFormatsMoney(t *testing.T) {
	for _, c := range []struct {
		a      float64
		cur    string
		d      float64
		expect string
	}{{1_500_000, "IDR", 0, "IDR 1,500,000"}, {150_000_000, "IDR", 2, "IDR 1,500,000.00"}, {12_345, "USD", 2, "USD 123.45"}, {5, "USD", 2, "USD 0.05"}, {1_234_567, "KWD", 3, "KWD 1,234.567"}, {999, "JPY", 0, "JPY 999"}} {
		if got := apps.FormatMoney(c.a, c.cur, c.d); got != c.expect {
			t.Error(got, c.expect)
		}
	}
}

func TestThresholds(t *testing.T) {
	st := apps.FinansialSettings(nil)
	if jsonx.Stringify(st.Thresholds) != `{"IDR":1000000}` {
		t.Fatal(jsonx.Stringify(st.Thresholds))
	}
	for _, c := range []struct {
		a      float64
		cur    string
		d      int
		expect bool
	}{{1_000_000, "IDR", 0, true}, {999_999, "IDR", 0, false}, {100_000_000, "IDR", 2, true}, {99_999_999, "IDR", 2, false}, {10_000_000, "USD", 2, false}} {
		if apps.MeetsThreshold(c.a, c.cur, c.d, st) != c.expect {
			t.Error(c)
		}
	}
	usd := apps.FinansialSettings(obj("thresholds", obj("USD", 100.0, "bad", 5.0, "EUR", -1.0)))
	if jsonx.Stringify(usd.Thresholds) != `{"USD":100}` || !apps.MeetsThreshold(10_000, "USD", 2, usd) || apps.MeetsThreshold(9_999, "USD", 2, usd) {
		t.Fatal(jsonx.Stringify(usd.Thresholds))
	}
}

func TestPlainEnglishNotes(t *testing.T) {
	st := apps.FinansialSettings(nil)
	big := apps.FinansialDocument(txn("big", 1_500_000, nil, ""), st)
	if big.SourceID != "fin:txn:big" || big.Title != "Expense of IDR 1,500,000 to Tokopedia on 2026-11-09" || big.DocumentDate != "2026-11-09" ||
		big.Content != "Finansial transaction on 2026-11-09: an expense of IDR 1,500,000 paid to Tokopedia from the account BCA.\nCategory: Electronics.\nNote: new monitor\nTags: work.\nLink: https://finansial.zakiego.com/transactions/big" {
		t.Fatalf("%+v", big)
	}
	if apps.FinansialDocument(txn("small", 999_999, nil, ""), st) != nil || apps.FinansialDocument(txn("usd", 100_000_000, obj("currency", "USD", "minorUnitDigits", 2.0), ""), st) != nil {
		t.Fatal("not notable")
	}
	del := txn("del", 9_000_000, nil, "")
	del.Deleted = true
	if apps.FinansialDocument(del, st) != nil {
		t.Fatal("deleted")
	}
	if !strings.Contains(apps.FinansialDocument(txn("in", 2_000_000, obj("kind", "income", "payee", "Socium"), ""), st).Content, "an income of IDR 2,000,000 from Socium into the account BCA") {
		t.Fatal("income")
	}
	r := apps.FinansialDocument(rule("r1", "2026-11-10T01:00:00Z", nil), st)
	if !regexp.MustCompile(`^fin:rule:r1:[0-9a-f]{16}$`).MatchString(r.SourceID) || r.Title != "Recurring expense: Rent, IDR 5,000,000 monthly" ||
		!strings.Contains(r.Content, `Finansial recurring rule "Rent", as of 2026-11-10: a monthly expense of IDR 5,000,000 to Pak Budi.`) ||
		strings.Contains(r.Content, "Next due") || strings.Contains(r.Content, "2026-12-01") || !strings.Contains(r.Content, "It is active.") {
		t.Fatalf("%+v", r)
	}
	// Key order in the feed doesn't change the version.
	shuffled := rule("r1", "2026-11-10T01:00:00Z", nil)
	rev := jsonx.NewMap[any]()
	keys := shuffled.Data.Keys()
	for i := len(keys) - 1; i >= 0; i-- {
		v, _ := shuffled.Data.Get(keys[i])
		rev.Set(keys[i], v)
	}
	shuffled.Data = rev
	if apps.FinansialDocument(shuffled, st).SourceID != r.SourceID {
		t.Fatal("order changed the version")
	}
	gone := rule("r1", "2026-11-11T01:00:00Z", nil)
	gone.Deleted = true
	if apps.FinansialDocument(gone, st).SourceID != "fin:rule:r1:deleted" {
		t.Fatal("deleted rule")
	}
	loan := func(updatedAt string, data *jsonx.Object, deleted bool) apps.ChangeItem {
		base := obj("name", "Car", "principalMinor", 150_000_000.0, "currency", "IDR", "minorUnitDigits", 0.0, "lender", "BCA Finance", "status", "active")
		return apps.ChangeItem{Kind: "loan", ID: "l1", UpdatedAt: updatedAt, Deleted: deleted, Data: merge(base, data)}
	}
	l := apps.FinansialDocument(loan("2026-11-10T01:00:00Z", nil, false), st)
	if !regexp.MustCompile(`^fin:loan:l1:[0-9a-f]{16}$`).MatchString(l.SourceID) || l.Title != "Loan: Car, IDR 150,000,000" ||
		apps.FinansialDocument(loan("2026-11-12T01:00:00Z", obj("paidMinor", 5_000_000.0), false), st).SourceID != l.SourceID ||
		apps.FinansialDocument(loan("2026-11-12T01:00:00Z", obj("status", "paid_off"), false), st).SourceID == l.SourceID ||
		apps.FinansialDocument(loan("2026-11-12T01:00:00Z", nil, true), st).SourceID != "fin:loan:l1:deleted" {
		t.Fatalf("%+v", l)
	}
	if apps.FinansialDocument(apps.ChangeItem{Kind: "account", ID: "a", UpdatedAt: "2026-11-10T00:00:00Z", Data: jsonx.NewMap[any]()}, st) != nil {
		t.Fatal("account")
	}
}

func sourceIDs(ds []model.Document) []string {
	var out []string
	for _, d := range ds {
		out = append(out, s(d.SourceID))
	}
	sort.Strings(out)
	return out
}

func TestEveryRuleVersion(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, rule("r1", "2026-11-09T01:00:00Z", nil), txn("t1", 2_500_000, nil, "2026-11-09T02:00:00Z"), txn("t2", 10_000, nil, "2026-11-09T03:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 2 {
		t.Fatal(r)
	}
	v1 := rule("r1", "2026-11-09T01:00:00Z", nil)
	v2 := rule("r1", "2026-11-10T01:00:00Z", obj("amountMinor", 5_500_000.0))
	gone := rule("r1", "2026-11-10T03:00:00Z", nil)
	gone.Deleted, gone.Data = true, obj("name", "Rent")
	feed.Add(v2, gone)
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 2 {
		t.Fatal(r)
	}
	st := apps.FinansialSettings(nil)
	want := []string{apps.FinansialDocument(v1, st).SourceID, apps.FinansialDocument(v2, st).SourceID, "fin:rule:r1:deleted", "fin:txn:t1"}
	sort.Strings(want)
	all := e.docs(e.a)
	if strings.Join(sourceIDs(all), ",") != strings.Join(want, ",") {
		t.Fatal(sourceIDs(all), want)
	}
	by := map[string]model.Document{}
	for _, d := range all {
		by[s(d.SourceID)] = d
	}
	if !strings.Contains(s(by[apps.FinansialDocument(v2, st).SourceID].Content), "IDR 5,500,000") || !strings.Contains(s(by["fin:rule:r1:deleted"].Content), "was deleted on 2026-11-10") {
		t.Fatal("contents")
	}
	tx := by["fin:txn:t1"]
	if meta(tx, "source") != "finansial" || meta(tx, "kind") != "transaction" || meta(tx, "id") != "t1" || meta(tx, "url") != "https://finansial.zakiego.com/transactions/t1" || meta(tx, "documentDate") != "2026-11-09" {
		t.Fatal(jsonx.Stringify(tx.Metadata))
	}
	// Reading the whole feed again adds nothing.
	e.d.Exec(`UPDATE app_connectors SET cursor = NULL WHERE id = ?`, row.ID)
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T05:00:00Z")); !r.OK || r.Documents != 0 || len(e.docs(e.a)) != 4 {
		t.Fatal(r)
	}
}

func TestPostingRuleStaysOneDocument(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, rule("r1", "2026-09-01T01:00:00Z", obj("nextDueDate", "2026-10-01")))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	for _, p := range [][2]string{{"2026-10-01T01:00:00Z", "2026-11-01"}, {"2026-11-01T01:00:00Z", "2026-12-01"}, {"2026-11-10T03:00:00Z", "2027-01-01"}} {
		feed.Add(rule("r1", p[0], obj("nextDueDate", p[1])))
		if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 {
			t.Fatal(r)
		}
	}
	var ingests int
	e.d.QueryRow(`SELECT count(*) FROM jobs WHERE type = 'ingest'`).Scan(&ingests)
	if len(e.docs(e.a)) != 1 || ingests != 1 {
		t.Fatal(len(e.docs(e.a)), ingests)
	}
	// A real change (the amount) is a new version.
	feed.Add(rule("r1", "2026-11-10T05:00:00Z", obj("nextDueDate", "2027-01-01", "amountMinor", 6_000_000.0)))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T06:00:00Z")); !r.OK || r.Documents != 1 || len(e.docs(e.a)) != 2 {
		t.Fatal(r)
	}
	// Deleting it writes one "removed" document; a second delete event adds nothing.
	gone := rule("r1", "2026-11-10T07:00:00Z", nil)
	gone.Deleted, gone.Data = true, obj("name", "Rent")
	again := gone
	again.UpdatedAt = "2026-11-10T08:00:00Z"
	feed.Add(gone, again)
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T09:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	var removed model.Document
	for _, d := range e.docs(e.a) {
		if s(d.SourceID) == "fin:rule:r1:deleted" {
			removed = d
		}
	}
	if s(removed.Title) != "Recurring rule removed: Rent" || len(e.docs(e.a)) != 3 {
		t.Fatal(s(removed.Title))
	}
}

func TestThresholdsFromSettings(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, obj("thresholds", obj("USD", 50.0)), "")
	feed := apps.NewFakeFeed(finKey, txn("usd", 6_000, obj("currency", "USD", "minorUnitDigits", 2.0), "2026-11-09T01:00:00Z"), txn("idr", 9_000_000, nil, "2026-11-09T02:00:00Z"))
	e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))
	if d := e.docs(e.a); len(d) != 1 || s(d[0].Title) != "Expense of USD 60.00 to Tokopedia on 2026-11-09" {
		t.Fatal(d)
	}
}

// --- Finansial transaction edits and deletes -----------------------------------------------

var idrAmount = regexp.MustCompile(`IDR [\d,]+`)

// factModel plays the model: one fact per transaction naming the amount;
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

func (e *env) ingested(feed *apps.FakeFeed) model.Document {
	e.t.Helper()
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 0 {
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
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	old := e.ingested(feed)
	fp, _ := meta(old, "fingerprint").(string)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fp) {
		t.Fatal(fp)
	}
	feed.Add(txn("t1", 4_500_000, nil, "2026-11-10T03:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 1 {
		t.Fatal(r)
	}
	ds := e.docs(e.a)
	if len(ds) != 1 || s(ds[0].SourceID) != "fin:txn:t1" || s(ds[0].Title) != "Expense of IDR 4,500,000 to Tokopedia on 2026-11-09" || ds[0].ID == old.ID || meta(ds[0], "fingerprint") == fp {
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
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	old := e.ingested(feed)
	jobsBefore := e.count(`SELECT count(*) FROM jobs`)
	// A newer updated_at, tags in another order and a field Recall doesn't use.
	feed.Add(txn("t1", 5_000_000, obj("tags", []any{"work"}, "reconciled", true), "2026-11-10T03:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 0 {
		t.Fatal(r)
	}
	if ds := e.docs(e.a); len(ds) != 1 || ds[0].ID != old.ID || e.count(`SELECT count(*) FROM jobs`) != jobsBefore || len(e.current()) != 1 {
		t.Fatal(ds)
	}
}

func TestDeleteRemovesDocAndFacts(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	e.ingested(feed)
	del := txn("t1", 0, nil, "2026-11-10T03:00:00Z")
	del.Deleted, del.Data = true, jsonx.NewMap[any]()
	feed.Add(del)
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 1 {
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

func TestEditUnderThresholdRemoves(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	e.ingested(feed)
	feed.Add(txn("t1", 500_000, nil, "2026-11-10T03:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 1 || len(e.docs(e.a)) != 0 || len(e.current()) != 0 {
		t.Fatal(r)
	}
}

func TestIgnoresUnknownDeletedTransaction(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	del := txn("t9", 9_000_000, nil, "2026-11-09T01:00:00Z")
	del.Deleted = true
	feed := apps.NewFakeFeed(finKey, del, txn("small", 10_000, nil, "2026-11-09T02:00:00Z"))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 0 || len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM jobs`) != 0 {
		t.Fatal(r)
	}
}

func TestQueuedIngestDoesntResurrect(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))
	del := txn("t1", 0, nil, "2026-11-10T03:00:00Z")
	del.Deleted, del.Data = true, jsonx.NewMap[any]()
	feed.Add(del)
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T04:00:00Z")); r.Removed != 1 {
		t.Fatal(r)
	}
	e.drain(nil)
	if len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM chunks`) != 0 || len(e.current()) != 0 || e.unfinished() != 0 {
		t.Fatal("resurrected")
	}
}

func TestExtractInFlightWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", finKey, nil, "")
	feed := apps.NewFakeFeed(finKey, txn("t1", 5_000_000, nil, "2026-11-09T01:00:00Z"))
	e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z"))
	doc := e.docs(e.a)[0]
	// The delete lands while the model is answering.
	e.drain(func() { memories.DeleteDocumentAndMemories(e.d, e.a, doc.ID, time.Now().UnixMilli()) })
	if len(e.docs(e.a)) != 0 || e.count(`SELECT count(*) FROM memories`) != 0 || e.unfinished() != 0 {
		t.Fatal("facts written")
	}
}

// --- the job and isolation ------------------------------------------------------------------

func TestHourlyRunOneFailureDoesntBlock(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "oslo", osloKey, nil, "")
	e.setup(e.a, "finansial", finKey, nil, "")
	oslo := apps.NewFakeFeed(osloKey)
	fin := apps.NewFakeFeed(finKey, txn("t1", 2_000_000, nil, "2026-11-09T01:00:00Z"))
	oslo.FailNext("503", 1)
	route := func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.String(), "https://todo") {
			return oslo.Do(r)
		}
		return fin.Do(r)
	}
	run := apps.RunSync(context.Background(), e.d, apps.SyncDeps{Do: route, Now: at("2026-11-10T02:00:00Z")}, func(string) {})
	if run.Connectors != 2 || run.OK != 1 || run.Failed != 1 || run.Documents != 1 {
		t.Fatal(run)
	}
	if !strings.Contains(s(e.conn(e.a, "oslo").LastError), "503") || e.conn(e.a, "finansial").LastError != nil {
		t.Fatal("errors")
	}
}

func TestSkipsOffOrKeyless(t *testing.T) {
	e := newEnv(t)
	k := osloKey
	e.save(e.a, "https://todo.zakiego.com", false, &k, true)
	e.save(e.b, "https://todo.zakiego.com", true, nil, false)
	feed := apps.NewFakeFeed(osloKey)
	if run := apps.RunSync(context.Background(), e.d, deps(feed, "2026-11-10T02:00:00Z"), func(string) {}); run.Connectors != 0 || feed.CallCount() != 0 {
		t.Fatal(run)
	}
}

func TestOneHourlyJob(t *testing.T) {
	e := newEnv(t)
	now := ms("2026-11-10T02:20:00Z")
	e.setup(e.a, "finansial", finKey, nil, "")
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
	feed := apps.NewFakeFeed(finKey, txn("j1", 2_000_000, nil, "2026-11-09T01:00:00Z"))
	g := &testkit.Gateway{}
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Do: g.Do, Now: time.Now, AppSync: deps(feed, "2026-11-10T03:00:00Z")}), func(string) {})
	j, err := r.RunOnce(ms("2026-11-10T03:00:01Z"))
	if err != nil || j == nil || j.Type != "app_sync" {
		t.Fatal(j, err)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'done'`) != 1 || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'queued'`) != 1 {
		t.Fatal("job states")
	}
	if ids := sourceIDs(e.docs(e.a)); strings.Join(ids, ",") != "fin:txn:j1" {
		t.Fatal(ids)
	}
}

func TestNoJobWithoutSyncableConnector(t *testing.T) {
	e := newEnv(t)
	now := ms("2026-11-10T02:20:00Z")
	if _, ok, _ := apps.Schedule(e.d, now); ok {
		t.Fatal("queued with nothing to sync")
	}
	k := osloKey
	e.save(e.a, "https://todo.zakiego.com", false, &k, true)
	e.save(e.b, "https://todo.zakiego.com", true, nil, false)
	if _, ok, _ := apps.Schedule(e.d, now); ok || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync'`) != 0 {
		t.Fatal("queued for off or keyless")
	}
	e.save(e.a, "https://todo.zakiego.com", true, nil, false)
	if _, ok, _ := apps.Schedule(e.d, now); !ok {
		t.Fatal("not queued")
	}
	// Turned off before the hour: the queued run finds nothing and doesn't queue another.
	e.save(e.a, "https://todo.zakiego.com", false, nil, false)
	g := &testkit.Gateway{}
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Do: g.Do, Now: time.Now, AppSync: deps(apps.NewFakeFeed(osloKey), "2026-11-10T03:00:00Z")}), func(string) {})
	if j, _ := r.RunOnce(ms("2026-11-10T03:00:01Z")); j == nil || j.Type != "app_sync" {
		t.Fatal(j)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'done'`) != 1 || e.count(`SELECT count(*) FROM jobs WHERE type = 'app_sync' AND status = 'queued'`) != 0 {
		t.Fatal("job states")
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.setup(e.a, "oslo", osloKey, nil, "")
	b := e.setup(e.b, "oslo", "beta-key-SECRET", nil, "")
	feedA := apps.NewFakeFeed(osloKey, task("a1", "2026-11-09T03:00:00Z", obj("title", "Alpha work"), false))
	feedB := apps.NewFakeFeed("beta-key-SECRET", task("b1", "2026-11-09T03:00:00Z", obj("title", "Beta work"), false))
	e.sync(e.a, "oslo", deps(feedA, "2026-11-10T02:00:00Z"))
	e.sync(e.b, "oslo", deps(feedB, "2026-11-10T02:00:00Z"))
	da, db := e.docs(e.a), e.docs(e.b)
	if feedA.Calls[0].Auth != "Bearer "+osloKey || feedB.Calls[0].Auth != "Bearer beta-key-SECRET" || len(da) != 1 || len(db) != 1 ||
		!strings.Contains(s(da[0].Content), "Alpha work") || !strings.Contains(s(db[0].Content), "Beta work") || strings.Contains(s(da[0].Content), "Beta") ||
		s(da[0].SourceID) != s(db[0].SourceID) {
		t.Fatal(da, db)
	}
	if v, _ := apps.ConnectorView(e.d, e.b, "oslo"); v.LastSyncAt == nil {
		t.Fatal("no sync time")
	}
	// A sealed key copied into the other workspace's row doesn't open.
	e.d.Exec(`UPDATE app_connectors SET api_key = ? WHERE id = ?`, *a.APIKey, b.ID)
	res := e.sync(e.b, "oslo", deps(feedA, "2026-11-10T03:00:00Z"))
	if !regexp.MustCompile(`can't be read`).MatchString(s(res.Error)) || feedA.CallCount() != 1 {
		t.Fatal(res)
	}
}
