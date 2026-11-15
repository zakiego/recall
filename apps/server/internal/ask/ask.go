// Package ask is ask.ts: a plain-language answer grounded in a container's
// memories, with citations. The request enqueues an `ask` job and waits for
// it, so the gateway call runs in the job loop like every other gateway call.
package ask

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	Memories    = 12
	Chunks      = 6
	PersonFacts = 12
	// TimeoutMs is ASK_TIMEOUT_MS (src/lib/limits.ts).
	TimeoutMs    = 90_000
	excerptChars = 1_500
)

// NothingStored is the answer for a container with nothing in it.
const NothingStored = "Nothing is stored in this container yet, so there is nothing to answer from."

// Source is a fact or excerpt the model was shown. DocumentID is set for a
// chunk.
type Source struct {
	Type          string
	ID            string
	Text          string
	DocumentID    string
	DocumentTitle *string
}

// Citation is a source with its footnote number.
type Citation struct {
	Source
	Ref int
}

// MarshalJSON writes the TS object: a memory {type, id, text, ref}, a chunk
// {type, id, text, documentId, documentTitle, ref}.
func (c Citation) MarshalJSON() ([]byte, error) {
	o := jsonx.ObjectOf("type", c.Type, "id", c.ID, "text", c.Text)
	if c.Type == "chunk" {
		o.Set("documentId", c.DocumentID)
		o.Set("documentTitle", c.DocumentTitle)
	}
	o.Set("ref", c.Ref)
	return jsonx.Marshal(o)
}

var (
	bracket   = jsre.MustCompile(`\[([^[\]\n]{1,80})\]`, "g")
	refRE     = regexp.MustCompile(`(?i)^[mc][0-9]{1,3}$`)
	repeated  = jsre.MustCompile(`(\[\d+\])(?:\s*\1)+`, "g")
	beforeEnd = regexp.MustCompile(`[ \t]+([.,;:!?])`)
	spaces    = regexp.MustCompile(`[ \t]{2,}`)
)

// splitRefs is inner.split(/[\s,;]+/).filter(Boolean).
func splitRefs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || strx.IsSpace(r) })
}

// ResolveCitations is resolveCitations: each known ref the model cited
// becomes a footnote number in order of first use; made-up refs are dropped;
// brackets that aren't refs stay.
func ResolveCitations(answer string, sources map[string]Source) (string, []Citation) {
	order := map[string]int{}
	citations := []Citation{}
	text := bracket.ReplaceFunc(answer, func(m *jsre.Match) string {
		inner, _ := m.Group(1)
		tokens := splitRefs(inner)
		if len(tokens) == 0 {
			return m.Text()
		}
		for _, t := range tokens {
			if !refRE.MatchString(t) {
				return m.Text()
			}
		}
		var marks []string
		for _, t := range tokens {
			key := strx.Lower(t)
			src, ok := sources[key]
			if !ok {
				continue
			}
			n, seen := order[key]
			if !seen {
				n = len(citations) + 1
				order[key] = n
				citations = append(citations, Citation{Source: src, Ref: n})
			}
			mark := fmt.Sprintf("[%d]", n)
			dup := false
			for _, x := range marks {
				dup = dup || x == mark
			}
			if !dup {
				marks = append(marks, mark)
			}
		}
		return strings.Join(marks, "")
	})
	text = repeated.ReplaceFunc(text, func(m *jsre.Match) string {
		g, _ := m.Group(1)
		return g
	})
	text = beforeEnd.ReplaceAllString(text, "$1")
	text = spaces.ReplaceAllString(text, " ")
	return strx.Trim(text), citations
}

