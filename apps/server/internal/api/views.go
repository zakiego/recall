package api

import (
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
)

// objectOf is a value as a JSON object, to spread it like {...v}.
func objectOf(v any) (*jsonx.Object, error) {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonx.ParseObject(b)
}

// ListInput is listInput.
type ListInput struct {
	Container string
	Cursor    string
	History   bool
	Review    bool
	Limit     *int
	Kind      string
}

// List is list: a page of memories, each with its version count.
func List(q db.Q, workspaceID string, in ListInput) (*jsonx.Object, error) {
	page, next, err := memories.ListMemories(q, memories.ListOptions{WorkspaceID: workspaceID, Container: in.Container, Cursor: in.Cursor, History: in.History, Limit: in.Limit, Kind: in.Kind, Review: in.Review})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(page))
	for i, m := range page {
		ids[i] = m.ID
	}
	counts, err := memories.VersionCounts(q, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	list := make([]any, len(page))
	for i, m := range page {
		o, err := objectOf(m)
		if err != nil {
			return nil, err
		}
		n, _ := counts.Get(m.ID)
		o.Set("versions", n+1)
		list[i] = o
	}
	return jsonx.ObjectOf("memories", list, "nextCursor", next), nil
}

// MaxUpcomingDays is MAX_UPCOMING_DAYS.
const MaxUpcomingDays = 366

// UpcomingInput is upcomingInput.
type UpcomingInput struct {
	Container string
	All       bool
	Days      int
	PastDays  int
	TZ        string
	Limit     *int
}

// Upcoming is upcoming: current facts due from PastDays ago to Days ahead,
// soonest first, each with daysLeft. now is ms.
func Upcoming(q db.Q, workspaceID string, in UpcomingInput, now int64) (*jsonx.Object, error) {
	today := dates.TodayIn(in.TZ, now)
	from := dates.AddDays(today, -in.PastDays)
	to := dates.AddDays(today, in.Days)
	rng := memories.UpcomingRange{WorkspaceID: workspaceID, From: from, To: to}
	if !in.All {
		rng.Container = in.Container
	}
	rows, err := memories.ListUpcoming(q, rng, in.Limit)
	if err != nil {
		return nil, err
	}
	list := make([]any, len(rows))
	for i, m := range rows {
		o, err := objectOf(m)
		if err != nil {
			return nil, err
		}
		// A due date that isn't YYYY-MM-DD gives NaN in TS: null.
		if n, ok := dates.DaysBetweenJS(today, *m.DueDate); ok {
			o.Set("daysLeft", n)
		} else {
			o.Set("daysLeft", nil)
		}
		list[i] = o
	}
	var container any = in.Container
	if in.All {
		container = nil
	}
	return jsonx.ObjectOf("container", container, "today", today, "from", from, "to", to, "memories", list), nil
}

// ProfileInput is the /v1/profile query.
type ProfileInput struct {
	Container string
	Q         string
}

// Profile is profile: the container's profile, and with Q the search
// results for it (limit 10).
func Profile(d *db.DB, workspaceID string, in ProfileInput) (*jsonx.Object, error) {
	p, err := memories.GetProfile(d, workspaceID, in.Container)
	if err != nil {
		return nil, err
	}
	o, err := objectOf(p)
	if err != nil {
		return nil, err
	}
	if in.Q == "" {
		return o, nil
	}
	limit := 10
	res, err := Search(d, workspaceID, SearchInput{Container: in.Container, Q: in.Q, Limit: &limit})
	if err != nil {
		return nil, err
	}
	o.Set("searchResults", res.Results)
	return o, nil
}

// PersonView is personView: what the API and MCP return for a person.
type PersonView struct {
	ID              string   `json:"id"`
	Container       string   `json:"container"`
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Role            *string  `json:"role"`
	Organization    *string  `json:"organization"`
	Emails          []string `json:"emails"`
	Handles         []string `json:"handles"`
	Summary         string   `json:"summary"`
	FactCount       int64    `json:"factCount"`
	LastMentionedAt *int64   `json:"lastMentionedAt"`
	Edited          bool     `json:"edited"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
}

// ViewOf is personView.
func ViewOf(p people.Row) PersonView {
	return PersonView{p.ID, p.Container, p.Name, p.Aliases, p.Role, p.Organization, p.Emails, p.Handles, p.Summary, p.FactCount, p.LastMentionedAt, p.Manual, p.CreatedAt, p.UpdatedAt}
}

// FactView is factView: reviewNote only when set.
func FactViewOf(f people.Fact) *jsonx.Object {
	o := jsonx.ObjectOf("id", f.ID, "fact", f.Fact, "kind", f.Kind, "current", f.ValidTo == nil, "createdAt", f.CreatedAt, "eventDate", f.EventDate, "dueDate", f.DueDate)
	if f.ReviewNote != nil && *f.ReviewNote != "" {
		o.Set("reviewNote", *f.ReviewNote)
	}
	o.Set("source", f.Source)
	return o
}

// PeopleInput is listPeopleInput.
type PeopleInput struct {
	Container string
	Q         string
	Sort      string
	Limit     *int
	Offset    *int
}

// PeopleResult is people's result.
type PeopleResult struct {
	Container  string       `json:"container"`
	People     []PersonView `json:"people"`
	Total      int          `json:"total"`
	NextOffset *int         `json:"nextOffset"`
}

// People is people.
func People(q db.Q, workspaceID string, in PeopleInput) (PeopleResult, error) {
	page, err := people.List(q, people.ListOptions{WorkspaceID: workspaceID, Container: in.Container, Q: in.Q, Sort: in.Sort, Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return PeopleResult{}, err
	}
	out := PeopleResult{Container: in.Container, People: []PersonView{}, Total: page.Total, NextOffset: page.NextOffset}
	for _, p := range page.People {
		out.People = append(out.People, ViewOf(p))
	}
	return out, nil
}

// PersonResult is person's result.
type PersonResult struct {
	Person PersonView      `json:"person"`
	Facts  []*jsonx.Object `json:"facts"`
}

// Person is person: 404 when the person isn't in the workspace.
func Person(q db.Q, workspaceID, id string, history bool) (PersonResult, error) {
	found, ok, err := people.Get(q, workspaceID, id, history)
	if err != nil {
		return PersonResult{}, err
	}
	if !ok {
		return PersonResult{}, httpx.New(404, "not_found", "no such person")
	}
	out := PersonResult{Person: ViewOf(found.Person), Facts: []*jsonx.Object{}}
	for _, f := range found.Facts {
		out.Facts = append(out.Facts, FactViewOf(f))
	}
	return out, nil
}

// DocumentView is a document without its content, plus contentLength
// (UTF-16 code units).
func DocumentView(doc model.Document) (*jsonx.Object, error) {
	o, err := objectOf(doc)
	if err != nil {
		return nil, err
	}
	o.Delete("content")
	n := 0
	if doc.Content != nil {
		n = utf16Len(*doc.Content)
	}
	o.Set("contentLength", n)
	return o, nil
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
