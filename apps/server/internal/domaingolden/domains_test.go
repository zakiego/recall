// Package domaingolden replays testdata/domains.json, the scripted session
// testdata/gen/domains-goldens.ts ran through the TS domain code: memories,
// dated facts and upcoming, people, ask, the dates backfill and reminders.
// Each step's result, gateway requests, emails and table rows must equal the
// TS run's. It imports every domain package, so it lives on its own.
package domaingolden_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/backfill"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/email"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
	"github.com/zakiego/recall/apps/server/internal/users"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

type golden struct {
	WorkspaceID string            `json:"workspaceId"`
	Created     int64             `json:"created"`
	AskNow      string            `json:"askNow"`
	Dumps       [][2]string       `json:"dumps"`
	Pinned      []string          `json:"pinned"`
	Vectors     map[string]string `json:"vectors"`
	Steps       []struct {
		Step     map[string]any `json:"step"`
		Result   any            `json:"result"`
		Requests []string       `json:"requests"`
		Emails   []any          `json:"emails"`
		Rows     [][2]any       `json:"rows"`
	} `json:"steps"`
}

const W2 = "ws_other00000000000"

// norm is the generator's norm on JSON text: ids become prefix_N in order
// of first appearance, clock values the run wrote become "<now>", and a
// list cursor is shown decoded.
type norm struct {
	start  int64
	seen   map[string]string
	counts map[string]int
}

var (
	idPattern     = regexp.MustCompile(`\b(mem|per)_[0-9a-z]{16}\b`)
	clockPattern  = regexp.MustCompile(`"(createdAt|updatedAt|validFrom|validTo|lastMentionedAt|sentAt)":(-?[0-9.e+]+)`)
	cursorPattern = regexp.MustCompile(`"nextCursor":"([A-Za-z0-9_-]+)"`)
)

func (n *norm) ids(s string) string {
	return idPattern.ReplaceAllStringFunc(s, func(id string) string {
		if v, ok := n.seen[id]; ok {
			return v
		}
		prefix := id[:strings.IndexByte(id, '_')]
		n.counts[prefix]++
		v := fmt.Sprintf("%s_%d", prefix, n.counts[prefix])
		n.seen[id] = v
		return v
	})
}

