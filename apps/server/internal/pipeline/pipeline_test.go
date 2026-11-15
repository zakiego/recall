package pipeline_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/search"
)

func date(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02", s)
		if err != nil {
			panic(err)
		}
	}
	return t
}

func nowFn() int64 { return time.Now().UnixMilli() }

func add(f, keywords, kind string) gateway.AddItem {
	return gateway.AddItem{Fact: f, Keywords: keywords, Kind: kind}
}

func dyn(f, keywords string) gateway.AddItem { return add(f, keywords, "dynamic") }

func find(ms []model.Memory, pred func(model.Memory) bool) model.Memory {
	for _, m := range ms {
		if pred(m) {
			return m
		}
	}
	return model.Memory{}
}

func contains(sub string) func(model.Memory) bool {
	return func(m model.Memory) bool { return strings.Contains(m.Fact, sub) }
}

func factIs(f string) func(model.Memory) bool {
	return func(m model.Memory) bool { return m.Fact == f }
}

func facts(ms []model.Memory) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Fact)
	}
	sort.Strings(out)
	return out
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestIngestsExtractsAndSupersedes(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Loves Adidas shoes","keywords":"sneakers sepatu","kind":"dynamic"}]}`,
		`{"add":[{"fact":"Lives in Banjarbaru","keywords":"city kota","kind":"static"}]}`,
		`{"update":[{"ref":1,"fact":"Switched to Puma shoes","keywords":"sneakers sepatu puma"}]}`,
	}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	const C = "zaki"
	addText(t, d, C, "I love my Adidas shoes.")
	drain(t, r)
	addText(t, d, C, "I live in Banjarbaru.")
	drain(t, r)
	addText(t, d, C, "I switched to Puma, done with Adidas.")
	drain(t, r)

	// The third extract saw the Adidas memory as ref 1.
	if p := s.prompts[len(s.prompts)-2]; !regexp.MustCompile(`1\. \[dynamic\] Loves Adidas shoes`).MatchString(p) {
		t.Fatalf("third extract prompt:\n%s", p)
	}
	cur := current(t, d)
	eq(t, facts(cur), []string{"Lives in Banjarbaru", "Switched to Puma shoes"})
	puma := find(cur, contains("Puma"))
	chain, err := memories.Chain(d, W, puma.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0].Fact != "Loves Adidas shoes" || chain[1].Fact != "Switched to Puma shoes" {
		t.Fatalf("chain %v", chain)
	}
	if chain[0].ValidTo == nil {
		t.Fatal("old version still current")
	}

	// Search only returns the current version.
	v, err := embed.EmbedOne("Adidas shoes", embed.Query)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.HybridSearch(d, search.Input{WorkspaceID: W, Container: C, Q: "Adidas shoes", Vector: v})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Text == "Loves Adidas shoes" {
			t.Fatal("search returned the superseded version")
		}
	}
	if len(hits) == 0 || hits[0].Text != "Switched to Puma shoes" {
		t.Fatalf("first hit %v", hits)
	}

	for _, doc := range documentsAll(t, d) {
		if doc.Status != "done" {
			t.Fatalf("document %s is %s", doc.ID, doc.Status)
		}
	}
	var ws, cont, static string
	if err := d.QueryRow(`SELECT workspace_id, container, static_text FROM profiles`).Scan(&ws, &cont, &static); err != nil {
		t.Fatal(err)
	}
	eq(t, []string{ws, cont, static}, []string{W, C, "Static summary."})
	for _, m := range memoriesWhere(t, d, "") {
		if m.WorkspaceID != W {
			t.Fatal("memory in another workspace")
		}
	}
	for _, j := range jobsAll(t, d) {
		if j.WorkspaceID != W || j.Status != "done" {
			t.Fatalf("job %+v", j)
		}
	}
}

func TestInvalidatesIgnoresUnknownRefsAndChatInput(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Works at Speechify","keywords":"job kerja","kind":"dynamic"}]}`,
		`{"invalidate":[1,7],"update":[{"ref":9,"fact":"ghost","keywords":""}]}`,
	}}
	r := newRunner(d, s.deps(time.Now()))
	addChat(t, d, "c", "I work at Speechify", nil)
	drain(t, r)
	addChat(t, d, "c", "I quit my job", nil)
	drain(t, r)
	eq(t, len(current(t, d)), 0)
	eq(t, len(memoriesWhere(t, d, "")), 1)
}

func TestDropsSensitiveFacts(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Applied for a backend role at Acme","keywords":"job lamaran kerja","kind":"dynamic"},{"fact":"Passport number is C1234567","keywords":"paspor passport","kind":"static"},{"fact":"Lives at Jl. Mawar No. 5, Banjarbaru","keywords":"alamat rumah","kind":"static"}]}`,
		`{"update":[{"ref":1,"fact":"NIK is 3171011508900001","keywords":"ktp nik"},{"ref":2,"fact":"Pays with card 4111 1111 1111 1111","keywords":"kartu kredit"}]}`,
	}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	first := addText(t, d, "c", "Job email: application to Acme, passport C1234567 attached.")
	drain(t, r)
	second := addText(t, d, "c", "Form: NIK 3171011508900001, card 4111 1111 1111 1111.")
	drain(t, r)

	// The street address is the prompt's job; the code guard only blocks patterns it can check.
	eq(t, facts(memoriesWhere(t, d, "")), []string{"Applied for a backend role at Acme", "Lives at Jl. Mawar No. 5, Banjarbaru"})
	eq(t, len(current(t, d)), 2)
	expectStats(t, extractStats(t, docByID(t, d, first.ID)), map[string]float64{"added": 2, "updated": 0, "redacted": 1})
	expectStats(t, extractStats(t, docByID(t, d, second.ID)), map[string]float64{"added": 0, "updated": 0, "redacted": 2})
}

