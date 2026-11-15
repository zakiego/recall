package httpapi

// fns.ts: session, workspaces, containers, memories, upcoming, reminders.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/email"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// The shared fields of fns.ts.
var (
	rpcContainer = zod.String().Trim().Min(1).Max(memories.MaxContainerLen)
	rpcID        = zod.String().Min(1).Max(64)
	rpcRole      = zod.Enum(users.Owner, users.Admin, users.MemberRole)
	// dateField: "2026-11-05", "2026-11" or "2026"; null clears, missing keeps.
	rpcDate = zod.Refined(zod.Optional(zod.Nullable(zod.String())), func(v any) bool {
		s, ok := v.(string)
		return !ok || dates.Parse(s) != nil
	}, "Use a full date, a month or a year.")
)

func toObject(v any) *jsonx.Object {
	o, err := jsonx.ToObject(v)
	if err != nil {
		panic(err)
	}
	return o
}

var tzCookie = regexp.MustCompile(`(?:^|;\s*)recall\.tz=([^;]+)`)

// viewerTimezone is viewerTimezone: the saved reminder zone, else the
// browser's from the recall.tz cookie, else UTC.
func viewerTimezone(r *http.Request, saved *reminders.Settings) string {
	if saved != nil && saved.UpdatedAt != nil {
		return saved.Timezone
	}
	m := tzCookie.FindStringSubmatch(r.Header.Get("cookie"))
	if m == nil {
		return "UTC"
	}
	tz, err := url.PathUnescape(m[1])
	if err != nil {
		return "UTC"
	}
	if !dates.IsTimezone(tz) {
		return "UTC"
	}
	return tz
}

func memoryObjects(list []model.Memory) []*jsonx.Object {
	out := make([]*jsonx.Object, len(list))
	for i, m := range list {
		out[i] = toObject(m)
	}
	return out
}

func init() {
	registerRPC(
		RPC{Name: "getSession", Method: "GET", Fn: func(c *Call) (any, error) {
			v, err := c.Viewer()
			if err != nil {
				return nil, err
			}
			var saved *reminders.Settings
			if v != nil {
				s, err := reminders.GetSettings(c.DB(), v.User.ID)
				if err != nil {
					return nil, err
				}
				saved = &s
			}
			n, err := users.Count(c.DB())
			if err != nil {
				return nil, err
			}
			var vv any
			if v != nil {
				vv = v
			}
			return jsonx.ObjectOf(
				"viewer", vv,
				"today", dates.TodayIn(viewerTimezone(c.R, saved), c.Now()),
				"configured", strx.Len16(config.Load().SessionSecret) >= 32,
				"hasUsers", n > 0,
				"defaultContainer", config.Load().DefaultContainer,
			), nil
		}},
		RPC{Name: "switchWorkspace", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			role, err := users.GetRole(c.DB(), v.User.ID, c.Str("id"))
			if err != nil {
				return nil, err
			}
			if role == "" {
				return nil, &viewer.Forbidden{Msg: "You're not a member of that workspace."}
			}
			id := c.Str("id")
			if err := auth.SetActiveWorkspace(c.DB(), v.SessionID, &id); err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("ok", true), nil
		}},
		RPC{Name: "newWorkspace", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("name", zod.String().Trim().Min(1).Max(80))), Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			var ws users.Workspace
			err = c.DB().Tx(func(tx db.Q) error {
				var err error
				if ws, err = users.CreateWorkspace(tx, c.Str("name"), "", ""); err != nil {
					return err
				}
				if _, err = users.AddMember(tx, ws.ID, v.User.ID, users.Owner); err != nil {
					return err
				}
				return auth.SetActiveWorkspace(tx, v.SessionID, &ws.ID)
			})
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("id", ws.ID, "name", ws.Name), nil
		}},
		RPC{Name: "getContainers", Method: "GET", Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			list, err := memories.ListContainers(c.DB(), m.WS)
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("containers", list, "defaultContainer", config.Load().DefaultContainer), nil
		}},
		RPC{Name: "getMemories", Method: "GET", Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("cursor", zod.Optional(zod.String())),
			zod.F("history", zod.Optional(zod.Bool())),
			zod.F("review", zod.Optional(zod.Bool())),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(200))),
		), Fn: rpcGetMemories},
		RPC{Name: "getChain", Method: "GET", Input: zod.Object(zod.F("id", rpcID)), Fn: rpcGetChain},
		RPC{Name: "updateMemory", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("id", rpcID),
			zod.F("fact", zod.String().Trim().Min(1).Max(500)),
			zod.F("keywords", zod.Optional(zod.String().Trim().Max(500))),
			zod.F("kind", zod.Optional(zod.Enum("static", "dynamic"))),
			zod.F("eventDate", rpcDate),
			zod.F("dueDate", rpcDate),
		), Fn: rpcUpdateMemory},
		RPC{Name: "resolveMemory", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			row, ok, err := memories.ResolveConflict(c.DB(), m.WS, c.Str("id"))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("This fact changed since you opened it. Reload to see the current version.")
			}
			return row, nil
		}},
		RPC{Name: "deleteMemory", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			row, ok, err := memories.ForgetMemory(c.DB(), m.WS, c.Str("id"), c.Now())
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("No such memory.")
			}
			return row, nil
		}},
		RPC{Name: "getUpcoming", Method: "GET", Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("days", zod.Int().Min(1).Max(366)),
			zod.F("all", zod.Optional(zod.Bool())),
		), Fn: rpcGetUpcoming},
		RPC{Name: "getReminders", Method: "GET", Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			s, err := reminders.GetSettings(c.DB(), v.User.ID)
			if err != nil {
				return nil, err
			}
			var from any
			if f := config.Load().EmailFrom; f != "" {
				from = f
			}
			return jsonx.ObjectOf("settings", s, "configured", email.Configured(), "from", from), nil
		}},
		RPC{Name: "saveReminders", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("enabled", zod.Bool()),
			zod.F("email", zod.Union(zod.Email("Enter an email address like you@example.com.").Max(254), zod.Literal(""))),
			zod.F("leadDays", zod.Array(zod.Int().Min(0).Max(reminders.MaxLeadDays)).Max(reminders.MaxLeads*2)),
			zod.F("timezone", zod.String().Max(64)),
		), Fn: rpcSaveReminders},
		RPC{Name: "sendReminderTest", Method: "POST", SameOrigin: true, Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			if wait := testSends.Take(v.User.ID, c.Now()); wait > 0 {
				return nil, rpcFail("That's a lot of test emails. Try again in " + strconv.Itoa((wait+59)/60) + " minutes.")
			}
			res, err := reminders.SendTest(context.WithoutCancel(c.R.Context()), c.DB(), v.User.ID, reminders.Deps{})
			if err != nil {
				return nil, rpcFail(err.Error())
			}
			return res, nil
		}},
	)
}