// Deps are the ask job's gateway (Do nil is the real one) and clock (nil is
// time.Now).
type Deps struct {
	Do  gateway.Do
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

// Payload is the ask job's payload.
type Payload struct {
	Container     string
	Question      string
	IncludeChunks bool
}

// PayloadOf reads a job's payload.
func PayloadOf(j jobs.Job) Payload {
	inc, _ := j.Payload.Get("includeChunks")
	return Payload{Container: j.PayloadString("container"), Question: j.PayloadString("question"), IncludeChunks: jsonx.Truthy(inc)}
}

// Answer is AskAnswer: what the job returns.
type Answer struct {
	Answer    string       `json:"answer"`
	Covered   bool         `json:"covered"`
	Citations []Citation   `json:"citations"`
	Model     *string      `json:"model"`
	People    []people.Ref `json:"people"`
}

type candidate struct {
	id, text, kind string
	createdAt      int64
	event, due     *dates.Dated
}

// AnswerQuestion is answerQuestion. The workspace is the job's: search and
// profile never leave it. ctx is the job's abort.
func AnswerQuestion(ctx context.Context, d *db.DB, workspaceID string, in Payload, deps Deps) (Answer, error) {
	limit := Memories
	memoryHits, err := api.Search(d, workspaceID, api.SearchInput{Container: in.Container, Q: in.Question, Limit: &limit})
	if err != nil {
		return Answer{}, err
	}
	var chunkHits []api.SearchHit
	if in.IncludeChunks {
		cl := Chunks
		res, err := api.Search(d, workspaceID, api.SearchInput{Container: in.Container, Q: in.Question, Limit: &cl, Include: []string{"chunks"}})
		if err != nil {
			return Answer{}, err
		}
		chunkHits = res.Results
	}
	// "What do I know about Clara?": the facts on Clara's person page come
	// first, whether or not search ranks them, and her card goes in as
	// background.
	named, err := people.NamedIn(d, search.Scope{WorkspaceID: workspaceID, Container: in.Container}, in.Question)
	if err != nil {
		return Answer{}, err
	}
	var personHits []people.Fact
	for _, p := range named {
		fs, err := people.Facts(d, workspaceID, p.ID, false, PersonFacts)
		if err != nil {
			return Answer{}, err
		}
		personHits = append(personHits, fs...)
	}
	if len(memoryHits.Results) == 0 && len(chunkHits) == 0 && len(personHits) == 0 {
		return Answer{Answer: NothingStored, Citations: []Citation{}, People: []people.Ref{}}, nil
	}

	sources := map[string]Source{}
	seen := map[string]bool{}
	var candidates []candidate
	for _, f := range personHits {
		if !seen[f.ID] {
			seen[f.ID] = true
			candidates = append(candidates, candidate{f.ID, f.Fact, f.Kind, f.CreatedAt, dates.FromColumns(f.EventDate, f.EventPrecision), dates.FromColumns(f.DueDate, f.DuePrecision)})
		}
	}
	for _, h := range memoryHits.Results {
		if h.Type == "memory" && !seen[h.ID] {
			seen[h.ID] = true
			candidates = append(candidates, candidate{h.ID, h.Text, h.Kind, h.CreatedAt, dates.FromColumns(h.EventDate, h.EventPrecision), dates.FromColumns(h.DueDate, h.DuePrecision)})
		}
	}
	now := deps.now()
	today := dates.ISODate(now.UnixMilli())
	facts := make([]gateway.AskFact, len(candidates))
	for i, c := range candidates {
		ref := fmt.Sprintf("m%d", i+1)
		sources[ref] = Source{Type: "memory", ID: c.id, Text: c.text}
		f := gateway.AskFact{Ref: ref, Text: c.text, Kind: c.kind, Date: dates.ISODate(c.createdAt)}
		if c.event != nil {
			f.Happened = c.event.String()
		}
		if c.due != nil {
			due := c.due.String()
			f.Due = due
			// "2026-11" passes once the month is over: compare at the date's own precision.
			f.DuePassed = due < today[:min(len(due), len(today))]
		}
		facts[i] = f
	}
	var excerpts []gateway.AskExcerpt
	for i, h := range chunkHits {
		if h.Type != "chunk" {
			continue
		}
		ref := fmt.Sprintf("c%d", i+1)
		sources[ref] = Source{Type: "chunk", ID: h.ID, Text: h.Text, DocumentID: h.DocumentID, DocumentTitle: h.DocumentTitle}
		text := h.Text
		if strx.Len16(text) > excerptChars {
			text = strx.Slice16(text, excerptChars) + "..."
		}
		excerpts = append(excerpts, gateway.AskExcerpt{Ref: ref, Text: text, Title: h.DocumentTitle})
	}
	profile, err := memories.GetProfile(d, workspaceID, in.Container)
	if err != nil {
		return Answer{}, err
	}
	persons := make([]gateway.AskPerson, len(named))
	refs := make([]people.Ref, len(named))
	for i, p := range named {
		persons[i] = gateway.AskPerson{Name: p.Name, Aliases: p.Aliases, Role: p.Role, Organization: p.Organization, Summary: p.Summary, Contact: append(append([]string{}, p.Emails...), p.Handles...)}
		refs[i] = people.Ref{ID: p.ID, Name: p.Name}
	}
	data, model, err := gateway.ChatJSON(ctx, gateway.Request{
		System: gateway.AskSystem,
		Prompt: gateway.AskPrompt(gateway.AskInput{Question: in.Question, Facts: facts, Excerpts: excerpts, ProfileStatic: profile.Static, ProfileDyn: profile.Dynamic, Now: now, People: persons}),
	}, gateway.ParseAsk, deps.Do)
	if err != nil {
		return Answer{}, err
	}
	answer, citations := ResolveCitations(data.Answer, sources)
	return Answer{Answer: answer, Covered: data.Covered, Citations: citations, Model: &model, People: refs}, nil
}

// Handler is the `ask` job: it runs under an abort the waiting request can
// fire (see Ask).
func Handler(d *db.DB, deps Deps) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) (any, error) {
		ctx = jobs.JobAbortContext(ctx, j.ID)
		defer jobs.ReleaseJobAbort(j.ID)
		return AnswerQuestion(ctx, d, j.WorkspaceID, PayloadOf(j), deps)
	}
}

