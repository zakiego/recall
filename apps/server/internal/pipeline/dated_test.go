package pipeline_test

// dated-facts.test.ts: extraction, relative dates and edits. The schema,
// PATCH parsing, upcoming, search, Ask, MCP and backfill cases are ported
// with their areas (gateway golden, M4, M5).

import (
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
)

const dC = "zane"

func dateCols(m model.Memory) []any {
	return []any{deref(m.EventDate), deref(m.EventPrecision), deref(m.DueDate), deref(m.DuePrecision)}
}

func TestStoresDatesWithPrecision(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[
		{"fact":"Contract with Vortek runs until the end of 2027","keywords":"contract kontrak","kind":"dynamic","event_date":"2026-11-05","due_date":"2027-12-31"},
		{"fact":"Sister's wedding is in March 2027","keywords":"wedding nikah","kind":"dynamic","due_date":"2027-03"},
		{"fact":"Prefers Puma running shoes","keywords":"shoes sepatu","kind":"dynamic"}]}`}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	addText(t, d, dC, "Signed with Vortek. Wedding in March. Puma shoes.")
	drain(t, r)
	by := map[string]model.Memory{}
	for _, m := range current(t, d) {
		by[strings.Split(m.Fact, " ")[0]] = m
	}
	eq(t, dateCols(by["Contract"]), []any{"2026-11-05", "day", "2027-12-31", "day"})
	eq(t, dateCols(by["Sister's"]), []any{nil, nil, "2027-03-01", "month"})
	eq(t, dateCols(by["Prefers"]), []any{nil, nil, nil, nil})
}

func TestUpdateKeepsOldDatesUnlessSent(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Office lease with Ruang Kerja ends 2027-06-30","keywords":"office lease sewa kantor","kind":"dynamic","event_date":"2025-07-01","due_date":"2027-06-30"}]}`,
		`{"update":[{"ref":1,"fact":"Office lease with Ruang Kerja ends mid 2027, rent paid quarterly","keywords":"office lease sewa kantor quarterly"}]}`,
		`{"update":[{"ref":1,"fact":"Office lease with Ruang Kerja extended to the end of 2028","keywords":"office lease sewa kantor","due_date":"2028-12-31"}]}`,
		`{"update":[{"ref":1,"fact":"Office lease with Ruang Kerja is now month to month","keywords":"office lease sewa kantor","due_date":null}]}`,
	}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	for _, text := range []string{"Lease signed.", "Rent is quarterly now.", "We extended the lease to end of 2028.", "Lease is month to month now."} {
		addText(t, d, dC, "Office lease with Ruang Kerja. "+text)
		drain(t, r)
	}
	cur := current(t, d)
	chain, err := memories.Chain(d, W, cur[0].ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	var got [][]any
	for _, m := range chain {
		got = append(got, []any{deref(m.EventDate), deref(m.DueDate)})
	}
	eq(t, got, [][]any{
		{"2025-07-01", "2027-06-30"},
		{"2025-07-01", "2027-06-30"}, // left out: carried over
		{"2025-07-01", "2028-12-31"}, // new due date, event date carried over
		{"2025-07-01", nil},          // null clears it
	})
}

func TestConflictFactGetsOwnDates(t *testing.T) {
	d := openDB(t)
	m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: W, Container: dC, Fact: "Car insurance renews on 2026-12-01", Keywords: "car insurance asuransi", Kind: "dynamic",
		Due: &dates.Dated{Date: "2026-12-01", Precision: dates.Day}}, nowFn())
	if err != nil {
		t.Fatal(err)
	}
	s := &scripted{replies: []string{`{"conflict":[{"ref":1,"fact":"Car insurance renews on 2026-12-15","keywords":"car insurance asuransi","note":"Dates differ","due_date":"2026-12-15"}]}`}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	addText(t, d, dC, "Car insurance renews 15 December.")
	drain(t, r)
	rows := current(t, d)
	eq(t, deref(find(rows, func(x model.Memory) bool { return x.ID == m.ID }).DueDate), "2026-12-01")
	other := find(rows, func(x model.Memory) bool { return x.ID != m.ID })
	eq(t, []any{deref(other.DueDate), deref(other.DuePrecision)}, []any{"2026-12-15", "day"})
}