// value normalizes v's JSON and decodes it for comparison.
func (n *norm) value(t *testing.T, v any) any {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s := clockPattern.ReplaceAllStringFunc(string(b), func(m string) string {
		p := clockPattern.FindStringSubmatch(m)
		if f, err := strconv.ParseFloat(p[2], 64); err == nil && f >= float64(n.start) {
			return `"` + p[1] + `":"<now>"`
		}
		return m
	})
	s = cursorPattern.ReplaceAllStringFunc(s, func(m string) string {
		raw, err := base64.RawURLEncoding.DecodeString(cursorPattern.FindStringSubmatch(m)[1])
		if err != nil {
			t.Fatal(err)
		}
		return `"nextCursor":` + jsonx.Stringify("cursor("+string(raw)+")")
	})
	var out any
	if err := json.Unmarshal([]byte(n.ids(s)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rowsOf(t *testing.T, d *db.DB, query string) []any {
	t.Helper()
	rows, err := d.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return out
}

func remarshal(t *testing.T, from, to any) {
	t.Helper()
	b, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, to); err != nil {
		t.Fatal(err)
	}
}

// datePatch reads a step's event/due: absent keeps, null clears.
func datePatch(t *testing.T, p map[string]any, key string) memories.DateValue {
	v, ok := p[key]
	if !ok {
		return memories.DateValue{}
	}
	if v == nil {
		return memories.Value(nil)
	}
	var d dates.Dated
	remarshal(t, v, &d)
	return memories.Value(&d)
}

func optStr(p map[string]any, key string) *string {
	if v, ok := p[key].(string); ok {
		return &v
	}
	return nil
}

func TestDomainSessionMatchesTS(t *testing.T) {
	var g golden
	testutil.LoadJSON(t, "domains.json", &g)
	t.Setenv("EXTRACT_MODEL", "claude-haiku-5-5")
	t.Setenv("FALLBACK_MODEL", "claude-sonnet-5-5")
	d := testkit.OpenDB(t)
	W := g.WorkspaceID
	start := time.Now().UnixMilli()
	n := &norm{start: start, seen: map[string]string{}, counts: map[string]int{}}

	// Every embedding comes from the TS run.
	embed.Override = func(texts []string, kind embed.Kind) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, text := range texts {
			b64, ok := g.Vectors[string(kind)+":"+text]
			if !ok {
				return nil, fmt.Errorf("the TS run never embedded %s %q", kind, text)
			}
			raw, _ := base64.StdEncoding.DecodeString(b64)
			v := make([]float32, len(raw)/4)
			for k := range v {
				v[k] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*k:]))
			}
			out[i] = v
		}
		return out, nil
	}
	t.Cleanup(func() { embed.Override = nil })

	var mu sync.Mutex
	var queue, requests []string
	do := func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.String() != "http://gateway.test/v1/chat/completions" {
			return nil, fmt.Errorf("network access refused: %s", r.URL)
		}
		requests = append(requests, string(b))
		if len(queue) == 0 {
			return nil, fmt.Errorf("no scripted reply left")
		}
		text := queue[0]
		queue = queue[1:]
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(testkit.SSE(text)))}, nil
	}

	var emails []any
	sendFails := false
	remDeps := func(now string) reminders.Deps {
		at, err := time.Parse(time.RFC3339, now)
		if err != nil {
			t.Fatal(err)
		}
		return reminders.Deps{
			Configured: func() bool { return true },
			Now:        func() time.Time { return at },
			Send: func(_ context.Context, m email.Message) (string, error) {
				e := jsonx.ObjectOf("to", m.To, "subject", m.Subject, "text", m.Text, "html", m.HTML)
				if m.IdempotencyKey != "" {
					e.Set("idempotencyKey", m.IdempotencyKey)
				}
				emails = append(emails, e)
				if sendFails {
					return "", fmt.Errorf("Resend 500: down")
				}
				return fmt.Sprintf("re_%d", len(emails)), nil
			},
		}
	}

	current := func(fact, ws string) string {
		var id string
		if err := d.QueryRow(`SELECT id FROM memories WHERE fact = ? AND workspace_id = ? AND valid_to IS NULL`, fact, ws).Scan(&id); err != nil {
			t.Fatalf("no current memory %q: %v", fact, err)
		}
		return id
	}
	person := func(name string) string {
		var id string
		if err := d.QueryRow(`SELECT id FROM people WHERE name = ? AND workspace_id = ?`, name, W).Scan(&id); err != nil {
			t.Fatalf("no person %q: %v", name, err)
		}
		return id
	}
	idsIn := func(container string, currentOnly bool) []string {
		q := `SELECT id FROM memories WHERE workspace_id = ? AND container = ?`
		if currentOnly {
			q += ` AND valid_to IS NULL`
		}
		rows, err := d.Query(q+` ORDER BY rowid`, W, container)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			out = append(out, id)
		}
		return out
	}
	cursors := map[string]string{}
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	last := map[string]any{}
	for i, gs := range g.Steps {
		step := gs.Step
		label := fmt.Sprintf("step %d (%s)", i, step["op"])
		mu.Lock()
		requests = nil
		if rs, ok := step["replies"].([]any); ok {
			for _, r := range rs {
				queue = append(queue, r.(string))
			}
		}
		mu.Unlock()
		emails = nil
		ws := W
		if s, ok := step["workspace"].(string); ok {
			ws = s
		}
		now := time.Now().UnixMilli()
		var result any
		switch step["op"] {
		case "setup":
			_, err := users.CreateWorkspace(d, "Zaki", W, "zaki")
			must(err)
			_, err = users.CreateWorkspace(d, "Other", W2, "other")
			must(err)
			for _, u := range [][4]string{{"usr_zaki", "zaki", W, "Zaki Munziri"}, {"usr_rina", "rina", W2, "Rina"}} {
				row, err := users.Insert(d, users.NewUser{Username: u[1], PasswordHash: "x", Name: u[3]})
				must(err)
				_, err = d.Exec(`PRAGMA foreign_keys = OFF`)
				must(err)
				_, err = d.Exec(`UPDATE user SET id = ? WHERE id = ?`, u[0], row.ID)
				must(err)
				_, err = d.Exec(`UPDATE account SET user_id = ?, account_id = ? WHERE user_id = ?`, u[0], u[0], row.ID)
				must(err)
				_, err = d.Exec(`PRAGMA foreign_keys = ON`)
				must(err)
				_, err = users.AddMember(d, u[2], u[0], users.Owner)
				must(err)
			}
		case "add":
			in := step["input"].(map[string]any)
			nm := memories.NewMemory{WorkspaceID: ws, Container: in["container"].(string), Fact: in["fact"].(string), Keywords: in["keywords"].(string), Kind: in["kind"].(string)}
			if v := datePatch(t, in, "event"); v.Set {
				nm.Event = v.Value
			}
			if v := datePatch(t, in, "due"); v.Set {
				nm.Due = v.Value
			}
			m, err := memories.AddMemoryDirect(d, nm, now)
			must(err)
			result = m
		case "list":
			o := step["opts"].(map[string]any)
			lo := memories.ListOptions{WorkspaceID: W, Container: o["container"].(string)}
			if v, ok := o["limit"].(float64); ok {
				l := int(v)
				lo.Limit = &l
			}
			lo.Kind, _ = o["kind"].(string)
			lo.History, _ = o["history"].(bool)
			lo.Review, _ = o["review"].(bool)
			key := fmt.Sprint(o["container"], o["limit"], o["kind"], o["history"], o["review"])
			if _, ok := o["page"]; ok {
				lo.Cursor = cursors[key]
			}
			list, next, err := memories.ListMemories(d, lo)
			must(err)
			cursors[key] = ""
			if next != nil {
				cursors[key] = *next
			}
			result = jsonx.ObjectOf("memories", list, "nextCursor", next)
		case "edit":
			p := step["patch"].(map[string]any)
			patch := memories.Patch{Fact: optStr(p, "fact"), Keywords: optStr(p, "keywords"), Kind: optStr(p, "kind"), Event: datePatch(t, p, "event"), Due: datePatch(t, p, "due")}
			m, ok, err := memories.EditMemory(d, W, current(step["fact"].(string), W), patch, func() int64 { return time.Now().UnixMilli() })
			must(err)
			if ok {
				result = m
			}
		case "forget":
			m, ok, err := memories.ForgetMemory(d, W, current(step["fact"].(string), W), now)
			must(err)
			if ok {
				result = m
			}
		case "sql":
			_, err := d.Exec(step["sql"].(string))
			must(err)
		case "review":
			c, err := memories.ReviewCount(d, W, step["container"].(string))
			must(err)
			result = c
		case "chain":
			c, err := memories.Chain(d, W, current(step["fact"].(string), W), 500)
			must(err)
			result = c
		case "versions":
			c, err := memories.VersionCounts(d, W, idsIn(step["container"].(string), true))
			must(err)
			result = c
		case "upcoming":
			r := step["range"].(map[string]any)
			rng := memories.UpcomingRange{WorkspaceID: W, From: r["from"].(string), To: r["to"].(string)}
			rng.Container, _ = r["container"].(string)
			var limit *int
			if v, ok := step["limit"].(float64); ok {
				l := int(v)
				limit = &l
			}
			list, err := memories.ListUpcoming(d, rng, limit)
			must(err)
			cnt, err := memories.CountUpcoming(d, rng)
			must(err)
			result = jsonx.ObjectOf("list", list, "count", cnt)
		case "link":
			var list []people.Mention
			for _, raw := range step["mentions"].([]any) {
				m := raw.(map[string]any)
				mm := m["mention"].(map[string]any)
				pm := gateway.PersonMention{Name: mm["name"].(string), Aliases: []string{}, Facts: []string{}, Role: optStr(mm, "role"), Organization: optStr(mm, "organization"), Email: optStr(mm, "email"), Handle: optStr(mm, "handle"), Summary: optStr(mm, "summary")}
				for _, a := range mm["aliases"].([]any) {
					pm.Aliases = append(pm.Aliases, a.(string))
				}
				var ids []string
				for _, f := range m["facts"].([]any) {
					ids = append(ids, current(f.(string), W))
				}
				list = append(list, people.Mention{Mention: pm, MemoryIDs: ids})
			}
			st, err := people.LinkMentions(d, search.Scope{WorkspaceID: W, Container: step["container"].(string)}, list)
			must(err)
			result = st
		case "people":
			o := step["opts"].(map[string]any)
			lo := people.ListOptions{WorkspaceID: W, Container: o["container"].(string)}
			lo.Q, _ = o["q"].(string)
			lo.Sort, _ = o["sort"].(string)
			if v, ok := o["limit"].(float64); ok {
				l := int(v)
				lo.Limit = &l
			}
			if v, ok := o["offset"].(float64); ok {
				l := int(v)
				lo.Offset = &l
			}
			r, err := people.List(d, lo)
			must(err)
			result = r
		case "person":
			hist, _ := step["history"].(bool)
			p, ok, err := people.Get(d, W, person(step["name"].(string)), hist)
			must(err)
			if ok {
				result = p
			}
		case "forMemories":
			r, err := people.ForMemories(d, W, idsIn(step["container"].(string), false))
			must(err)
			result = r
		case "namedIn":
			r, err := people.NamedIn(d, search.Scope{WorkspaceID: W, Container: step["container"].(string)}, step["text"].(string))
			must(err)
			result = r
		case "updatePerson":
			p := step["patch"].(map[string]any)
			var patch people.Patch
			if v, ok := p["role"]; ok {
				patch.Role = people.OptString{Set: true, Value: optStr(map[string]any{"v": v}, "v")}
			}
			list := func(k string) *[]string {
				v, ok := p[k].([]any)
				if !ok {
					return nil
				}
				l := []string{}
				for _, s := range v {
					l = append(l, s.(string))
				}
				return &l
			}
			patch.Aliases, patch.Handles = list("aliases"), list("handles")
			r, ok, err := people.Update(d, W, person(step["name"].(string)), patch)
			must(err)
			if ok {
				result = r
			}
		case "suggest":
			r, err := people.SuggestMerges(d, search.Scope{WorkspaceID: W, Container: step["container"].(string)}, 0)
			must(err)
			result = r
		case "merge":
			r, err := people.Merge(d, W, person(step["keep"].(string)), person(step["drop"].(string)))
			must(err)
			result = r
		case "deletePerson":
			ok, err := people.Delete(d, W, person(step["name"].(string)))
			must(err)
			result = ok
		case "ask":
			in := step["input"].(map[string]any)
			at, _ := time.Parse(time.RFC3339, g.AskNow)
			inc, _ := in["includeChunks"].(bool)
			a, err := ask.AnswerQuestion(ctx, d, W, ask.Payload{Container: in["container"].(string), Question: in["question"].(string), IncludeChunks: inc}, ask.Deps{Do: do, Now: func() time.Time { return at }})
			must(err)
			result = a
		case "undated":
			c, err := backfill.CountUndated(d, W, step["container"].(string))
			must(err)
			result = c
		case "backfill":
			r, err := backfill.RunBatch(ctx, d, W, backfill.Payload{}, do)
			must(err)
			result = r
		case "settings":
			s, err := reminders.GetSettings(d, step["user"].(string))
			must(err)
			result = s
		case "saveSettings":
			in := step["input"].(map[string]any)
			si := reminders.SettingsInput{Timezone: in["timezone"].(string), LeadDays: []float64{}}
			si.Enabled, _ = in["enabled"].(bool)
			si.Email = optStr(in, "email")
			for _, l := range in["leadDays"].([]any) {
				si.LeadDays = append(si.LeadDays, l.(float64))
			}
			s, err := reminders.SaveSettings(d, step["user"].(string), si)
			must(err)
			result = s
		case "sendFails":
			sendFails = step["value"].(bool)
		case "remind":
			logs := []string{}
			run, err := reminders.RunReminders(ctx, d, remDeps(step["now"].(string)), func(m string) { logs = append(logs, m) })
			must(err)
			result = jsonx.ObjectOf("run", run, "logs", logs)
		case "sendTest":
			r, err := reminders.SendTest(ctx, d, step["user"].(string), remDeps(step["now"].(string)))
			must(err)
			result = r
		default:
			t.Fatalf("unknown op %v", step["op"])
		}
		if len(queue) > 0 {
			t.Fatalf("%s left %d replies", label, len(queue))
		}
		for _, table := range g.Pinned {
			_, err := d.Exec(`UPDATE `+table+` SET created_at = ? WHERE created_at >= ?`, g.Created+int64(i)*1000, start)
			must(err)
		}

		// The generator's order: result, requests, emails, rows.
		var got any
		if result != nil {
			got = n.value(t, result)
		}
		if !reflect.DeepEqual(got, gs.Result) {
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(gs.Result)
			t.Fatalf("%s result\n got %s\nwant %s", label, gb, wb)
		}
		mu.Lock()
		reqs := append([]string(nil), requests...)
		mu.Unlock()
		if len(reqs) != len(gs.Requests) {
			t.Fatalf("%s: %d gateway requests, want %d", label, len(reqs), len(gs.Requests))
		}
		for k := range reqs {
			if got := n.ids(reqs[k]); got != gs.Requests[k] {
				t.Fatalf("%s request %d\n got %s\nwant %s", label, k, got, gs.Requests[k])
			}
		}
		gotEmails := n.value(t, append([]any{}, emails...))
		if !reflect.DeepEqual(gotEmails, n.value(t, gs.Emails)) {
			gb, _ := json.Marshal(gotEmails)
			wb, _ := json.Marshal(gs.Emails)
			t.Fatalf("%s emails\n got %s\nwant %s", label, gb, wb)
		}
		for k, dump := range g.Dumps {
			name, query := dump[0], dump[1]
			raw := rowsOf(t, d, query)
			b, _ := jsonx.Marshal(raw)
			same := string(b) == fmt.Sprint(last[name])
			last[name] = string(b)
			want := gs.Rows[k][1]
			if want == nil {
				if !same {
					t.Fatalf("%s %s changed; the TS rows did not:\n got %s", label, name, n.ids(string(b)))
				}
				continue
			}
			var got any
			json.Unmarshal([]byte(n.ids(string(b))), &got)
			if !reflect.DeepEqual(got, want) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want)
				t.Fatalf("%s %s\n got %s\nwant %s", label, name, gb, wb)
			}
		}
	}
}
