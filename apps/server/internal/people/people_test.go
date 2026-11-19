package people_test

// people.test.ts. The CLI's backfill-people, the ask case and the REST and
// search halves of "tenant isolation" are ported with admin-cli, ask and
// the routes (M5).

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

const (
	W = "ws_people"
	C = "zaki"
)

var scope = search.Scope{WorkspaceID: W, Container: C}

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %#v\nwant %#v", got, want)
	}
}

func s(v string) *string { return &v }

type extra struct {
	aliases                                    []string
	role, organization, email, handle, summary *string
}

func mention(name string, e extra) gateway.PersonMention {
	m := gateway.PersonMention{Name: name, Aliases: e.aliases, Role: e.role, Organization: e.organization, Email: e.email, Handle: e.handle, Summary: e.summary, Facts: []string{}}
	if m.Aliases == nil {
		m.Aliases = []string{}
	}
	return m
}

func fact(t *testing.T, d *db.DB, text string, ws, container string) model.Memory {
	t.Helper()
	m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: ws, Container: container, Fact: text, Kind: "dynamic"}, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func f(t *testing.T, d *db.DB, text string) model.Memory { return fact(t, d, text, W, C) }

func link(t *testing.T, d *db.DB, sc search.Scope, list ...people.Mention) people.LinkStats {
	t.Helper()
	st, err := people.LinkMentions(d, sc, list)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func one(m gateway.PersonMention, ids ...string) people.Mention {
	return people.Mention{Mention: m, MemoryIDs: ids}
}

func list(t *testing.T, d *db.DB, o people.ListOptions) people.ListResult {
	t.Helper()
	if o.WorkspaceID == "" {
		o.WorkspaceID, o.Container = W, C
	}
	r, err := people.List(d, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func names(rows []people.Row) []string {
	out := []string{}
	for _, p := range rows {
		out = append(out, p.Name)
	}
	return out
}

func nameCounts(rows []people.Row) [][2]any {
	out := [][2]any{}
	for _, p := range rows {
		out = append(out, [2]any{p.Name, p.FactCount})
	}
	return out
}

func drain(t *testing.T, r *jobs.Runner) {
	t.Helper()
	for i := 0; i < 50; i++ {
		j, err := r.RunOnce(time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			return
		}
	}
}

// --- extract people block ------------------------------------------------------

func TestExtractLinksPeople(t *testing.T) {
	d := testkit.OpenDB(t)
	g := &testkit.Gateway{Replies: []string{
		`{"add":[{"fact":"Works with Clara Simmons on the Vorneo listing redesign","keywords":"design vorneo","kind":"dynamic"},{"fact":"Prefers Puma running shoes","keywords":"shoes","kind":"dynamic"}],
		  "people":[{"name":"Clara Simmons","aliases":["Clara"],"role":"Product designer","organization":"Vorneo","handle":"@clara","facts":["a1"]},{"name":"Zaki","facts":["a1","a2"]},{"name":"Ghost","facts":["a9"]}]}`,
		`{"update":[{"ref":1,"fact":"Works with Clara on the Vorneo listing redesign and the mobile app","keywords":"design"}],"people":[{"name":"clara","facts":["u1"]}]}`,
	}}
	r := jobs.NewRunner(d, pipeline.Handlers(d, pipeline.Deps{Do: g.Do, Now: func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }}), func(string) {})
	content := "Clara Simmons from Vorneo and I are redesigning the listing page. I run in Puma."
	if _, err := api.AddContent(d, W, api.AddInput{Container: C, Content: &content}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	if !strings.Contains(g.Systems[0], `"people"`) || !strings.Contains(g.Systems[0], `memory named "zaki"`) {
		t.Fatal("extract system has no people rules")
	}
	l := list(t, d, people.ListOptions{})
	eq(t, names(l.People), []string{"Clara Simmons"})
	clara := l.People[0]
	if *clara.Role != "Product designer" || *clara.Organization != "Vorneo" || !reflect.DeepEqual(clara.Handles, []string{"@clara"}) || !reflect.DeepEqual(clara.Aliases, []string{"Clara"}) || clara.FactCount != 1 {
		t.Fatalf("%+v", clara)
	}
	// A later update names her by first name only: same person, linked to the new version.
	content = "Clara and I also took on the mobile app."
	api.AddContent(d, W, api.AddInput{Container: C, Content: &content}, time.Now().UnixMilli())
	drain(t, r)
	eq(t, list(t, d, people.ListOptions{}).Total, 1)
	page, _, _ := people.Get(d, W, clara.ID, false)
	eq(t, len(page.Facts), 1)
	eq(t, page.Facts[0].Fact, "Works with Clara on the Vorneo listing redesign and the mobile app")
	hist, _, _ := people.Get(d, W, clara.ID, true)
	eq(t, len(hist.Facts), 2)
}

// --- resolver --------------------------------------------------------------------

func TestMatchesExactNamesAliasesEmailsHandles(t *testing.T) {
	d := testkit.OpenDB(t)
	a, b, c, dd := f(t, d, "Budi handles payments"), f(t, d, "Budi sent the invoice"), f(t, d, "Renée reviewed the contract"), f(t, d, "Pinged @budi.s on Slack")
	link(t, d, scope, one(mention("Budi Santoso", extra{aliases: []string{"Budi"}, email: s("budi@kebunsawit.id")}), a.ID))
	link(t, d, scope,
		one(mention("BUDI SANTOSO", extra{}), b.ID),
		one(mention("Pak Budi", extra{email: s("Budi@KebunSawit.id"), handle: s("@budi.s")}), dd.ID),
		one(mention("Renee", extra{}), c.ID),
		one(mention("renée", extra{}), c.ID))
	ps := list(t, d, people.ListOptions{Sort: "name"}).People
	eq(t, nameCounts(ps), [][2]any{{"Budi Santoso", int64(3)}, {"Renee", int64(1)}})
	eq(t, ps[0].Handles, []string{"@budi.s"})
	if !contains(ps[0].Aliases, "Pak Budi") {
		t.Fatal(ps[0].Aliases)
	}
}

func contains(l []string, x string) bool {
	for _, v := range l {
		if v == x {
			return true
		}
	}
	return false
}

func TestFuzzyMatchNeedsEmbedding(t *testing.T) {
	eq(t, people.NamesLookAlike("Clara", "Clara Simmons"), true)
	eq(t, people.NamesLookAlike("Clara S.", "Clara Simmons"), true)
	eq(t, people.NamesLookAlike("Budi Santosa", "Budi Santoso"), true)
	eq(t, people.NamesLookAlike("Clara Wong", "Clara Simmons"), false)
	d := testkit.OpenDB(t)
	work := f(t, d, "Works with Clara Simmons, a product designer at Vorneo, on the listing page redesign")
	link(t, d, scope, one(mention("Clara Simmons", extra{role: s("Product designer"), organization: s("Vorneo"), summary: s("Product designer at Vorneo")}), work.ID))
	// Same Clara: a designer reviewing mockups.
	mockups := f(t, d, "Clara recommended switching the Vorneo listing cards to a two-column grid")
	link(t, d, scope, one(mention("Clara", extra{}), mockups.ID))
	// A different Clara: the subject's sister.
	sister := f(t, d, "Clara is his younger sister and studies medicine in Surabaya")
	link(t, d, scope, one(mention("Clara", extra{role: s("Sister"), summary: s("Younger sister studying medicine")}), sister.ID))
	eq(t, nameCounts(list(t, d, people.ListOptions{Sort: "name"}).People), [][2]any{{"Clara", int64(1)}, {"Clara Simmons", int64(2)}})
	byMem, _ := people.ForMemories(d, W, []string{sister.ID})
	refs, _ := byMem.Get(sister.ID)
	eq(t, len(refs), 1)
	eq(t, refs[0].Name, "Clara")
}

func TestKeepsNumberedNamesAndOtherEmailsApart(t *testing.T) {
	eq(t, people.NamesLookAlike("Contractor 1", "Contractor 2"), false)
	d := testkit.OpenDB(t)
	for _, i := range []string{"1", "2"} {
		m := f(t, d, "Contractor "+i+" delivered the audit")
		link(t, d, scope, one(mention("Contractor "+i, extra{}), m.ID))
	}
	x, y := f(t, d, "Clara sent the invoice"), f(t, d, "Clara sent the invoice again")
	link(t, d, scope, one(mention("Clara Simmons", extra{email: s("clara@vorneo.id")}), x.ID))
	link(t, d, scope, one(mention("Clara", extra{email: s("clara@gmail.com")}), y.ID))
	eq(t, names(list(t, d, people.ListOptions{Sort: "name"}).People), []string{"Clara", "Clara Simmons", "Contractor 1", "Contractor 2"})
}

// Pairs the resolver split in production (Gmail import, 2026-11-10).
func TestFoldsLaterMentionUnderAnotherEmail(t *testing.T) {
	pairs := []struct {
		first      string
		firstExtra extra
		firstFact  string
		second     string
		secondEx   extra
		secondFact string
		kept       string
	}{
		{"Rizky Kurniawan", extra{email: s("rizky.kurniawan@example.com"), role: s("Data analyst"), organization: s("WargaLapor"), summary: s("Data analyst at WargaLapor who sends the weekly case recaps")},
			"Rizky Kurniawan sent the weekly COVID-19 case recap for Jakarta to the WargaLapor team", "Rizky Kurn", extra{email: s("rizky@wargalapor.example"), organization: s("WargaLapor")},
			"Rizky Kurn sent the WargaLapor case recap for Jakarta and asked for corrections", "Rizky Kurniawan"},
		{"Sekar Ayu Lestari", extra{email: s("sekar.lestari@example.com"), role: s("Coordinator"), organization: s("WargaLapor"), summary: s("Coordinates citizen reports at WargaLapor")},
			"Sekar Ayu Lestari asked for the March report on vaccine complaints from WargaLapor", "Sekar Ayu Lestarii", extra{email: s("sekar@wargalapor.example"), organization: s("WargaLapor")},
			"Sekar Ayu Lestarii forwarded citizen complaints about vaccines to the WargaLapor team", "Sekar Ayu Lestari"},
		{"Maya Lee Hartono", extra{email: s("maya@foundation.example"), role: s("Program officer"), organization: s("Open Data Foundation"), summary: s("Program officer handling the grant")},
			"Maya Lee Hartono sent the grant agreement for signature", "Maya Hartono", extra{email: s("maya.hartono@example.com"), role: s("Program officer")},
			"Maya Hartono scheduled a call to review the quarterly grant report", "Maya Lee Hartono"},
		{"Dewi Ratna", extra{email: s("dewi@kantor.example"), role: s("Finance staff"), summary: s("Handles reimbursements and honorarium payments")},
			"Dewi Ratna approved the travel reimbursement for the Surabaya trip", "Dewi Ratna Puspita S", extra{email: s("dewi.ratna@example.com"), role: s("Finance staff")},
			"Dewi Ratna Puspita S sent the payment slip for the October honorarium", "Dewi Ratna Puspita S"},
		{"Bayu Setiawan", extra{email: s("adi@vorneo.id"), role: s("Backend engineer"), organization: s("Vorneo"), summary: s("Backend engineer at Vorneo")},
			"Bayu Setiawan reviewed the pull request for the Vorneo listing API", "Bayu Settiawan", extra{email: s("bayu.settiawan@example.com"), organization: s("Vorneo")},
			"Bayu Settiawan merged the Vorneo listing API pull request", "Bayu Setiawan"},
	}
	for _, p := range pairs {
		t.Run(p.first, func(t *testing.T) {
			d := testkit.OpenDB(t)
			a := f(t, d, p.firstFact)
			link(t, d, scope, one(mention(p.first, p.firstExtra), a.ID))
			b := f(t, d, p.secondFact)
			link(t, d, scope, one(mention(p.second, p.secondEx), b.ID))
			ps := list(t, d, people.ListOptions{}).People
			eq(t, nameCounts(ps), [][2]any{{p.kept, int64(2)}})
			eq(t, ps[0].Emails, []string{*p.firstExtra.email, *p.secondEx.email})
		})
	}
}

func TestSharedFirstNameStaysApart(t *testing.T) {
	eq(t, people.NamesLookAlike("Rizky Kurn", "Rizky Kurniawan"), true)
	eq(t, people.NamesLookAlike("Ryan Boyd", "Ryan Castriotta"), false)
	eq(t, people.NamesLookAlike("Muhammad Rizki", "Muhammad Fauzan"), false)
	d := testkit.OpenDB(t)
	for _, pair := range [][2]string{{"Clara Simmons", "Clara Wong"}, {"Ryan Boyd", "Ryan Castriotta"}, {"Muhammad Rizki", "Muhammad Fauzan"}} {
		a := f(t, d, pair[0]+" sent the Vorneo deployment checklist")
		link(t, d, scope, one(mention(pair[0], extra{organization: s("Vorneo")}), a.ID))
		b := f(t, d, pair[1]+" sent the Vorneo deployment checklist")
		link(t, d, scope, one(mention(pair[1], extra{organization: s("Vorneo")}), b.ID))
	}
	eq(t, list(t, d, people.ListOptions{}).Total, 6)
}

func TestTeamMailboxes(t *testing.T) {
	for _, e := range []string{"sekretwargalapor@example.com", "info@vorneo.id", "noreply@github.com", "support+billing@acme.com", "hr@kebunsawit.id", "admin.vorneo@example.com"} {
		if !people.IsSharedMailbox(e) {
			t.Errorf("%s is shared", e)
		}
	}
	for _, e := range []string{"clara@vorneo.id", "csaba@example.com", "helena@example.com", "information.zaki@example.com", "hrafn@example.com", "tim@vorneo.id"} {
		if people.IsSharedMailbox(e) {
			t.Errorf("%s is personal", e)
		}
	}
	eq(t, people.LooksLikeOrganization("Sekret WargaLapor", s("sekretwargalapor@example.com")), true)
	eq(t, people.LooksLikeOrganization("PT Vorneo Digital", nil), true)
	eq(t, people.LooksLikeOrganization("Vorneo Support Team", nil), true)
	eq(t, people.LooksLikeOrganization("Yayasan Kebun Sawit", nil), true)
	for _, n := range []string{"Clara Simmons", "Contractor 1", "Tim Cook", "Ana Sales", "Mickey Kantor"} {
		if people.LooksLikeOrganization(n, nil) {
			t.Errorf("%s is a person", n)
		}
	}
	eq(t, people.LooksLikeOrganization("Sekar Ayu Lestari", s("sekretwargalapor@example.com")), false)

	// Sekar and Rizky both write from the team's shared mailbox.
	d := testkit.OpenDB(t)
	shared := "sekretwargalapor@example.com"
	a := f(t, d, "Sekret WargaLapor sent the weekly volunteer newsletter")
	b := f(t, d, "Sekar Ayu Lestari asked for the March report on vaccine complaints")
	c := f(t, d, "Rizky Kurniawan sent the weekly COVID-19 case recap")
	st := link(t, d, scope,
		one(mention("Sekret WargaLapor", extra{email: &shared}), a.ID),
		one(mention("Sekar Ayu Lestari", extra{email: &shared, role: s("Coordinator")}), b.ID),
		one(mention("Rizky Kurniawan", extra{email: &shared, role: s("Data analyst")}), c.ID))
	eq(t, []int{st.Created, st.Skipped}, []int{2, 1})
	ps := list(t, d, people.ListOptions{Sort: "name"}).People
	for i, want := range []string{"Rizky Kurniawan", "Sekar Ayu Lestari"} {
		if ps[i].Name != want || len(ps[i].Emails) != 0 || len(ps[i].Aliases) != 0 {
			t.Fatalf("%+v", ps[i])
		}
	}
	// A person saved before this rule may still carry the mailbox: it no longer pulls others in.
	if _, _, err := people.Update(d, W, ps[0].ID, people.Patch{Emails: &[]string{shared}}); err != nil {
		t.Fatal(err)
	}
	dd := f(t, d, "Clara Simmons replied from the WargaLapor inbox")
	link(t, d, scope, one(mention("Clara Simmons", extra{email: &shared}), dd.ID))
	eq(t, list(t, d, people.ListOptions{}).Total, 3)
}

func TestNeverCreatesTheSubject(t *testing.T) {
	owner := func(c string, m gateway.PersonMention) bool { return people.IsContainerOwner(c, m, nil) }
	eq(t, owner("zaki", mention("Zaki", extra{})), true)
	eq(t, owner("zaki", mention("Zaki Munziri", extra{})), true)
	eq(t, owner("zaki-work", mention("zaki", extra{})), true)
	eq(t, owner("zaki", mention("Muhammad", extra{aliases: []string{"Zaki"}})), true)
	eq(t, owner("zaki", mention("Someone", extra{email: s("zaki@example.com")})), true)
	eq(t, owner("zaki", mention("the user", extra{})), true)
	eq(t, owner("zaki", mention("Clara Simmons", extra{})), false)
	eq(t, owner("vorneo", mention("Zaki", extra{})), false)
	d := testkit.OpenDB(t)
	m := f(t, d, "Zaki lives in Banjarbaru")
	st := link(t, d, scope, one(mention("Zaki", extra{}), m.ID), one(mention("me", extra{}), m.ID))
	eq(t, st, people.LinkStats{Skipped: 2})
	// In someone else's container, Zaki is a contact like anyone else.
	other := fact(t, d, "Zaki reviews the Vorneo roadmap", W, "vorneo")
	link(t, d, search.Scope{WorkspaceID: W, Container: "vorneo"}, one(mention("Zaki", extra{}), other.ID))
	eq(t, names(list(t, d, people.ListOptions{WorkspaceID: W, Container: "vorneo"}).People), []string{"Zaki"})
}

func TestSearchIgnoresCaseAndAccents(t *testing.T) {
	d := testkit.OpenDB(t)
	m := f(t, d, "Élodie Durand translates the French site")
	link(t, d, scope, one(mention("Élodie Durand", extra{organization: s("Société Générale")}), m.ID))
	for _, q := range []string{"elodie", "ÉLODIE", "societe", "durand"} {
		eq(t, list(t, d, people.ListOptions{Q: q}).Total, 1)
	}
	eq(t, list(t, d, people.ListOptions{Q: "nobody"}).Total, 0)
}

// The TS test creates the owner with the CLI's create-user; here the rows
// are written the way that command writes them.
func TestDefaultContainerOwnerIsTheSubject(t *testing.T) {
	d := testkit.OpenDB(t)
	now := time.Now().UnixMilli()
	ws := "ws_zaki000000000000"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user (id, name, email, username, display_username, created_at, updated_at) VALUES ('usr_zaki', 'Zaki Munziri', 'zaki@recall.local', 'zaki', 'zaki', ?, ?)`, []any{now, now}},
		{`INSERT INTO organization (id, name, slug, created_at) VALUES (?, 'zaki', 'zaki', ?)`, []any{ws, now}},
		{`INSERT INTO member (id, organization_id, user_id, role, created_at) VALUES ('mbr_zaki', ?, 'usr_zaki', 'owner', ?)`, []any{ws, now}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	m := fact(t, d, "Zaki Munziri met Clara", ws, "default")
	st := link(t, d, search.Scope{WorkspaceID: ws, Container: "default"}, one(mention("Zaki Munziri", extra{}), m.ID), one(mention("Clara", extra{}), m.ID))
	eq(t, []int{st.Created, st.Skipped}, []int{1, 1})
	eq(t, names(list(t, d, people.ListOptions{WorkspaceID: ws, Container: "default"}).People), []string{"Clara"})
}

func TestMergeKeepsLinksAliasesContacts(t *testing.T) {
	d := testkit.OpenDB(t)
	a, b, c := f(t, d, "Ms Simmons sent the mockups"), f(t, d, "Clara Simmons leads the redesign"), f(t, d, "Ms Simmons and Clara Simmons met")
	// Names that don't look alike stay two people until someone merges them.
	link(t, d, scope, one(mention("Ms Simmons", extra{email: s("clara@gmail.com")}), a.ID, c.ID))
	link(t, d, scope, one(mention("Clara Simmons", extra{role: s("Designer"), organization: s("Vorneo")}), b.ID, c.ID))
	rows := list(t, d, people.ListOptions{Sort: "name"}).People
	eq(t, names(rows), []string{"Clara Simmons", "Ms Simmons"})
	full, short := rows[0], rows[1]
	kept, err := people.Merge(d, W, full.ID, short.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "Clara Simmons" || *kept.Role != "Designer" || !reflect.DeepEqual(kept.Emails, []string{"clara@gmail.com"}) || kept.FactCount != 3 || !contains(kept.Aliases, "Ms Simmons") {
		t.Fatalf("%+v", kept)
	}
	if _, ok, _ := people.Get(d, W, short.ID, false); ok {
		t.Fatal("dropped person still there")
	}
	// The old name now resolves to the kept person.
	dd := f(t, d, "Ms Simmons booked a room")
	link(t, d, scope, one(mention("ms simmons", extra{}), dd.ID))
	eq(t, nameCounts(list(t, d, people.ListOptions{}).People), [][2]any{{"Clara Simmons", int64(4)}})
	if _, err := people.Merge(d, W, full.ID, full.ID); err == nil || !strings.Contains(err.Error(), "two different") {
		t.Fatal(err)
	}
	if _, err := people.Merge(d, "ws_other", full.ID, short.ID); err == nil || !strings.Contains(err.Error(), "No such person") {
		t.Fatal(err)
	}
}

func TestKeepsHandEdits(t *testing.T) {
	d := testkit.OpenDB(t)
	a := f(t, d, "Budi handles payments")
	link(t, d, scope, one(mention("Budi", extra{role: s("Accountant")}), a.ID))
	budi := list(t, d, people.ListOptions{}).People[0]
	if _, _, err := people.Update(d, W, budi.ID, people.Patch{Name: s("Budi Santoso"), Role: people.OptString{Set: true, Value: s("Finance lead")}, Aliases: &[]string{"Budi"}}); err != nil {
		t.Fatal(err)
	}
	b := f(t, d, "Budi approved the budget")
	link(t, d, scope, one(mention("Budi", extra{role: s("Clerk"), email: s("budi@ks.id")}), b.ID))
	after, _, _ := people.Get(d, W, budi.ID, false)
	p := after.Person
	if p.Name != "Budi Santoso" || *p.Role != "Finance lead" || !reflect.DeepEqual(p.Emails, []string{"budi@ks.id"}) || !p.Manual || p.FactCount != 2 {
		t.Fatalf("%+v", p)
	}
}

func TestLinksFollowEditedFact(t *testing.T) {
	d := testkit.OpenDB(t)
	a := f(t, d, "Clara leads the redesign")
	link(t, d, scope, one(mention("Clara", extra{}), a.ID))
	next, ok, err := memories.EditMemory(d, W, a.ID, memories.Patch{Fact: s("Clara leads the redesign and the app")}, func() int64 { return time.Now().UnixMilli() })
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	byMem, _ := people.ForMemories(d, W, []string{next.ID})
	refs, _ := byMem.Get(next.ID)
	eq(t, len(refs), 1)
	eq(t, refs[0].Name, "Clara")
}

// --- merge suggestions -------------------------------------------------------------

// seedPerson writes a person row as the old resolver left it.
func seedPerson(t *testing.T, d *db.DB, name string, e map[string]any, facts []string, container string) string {
	t.Helper()
	id := "per_" + regexp.MustCompile(`\W+`).ReplaceAllString(name, "_") + "_" + container
	cols := []string{"id", "workspace_id", "container", "name"}
	args := []any{id, W, container, name}
	for _, k := range []string{"aliases", "emails", "handles", "role", "organization", "summary"} {
		if v, ok := e[k]; ok {
			cols = append(cols, k)
			if l, isList := v.([]string); isList {
				args = append(args, jsonx.Stringify(l))
			} else {
				args = append(args, v)
			}
		}
	}
	if _, err := d.Exec(`INSERT INTO people (`+strings.Join(cols, ", ")+`) VALUES (?`+strings.Repeat(", ?", len(cols)-1)+`)`, args...); err != nil {
		t.Fatal(err)
	}
	for _, text := range facts {
		m := fact(t, d, text, W, container)
		if _, err := d.Exec(`INSERT INTO person_memories (person_id, memory_id, workspace_id) VALUES (?, ?, ?)`, id, m.ID, W); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestMergeSuggestions(t *testing.T) {
	d := testkit.OpenDB(t)
	kurn := seedPerson(t, d, "Rizky Kurn", map[string]any{"emails": []string{"rizky@wargalapor.example", "rizky.kurniawan@example.com"}}, []string{"Rizky Kurn shared the volunteer schedule"}, C)
	rizky := seedPerson(t, d, "Rizky Kurniawan", map[string]any{"emails": []string{"rizky.kurniawan@example.com", "rizky@wargalapor.example"}, "role": "Data analyst"},
		[]string{"Rizky Kurniawan sent the weekly COVID-19 case recap", "Rizky Kurniawan fixed the Jakarta numbers"}, C)
	maya := seedPerson(t, d, "Maya Lee Hartono", map[string]any{"emails": []string{"maya@foundation.example"}, "role": "Program officer", "summary": "Program officer handling the grant"},
		[]string{"Maya Lee Hartono sent the grant agreement for signature"}, C)
	hartono := seedPerson(t, d, "Maya Hartono", map[string]any{"emails": []string{"maya.hartono@example.com"}, "role": "Program officer"}, []string{"Maya Hartono scheduled a call to review the quarterly grant report"}, C)
	seedPerson(t, d, "Slack Ryan", map[string]any{"handles": []string{"@ryan"}}, []string{"Slack Ryan posted the deploy log"}, C)
	seedPerson(t, d, "Ryan Boyd", map[string]any{"handles": []string{"ryan"}}, []string{"Ryan Boyd posted the deploy log", "Ryan Boyd rolled back the release"}, C)
	// Never suggested: different people, a first name with another email, or a team row.
	seedPerson(t, d, "Clara Simmons", map[string]any{"emails": []string{"clara@vorneo.id"}, "organization": "Vorneo"}, []string{"Clara Simmons sent the Vorneo deployment checklist"}, C)
	seedPerson(t, d, "Clara Wong", map[string]any{"emails": []string{"clara.wong@acme.com"}, "organization": "Vorneo"}, []string{"Clara Wong sent the Vorneo deployment checklist"}, C)
	seedPerson(t, d, "Clara", map[string]any{"emails": []string{"clara@gmail.com"}}, []string{"Clara sent the Vorneo deployment checklist"}, C)
	seedPerson(t, d, "Muhammad Rizki", map[string]any{"aliases": []string{"Muhammad"}}, []string{"Muhammad Rizki joined the standup"}, C)
	seedPerson(t, d, "Muhammad Fauzan", map[string]any{"aliases": []string{"Muhammad"}}, []string{"Muhammad Fauzan joined the standup"}, C)
	seedPerson(t, d, "Sekret WargaLapor", map[string]any{"aliases": []string{"Sekar Ayu Lestari"}, "emails": []string{"sekretwargalapor@example.com"}}, []string{"Sekret WargaLapor sent the newsletter"}, C)
	seedPerson(t, d, "Sekar Ayu Lestari", map[string]any{"emails": []string{"sekretwargalapor@example.com"}}, []string{"Sekar Ayu Lestari asked for the March report"}, C)
	// Another container's twin is out of scope.
	seedPerson(t, d, "Rizky Kurn", map[string]any{"emails": []string{"rizky@wargalapor.example"}}, []string{"Rizky Kurn joined"}, "vorneo")

	before := list(t, d, people.ListOptions{}).Total
	found, err := people.SuggestMerges(d, scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got [][]any
	for _, x := range found {
		got = append(got, []any{x.Keep.Name, x.Drop.Name, x.Reasons})
	}
	eq(t, got, [][]any{
		{"Rizky Kurniawan", "Rizky Kurn", []string{"same email rizky@wargalapor.example", "same email rizky.kurniawan@example.com"}},
		{"Ryan Boyd", "Slack Ryan", []string{"same handle @ryan"}},
		{"Maya Lee Hartono", "Maya Hartono", []string{"names look alike"}},
	})
	eq(t, []string{found[0].Keep.ID, found[0].Drop.ID, found[2].Keep.ID, found[2].Drop.ID}, []string{rizky, kurn, maya, hartono})
	if found[0].Similarity != nil || found[2].Similarity == nil || *found[2].Similarity < 0.855 {
		t.Fatalf("similarities %v %v", found[0].Similarity, found[2].Similarity)
	}
	eq(t, list(t, d, people.ListOptions{}).Total, before)
}

// --- tenant isolation ----------------------------------------------------------------

func TestTenantIsolation(t *testing.T) {
	const A, B = "ws_alpha", "ws_beta"
	d := testkit.OpenDB(t)
	a := fact(t, d, "Clara leads the redesign", A, "shared")
	b := fact(t, d, "Clara runs payroll", B, "shared")
	link(t, d, search.Scope{WorkspaceID: A, Container: "shared"}, one(mention("Clara", extra{}), a.ID))
	// A memory id from workspace A, offered while linking in B, is ignored.
	st := link(t, d, search.Scope{WorkspaceID: B, Container: "shared"}, one(mention("Clara", extra{}), b.ID, a.ID))
	eq(t, st.Linked, 1)
	ca := list(t, d, people.ListOptions{WorkspaceID: A, Container: "shared"}).People[0]
	cb := list(t, d, people.ListOptions{WorkspaceID: B, Container: "shared"}).People[0]
	if ca.ID == cb.ID {
		t.Fatal("same person across workspaces")
	}
	pa, _, _ := people.Get(d, A, ca.ID, false)
	eq(t, []string{pa.Facts[0].ID}, []string{a.ID})
	if _, ok, _ := people.Get(d, B, ca.ID, false); ok {
		t.Fatal("B sees A's person")
	}
	byMem, _ := people.ForMemories(d, B, []string{a.ID})
	eq(t, byMem.Len(), 0)
	if _, err := people.Merge(d, B, cb.ID, ca.ID); err == nil || !strings.Contains(err.Error(), "No such person") {
		t.Fatal(err)
	}
	if ok, _ := people.Delete(d, B, ca.ID); ok {
		t.Fatal("B deleted A's person")
	}
	if _, ok, _ := people.Update(d, B, ca.ID, people.Patch{Name: s("Hijacked")}); ok {
		t.Fatal("B renamed A's person")
	}
}

// --- backfill -----------------------------------------------------------------------

func TestBackfillLinksOnce(t *testing.T) {
	d := testkit.OpenDB(t)
	facts := []model.Memory{f(t, d, "Works with Clara Simmons at Vorneo"), f(t, d, "Budi Santoso handles payments for Kebun Sawit"), f(t, d, "Prefers Puma running shoes"), f(t, d, "Zaki lives in Banjarbaru")}
	g := &testkit.Gateway{Replies: []string{`{"people":[{"name":"Clara Simmons","organization":"Vorneo","facts":[1]},{"name":"Budi Santoso","facts":["2"]},{"name":"Zaki","facts":[4]},{"name":"Nobody","facts":[99]}]}`}}
	first, err := people.Backfill(context.Background(), d, scope, g.Do, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, first, people.BackfillStats{Scanned: 4, Linked: 2, Created: 2, Calls: 1})
	if !strings.Contains(g.Systems[0], `memory named "zaki"`) {
		t.Fatal(g.Systems[0])
	}
	byMem, _ := people.ForMemories(d, W, []string{facts[0].ID})
	refs, _ := byMem.Get(facts[0].ID)
	eq(t, refs[0].Name, "Clara Simmons")
	again, _ := people.Backfill(context.Background(), d, scope, g.Do, 0)
	eq(t, again, people.BackfillStats{})
	all, _ := g.Calls()
	eq(t, all, 1)
	eq(t, list(t, d, people.ListOptions{}).Total, 2)
	// A new fact is the only one the next run sends.
	f(t, d, "Clara Simmons booked the design review")
	g2 := &testkit.Gateway{Replies: []string{`{"people":[{"name":"Clara Simmons","facts":[1]}]}`}}
	third, _ := people.Backfill(context.Background(), d, scope, g2.Do, 0)
	eq(t, third, people.BackfillStats{Scanned: 1, Linked: 1, Calls: 1})
	if !strings.Contains(g2.Prompts[0], "booked the design review") {
		t.Fatal(g2.Prompts[0])
	}
}

func TestBackfillOneBatchPerJob(t *testing.T) {
	d := testkit.OpenDB(t)
	for i := 0; i < 41; i++ {
		f(t, d, "Note "+jsonx.NumberString(float64(i))+" about the roadmap")
	}
	g := &testkit.Gateway{Replies: []string{`{"people":[]}`, `{"people":[]}`}}
	r := jobs.NewRunner(d, pipeline.Handlers(d, pipeline.Deps{Do: g.Do, Now: time.Now}), func(string) {})
	if _, _, err := jobs.Enqueue(d, W, jobs.PeopleBackfill, jsonx.ObjectOf("container", C), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	drain(t, r)
	var statuses []string
	rows, _ := d.Query(`SELECT status FROM jobs WHERE type = 'people_backfill' ORDER BY id`)
	for rows.Next() {
		var st string
		rows.Scan(&st)
		statuses = append(statuses, st)
	}
	rows.Close()
	eq(t, statuses, []string{"done", "done"})
	_, nonProfile := g.Calls()
	eq(t, nonProfile, 2)
}

func TestBackfillOneJobPerContainer(t *testing.T) {
	d := testkit.OpenDB(t)
	f(t, d, "Works with Clara Simmons at Vorneo")
	_, ok1, _ := jobs.Enqueue(d, W, jobs.PeopleBackfill, jsonx.ObjectOf("container", C), time.Now().UnixMilli())
	_, ok2, _ := jobs.Enqueue(d, W, jobs.PeopleBackfill, jsonx.ObjectOf("container", C), time.Now().UnixMilli())
	eq(t, []bool{ok1, ok2}, []bool{true, false})
	g := &testkit.Gateway{Replies: []string{`{"people":[{"name":"Clara Simmons","facts":[1]}]}`}}
	r := jobs.NewRunner(d, pipeline.Handlers(d, pipeline.Deps{Do: g.Do, Now: time.Now}), func(string) {})
	drain(t, r)
	var n int
	d.QueryRow(`SELECT count(*) FROM jobs WHERE type = 'people_backfill' AND status = 'done'`).Scan(&n)
	eq(t, n, 1)
	eq(t, list(t, d, people.ListOptions{}).Total, 1)
}

// --- ask: names in a question --------------------------------------------------------

func TestNamedIn(t *testing.T) {
	d := testkit.OpenDB(t)
	a := f(t, d, "Works with Clara Simmons, a product designer at Vorneo, on the listing redesign")
	b := f(t, d, "Meets Clara every Tuesday to review Figma mockups")
	f(t, d, "Prefers Puma running shoes")
	link(t, d, scope, one(mention("Clara Simmons", extra{aliases: []string{"Clara"}, role: s("Product designer"), organization: s("Vorneo"), email: s("clara@vorneo.id")}), a.ID, b.ID))
	named := func(text string) []string {
		rows, err := people.NamedIn(d, scope, text)
		if err != nil {
			t.Fatal(err)
		}
		return names(rows)
	}
	eq(t, named("what do I know about clara?"), []string{"Clara Simmons"})
	eq(t, named("what shoes do I wear?"), []string{})
	// A one-word name that is also a word needs a capital letter, unless the question is all lowercase.
	will := f(t, d, "Will fixes the office printer")
	link(t, d, scope, one(mention("Will", extra{}), will.ID))
	eq(t, named("What will I do next week?"), []string{})
	eq(t, named("What does Will do?"), []string{"Will"})
	eq(t, named("what does will do?"), []string{"Will"})
}