var testSends = ratelimit.New(5, 10*60_000)

func rpcGetMemories(c *Call) (any, error) {
	m, err := c.RequireMember(users.MemberRole)
	if err != nil {
		return nil, err
	}
	o := c.Obj()
	limit := 100
	if v, ok := c.Opt("limit"); ok {
		limit = int(v.(float64))
	}
	history, _ := o.Get("history")
	review, _ := o.Get("review")
	container := c.Str("container")
	list, next, err := memories.ListMemories(c.DB(), memories.ListOptions{WorkspaceID: m.WS, Container: container, Cursor: c.Str("cursor"), History: history == true, Review: review == true, Limit: &limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	counts, err := memories.VersionCounts(c.DB(), m.WS, ids)
	if err != nil {
		return nil, err
	}
	titles := map[string]*string{}
	for _, x := range list {
		if x.SourceDocumentID != nil {
			if _, ok := titles[*x.SourceDocumentID]; !ok {
				t, err := memories.GetDocumentTitle(c.DB(), m.WS, *x.SourceDocumentID)
				if err != nil {
					return nil, err
				}
				titles[*x.SourceDocumentID] = t
			}
		}
	}
	linked, err := people.ForMemories(c.DB(), m.WS, ids)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(list))
	for i, x := range list {
		o := toObject(x)
		n, _ := counts.Get(x.ID)
		o.Set("versions", n+1)
		var title any
		if x.SourceDocumentID != nil {
			if t := titles[*x.SourceDocumentID]; t != nil {
				title = *t
			}
		}
		o.Set("sourceTitle", title)
		var other any
		if x.ReviewNote != nil && *x.ReviewNote != "" && x.ConflictsWithID != nil {
			if om, ok, err := memories.GetMemory(c.DB(), m.WS, *x.ConflictsWithID); err != nil {
				return nil, err
			} else if ok {
				other = jsonx.ObjectOf("id", om.ID, "fact", om.Fact, "current", om.ValidTo == nil)
			}
		}
		o.Set("conflictWith", other)
		refs, ok := linked.Get(x.ID)
		if !ok {
			refs = []people.Ref{}
		}
		o.Set("people", refs)
		out[i] = o
	}
	rc, err := memories.ReviewCount(c.DB(), m.WS, container)
	if err != nil {
		return nil, err
	}
	profile, err := memories.GetProfile(c.DB(), m.WS, container)
	if err != nil {
		return nil, err
	}
	return jsonx.ObjectOf("memories", out, "nextCursor", next, "reviewCount", rc, "profile", profile), nil
}

func rpcGetChain(c *Call) (any, error) {
	m, err := c.RequireMember(users.MemberRole)
	if err != nil {
		return nil, err
	}
	chain, err := memories.Chain(c.DB(), m.WS, c.Str("id"), 500)
	if err != nil {
		return nil, err
	}
	first := 1.0
	if len(chain) > 0 {
		counts, err := memories.VersionCounts(c.DB(), m.WS, []string{chain[0].ID})
		if err != nil {
			return nil, err
		}
		n, _ := counts.Get(chain[0].ID)
		first = n + 1
	}
	hasNewer := false
	if len(chain) > 0 {
		last := chain[len(chain)-1]
		if last.ValidTo != nil {
			if hasNewer, err = memories.HasNewerVersion(c.DB(), m.WS, last.ID); err != nil {
				return nil, err
			}
		}
	}
	versions := make([]any, len(chain))
	for i, x := range chain {
		o := toObject(x)
		var title any
		if x.SourceDocumentID != nil {
			t, err := memories.GetDocumentTitle(c.DB(), m.WS, *x.SourceDocumentID)
			if err != nil {
				return nil, err
			}
			if t != nil {
				title = *t
			}
		}
		o.Set("sourceTitle", title)
		versions[i] = o
	}
	return jsonx.ObjectOf("firstVersion", first, "hasNewer", hasNewer, "versions", versions), nil
}

func dateOpt(c *Call, k string) memories.DateValue {
	v, ok := c.Opt(k)
	if !ok {
		return memories.DateValue{}
	}
	s, isStr := v.(string)
	if !isStr {
		return memories.Value(nil)
	}
	return memories.Value(dates.Parse(s))
}

func rpcUpdateMemory(c *Call) (any, error) {
	m, err := c.RequireMember(users.MemberRole)
	if err != nil {
		return nil, err
	}
	fact := c.Str("fact")
	p := memories.Patch{Fact: &fact, Event: dateOpt(c, "eventDate"), Due: dateOpt(c, "dueDate")}
	if v, ok := c.Opt("keywords"); ok {
		s := v.(string)
		p.Keywords = &s
	}
	if v, ok := c.Opt("kind"); ok {
		s := v.(string)
		p.Kind = &s
	}
	row, ok, err := memories.EditMemory(c.DB(), m.WS, c.Str("id"), p, c.S.Now)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, rpcFail("This fact changed since you opened it. Reload to edit the current version.")
	}
	return row, nil
}

