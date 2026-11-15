package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type golden struct {
	Prompts struct {
		Extract []struct {
			Input struct {
				Content  string `json:"content"`
				Existing []struct {
					Ref  int    `json:"ref"`
					Fact string `json:"fact"`
					Kind string `json:"kind"`
				} `json:"existing"`
				Now          string `json:"now"`
				DocumentDate string `json:"documentDate"`
				Source       string `json:"source"`
			} `json:"input"`
			Out string `json:"out"`
		} `json:"extract"`
		Profile []struct {
			Input struct {
				Facts []struct {
					Fact string `json:"fact"`
					Kind string `json:"kind"`
					AsOf string `json:"asOf"`
				} `json:"facts"`
				Now *string `json:"now"`
			} `json:"input"`
			Out string `json:"out"`
		} `json:"profile"`
		Ask []struct {
			Input struct {
				Question string `json:"question"`
				Facts    []struct {
					Ref       string `json:"ref"`
					Text      string `json:"text"`
					Kind      string `json:"kind"`
					Date      string `json:"date"`
					Happened  string `json:"happened"`
					Due       string `json:"due"`
					DuePassed bool   `json:"duePassed"`
				} `json:"facts"`
				Excerpts []struct {
					Ref   string  `json:"ref"`
					Text  string  `json:"text"`
					Title *string `json:"title"`
				} `json:"excerpts"`
				Profile struct{ Static, Dynamic string } `json:"profile"`
				Now     string                           `json:"now"`
				People  []struct {
					Name         string   `json:"name"`
					Aliases      []string `json:"aliases"`
					Role         *string  `json:"role"`
					Organization *string  `json:"organization"`
					Summary      string   `json:"summary"`
					Contact      []string `json:"contact"`
				} `json:"people"`
			} `json:"input"`
			Out string `json:"out"`
		} `json:"ask"`
		Rules []struct {
			Container      string `json:"container"`
			Rules          string `json:"rules"`
			BackfillSystem string `json:"backfillSystem"`
		} `json:"rules"`
		Small struct {
			PeopleBackfill string      `json:"peopleBackfill"`
			Backfill       string      `json:"backfill"`
			AskPeople      string      `json:"askPeople"`
			FenceSafe      [][2]string `json:"fenceSafe"`
		} `json:"small"`
	} `json:"prompts"`
	Schemas   map[string][]schemaCase `json:"schemas"`
	JSONTexts []struct {
		Text string     `json:"text"`
		Out  rawOutcome `json:"out"`
	} `json:"jsonTexts"`
	Streams map[string]struct {
		Text  string  `json:"text"`
		OK    *string `json:"ok"`
		Error *struct {
			Name, Type, Message string
			Status              int
		} `json:"error"`
	} `json:"streams"`
	Chat []struct {
		Name  string  `json:"name"`
		Model *string `json:"model"`
		Abort *string `json:"abort"`
		Steps []struct {
			Status  int               `json:"status"`
			Body    string            `json:"body"`
			Headers map[string]string `json:"headers"`
			Stall   bool              `json:"stall"`
		} `json:"steps"`
		Requests []struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    json.RawMessage   `json:"body"`
		} `json:"requests"`
		Outcome struct {
			OK *struct {
				Data  json.RawMessage `json:"data"`
				Model string          `json:"model"`
			} `json:"ok"`
			Error *struct {
				Name         string   `json:"name"`
				Message      string   `json:"message"`
				Status       *int     `json:"status"`
				Type         *string  `json:"type"`
				RetryAfterMs *float64 `json:"retryAfterMs"`
			} `json:"error"`
		} `json:"outcome"`
	} `json:"chat"`
}

type schemaCase struct {
	Input json.RawMessage `json:"input"`
	Out   rawOutcome      `json:"out"`
}

type rawOutcome struct {
	OK    json.RawMessage `json:"ok"`
	Error *string         `json:"error"`
}