func TestDropsSensitiveConflict(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Passport is held by the employer","keywords":"paspor passport","kind":"dynamic"}]}`,
		`{"conflict":[{"ref":1,"fact":"Passport number is C1234567","keywords":"paspor","note":"unclear"}],"invalidate":[1]}`,
	}}
	r := newRunner(d, s.deps(time.Now()))
	addText(t, d, "c", "HR keeps my passport.")
	drain(t, r)
	second := addText(t, d, "c", "Passport C1234567 attached.")
	drain(t, r)

	// The dropped conflict still outranks "invalidate", so the memory stays as it was.
	cur := current(t, d)
	if len(cur) != 1 || cur[0].Fact != "Passport is held by the employer" || cur[0].ReviewNote != nil {
		t.Fatalf("current %+v", cur)
	}
	expectStats(t, extractStats(t, docByID(t, d, second.ID)), map[string]float64{"conflicts": 0, "invalidated": 0, "redacted": 1})
}

func apiKey() string {
	return strings.Join([]string{"rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2"}, "")
}

func TestMasksKeysAndCards(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[]}`, `{"add":[]}`}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	key := apiKey()
	plain := addText(t, d, "c", "Staging key "+key+", card 4111 1111 1111 1111. Call me at 0812-3456-7890.")
	chatDoc := addChat(t, d, "c", "my key is "+key, nil)
	drain(t, r)

	rows, err := d.Query(`SELECT text FROM chunks`)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		texts = append(texts, s)
	}
	rows.Close()
	stored := strings.Join(texts, "\n")
	if strings.Contains(stored, key) || strings.Contains(stored, "4111 1111 1111 1111") {
		t.Fatalf("chunks hold the secret: %s", stored)
	}
	if !strings.Contains(stored, "Staging key [redacted:secret], card [redacted:card]. Call me at 0812-3456-7890.") {
		t.Fatalf("chunks: %s", stored)
	}
	if strings.Contains(strings.Join(s.prompts, "\n"), key) {
		t.Fatal("a prompt holds the key")
	}
	p := docByID(t, d, plain.ID)
	if p.Content == nil || strings.Contains(*p.Content, key) {
		t.Fatal("content holds the key")
	}
	masked, _ := p.Metadata.Get("masked")
	eq(t, masked, float64(2))
	mb, _ := jsonx.Marshal(docByID(t, d, chatDoc.ID).Metadata)
	if strings.Contains(string(mb), key) {
		t.Fatalf("chat metadata holds the key: %s", mb)
	}

	// Indexed text is masked too: the key finds nothing.
	v, err := embed.EmbedOne(key, embed.Query)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.HybridSearch(d, search.Input{WorkspaceID: W, Container: "c", Q: key, Vector: v, Include: []string{"chunks"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if strings.Contains(h.Text, key) {
			t.Fatal("search found the key")
		}
	}
}

func TestMasksKeyInTitle(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[]}`, `{"add":[]}`}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	key := apiKey()
	// The title comes from the first line of the content.
	fromContent := addText(t, d, "c", "key "+key+"\nfor staging")
	eq(t, deref(docByID(t, d, fromContent.ID).Title), "key [redacted:secret]")
	body := "body"
	explicit, err := api.AddContent(d, W, api.AddInput{Container: "c", Content: &body, Title: str("deploy " + key)}, nowFn())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, deref(docByID(t, d, explicit.ID).Title), "deploy [redacted:secret]")
	// A row written before titles were masked is cleaned up when it's ingested.
	if _, err := d.Exec(`UPDATE documents SET title = ?`, "old "+key); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	var titles []any
	for _, doc := range documentsAll(t, d) {
		titles = append(titles, deref(doc.Title))
	}
	eq(t, titles, []any{"old [redacted:secret]", "old [redacted:secret]"})
}

func TestKeepsBothSidesOfConflict(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Salary is paid weekly after timesheet approval","keywords":"salary pay gaji","kind":"dynamic"}]}`,
		`{"conflict":[{"ref":1,"fact":"Salary is paid on the last Friday of the month","keywords":"salary pay gaji","note":"Email says monthly on the last Friday; the contract says weekly after timesheet approval"},{"ref":5,"fact":"ghost","keywords":"","note":"unknown ref"}],"invalidate":[1]}`,
	}}
	r := newRunner(d, s.deps(time.Now()))
	addText(t, d, "c", "Contract: salary is paid weekly after timesheet approval.")
	drain(t, r)
	email := addText(t, d, "c", "Email: salary goes out on the last Friday of the month.")
	drain(t, r)

	if p := s.prompts[len(s.prompts)-2]; !regexp.MustCompile(`1\. \[dynamic\] Salary is paid weekly`).MatchString(p) {
		t.Fatalf("prompt:\n%s", p)
	}
	cur := current(t, d)
	eq(t, len(cur), 2)
	weekly := find(cur, contains("weekly"))
	monthly := find(cur, contains("Friday"))
	eq(t, []any{monthly.Kind, deref(monthly.SourceDocumentID), deref(monthly.SupersedesID)}, []any{"dynamic", email.ID, nil})
	eq(t, deref(monthly.ReviewNote), `Conflicts with "Salary is paid weekly after timesheet approval": Email says monthly on the last Friday; the contract says weekly after timesheet approval`)
	if weekly.ReviewNote == nil || !strings.HasPrefix(*weekly.ReviewNote, `Conflicts with "Salary is paid on the last Friday of the month": Email says`) {
		t.Fatalf("weekly note %v", deref(weekly.ReviewNote))
	}
	expectStats(t, extractStats(t, docByID(t, d, email.ID)), map[string]float64{"added": 0, "updated": 0, "invalidated": 0, "conflicts": 1})
}

