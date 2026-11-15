package memories_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/memories"
)

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

func intp(n int) *int { return &n }

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %#v\nwant %#v", got, want)
	}
}

// --- list-documents.test.ts: listDocuments pages newest first with a
// (createdAt, id) keyset cursor.

const (
	docsW = "ws_docs"
	docsC = "zaki"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

func doc(t *testing.T, d *db.DB, id string, createdAt int64, container string) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, title, status, created_at) VALUES (?, ?, ?, 'email', ?, 'done', ?)`,
		id, docsW, container, "Title "+id, createdAt); err != nil {
		t.Fatal(err)
	}
}

func pad(n int) string { return fmt.Sprintf("%04d", n) }

func list(t *testing.T, d *db.DB, cursor *string, limit *int) ([]string, *string) {
	t.Helper()
	c := ""
	if cursor != nil {
		c = *cursor
	}
	docs, next, err := memories.ListDocuments(d, docsW, docsC, c, limit)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, x := range docs {
		ids = append(ids, x.ID)
	}
	return ids, next
}

// allPages is every page from the first to the last, following nextCursor.
func allPages(t *testing.T, d *db.DB, limit int) [][]string {
	var pages [][]string
	var cursor *string
	for {
		ids, next := list(t, d, cursor, intp(limit))
		pages = append(pages, ids)
		cursor = next
		if cursor == nil || len(pages) >= 100 {
			return pages
		}
	}
}

func lens(pages [][]string) []int {
	out := []int{}
	for _, p := range pages {
		out = append(out, len(p))
	}
	return out
}

func flat(pages [][]string) []string {
	out := []string{}
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

func TestListDocumentsDefaultsToNewest50(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 120; i++ {
		doc(t, d, "doc_"+pad(i), t0-int64(i)*1000, docsC)
	}
	ids, next := list(t, d, nil, nil)
	eq(t, len(ids), 50)
	eq(t, ids[0], "doc_0000")
	if next == nil {
		t.Fatal("no cursor")
	}
	n, _ := memories.CountDocuments(d, docsW, docsC)
	eq(t, n, int64(120))
}

func TestListDocumentsReachesEveryDocumentOnce(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 120; i++ {
		doc(t, d, "doc_"+pad(i), t0-int64(i)*1000, docsC)
	}
	pages := allPages(t, d, 50)
	eq(t, lens(pages), []int{50, 50, 20})
	var want []string
	for i := 0; i < 120; i++ {
		want = append(want, "doc_"+pad(i))
	}
	eq(t, flat(pages), want)
}

func TestListDocumentsSplitsSameMillisecondByID(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 7; i++ {
		doc(t, d, fmt.Sprintf("doc_same_%d", i), t0, docsC)
	}
	doc(t, d, "doc_older", t0-1, docsC)
	pages := allPages(t, d, 3)
	eq(t, lens(pages), []int{3, 3, 2})
	var want []string
	for i := 0; i < 7; i++ {
		want = append(want, fmt.Sprintf("doc_same_%d", 6-i))
	}
	eq(t, flat(pages), append(want, "doc_older"))
}

func TestListDocumentsStableWhenNewerArrives(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 4; i++ {
		doc(t, d, fmt.Sprintf("doc_%d", i), t0-int64(i)*1000, docsC)
	}
	_, next := list(t, d, nil, intp(2))
	doc(t, d, "doc_new", t0+1000, docsC)
	ids, next2 := list(t, d, next, intp(2))
	eq(t, ids, []string{"doc_2", "doc_3"})
	if next2 != nil {
		t.Fatal("cursor on the last page")
	}
}

func TestListDocumentsCursorAndLimitEdges(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 250; i++ {
		doc(t, d, "doc_"+pad(i), t0-int64(i)*1000, docsC)
	}
	ids, _ := list(t, d, nil, intp(1000))
	eq(t, len(ids), 200)
	ids, _ = list(t, d, memoriesStr("not-a-cursor"), intp(2))
	eq(t, ids, []string{"doc_0000", "doc_0001"})
	if _, next := list(t, d, nil, intp(250)); next == nil {
		t.Fatal("no cursor with 250 documents")
	}
	_, first := list(t, d, nil, intp(200))
	tail, next := list(t, d, first, intp(200))
	eq(t, len(tail), 50)
	if next != nil {
		t.Fatal("cursor on the last page")
	}
}

func memoriesStr(s string) *string { return &s }

func TestListDocumentsStaysInContainer(t *testing.T) {
	d := openDB(t)
	doc(t, d, "doc_mine", t0, docsC)
	doc(t, d, "doc_other", t0, "other")
	ids, _ := list(t, d, nil, nil)
	eq(t, ids, []string{"doc_mine"})
	n, _ := memories.CountDocuments(d, docsW, docsC)
	eq(t, n, int64(1))
}

// --- upcoming-count.test.ts: countUpcoming counts what listUpcoming would
// return without its row limit.

const upW = "ws_upcoming"

func dueFact(t *testing.T, d *db.DB, id, due, container string, validTo *int64) {
	t.Helper()
	if container == "" {
		container = "zaki"
	}
	if _, err := d.Exec(`INSERT INTO memories (id, workspace_id, container, fact, due_date, due_precision, valid_to, created_at) VALUES (?, ?, ?, ?, ?, 'day', ?, ?)`,
		id, upW, container, "fact "+id, due, validTo, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestCountUpcomingPastTheLimit(t *testing.T) {
	d := openDB(t)
	for i := 0; i < 250; i++ {
		dueFact(t, d, fmt.Sprintf("m_%d", i), fmt.Sprintf("2026-12-%02d", i%28+1), "", nil)
	}
	r := memories.UpcomingRange{WorkspaceID: upW, Container: "zaki", From: "2026-12-01", To: "2026-12-31"}
	l, err := memories.ListUpcoming(d, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(l), 200)
	n, _ := memories.CountUpcoming(d, r)
	eq(t, n, int64(250))
}

func TestCountUpcomingSameFilters(t *testing.T) {
	d := openDB(t)
	closed := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	dueFact(t, d, "in", "2026-12-10", "", nil)
	dueFact(t, d, "edge", "2026-12-31", "", nil)
	dueFact(t, d, "later", "2027-01-01", "", nil)
	dueFact(t, d, "closed", "2026-12-10", "", &closed)
	dueFact(t, d, "other", "2026-12-10", "work", nil)
	r := memories.UpcomingRange{WorkspaceID: upW, From: "2026-12-01", To: "2026-12-31"}
	mine := r
	mine.Container = "zaki"
	n, _ := memories.CountUpcoming(d, mine)
	eq(t, n, int64(2))
	l, _ := memories.ListUpcoming(d, mine, nil)
	var ids []string
	for _, m := range l {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	eq(t, ids, []string{"edge", "in"})
	// No container: every container in the workspace.
	n, _ = memories.CountUpcoming(d, r)
	eq(t, n, int64(3))
}