func load(t *testing.T) *golden {
	var g golden
	testutil.LoadJSON(t, "gateway.json", &g)
	return &g
}

func mustTime(t *testing.T, s string) time.Time {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestPromptsMatchTS(t *testing.T) {
	g := load(t)
	for i, c := range g.Prompts.Extract {
		in := gateway.ExtractInput{Content: c.Input.Content, Now: mustTime(t, c.Input.Now), DocumentDate: c.Input.DocumentDate, Source: c.Input.Source}
		for _, e := range c.Input.Existing {
			in.Existing = append(in.Existing, gateway.ExistingMemory{Ref: e.Ref, Fact: e.Fact, Kind: e.Kind})
		}
		if got := gateway.ExtractPrompt(in); got != c.Out {
			t.Errorf("extract %d:\n got %q\nwant %q", i, got, c.Out)
		}
	}
	for i, c := range g.Prompts.Profile {
		var facts []gateway.ProfileFact
		for _, f := range c.Input.Facts {
			facts = append(facts, gateway.ProfileFact{Fact: f.Fact, Kind: f.Kind, AsOf: f.AsOf})
		}
		var now *time.Time
		if c.Input.Now != nil {
			tm := mustTime(t, *c.Input.Now)
			now = &tm
		}
		if got := gateway.ProfilePrompt(facts, now); got != c.Out {
			t.Errorf("profile %d:\n got %q\nwant %q", i, got, c.Out)
		}
	}
	for i, c := range g.Prompts.Ask {
		in := gateway.AskInput{Question: c.Input.Question, ProfileStatic: c.Input.Profile.Static, ProfileDyn: c.Input.Profile.Dynamic, Now: mustTime(t, c.Input.Now)}
		for _, f := range c.Input.Facts {
			in.Facts = append(in.Facts, gateway.AskFact{Ref: f.Ref, Text: f.Text, Kind: f.Kind, Date: f.Date, Happened: f.Happened, Due: f.Due, DuePassed: f.DuePassed})
		}
		for _, e := range c.Input.Excerpts {
			in.Excerpts = append(in.Excerpts, gateway.AskExcerpt{Ref: e.Ref, Text: e.Text, Title: e.Title})
		}
		for _, p := range c.Input.People {
			in.People = append(in.People, gateway.AskPerson{Name: p.Name, Aliases: p.Aliases, Role: p.Role, Organization: p.Organization, Summary: p.Summary, Contact: p.Contact})
		}
		if got := gateway.AskPrompt(in); got != c.Out {
			t.Errorf("ask %d:\n got %q\nwant %q", i, got, c.Out)
		}
	}
	for _, r := range g.Prompts.Rules {
		if got := gateway.PeopleRules(r.Container); got != r.Rules {
			t.Errorf("peopleRules(%q)", r.Container)
		}
		if got := gateway.PeopleBackfillSystem(r.Container); got != r.BackfillSystem {
			t.Errorf("peopleBackfillSystem(%q)", r.Container)
		}
	}
	s := g.Prompts.Small
	if got := gateway.PeopleBackfillPrompt([]gateway.RefFact{{Ref: 1, Fact: "Met Clara <<< >>>"}, {Ref: 2, Fact: "x"}}); got != s.PeopleBackfill {
		t.Errorf("peopleBackfillPrompt %q", got)
	}
	if got := gateway.BackfillPrompt([]gateway.BackfillFact{{Ref: 1, Fact: "Contract >>> ends", Stored: "2026-03-02"}, {Ref: 3, Fact: "y", Stored: "2026-01-01"}}); got != s.Backfill {
		t.Errorf("backfillPrompt %q", got)
	}
	r := "r"
	if got := gateway.AskPeopleSection([]gateway.AskPerson{{Name: "A", Aliases: []string{"B", "C"}, Role: &r, Summary: "s", Contact: []string{"c"}}}); got != s.AskPeople {
		t.Errorf("askPeopleSection %q", got)
	}
	for _, f := range s.FenceSafe {
		if got := gateway.FenceSafe(f[0]); got != f[1] {
			t.Errorf("fenceSafe(%q) = %q", f[0], got)
		}
	}
}

// plain turns a Go result into the JSON the TS result serializes to.
func dateJSON(o *jsonx.Object, key string, f gateway.DateField) {
	if !f.Set {
		return
	}
	if f.Value == nil {
		o.Set(key, nil)
		return
	}
	d := jsonx.NewMap[any]()
	d.Set("date", f.Value.Date)
	d.Set("precision", f.Value.Precision)
	o.Set(key, d)
}

func extractJSON(r gateway.ExtractResult, people bool) any {
	o := jsonx.NewMap[any]()
	add := []any{}
	for _, a := range r.Add {
		x := jsonx.NewMap[any]()
		x.Set("fact", a.Fact)
		x.Set("keywords", a.Keywords)
		x.Set("kind", a.Kind)
		dateJSON(x, "event_date", a.EventDate)
		dateJSON(x, "due_date", a.DueDate)
		add = append(add, x)
	}
	upd := []any{}
	for _, u := range r.Update {
		x := jsonx.NewMap[any]()
		x.Set("ref", u.Ref)
		x.Set("fact", u.Fact)
		x.Set("keywords", u.Keywords)
		dateJSON(x, "event_date", u.EventDate)
		dateJSON(x, "due_date", u.DueDate)
		upd = append(upd, x)
	}
	inv := []any{}
	for _, n := range r.Invalidate {
		inv = append(inv, n)
	}
	con := []any{}
	for _, c := range r.Conflict {
		x := jsonx.NewMap[any]()
		x.Set("ref", c.Ref)
		x.Set("fact", c.Fact)
		x.Set("keywords", c.Keywords)
		x.Set("note", c.Note)
		dateJSON(x, "event_date", c.EventDate)
		dateJSON(x, "due_date", c.DueDate)
		con = append(con, x)
	}
	o.Set("add", add)
	o.Set("update", upd)
	o.Set("invalidate", inv)
	o.Set("conflict", con)
	if people {
		o.Set("people", peopleJSON(r.People))
	}
	return o
}

func peopleJSON(ps []gateway.PersonMention) []any {
	out := []any{}
	for _, p := range ps {
		x := jsonx.NewMap[any]()
		x.Set("name", p.Name)
		x.Set("aliases", p.Aliases)
		for _, kv := range []struct {
			k string
			v *string
		}{{"role", p.Role}, {"organization", p.Organization}, {"email", p.Email}, {"handle", p.Handle}, {"summary", p.Summary}} {
			if kv.v != nil {
				x.Set(kv.k, *kv.v)
			}
		}
		x.Set("facts", p.Facts)
		out = append(out, x)
	}
	return out
}

func checkSchema(t *testing.T, name string, cases []schemaCase, run func(any) (any, error)) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatalf("%s: no cases", name)
	}
	for i, c := range cases {
		in, err := jsonx.Parse(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		got, gerr := run(in)
		if c.Out.Error != nil {
			if gerr == nil {
				t.Errorf("%s %d %s: no error, want %q", name, i, c.Input, *c.Out.Error)
				continue
			}
			if msg := jserr.Format(gerr); msg != *c.Out.Error {
				t.Errorf("%s %d %s: error text\n got %s\nwant %s", name, i, c.Input, msg, *c.Out.Error)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s %d %s: %v", name, i, c.Input, gerr)
			continue
		}
		want, _ := jsonx.Parse(c.Out.OK)
		if jsonx.Stringify(got) != jsonx.Stringify(want) {
			t.Errorf("%s %d %s:\n got %s\nwant %s", name, i, c.Input, jsonx.Stringify(got), jsonx.Stringify(want))
		}
	}
}

func TestSchemasMatchTS(t *testing.T) {
	g := load(t)
	checkSchema(t, "extract", g.Schemas["extract"], func(v any) (any, error) {
		r, err := gateway.ParseExtract(v)
		return extractJSON(r, false), err
	})
	checkSchema(t, "extractWithPeople", g.Schemas["extractWithPeople"], func(v any) (any, error) {
		r, err := gateway.ParseExtractWithPeople(v)
		return extractJSON(r, true), err
	})
	checkSchema(t, "peopleBackfill", g.Schemas["peopleBackfill"], func(v any) (any, error) {
		p, err := gateway.ParsePeopleBackfill(v)
		o := jsonx.NewMap[any]()
		o.Set("people", peopleJSON(p))
		return o, err
	})
	checkSchema(t, "profile", g.Schemas["profile"], func(v any) (any, error) {
		p, err := gateway.ParseProfile(v)
		o := jsonx.NewMap[any]()
		o.Set("static", p.Static)
		o.Set("dynamic", p.Dynamic)
		return o, err
	})
	checkSchema(t, "ask", g.Schemas["ask"], func(v any) (any, error) {
		a, err := gateway.ParseAsk(v)
		o := jsonx.NewMap[any]()
		o.Set("answer", a.Answer)
		o.Set("covered", a.Covered)
		return o, err
	})
	checkSchema(t, "backfill", g.Schemas["backfill"], func(v any) (any, error) {
		ds, err := gateway.ParseBackfill(v)
		list := []any{}
		for _, d := range ds {
			x := jsonx.NewMap[any]()
			x.Set("ref", d.Ref)
			dateJSON(x, "event_date", d.EventDate)
			dateJSON(x, "due_date", d.DueDate)
			list = append(list, x)
		}
		o := jsonx.NewMap[any]()
		o.Set("dates", list)
		return o, err
	})
}

func TestExtractJSONMatchesTS(t *testing.T) {
	g := load(t)
	for _, c := range g.JSONTexts {
		got, err := gateway.ExtractJSON(c.Text)
		if c.Out.Error != nil {
			if err == nil || jserr.Format(err) != *c.Out.Error {
				t.Errorf("%q: %v, want %s", c.Text, err, *c.Out.Error)
			}
			continue
		}
		want, _ := jsonx.Parse(c.Out.OK)
		if err != nil || jsonx.Stringify(got) != jsonx.Stringify(want) {
			t.Errorf("%q: %s %v, want %s", c.Text, jsonx.Stringify(got), err, c.Out.OK)
		}
	}
}

func TestReadStreamTextMatchesTS(t *testing.T) {
	g := load(t)
	if len(g.Streams) < 18 {
		t.Fatal("golden incomplete")
	}
	for name, c := range g.Streams {
		got, err := gateway.ReadStreamText(strings.NewReader(c.Text))
		if c.OK != nil {
			if err != nil || got != *c.OK {
				t.Errorf("%s: %q %v, want %q", name, got, err, *c.OK)
			}
			continue
		}
		var ge *gateway.Error
		if !errors.As(err, &ge) || ge.Status != c.Error.Status || ge.Type != c.Error.Type {
			t.Errorf("%s: %v, want %+v", name, err, c.Error)
			continue
		}
		// The bad_stream message is the AI SDK's validation text (Known gaps, M3).
		if ge.Type == "bad_stream" {
			if !strings.HasPrefix(ge.Msg, "bad stream chunk: ") {
				t.Errorf("%s: %q", name, ge.Msg)
			}
			continue
		}
		if ge.Msg != c.Error.Message {
			t.Errorf("%s: %q, want %q", name, ge.Msg, c.Error.Message)
		}
	}
}

func TestChatJSONMatchesTS(t *testing.T) {
	g := load(t)
	t.Setenv("GATEWAY_KEY", "zkg_test")
	t.Setenv("GATEWAY_URL", "http://gateway.test/")
	t.Setenv("EXTRACT_MODEL", "claude-haiku-5-5")
	t.Setenv("FALLBACK_MODEL", "claude-haiku-4-5")
	for _, sc := range g.Chat {
		var reqs []*http.Request
		var bodies [][]byte
		i := 0
		do := func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			reqs = append(reqs, r)
			bodies = append(bodies, b)
			if i >= len(sc.Steps) {
				return nil, errors.New("no more steps")
			}
			st := sc.Steps[i]
			i++
			h := http.Header{}
			for k, v := range st.Headers {
				h.Set(k, v)
			}
			if st.Stall {
				return &http.Response{StatusCode: st.Status, Header: h, Body: io.NopCloser(&stallReader{data: []byte(st.Body), ctx: r.Context()})}, nil
			}
			return &http.Response{StatusCode: st.Status, Header: h, Body: io.NopCloser(strings.NewReader(st.Body))}, nil
		}
		req := gateway.Request{System: "SYS", Prompt: "PROMPT <<<x>>>"}
		if sc.Model != nil {
			req.Model = *sc.Model
		}
		ctx := context.Background()
		if sc.Abort != nil {
			switch *sc.Abort {
			case "timeout":
				c, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				ctx = c
			case "reason":
				c, cancel := context.WithCancelCause(ctx)
				t := time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("job cancelled")) })
				defer t.Stop()
				ctx = c
			}
		}
		data, model, err := gateway.ChatJSON(ctx, req, gateway.ParseProfile, do)
		if len(reqs) != len(sc.Requests) {
			t.Errorf("%s: %d requests, want %d", sc.Name, len(reqs), len(sc.Requests))
			continue
		}
		for k, want := range sc.Requests {
			r := reqs[k]
			if r.URL.String() != want.URL || r.Method != "POST" || r.Header.Get("authorization") != want.Headers["authorization"] || r.Header.Get("content-type") != want.Headers["content-type"] {
				t.Errorf("%s request %d: %s %v", sc.Name, k, r.URL, r.Header)
			}
			w, _ := jsonx.Parse(want.Body)
			if string(bodies[k]) != jsonx.Stringify(w) {
				t.Errorf("%s request %d body:\n got %s\nwant %s", sc.Name, k, bodies[k], jsonx.Stringify(w))
			}
		}
		if sc.Outcome.OK != nil {
			o := jsonx.NewMap[any]()
			o.Set("static", data.Static)
			o.Set("dynamic", data.Dynamic)
			w, _ := jsonx.Parse(sc.Outcome.OK.Data)
			if err != nil || jsonx.Stringify(o) != jsonx.Stringify(w) || model != sc.Outcome.OK.Model {
				t.Errorf("%s: %v %s %s", sc.Name, err, jsonx.Stringify(o), model)
			}
			continue
		}
		e := sc.Outcome.Error
		if err == nil || jserr.Name(err) != e.Name || err.Error() != e.Message {
			t.Errorf("%s: %v (%s), want %s: %q", sc.Name, err, jserr.Name(err), e.Name, e.Message)
			continue
		}
		var ge *gateway.Error
		if errors.As(err, &ge) {
			if e.Status == nil || ge.Status != *e.Status || ge.Type != *e.Type || (e.RetryAfterMs == nil) != (ge.RetryAfterMs == 0) || (e.RetryAfterMs != nil && *e.RetryAfterMs != ge.RetryAfterMs) {
				t.Errorf("%s: %+v, want %+v", sc.Name, ge, e)
			}
		}
	}
}

