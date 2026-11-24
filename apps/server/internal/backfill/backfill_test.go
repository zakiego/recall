package backfill_test

// dated-facts.test.ts "backfill". The CLI half of the second case
// (backfill-dates queues one job per workspace) is ported with admin-cli.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/backfill"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

const (
	W = "ws_test"
	C = "zane"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

func TestDatesInPlaceAndSkipsChangedFacts(t *testing.T) {
	d := testkit.OpenDB(t)
	add := func(ws, fact, keywords, kind string, due *dates.Dated) string {
		m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: ws, Container: C, Fact: fact, Keywords: keywords, Kind: kind, Due: due}, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	a := add(W, "Contract with Vortek runs until the end of 2027", "contract", "dynamic", nil)
	b := add(W, "Prefers Puma running shoes", "shoes", "dynamic", nil)
	c := add(W, "Moved to Bandarjaya in 2019", "city", "static", nil)
	dated := add(W, "Already dated", "", "dynamic", &dates.Dated{Date: "2027-01-01", Precision: dates.Year})
	add("ws_other", "Other workspace contract ends 2027", "", "dynamic", nil)
	if n, _ := backfill.CountUndated(d, W, ""); n != 3 {
		t.Fatal(n)
	}
	g := &testkit.Gateway{}
	g.Reply = func(system, prompt string) (string, bool) {
		// The fact the model calls ref 3 is edited while the call is out.
		d.Exec(`UPDATE memories SET event_date = '2020-01-01', event_precision = 'year' WHERE id = ?`, c)
		return `{"dates":[{"ref":1,"due_date":"2027-12-31"},{"ref":3,"event_date":"2019"},{"ref":2,"due_date":"not a date"},{"ref":9,"due_date":"2030-01-01"}]}`, true
	}
	res, err := backfill.RunBatch(context.Background(), d, W, backfill.Payload{}, g.Do)
	if err != nil || res != (backfill.Result{Checked: 3, Dated: 1}) {
		t.Fatal(res, err)
	}
	p := g.Prompts[0]
	if strings.Contains(p, "Already dated") || strings.Contains(p, "Other workspace") || !regexp.MustCompile(`1\. \(stored [\d-]+\) Contract with Vortek`).MatchString(p) {
		t.Fatal(p)
	}
	get := func(id string) (ev, due, duePrec any, validTo *int64) {
		m, _, _ := memories.GetMemory(d, W, id)
		return deref(m.EventDate), deref(m.DueDate), deref(m.DuePrecision), m.ValidTo
	}
	if _, due, prec, vt := get(a); due != "2027-12-31" || prec != "day" || vt != nil {
		t.Fatal(due, prec, vt)
	}
	if chain, _ := memories.Chain(d, W, a, 500); len(chain) != 1 {
		t.Fatal("not in place")
	}
	if ev, due, _, _ := get(b); ev != nil || due != nil {
		t.Fatal(ev, due)
	}
	if ev, _, _, _ := get(c); ev != "2020-01-01" {
		t.Fatal("the concurrent write lost", ev)
	}
	if _, due, _, _ := get(dated); due != "2027-01-01" {
		t.Fatal(due)
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestQueuesNextBatchWithCursor(t *testing.T) {
	d := testkit.OpenDB(t)
	for i := 0; i < 41; i++ {
		if _, err := d.Exec(`INSERT INTO memories (id, workspace_id, container, fact, created_at) VALUES (?, ?, ?, ?, ?)`, fmt.Sprintf("mem_%03d", i), W, C, fmt.Sprintf("Fact %d", i), 1_000+i); err != nil {
			t.Fatal(err)
		}
	}
	g := &testkit.Gateway{Replies: []string{`{"dates":[]}`, `{"dates":[{"ref":1,"due_date":"2027-05"}]}`}}
	r := jobs.NewRunner(d, pipeline.Handlers(d, pipeline.Deps{Do: g.Do, Now: time.Now}), func(string) {})
	// What `backfill-dates` queues for a workspace with undated facts.
	if _, _, err := jobs.Enqueue(d, W, jobs.BackfillDates, jsonx.NewMap[any](), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if j, _ := r.RunOnce(time.Now().UnixMilli()); j == nil {
			break
		}
	}
	if len(g.Prompts) != 2 || !strings.Contains(g.Prompts[1], "1. (stored 1970-01-01) Fact 40") {
		t.Fatal(g.Prompts)
	}
	m, _, _ := memories.GetMemory(d, W, "mem_040")
	if deref(m.DueDate) != "2027-05-01" || deref(m.DuePrecision) != "month" {
		t.Fatalf("%+v", m)
	}
	rows, _ := d.Query(`SELECT status, payload FROM jobs WHERE type = 'backfill_dates' ORDER BY id`)
	var statuses, payloads []string
	for rows.Next() {
		var s, p string
		rows.Scan(&s, &p)
		statuses, payloads = append(statuses, s), append(payloads, p)
	}
	rows.Close()
	if strings.Join(statuses, ",") != "done,done" || payloads[1] != `{"cursor":{"createdAt":1039,"id":"mem_039"},"checked":40,"dated":0}` {
		t.Fatal(statuses, payloads)
	}
}