func TestClearUpdateWinsOverConflict(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Works at Speechify","keywords":"job kerja","kind":"dynamic"}]}`,
		`{"update":[{"ref":1,"fact":"Works at Anthropic","keywords":"job kerja"}],"conflict":[{"ref":1,"fact":"Works at Google","keywords":"job kerja","note":"unclear"}]}`,
	}}
	r := newRunner(d, s.deps(time.Now()))
	addText(t, d, "c", "I work at Speechify")
	drain(t, r)
	addText(t, d, "c", "I moved to Anthropic")
	drain(t, r)
	cur := current(t, d)
	if len(cur) != 1 || cur[0].Fact != "Works at Anthropic" || cur[0].ReviewNote != nil {
		t.Fatalf("current %+v", cur)
	}
}

const twoLetters = `{"conflict":[{"ref":1,"fact":"Lives in Martapura","keywords":"city kota","note":"A says Martapura"},{"ref":1,"fact":"Lives in Banjarmasin","keywords":"city kota","note":"B says Banjarmasin"}]}`

func TestAppendsSecondConflictNote(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[{"fact":"Lives in Banjarbaru","keywords":"city kota","kind":"static"}]}`, twoLetters}}
	r := newRunner(d, s.deps(time.Now()))
	addText(t, d, "c", "I live in Banjarbaru")
	drain(t, r)
	addText(t, d, "c", "Two letters disagree about where I live")
	drain(t, r)
	cur := current(t, d)
	eq(t, len(cur), 3)
	for _, m := range cur {
		if m.Kind != "static" || m.ReviewNote == nil || *m.ReviewNote == "" {
			t.Fatalf("memory %+v", m)
		}
	}
	original := find(cur, factIs("Lives in Banjarbaru"))
	eq(t, strings.Split(*original.ReviewNote, "\n"), []string{`Conflicts with "Lives in Martapura": A says Martapura`, `Conflicts with "Lives in Banjarmasin": B says Banjarmasin`})
}

// conflictPair leaves two current facts flagged against each other, the way
// extraction leaves them.
func conflictPair(t *testing.T, d *db.DB) (a, b model.Memory) {
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Works at Bubungan Digital","keywords":"job kerja","kind":"dynamic"}]}`,
		`{"conflict":[{"ref":1,"fact":"Works at Speechify","keywords":"job kerja","note":"two sources disagree"}]}`,
	}}
	r := newRunner(d, s.deps(time.Now()))
	addText(t, d, "c", "I work at Bubungan Digital")
	drain(t, r)
	addText(t, d, "c", "I work at Speechify")
	drain(t, r)
	cur := current(t, d)
	return find(cur, contains("Bubungan")), find(cur, contains("Speechify"))
}