// stallReader sends its data, then blocks until ctx ends and fails the way a
// body read on a cancelled request fails.
type stallReader struct {
	data []byte
	ctx  context.Context
}

func (s *stallReader) Read(p []byte) (int, error) {
	if len(s.data) > 0 {
		n := copy(p, s.data)
		s.data = s.data[n:]
		return n, nil
	}
	<-s.ctx.Done()
	return 0, s.ctx.Err()
}

// client.test.ts "throws on an in-stream error chunk" and "joins the text parts".
func TestFixtures(t *testing.T) {
	b, _ := os.ReadFile(testutil.Testdata("gateway", "extract-ok.sse"))
	text, err := gateway.ReadStreamText(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := gateway.ExtractJSON(text)
	r, err2 := gateway.ParseExtract(v)
	if err != nil || err2 != nil || len(r.Add) != 1 || r.Add[0].Fact != "Loves Adidas shoes" || r.Add[0].Keywords != "sepatu sneakers" {
		t.Fatalf("%+v %v %v", r, err, err2)
	}
	t.Setenv("GATEWAY_KEY", "")
	_, err = gateway.ChatOnce(context.Background(), gateway.Request{}, nil)
	var ge *gateway.Error
	if !errors.As(err, &ge) || ge.Msg != "GATEWAY_KEY is not set" || ge.Type != "config" {
		t.Fatal(err)
	}
}

type dateGolden struct {
	Parse []struct {
		S string       `json:"s"`
		D *dates.Dated `json:"d"`
	} `json:"parse"`
	ToString []struct {
		D    dates.Dated `json:"d"`
		S, F string
	} `json:"toString"`
	Dated []struct {
		A, B *string
		D    *dates.Dated
	} `json:"dated"`
	AddDays []struct {
		A   string `json:"a"`
		N   int    `json:"n"`
		Out string `json:"out"`
	} `json:"addDays"`
	DaysBetween []struct {
		A, B string
		Out  int
	} `json:"daysBetween"`
	Relative []struct {
		N   int
		Out string
	} `json:"relative"`
	Zones []struct {
		TZ    string  `json:"tz"`
		Valid bool    `json:"valid"`
		Today [][]any `json:"today"`
	} `json:"zones"`
}

func TestDatesMatchTS(t *testing.T) {
	var g struct {
		Dates dateGolden `json:"dates"`
	}
	testutil.LoadJSON(t, "gateway.json", &g)
	d := g.Dates
	for _, c := range d.Parse {
		got := dates.Parse(c.S)
		if fmt.Sprint(got) != fmt.Sprint(c.D) {
			t.Errorf("parse %q: %v, want %v", c.S, got, c.D)
		}
	}
	for _, c := range d.ToString {
		if c.D.String() != c.S || dates.Format(c.D) != c.F {
			t.Errorf("%+v: %s %s", c, c.D.String(), dates.Format(c.D))
		}
	}
	for _, c := range d.Dated {
		if got := dates.FromColumns(c.A, c.B); fmt.Sprint(got) != fmt.Sprint(c.D) {
			t.Errorf("dated %v %v: %v", c.A, c.B, got)
		}
	}
	for _, c := range d.AddDays {
		if got := dates.AddDays(c.A, c.N); got != c.Out {
			t.Errorf("addDays %v %d: %s", c.A, c.N, got)
		}
	}
	for _, c := range d.DaysBetween {
		if got := dates.DaysBetween(c.A, c.B); got != c.Out {
			t.Errorf("daysBetween %v %v: %d", c.A, c.B, got)
		}
	}
	for _, c := range d.Relative {
		if got := dates.RelativeDays(c.N); got != c.Out {
			t.Errorf("relative %d: %s", c.N, got)
		}
	}
	for _, z := range d.Zones {
		if dates.IsTimezone(z.TZ) != z.Valid {
			t.Errorf("isTimezone(%q)", z.TZ)
		}
		for _, row := range z.Today {
			ms := int64(row[0].(float64))
			if got := dates.TodayIn(z.TZ, ms); got != row[1].(string) {
				t.Errorf("todayIn(%q, %d) = %s, want %s", z.TZ, ms, got, row[1])
			}
			if got := dates.HourIn(z.TZ, ms); float64(got) != row[2].(float64) {
				t.Errorf("hourIn(%q, %d) = %d, want %v", z.TZ, ms, got, row[2])
			}
		}
	}
}
