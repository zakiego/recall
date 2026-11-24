package ask_test

// ask.test.ts, and the "ask" case of people.test.ts.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

const W = "ws_ask"

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Setenv("EXTRACT_MODEL", "claude-haiku-5-5")
	os.Exit(m.Run())
}

func p(s string) *string { return &s }

// --- resolveCitations --------------------------------------------------------------

var sources = map[string]ask.Source{
	"m1": {Type: "memory", ID: "mem_a", Text: "Wears Puma"},
	"m2": {Type: "memory", ID: "mem_b", Text: "Lives in Bandarjaya"},
	"c1": {Type: "chunk", ID: "chk_a", Text: "excerpt", DocumentID: "doc_a", DocumentTitle: p("Notes")},
}

func TestCitationsByFirstUse(t *testing.T) {
	text, cites := ask.ResolveCitations("Lives in Bandarjaya [m2]. Wears Puma [m1][c1], still [M2].", sources)
	if text != "Lives in Bandarjaya [1]. Wears Puma [2][3], still [1]." {
		t.Fatal(text)
	}
	var got []string
	for _, c := range cites {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != "mem_b,mem_a,chk_a" || cites[2].Ref != 3 {
		t.Fatal(got)
	}
}

func TestCitationsDropMadeUpRefs(t *testing.T) {
	text, cites := ask.ResolveCitations("Wears Puma [m1, m7]. Owns a cat [m9].", sources)
	if text != "Wears Puma [1]. Owns a cat." || len(cites) != 1 {
		t.Fatal(text, cites)
	}
}

func TestCitationsLeaveOtherBrackets(t *testing.T) {
	if text, _ := ask.ResolveCitations("He said [sic] it [m1].", sources); text != "He said [sic] it [1]." {
		t.Fatal(text)
	}
}

// --- ask ------------------------------------------------------------------------------

func seed(t *testing.T, d *db.DB) {
	for _, m := range []memories.NewMemory{
		{WorkspaceID: W, Container: "me", Fact: "Wears Puma running shoes", Keywords: "shoes sneakers sepatu lari", Kind: "dynamic"},
		{WorkspaceID: W, Container: "me", Fact: "Lives in Bandarjaya", Keywords: "city home kota tinggal", Kind: "static"},
		{WorkspaceID: W, Container: "other", Fact: "Wears Nike shoes", Keywords: "shoes sepatu", Kind: "dynamic"},
	} {
		if _, err := memories.AddMemoryDirect(d, m, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
}

func start(t *testing.T, d *db.DB, do gateway.Do) {
	deps := pipeline.Deps{Ask: ask.Deps{Do: do, Now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}}
	r := jobs.NewRunner(d, pipeline.Handlers(d, deps), func(string) {})
	r.Start()
	t.Cleanup(r.Stop)
}

func askStatus(t *testing.T, d *db.DB) string {
	var s string
	d.QueryRow(`SELECT status FROM jobs WHERE type = 'ask'`).Scan(&s)
	return s
}

func run(t *testing.T, d *db.DB, q string) (ask.Response, error) {
	return ask.Ask(context.Background(), d, W, ask.Input{Container: "me", Question: q}, 0)
}

func TestAnswersFromRecordedStream(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	fixture, err := os.ReadFile(testutil.Testdata("gateway", "ask-ok.sse"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []string
	start(t, d, func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
	})
	res, err := run(t, d, "What shoes do they wear and where do they live?")
	if err != nil {
		t.Fatal(err)
	}
	_, prompt := testkit.ChatRequest([]byte(bodies[0]))
	if strings.Contains(prompt, "Nike") || strings.Contains(prompt, "mem_") {
		t.Fatal("prompt leaks", prompt)
	}
	if res.Answer.Answer != "They wear Puma running shoes [1] and live in Bandarjaya [2]." || !res.Covered || *res.Model != "claude-haiku-5-5" || len(res.Citations) != 2 {
		t.Fatalf("%+v", res)
	}
	// Each footnote is the row the prompt numbered m1 / m2.
	rows := map[string]string{}
	rs, _ := d.Query(`SELECT id, fact FROM memories WHERE container = 'me'`)
	for rs.Next() {
		var id, fact string
		rs.Scan(&id, &fact)
		rows[fact] = id
	}
	rs.Close()
	for i, ref := range []string{"m1", "m2"} {
		m := regexp.MustCompile(`(?m)^\[` + ref + `\] \([a-z]+, [\d-]+\) (.+)$`).FindStringSubmatch(prompt)
		if m == nil {
			t.Fatalf("no %s in\n%s", ref, prompt)
		}
		c := res.Citations[i]
		if c.Ref != i+1 || c.Type != "memory" || c.Text != m[1] || c.ID != rows[m[1]] {
			t.Fatalf("citation %d: %+v (fact %q)", i, c, m[1])
		}
	}
	if askStatus(t, d) != "done" {
		t.Fatal(askStatus(t, d))
	}
}

func TestNotInMemoryAnswer(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	g := &testkit.Gateway{Default: `{"answer":"Nothing in memory says where they work.","covered":false}`}
	start(t, d, g.Do)
	res, err := run(t, d, "Where do they work?")
	if err != nil || res.Answer.Answer != "Nothing in memory says where they work." || res.Covered || len(res.Citations) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestEmptyContainerSkipsGateway(t *testing.T) {
	d := testkit.OpenDB(t)
	g := &testkit.Gateway{Default: `{"answer":"x"}`}
	start(t, d, g.Do)
	res, err := ask.Ask(context.Background(), d, W, ask.Input{Container: "empty", Question: "anything?"}, 0)
	if err != nil || res.Answer.Answer != ask.NothingStored || res.Covered || len(res.Citations) != 0 || res.Model != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if all, _ := g.Calls(); all != 0 {
		t.Fatal("gateway called")
	}
}

func TestTimesOutAndCancelsQueuedJob(t *testing.T) {
	d := testkit.OpenDB(t)
	// No runner: the job never runs.
	_, err := ask.Ask(context.Background(), d, W, ask.Input{Container: "me", Question: "anything?"}, 50)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 504 || he.Type != "timeout" || !strings.HasPrefix(he.Message, "No answer within 0 s") {
		t.Fatal(err)
	}
	if askStatus(t, d) != "failed" {
		t.Fatal(askStatus(t, d))
	}
}

// hanging is a gateway that never answers until its request is aborted.
type hanging struct {
	mu   sync.Mutex
	ctxs []context.Context
}

func (h *hanging) do(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.ctxs = append(h.ctxs, r.Context())
	h.mu.Unlock()
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func (h *hanging) seen() []context.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]context.Context(nil), h.ctxs...)
}

func settled(t *testing.T, d *db.DB) string {
	for i := 0; i < 100; i++ {
		if s := askStatus(t, d); s != "running" && s != "queued" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "still running"
}

func TestAbortsRunningCallAfter504(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	h := &hanging{}
	start(t, d, h.do)
	_, err := ask.Ask(context.Background(), d, W, ask.Input{Container: "me", Question: "shoes?"}, 300)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 504 {
		t.Fatal(err)
	}
	if s := settled(t, d); s != "failed" {
		t.Fatal(s)
	}
	ctxs := h.seen()
	if len(ctxs) != 1 || ctxs[0].Err() == nil {
		t.Fatal("gateway call not aborted")
	}
}

func TestAbortsRunningCallWhenCallerGoesAway(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	h := &hanging{}
	start(t, d, h.do)
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ask.Ask(caller, d, W, ask.Input{Container: "me", Question: "shoes?"}, 0)
		done <- err
	}()
	for i := 0; i < 100 && len(h.seen()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if s := settled(t, d); s != "failed" {
		t.Fatal(s)
	}
	if h.seen()[0].Err() == nil {
		t.Fatal("gateway call not aborted")
	}
}

func TestGatewayFailureIs502WithoutRetry(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	var mu sync.Mutex
	calls := 0
	start(t, d, func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"10"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"overloaded","message":"all slots busy"}}`))}, nil
	})
	_, err := run(t, d, "shoes?")
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 502 || he.Type != "gateway_error" {
		t.Fatal(err)
	}
	if calls != 1 || askStatus(t, d) != "failed" {
		t.Fatal(calls, askStatus(t, d))
	}
}

func TestUnreachableGateway(t *testing.T) {
	d := testkit.OpenDB(t)
	seed(t, d)
	start(t, d, func(*http.Request) (*http.Response, error) { return nil, errors.New("connect: connection refused") })
	_, err := run(t, d, "shoes?")
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 502 || he.Type != "gateway_unreachable" {
		t.Fatal(err)
	}
}

func TestRefusesWhenJobLoopIsOff(t *testing.T) {
	d := testkit.OpenDB(t)
	t.Setenv("RECALL_JOBS", "off")
	_, err := run(t, d, "x")
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 503 {
		t.Fatal(err)
	}
}

// people.test.ts "answers questions about a person from their page".
func TestAnswersAboutAPersonFromTheirPage(t *testing.T) {
	d := testkit.OpenDB(t)
	const C = "zane"
	add := func(text string) string {
		m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: W, Container: C, Fact: text, Kind: "dynamic"}, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	a := add("Works with Clara Simmons, a product designer at Vortek, on the listing redesign")
	b := add("Meets Clara every Tuesday to review Figma mockups")
	add("Prefers Puma running shoes")
	clara := gateway.PersonMention{Name: "Clara Simmons", Aliases: []string{"Clara"}, Role: p("Product designer"), Organization: p("Vortek"), Email: p("clara@vortek.id"), Facts: []string{}}
	if _, err := people.LinkMentions(d, search.Scope{WorkspaceID: W, Container: C}, []people.Mention{{Mention: clara, MemoryIDs: []string{a, b}}}); err != nil {
		t.Fatal(err)
	}
	g := &testkit.Gateway{Default: `{"answer":"Clara Simmons is a product designer at Vortek [m1]. You meet her on Tuesdays [m2].","covered":true}`}
	out, err := ask.AnswerQuestion(context.Background(), d, W, ask.Payload{Container: C, Question: "What do I know about Clara?"}, ask.Deps{Do: g.Do, Now: func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	prompt := g.Prompts[0]
	if !strings.Contains(prompt, "PEOPLE named in the question") || !strings.Contains(prompt, "Clara Simmons; also called Clara; Product designer at Vortek; contact clara@vortek.id") {
		t.Fatal(prompt)
	}
	// Her facts come first, ahead of the search hits.
	if !regexp.MustCompile(`\[m1\][^\n]*(Clara|Figma)`).MatchString(prompt) {
		t.Fatal(prompt)
	}
	if len(out.People) != 1 || out.People[0].Name != "Clara Simmons" {
		t.Fatal(out.People)
	}
	var ids []string
	for _, c := range out.Citations {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	want := []string{a, b}
	sort.Strings(want)
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatal(ids, want)
	}
}