func flags(t *testing.T, d *db.DB) []string {
	var out []string
	for _, m := range current(t, d) {
		out = append(out, strings.Join([]string{m.Fact, boolText(m.ReviewNote != nil), boolText(m.ConflictsWithID != nil)}, ","))
	}
	sort.Strings(out)
	return out
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestSettlingConflict(t *testing.T) {
	t.Run("links the two sides", func(t *testing.T) {
		d := openDB(t)
		a, b := conflictPair(t, d)
		eq(t, deref(a.ConflictsWithID), b.ID)
		eq(t, deref(b.ConflictsWithID), a.ID)
	})
	t.Run("clears both when one is marked resolved", func(t *testing.T) {
		d := openDB(t)
		a, _ := conflictPair(t, d)
		if _, err := api.PatchMemory(d, W, a.ID, api.PatchInput{Resolve: true}, nowFn); err != nil {
			t.Fatal(err)
		}
		eq(t, flags(t, d), []string{"Works at Bubungan Digital,false,false", "Works at Speechify,false,false"})
	})
	t.Run("clears the survivor when the other side is deleted", func(t *testing.T) {
		d := openDB(t)
		a, _ := conflictPair(t, d)
		if _, _, err := memories.ForgetMemory(d, W, a.ID, nowFn()); err != nil {
			t.Fatal(err)
		}
		eq(t, flags(t, d), []string{"Works at Speechify,false,false"})
	})
	t.Run("keeps a fact flagged while another conflict on it is still open", func(t *testing.T) {
		d := openDB(t)
		s := &scripted{replies: []string{`{"add":[{"fact":"Lives in Banjarbaru","keywords":"city kota","kind":"static"}]}`, twoLetters}}
		r := newRunner(d, s.deps(time.Now()))
		addText(t, d, "c", "I live in Banjarbaru")
		drain(t, r)
		addText(t, d, "c", "Two letters disagree about where I live")
		drain(t, r)
		byFact := func(f string) model.Memory { return find(current(t, d), factIs(f)) }
		martapura := byFact("Lives in Martapura")
		// The original links to the newest conflict, Banjarmasin. Deleting that
		// one must leave the original flagged against Martapura.
		if _, _, err := memories.ForgetMemory(d, W, byFact("Lives in Banjarmasin").ID, nowFn()); err != nil {
			t.Fatal(err)
		}
		eq(t, deref(byFact("Lives in Banjarbaru").ConflictsWithID), martapura.ID)
		if byFact("Lives in Banjarbaru").ReviewNote == nil {
			t.Fatal("note cleared")
		}
		// Resolving the original settles everything that pointed at it.
		if _, err := api.PatchMemory(d, W, byFact("Lives in Banjarbaru").ID, api.PatchInput{Resolve: true}, nowFn); err != nil {
			t.Fatal(err)
		}
		eq(t, flags(t, d), []string{"Lives in Banjarbaru,false,false", "Lives in Martapura,false,false"})
	})
	t.Run("clears both when one side is edited", func(t *testing.T) {
		d := openDB(t)
		_, b := conflictPair(t, d)
		if _, ok, err := memories.EditMemory(d, W, b.ID, memories.Patch{Fact: str("Works at Speechify as a contractor")}, nowFn); err != nil || !ok {
			t.Fatal(ok, err)
		}
		eq(t, flags(t, d), []string{"Works at Bubungan Digital,false,false", "Works at Speechify as a contractor,false,false"})
	})
}

func TestMarksDocumentFailedAfterLastRetry(t *testing.T) {
	d := openDB(t)
	do := func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 502, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"upstream","message":"gateway down"}}`))}, nil
	}
	r := newRunner(d, pipeline.Deps{Do: do, Now: time.Now})
	res := addText(t, d, "c", "something")
	now := time.Now().UnixMilli()
	for i := 0; i < 6; i++ {
		if _, err := r.RunOnce(now); err != nil {
			t.Fatal(err)
		}
		now += 3_600_000
	}
	doc := docByID(t, d, res.ID)
	eq(t, doc.Status, "failed")
	eq(t, deref(doc.Error), "gateway down")
}

func TestDeletedWhileExtractWaits(t *testing.T) {
	d := openDB(t)
	var id string
	s := &scripted{}
	s.reply = func(system, prompt string) string {
		if strings.Contains(system, "profile") {
			return `{"static":"","dynamic":""}`
		}
		if _, err := memories.DeleteDocument(d, W, id); err != nil {
			t.Error(err)
		}
		return `{"add":[{"fact":"Bought a monitor","keywords":"monitor","kind":"dynamic"}]}`
	}
	r := newRunner(d, s.deps(time.Now()))
	id = addText(t, d, "c", "I bought a monitor.").ID
	drain(t, r)
	eq(t, s.calls, 1)
	eq(t, len(documentsAll(t, d)), 0)
	eq(t, len(memoriesWhere(t, d, "")), 0)
	for _, j := range jobsAll(t, d) {
		eq(t, j.Status, "done")
	}
}

func TestDeletedBeforeIngest(t *testing.T) {
	d := openDB(t)
	s := &scripted{}
	r := newRunner(d, s.deps(time.Now()))
	res := addText(t, d, "c", "I bought a monitor.")
	if _, err := memories.DeleteDocument(d, W, res.ID); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	var n int
	d.QueryRow(`SELECT count(*) FROM chunks`).Scan(&n)
	eq(t, n, 0)
	var got []string
	for _, j := range jobsAll(t, d) {
		got = append(got, j.Type+","+j.Status)
	}
	eq(t, got, []string{"ingest,done"})
}

// --- dedupe -------------------------------------------------------------------------

var (
	contractor = dyn("Works as an independent contractor", "contractor freelance job kerja kontraktor")
	signed     = dyn("Signed the Unifrutti contractor agreement on 2026-10-01", "contract agreement signed date kontrak perjanjian tanda tangan tanggal unifrutti")
	city       = add("Lives in Banjarbaru", "city home residence kota rumah tempat tinggal banjarbaru", "static")
	zaki       = search.Scope{WorkspaceID: W, Container: "zaki"}
	bg         = context.Background()
)

func apply(t *testing.T, d *db.DB, scope search.Scope, refs []model.Memory, r gateway.ExtractResult) pipeline.ApplyStats {
	t.Helper()
	s, err := pipeline.ApplyExtract(bg, d, scope, "", refs, r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDedupeThreshold(t *testing.T) {
	// Cosine similarity of memoryText() vectors, measured with e5-small (q8).
	// Duplicates must clear DedupeSimilarity and distinct facts must stay under it.
	pairs := []struct {
		label     string
		a, b      gateway.AddItem
		duplicate bool
	}{
		{"restated signing date (0.986)", signed, dyn("Contract with Unifrutti was signed on October 1, 2026", "contract signing date timeline kontrak tanggal tanda tangan unifrutti"), true},
		{"paraphrase (0.980)", city, dyn("Resides in Banjarbaru", city.Keywords), true},
		{"punctuation and case (0.992)", contractor, dyn("works as an independent contractor.", contractor.Keywords), true},
		{"same topic, other value (0.970)", dyn("Skilled in TypeScript", "programming language skill typescript bahasa pemrograman keahlian"), dyn("Skilled in Python", "programming language skill python bahasa pemrograman keahlian"), false},
		{"same topic, other brand (0.962)", dyn("Prefers Puma running shoes", "shoes sneakers running footwear sepatu lari puma"), dyn("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas"), false},
		{"contract terms (0.884)", dyn("Paid USD 50 per hour under the Unifrutti contract", "rate pay hourly salary tarif gaji per jam unifrutti"), dyn("Unifrutti contract runs for 12 months", "contract term duration kontrak durasi bulan unifrutti"), false},
	}
	for _, p := range pairs {
		v, err := embed.Embed([]string{memories.Text(p.a.Fact, p.a.Keywords), memories.Text(p.b.Fact, p.b.Keywords)}, embed.Passage)
		if err != nil {
			t.Fatal(err)
		}
		sim := 0.0
		for i := range v[0] {
			sim += float64(v[0][i]) * float64(v[1][i])
		}
		if (sim >= pipeline.DedupeSimilarity) != p.duplicate {
			t.Errorf("%s: %.4f", p.label, sim)
		}
	}
}

func TestDedupeSkipsDuplicatesOfExisting(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{contractor, signed}})
	stats := apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{
		// Exact fact, different keywords (0.975 by vector, caught by the text match).
		dyn("Works as an independent contractor", "employment status independent pekerjaan lepas mandiri"),
		dyn("Contract with Unifrutti was signed on October 1, 2026", "contract signing date timeline kontrak tanggal tanda tangan unifrutti"),
		city,
		dyn("Skilled in TypeScript", "programming language skill typescript bahasa pemrograman keahlian"),
	}})
	eq(t, stats, pipeline.ApplyStats{Added: 2, Deduped: 2})
	eq(t, currentFacts(t, d, "zaki"), []string{"Lives in Banjarbaru", "Signed the Unifrutti contractor agreement on 2026-10-01", "Skilled in TypeScript", "Works as an independent contractor"})
}

func TestDedupeInsideBatchAndAgainstUpdate(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{dyn("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas")}})
	refs := memoriesWhere(t, d, "")
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{
		Add:    []gateway.AddItem{city, dyn("Resides in Banjarbaru", city.Keywords), dyn("Prefers Puma running shoes", "shoes sneakers running footwear sepatu lari puma")},
		Update: []gateway.UpdateItem{{Ref: 1, Fact: "Prefers Puma running shoes", Keywords: "shoes sneakers running footwear sepatu lari puma"}},
	})
	eq(t, stats, pipeline.ApplyStats{Added: 1, Updated: 1, Deduped: 2})
	eq(t, currentFacts(t, d, "zaki"), []string{"Lives in Banjarbaru", "Prefers Puma running shoes"})
}

func TestDedupeKeepsNumbersAndRetiredMatches(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{signed, contractor}})
	refs := memoriesWhere(t, d, "")
	contractorRef := 0
	for i, m := range refs {
		if m.Fact == contractor.Fact {
			contractorRef = i + 1
		}
	}
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{
		Add:        []gateway.AddItem{dyn("Signed the Unifrutti contractor agreement on 2026-11-15", signed.Keywords), contractor},
		Invalidate: []int{contractorRef},
	})
	eq(t, stats, pipeline.ApplyStats{Added: 2, Invalidated: 1})
	eq(t, len(currentFacts(t, d, "zaki")), 3)
}

func TestDedupeAddRestatingConflict(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{city}})
	refs := memoriesWhere(t, d, "")
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{
		Add:      []gateway.AddItem{dyn("Lives in Martapura", city.Keywords)},
		Conflict: []gateway.ConflictItem{{Ref: 1, Fact: "Lives in Martapura", Keywords: city.Keywords, Note: "Letter says Martapura"}},
	})
	eq(t, stats, pipeline.ApplyStats{Deduped: 1, Conflicts: 1})
	eq(t, currentFacts(t, d, "zaki"), []string{"Lives in Banjarbaru", "Lives in Martapura"})
	for _, m := range memoriesWhere(t, d, "") {
		if m.ReviewNote == nil || *m.ReviewNote == "" {
			t.Fatalf("no note on %s", m.Fact)
		}
	}
}

func TestDedupeNeverAcrossContainers(t *testing.T) {
	d := openDB(t)
	apply(t, d, search.Scope{WorkspaceID: W, Container: "a"}, nil, gateway.ExtractResult{Add: []gateway.AddItem{contractor}})
	stats := apply(t, d, search.Scope{WorkspaceID: W, Container: "b"}, nil, gateway.ExtractResult{Add: []gateway.AddItem{contractor}})
	eq(t, stats.Deduped, 0)
	eq(t, currentFacts(t, d, "a"), []string{contractor.Fact})
	eq(t, currentFacts(t, d, "b"), []string{contractor.Fact})
}

func TestDedupeRecordedInStats(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{
		`{"add":[{"fact":"Works as an independent contractor","keywords":"contractor freelance job kerja kontraktor","kind":"dynamic"}]}`,
		`{"add":[{"fact":"works as an independent contractor.","keywords":"contractor freelance job kerja kontraktor","kind":"dynamic"},{"fact":"Works as a contractor for Unifrutti","keywords":"contractor unifrutti client job kerja kontraktor klien","kind":"dynamic"}]}`,
	}}
	r := newRunner(d, s.deps(date("2026-11-09")))
	addText(t, d, "zaki", "I work as an independent contractor.")
	drain(t, r)
	res := addText(t, d, "zaki", "Independent contractor agreement with Unifrutti.")
	drain(t, r)
	expectStats(t, extractStats(t, docByID(t, d, res.ID)), map[string]float64{"added": 1, "updated": 0, "invalidated": 0, "deduped": 1})
}

// --- stale refs ---------------------------------------------------------------------

func TestStaleUpdateAfterForget(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{city}})
	refs := memoriesWhere(t, d, "")
	// The user forgets the fact while the extract call is out at the gateway.
	if _, _, err := memories.ForgetMemory(d, W, refs[0].ID, nowFn()); err != nil {
		t.Fatal(err)
	}
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{Update: []gateway.UpdateItem{{Ref: 1, Fact: "Lives in Martapura", Keywords: city.Keywords}}})
	eq(t, []int{stats.Updated, stats.Stale}, []int{0, 1})
	eq(t, len(currentFacts(t, d, "zaki")), 0)
	eq(t, len(memoriesWhere(t, d, "")), 1)
}

func TestNeverTwoCurrentSuccessors(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{city}})
	refs := memoriesWhere(t, d, "")
	update := func(f string) pipeline.ApplyStats {
		return apply(t, d, zaki, refs, gateway.ExtractResult{Update: []gateway.UpdateItem{{Ref: 1, Fact: f, Keywords: city.Keywords}}})
	}
	// Two extracts read the same refs, then both write.
	var a, b pipeline.ApplyStats
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a = update("Lives in Martapura") }()
	go func() { defer wg.Done(); b = update("Lives in Banjarmasin") }()
	wg.Wait()
	eq(t, a.Updated+b.Updated, 1)
	eq(t, a.Stale+b.Stale, 1)
	eq(t, len(currentFacts(t, d, "zaki")), 1)
	eq(t, len(memoriesWhere(t, d, "WHERE supersedes_id = ?", refs[0].ID)), 1)
}

// whileEmbedding runs fn while the next editMemory or addMemoryDirect vector
// is computed (the await in the TS functions).
func whileEmbedding(t *testing.T, fn func()) {
	orig := memories.EmbedPassage
	t.Cleanup(func() { memories.EmbedPassage = orig })
	memories.EmbedPassage = func(text string) ([]float32, error) {
		memories.EmbedPassage = orig
		fn()
		return orig(text)
	}
}

func addCity(t *testing.T, d *db.DB) model.Memory {
	m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: W, Container: "zaki", Fact: city.Fact, Keywords: city.Keywords, Kind: city.Kind}, nowFn())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEditMemoryForgottenMidEdit(t *testing.T) {
	d := openDB(t)
	m := addCity(t, d)
	whileEmbedding(t, func() {
		if _, _, err := memories.ForgetMemory(d, W, m.ID, nowFn()); err != nil {
			t.Error(err)
		}
	})
	_, ok, err := memories.EditMemory(d, W, m.ID, memories.Patch{Fact: str("Lives in Martapura")}, nowFn)
	if err != nil || ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	eq(t, len(currentFacts(t, d, "zaki")), 0)
}

func TestPatchMemoryRace(t *testing.T) {
	d := openDB(t)
	m := addCity(t, d)
	whileEmbedding(t, func() {
		if _, _, err := memories.ForgetMemory(d, W, m.ID, nowFn()); err != nil {
			t.Error(err)
		}
	})
	_, err := api.PatchMemory(d, W, m.ID, api.PatchInput{Fact: str("Lives in Martapura")}, nowFn)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 409 || he.Type != "not_current" {
		t.Fatalf("err %v", err)
	}
}

// --- invalidation cap ---------------------------------------------------------------

func TestInvalidationCap(t *testing.T) {
	d := openDB(t)
	var adds []gateway.AddItem
	for _, f := range []string{"Owns a cat", "Drinks oat milk", "Runs on Sundays", "Plays chess", "Reads sci-fi", "Uses a Kindle", "Grows chillies", "Bikes to work"} {
		adds = append(adds, dyn(f, ""))
	}
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: adds})
	refs := memoriesWhere(t, d, "")
	eq(t, len(refs), 8)
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{Invalidate: []int{1, 2, 3, 4, 5, 6, 7, 8}})
	eq(t, []int{stats.Invalidated, stats.CappedInvalidations}, []int{pipeline.MaxInvalidations, 8 - pipeline.MaxInvalidations})
	eq(t, len(currentFacts(t, d, "zaki")), 8-pipeline.MaxInvalidations)
}

// --- replays and repeated refs ------------------------------------------------------

var cityConflict = gateway.ConflictItem{Ref: 1, Fact: "Lives in Martapura", Keywords: city.Keywords, Note: "Letter says Martapura"}

func TestReplayedConflictWrittenOnce(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{city}})
	refs := memoriesWhere(t, d, "")
	first := apply(t, d, zaki, refs, gateway.ExtractResult{Conflict: []gateway.ConflictItem{cityConflict}})
	eq(t, []int{first.Conflicts, first.Deduped}, []int{1, 0})
	// A retry of the same window sees the same refs and gets the same answer.
	again := apply(t, d, zaki, refs, gateway.ExtractResult{Conflict: []gateway.ConflictItem{cityConflict}})
	eq(t, []int{again.Conflicts, again.Deduped}, []int{0, 1})
	eq(t, currentFacts(t, d, "zaki"), []string{"Lives in Banjarbaru", "Lives in Martapura"})
	original := memoriesWhere(t, d, "WHERE fact = ?", city.Fact)[0]
	eq(t, len(strings.Split(*original.ReviewNote, "\n")), 1)
}

func TestConflictSameTextIsNoop(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{city}})
	refs := memoriesWhere(t, d, "")
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{Conflict: []gateway.ConflictItem{{Ref: 1, Fact: "lives in  Banjarbaru.", Keywords: city.Keywords, Note: "Same thing"}}})
	eq(t, []int{stats.Conflicts, stats.Deduped}, []int{0, 1})
	eq(t, currentFacts(t, d, "zaki"), []string{city.Fact})
	all := memoriesWhere(t, d, "")
	if len(all) != 1 || all[0].ReviewNote != nil || all[0].ConflictsWithID != nil {
		t.Fatalf("rows %+v", all)
	}
}

func TestRepeatedUpdateRefIgnored(t *testing.T) {
	d := openDB(t)
	apply(t, d, zaki, nil, gateway.ExtractResult{Add: []gateway.AddItem{dyn("Prefers Adidas running shoes", "shoes sneakers running footwear sepatu lari adidas")}})
	refs := memoriesWhere(t, d, "")
	nike := dyn("Prefers Nike basketball shoes", "shoes sneakers basketball footwear sepatu basket nike")
	stats := apply(t, d, zaki, refs, gateway.ExtractResult{
		Add: []gateway.AddItem{nike},
		Update: []gateway.UpdateItem{
			{Ref: 1, Fact: "Prefers Puma running shoes", Keywords: "shoes sneakers running footwear sepatu lari puma"},
			// Never written (the first update for ref 1 wins), so it must not dedupe the add.
			{Ref: 1, Fact: nike.Fact, Keywords: nike.Keywords},
		},
	})
	eq(t, []int{stats.Added, stats.Updated, stats.Deduped}, []int{1, 1, 0})
	eq(t, currentFacts(t, d, "zaki"), []string{"Prefers Nike basketball shoes", "Prefers Puma running shoes"})
}

// --- document dates -----------------------------------------------------------------

func TestEmailDate(t *testing.T) {
	eq(t, pipeline.EmailDate("Subject: Hi\nFrom: a@b.c\nDate: Tue, 3 May 2016 23:30:00 -0700\n\nBody"), "2016-05-03")
	eq(t, pipeline.EmailDate("Date: 7 Jan 2021 08:00:00 +0700"), "2021-01-07")
	eq(t, pipeline.EmailDate("Subject: no date\n\nBody"), "")
	eq(t, pipeline.EmailDate("Date: 31 Feb 2021"), "")
}

func TestDocumentDate(t *testing.T) {
	createdAt := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	content := "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPlease pay."
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{SourceType: "email", Content: &content, Metadata: jsonx.NewMap[any](), CreatedAt: createdAt}), "2021-08-01")
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{SourceType: "email", Content: &content, Metadata: jsonx.ObjectOf("documentDate", "2021-07-30"), CreatedAt: createdAt}), "2021-07-30")
	// Only emails: a pasted note quoting a Date line is still dated by when it was added.
	eq(t, pipeline.DocumentDate(pipeline.DocInfo{SourceType: "text", Content: &content, Metadata: jsonx.NewMap[any](), CreatedAt: createdAt}), "2026-11-10")
}

func TestTellsModelHowOldEmailIs(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[]}`}}
	r := newRunner(d, s.deps(date("2026-11-10")))
	res := addText(t, d, "c", "Please pay the hosting invoice by Friday.")
	// Email ingest only goes to Gmail when the text isn't stored yet, so store it as Gmail would give it.
	email := "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPlease pay the hosting invoice by Friday."
	if _, err := d.Exec(`UPDATE documents SET source_type = 'email', content = ? WHERE id = ?`, email, res.ID); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	if !regexp.MustCompile(`DOCUMENT DATE: 2021-08-01 `).MatchString(s.prompts[0]) ||
		!strings.Contains(s.prompts[0], `This document is about 5 years old: follow the "Old content" rules.`) {
		t.Fatalf("prompt:\n%s", s.prompts[0])
	}
}