// upcomingWindows is UPCOMING_WINDOWS; pastDays is PAST_DAYS.
var upcomingWindows = []int{7, 30, 90, 365}

const pastDays = 30

func rpcGetUpcoming(c *Call) (any, error) {
	m, err := c.RequireMember(users.MemberRole)
	if err != nil {
		return nil, err
	}
	settings, err := reminders.GetSettings(c.DB(), m.Viewer.User.ID)
	if err != nil {
		return nil, err
	}
	all, _ := c.Obj().Get("all")
	container := c.Str("container")
	days, _ := c.Obj().Get("days")
	res, err := api.Upcoming(c.DB(), m.WS, api.UpcomingInput{Container: container, All: all == true, Days: int(days.(float64)), PastDays: pastDays, TZ: viewerTimezone(c.R, &settings)}, c.Now())
	if err != nil {
		return nil, err
	}
	today, _ := res.Get("today")
	from, _ := res.Get("from")
	to, _ := res.Get("to")
	rng := memories.UpcomingRange{WorkspaceID: m.WS}
	if all != true {
		rng.Container = container
	}
	counts := jsonx.NewMap[any]()
	for _, d := range upcomingWindows {
		r := rng
		r.From, r.To = today.(string), dates.AddDays(today.(string), d)
		n, err := memories.CountUpcoming(c.DB(), r)
		if err != nil {
			return nil, err
		}
		counts.Set(strconv.Itoa(d), n)
	}
	list, _ := res.Get("memories")
	items, _ := list.([]any)
	ids := make([]string, len(items))
	for i, it := range items {
		v, _ := it.(*jsonx.Object).Get("id")
		ids[i] = v.(string)
	}
	vc, err := memories.VersionCounts(c.DB(), m.WS, ids)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		o := it.(*jsonx.Object)
		id, _ := o.Get("id")
		n, _ := vc.Get(id.(string))
		o.Set("versions", n+1)
	}
	r := rng
	r.From, r.To = from.(string), to.(string)
	total, err := memories.CountUpcoming(c.DB(), r)
	if err != nil {
		return nil, err
	}
	return jsonx.ObjectOf(
		"today", today,
		"memories", items,
		"counts", counts,
		"total", total,
		"pastDays", pastDays,
		"reminders", jsonx.ObjectOf("enabled", settings.Enabled, "email", settings.Email, "leadDays", settings.LeadDays, "timezone", settings.Timezone, "configured", email.Configured()),
	), nil
}

func rpcSaveReminders(c *Call) (any, error) {
	v, err := c.RequireUser()
	if err != nil {
		return nil, err
	}
	enabled, _ := c.Obj().Get("enabled")
	mail := c.Str("email")
	leadsV, _ := c.Obj().Get("leadDays")
	leads := []float64{}
	for _, x := range leadsV.([]any) {
		leads = append(leads, x.(float64))
	}
	tz := c.Str("timezone")
	if enabled == true && mail == "" {
		return nil, rpcFail("Add an email address to turn reminders on.")
	}
	if enabled == true && len(leads) == 0 {
		return nil, rpcFail("Pick at least one time to be reminded.")
	}
	if !dates.IsTimezone(tz) {
		return nil, rpcFail("Pick a time zone from the list.")
	}
	in := reminders.SettingsInput{Enabled: enabled == true, LeadDays: leads, Timezone: tz}
	if mail != "" {
		in.Email = &mail
	}
	return reminders.SaveSettings(c.DB(), v.User.ID, in)
}