func TestSendsCallersDocumentDate(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[{"fact":"Dentist appointment","keywords":"dentist dokter gigi","kind":"dynamic","due_date":"2026-11-06"}]}`}}
	// Extraction runs on 2026-11-20 for an email written on Tuesday 2026-11-03.
	r := newRunner(d, s.deps(date("2026-11-20T03:00:00Z")))
	content := "Dentist appointment next Friday."
	if _, err := api.AddContent(d, W, api.AddInput{Container: dC, Content: &content, DocumentDate: str("2026-11-03")}, nowFn()); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	var prompt string
	for _, p := range s.prompts {
		if strings.Contains(p, "Dentist") {
			prompt = p
			break
		}
	}
	if !strings.Contains(prompt, "Today is 2026-11-20.") || !strings.Contains(prompt, "DOCUMENT DATE: 2026-11-03") {
		t.Fatalf("prompt:\n%s", prompt)
	}
	if !regexpMatch(`Resolve relative dates .* against the DOCUMENT DATE, never against today's date`, s.systems[0]) {
		t.Fatal("extract system doesn't explain relative dates")
	}
	eq(t, deref(current(t, d)[0].DueDate), "2026-11-06")
}

func TestDocumentDateFallbacks(t *testing.T) {
	createdAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{Metadata: jsonx.ObjectOf("documentDate", "2026-11-03"), CreatedAt: createdAt}), "2026-11-03")
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{Metadata: jsonx.ObjectOf("date", "2026-10-20T08:00:00Z"), CreatedAt: createdAt}), "2026-10-20")
	// A month is not a day to count from.
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{Metadata: jsonx.ObjectOf("date", "2026-10"), CreatedAt: createdAt}), "2026-10-01")
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{Metadata: jsonx.ObjectOf("date", "Tue, 3 Nov 2026"), CreatedAt: createdAt}), "2026-10-01")
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{CreatedAt: createdAt}), "2026-10-01")
}

func TestRetriedExtractReadsSameDocumentDate(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[]}`, `{"add":[]}`}}
	today := date("2026-11-05")
	r := newRunner(d, pipeline.Deps{Do: s.do, Now: func() time.Time { return today }})
	res := addText(t, d, dC, "Payment due in two weeks.")
	if _, err := d.Exec(`UPDATE documents SET created_at = ? WHERE id = ?`, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), res.ID); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	today = date("2026-11-30")
	if _, err := d.Exec(`INSERT INTO jobs (workspace_id, type, payload) VALUES (?, 'extract', ?)`, W, `{"documentId":"`+res.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	var dated []string
	for _, p := range s.prompts {
		if strings.Contains(p, "Payment") {
			dated = append(dated, p)
		}
	}
	eq(t, len(dated), 2)
	for _, p := range dated {
		if !strings.Contains(p, "DOCUMENT DATE: 2026-11-01") {
			t.Fatalf("prompt:\n%s", p)
		}
	}
}

func TestDateEditIsNewVersion(t *testing.T) {
	d := openDB(t)
	m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: W, Container: dC, Fact: "Domain renews yearly", Keywords: "domain", Kind: "dynamic"}, nowFn())
	if err != nil {
		t.Fatal(err)
	}
	due := memories.Value(&dates.Dated{Date: "2026-12-02", Precision: dates.Day})
	v2, _, err := memories.EditMemory(d, W, m.ID, memories.Patch{Due: due}, nowFn)
	if err != nil || v2.ID == m.ID {
		t.Fatalf("v2 %+v %v", v2, err)
	}
	eq(t, []any{v2.Fact, deref(v2.DueDate), deref(v2.SupersedesID)}, []any{"Domain renews yearly", "2026-12-02", m.ID})
	same, _, _ := memories.EditMemory(d, W, v2.ID, memories.Patch{Due: due}, nowFn)
	eq(t, same.ID, v2.ID)
	// Editing the text alone keeps the dates.
	v3, _, _ := memories.EditMemory(d, W, v2.ID, memories.Patch{Fact: str("Domain zakiego.com renews yearly")}, nowFn)
	eq(t, []any{deref(v3.DueDate), deref(v3.DuePrecision)}, []any{"2026-12-02", "day"})
}

// "PATCH takes short dates and null": patchMemory with parsed dates (the
// parsing of event_date and due_date is patchInput's, ported in M5).
func TestPatchMemoryDates(t *testing.T) {
	d := openDB(t)
	m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: W, Container: dC, Fact: "Contract with Vortek", Keywords: "contract", Kind: "dynamic"}, nowFn())
	if err != nil {
		t.Fatal(err)
	}
	res, err := api.PatchMemory(d, W, m.ID, api.PatchInput{Event: memories.Value(dates.Parse("2026-01")), Due: memories.Value(dates.Parse("2027-12-31"))}, nowFn)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, dateCols(res.Memory), []any{"2026-01-01", "month", "2027-12-31", "day"})
	cleared, err := api.PatchMemory(d, W, res.Memory.ID, api.PatchInput{Event: memories.Value(nil)}, nowFn)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, dateCols(cleared.Memory), []any{nil, nil, "2027-12-31", "day"})
}