// --- coding sessions ----------------------------------------------------------------

func TestCodingSession(t *testing.T) {
	d := openDB(t)
	s := &scripted{replies: []string{`{"add":[]}`, `{"add":[]}`}}
	r := newRunner(d, s.deps(date("2026-11-10")))
	addChat(t, d, "c", "Open a draft PR for the r4 branch.", str("claude-code:abc123"))
	drain(t, r)
	addChat(t, d, "c", "I prefer pnpm over npm.", nil)
	drain(t, r)
	var extracts []string
	for _, p := range s.prompts {
		if strings.Contains(p, "NEW CONTENT") {
			extracts = append(extracts, p)
		}
	}
	if !strings.Contains(extracts[0], `SOURCE: a coding-agent session. Follow the "Coding sessions" rules.`) {
		t.Fatalf("first:\n%s", extracts[0])
	}
	if strings.Contains(extracts[1], "SOURCE:") {
		t.Fatalf("second:\n%s", extracts[1])
	}
}

// --- profile ------------------------------------------------------------------------

func fill(v float32) []float32 {
	out := make([]float32, embed.Dims)
	for i := range out {
		out[i] = v
	}
	return out
}

func profileDeps() (*scripted, pipeline.Deps) {
	s := &scripted{reply: func(string, string) string { return `{"static":"s","dynamic":"d"}` }}
	return s, s.deps(date("2026-11-10T12:00:00Z"))
}