// Input is askInput after validation; IncludeChunks nil is undefined.
type Input struct {
	Container     string
	Question      string
	IncludeChunks *bool
}

// Response is AskResponse: the answer plus the container, the question and
// the total time.
type Response struct {
	Answer
	Container string
	Question  string
	TimingMs  int64
}

// MarshalJSON writes {container, question, ...answer, timingMs}.
func (r Response) MarshalJSON() ([]byte, error) {
	b, err := jsonx.Marshal(r.Answer)
	if err != nil {
		return nil, err
	}
	a, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	o := jsonx.ObjectOf("container", r.Container, "question", r.Question)
	for _, k := range a.Keys() {
		v, _ := a.Get(k)
		o.Set(k, v)
	}
	o.Set("timingMs", r.TimingMs)
	return jsonx.Marshal(o)
}

// Ask is ask: enqueue the job and wait for it. timeoutMs <= 0 is
// TimeoutMs. ctx is the caller: when it goes away the job is dropped or its
// gateway call aborted.
func Ask(ctx context.Context, d *db.DB, workspaceID string, in Input, timeoutMs float64) (Response, error) {
	if !config.Load().JobsEnabled {
		return Response{}, httpx.New(503, "jobs_disabled", "The job loop is off on this server (RECALL_JOBS=off), so nothing can answer.")
	}
	if timeoutMs <= 0 {
		timeoutMs = TimeoutMs
	}
	started := time.Now()
	include := in.IncludeChunks != nil && *in.IncludeChunks
	id, _, w, err := jobs.EnqueueWatched(d, workspaceID, jobs.Ask, jsonx.ObjectOf("container", in.Container, "question", in.Question, "includeChunks", include), time.Now().UnixMilli())
	if err != nil {
		return Response{}, err
	}
	v, err := w.Wait(ctx, timeoutMs)
	if err == nil {
		a, _ := v.(Answer)
		return Response{Answer: a, Container: in.Container, Question: in.Question, TimingMs: api.RoundMs(time.Since(started))}, nil
	}
	var wt *jobs.WaitTimeout
	if errors.As(err, &wt) {
		// Still queued: drop it. Already running: stop its gateway call.
		if ok, _ := jobs.CancelQueued(d, id, "timed out waiting for a free job slot", time.Now().UnixMilli()); !ok {
			jobs.AbortJob(id, "the caller timed out")
		}
		return Response{}, httpx.New(504, "timeout", fmt.Sprintf("No answer within %d s. The gateway is slow or Recall is busy extracting a large document. Try again in a minute.", int64(math.Floor(timeoutMs/1000+0.5))))
	}
	if ctx.Err() != nil {
		if ok, _ := jobs.CancelQueued(d, id, "the caller went away", time.Now().UnixMilli()); !ok {
			jobs.AbortJob(id, "the caller went away")
		}
		return Response{}, err
	}
	var ge *gateway.Error
	if errors.As(err, &ge) {
		return Response{}, httpx.New(502, "gateway_error", "The gateway failed: "+ge.Msg)
	}
	var ie *gateway.InvalidJSONError
	if errors.As(err, &ie) {
		return Response{}, httpx.New(502, "bad_answer", "The model's reply couldn't be read. Try asking again.")
	}
	// fetch() itself failed: the gateway is down or unreachable.
	if jserr.Name(err) == "TypeError" && strings.Contains(strx.Lower(err.Error()), "fetch failed") {
		return Response{}, httpx.New(502, "gateway_unreachable", "Couldn't reach the gateway. Check that it's running and GATEWAY_URL is right.")
	}
	return Response{}, err
}