func TestFactAsOf(t *testing.T) {
	eq(t, pipeline.FactAsOf(nil, nil, "2016-05-03", "2026-11-10"), "2016-05-03")
	eq(t, pipeline.FactAsOf(str("2016-01-01"), nil, "2026-11-02", "2026-11-10"), "2026-11-02")
	eq(t, pipeline.FactAsOf(nil, str("2021-08-01"), "2021-07-20", "2026-11-10"), "2021-08-01")
	eq(t, pipeline.FactAsOf(str("2026-11-05"), str("2028-11-05"), "2026-11-05", "2026-11-10"), "2026-11-10")
}

func TestProfileBudgets(t *testing.T) {
	d := openDB(t)
	vector := fill(0.05)
	insert := func(f, kind string) {
		if _, err := memories.InsertMemory(d, memories.NewMemory{WorkspaceID: W, Container: "c", Fact: f, Kind: kind}, vector); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < pipeline.ProfileDynamicFacts+30; i++ {
		insert("Dynamic "+itoa(i), "dynamic")
	}
	for i := 0; i < pipeline.ProfileStaticFacts+5; i++ {
		insert("Static "+itoa(i), "static")
	}
	s, deps := profileDeps()
	if err := pipeline.BuildProfile(bg, d, W, "c", deps); err != nil {
		t.Fatal(err)
	}
	p := s.prompts[0]
	eq(t, len(regexp.MustCompile(`(?m)^- \[static\]`).FindAllString(p, -1)), pipeline.ProfileStaticFacts)
	eq(t, len(regexp.MustCompile(`(?m)^- \[dynamic, as of `).FindAllString(p, -1)), pipeline.ProfileDynamicFacts)
	if !strings.HasPrefix(p, "Today is 2026-11-10.") {
		t.Fatalf("prompt starts %q", p[:40])
	}
}

func itoa(i int) string { return jsonx.NumberString(float64(i)) }

func TestProfileRanksByWhenTrue(t *testing.T) {
	d := openDB(t)
	day := func(iso string) *dates.Dated { return &dates.Dated{Date: iso, Precision: dates.Day} }
	created := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, status, content, created_at) VALUES ('doc_old_email', ?, 'c', 'email', 'done', ?, ?)`,
		W, "Subject: Invoice\nDate: Sun, 1 Aug 2021 10:00:00 +0800\n\nPay by Friday.", created); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, status, metadata, created_at) VALUES ('doc_dated', ?, 'c', 'text', 'done', ?, ?)`,
		W, `{"documentDate":"2026-10-01"}`, created); err != nil {
		t.Fatal(err)
	}
	vector := fill(0.05)
	insert := func(f string, in memories.NewMemory, createdAt time.Time) {
		in.WorkspaceID, in.Container, in.Fact, in.Kind = W, "c", f, "dynamic"
		row, err := memories.InsertMemory(d, in, vector)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`UPDATE memories SET created_at = ? WHERE id = ?`, createdAt.UnixMilli(), row.ID); err != nil {
			t.Fatal(err)
		}
	}
	utc := func(y, m, dd int) time.Time { return time.Date(y, time.Month(m), dd, 0, 0, 0, 0, time.UTC) }
	// Stored last, but from a 2021 email.
	insert("Hosting invoice unpaid", memories.NewMemory{SourceDocumentID: str("doc_old_email")}, utc(2026, 11, 10))
	insert("Training for a marathon", memories.NewMemory{SourceDocumentID: str("doc_dated")}, utc(2026, 11, 1))
	insert("Contract with Vorneo runs to 2028", memories.NewMemory{Event: day("2026-11-05"), Due: day("2028-11-05")}, utc(2026, 10, 1))
	insert("Added by hand", memories.NewMemory{}, utc(2026, 11, 3))
	s, deps := profileDeps()
	if err := pipeline.BuildProfile(bg, d, W, "c", deps); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(s.prompts[0], "\n") {
		if strings.HasPrefix(l, "- ") {
			lines = append(lines, l)
		}
	}
	eq(t, strings.Join(lines, "\n"), strings.Join([]string{
		"- [dynamic, as of 2026-11-10] Contract with Vorneo runs to 2028",
		"- [dynamic, as of 2026-11-03] Added by hand",
		"- [dynamic, as of 2026-10-01] Training for a marathon",
		"- [dynamic, as of 2021-08-01] Hosting invoice unpaid",
	}, "\n"))
}

// --- long version chains ------------------------------------------------------------

func TestLongVersionChains(t *testing.T) {
	d := openDB(t)
	vector := fill(0.05)
	row, err := memories.InsertMemory(d, memories.NewMemory{WorkspaceID: W, Container: "c", Fact: "Working on day 1", Kind: "dynamic"}, vector)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 130; i++ {
		next, ok, err := memories.SupersedeMemory(d, row, memories.Next{Fact: "Working on day " + itoa(i)}, vector, nowFn())
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		row = next
	}
	counts, err := memories.VersionCounts(d, W, []string{row.ID})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := counts.Get(row.ID)
	eq(t, n, float64(129))
	chain, err := memories.Chain(d, W, row.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(chain), 130)
	eq(t, chain[0].Fact, "Working on day 1")
	// A shorter walk still knows where it starts.
	window, err := memories.Chain(d, W, row.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, window[0].Fact, "Working on day 120")
	counts, _ = memories.VersionCounts(d, W, []string{window[0].ID})
	n, _ = counts.Get(window[0].ID)
	eq(t, n, float64(119))
}
