package apps

// Generic feed items: what any app can send without code in Recall.
//
//	document: one document per item. A changed item replaces its document, a
//	          deleted one removes it with its facts.
//	event:    a timestamped line in a group (a chat, a project, a log). Lines
//	          wait in app_connector_staging until their local day is over, then
//	          each group's day becomes one digest document that never changes.
//
// The contract is apps/site/content/docs/feeds.mdx.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	KindDocument = "document"
	KindEvent    = "event"

	// MaxDocumentChars caps a document item's content.
	MaxDocumentChars = 100_000
	// MaxDigestChars caps one group's day; lines past it are cut.
	MaxDigestChars = 30_000
	maxTitleChars  = 300
	maxGroupChars  = 200
	eventKeyPrefix = "ev:"
)

// IsGenericKind reports whether Recall handles the kind itself.
func IsGenericKind(kind string) bool { return kind == KindDocument || kind == KindEvent }

var isoDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// DocumentSourceID is the source_id of a document item.
func DocumentSourceID(provider, id string) string { return "feed:" + provider + ":" + id }

// EventDaySourceID is the source_id of one group's day.
func EventDaySourceID(provider, group, day string) string {
	return "feed:" + provider + ":" + group + ":" + day
}

// FeedDocument is a parsed document item; nil from ParseDocument means the
// item has nothing to keep.
type FeedDocument struct {
	Title, Content, Date, Fingerprint string
}

// ParseDocument reads a document item's data: title and content are
// required, date (YYYY-MM-DD) is optional.
func ParseDocument(it ChangeItem) *FeedDocument {
	if it.Deleted {
		return nil
	}
	title := strx.Trim(ostr(get(it.Data, "title")))
	content := strx.Trim(ostr(get(it.Data, "content")))
	if title == "" || content == "" {
		return nil
	}
	title = strx.Slice16(title, maxTitleChars)
	if strx.Len16(content) > MaxDocumentChars {
		content = strx.Slice16(content, MaxDocumentChars)
	}
	date := ostr(get(it.Data, "date"))
	if !isoDay.MatchString(date) {
		date = ""
	}
	if it.URL != "" {
		content += "\n\n" + it.URL
	}
	sum := sha256.Sum256([]byte(title + "\x00" + content + "\x00" + date))
	return &FeedDocument{Title: title, Content: content, Date: date, Fingerprint: hex.EncodeToString(sum[:16])}
}

// syncDocument keeps one document per item. backfillFrom ("" for none)
// skips old items never seen.
func syncDocument(q db.Q, c Connector, it ChangeItem, backfillFrom string, now int64) (removed, written bool, err error) {
	sid := DocumentSourceID(c.Provider, it.ID)
	existing, found, err := memories.FindBySourceID(q, c.WorkspaceID, c.Container, sid)
	if err != nil {
		return false, false, err
	}
	doc := ParseDocument(it)
	if doc == nil {
		if !found {
			return false, false, nil
		}
		removed, err = memories.DeleteDocumentAndMemories(q, c.WorkspaceID, existing.ID, now)
		return removed, false, err
	}
	if found {
		if fp, _ := get(existing.Metadata, "fingerprint").(string); fp == doc.Fingerprint {
			return false, false, nil
		}
		if removed, err = memories.DeleteDocumentAndMemories(q, c.WorkspaceID, existing.ID, now); err != nil {
			return false, false, err
		}
	} else if backfillFrom != "" && doc.Date != "" && doc.Date < backfillFrom {
		return false, false, nil
	}
	meta := jsonx.ObjectOf("source", c.Provider, "kind", KindDocument, "id", it.ID, "url", it.URL, "fingerprint", doc.Fingerprint)
	written, err = writeDoc(q, c, docInput{sid, doc.Title, doc.Content, doc.Date, meta}, now)
	return removed, written, err
}

// FeedEvent is one staged event line.
type FeedEvent struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
	At    string `json:"at"`
	From  string `json:"from,omitempty"`
	Text  string `json:"text"`
}

// ParseEvent reads an event item's data: group, title, at and text are
// required; note and from are optional.
func ParseEvent(it ChangeItem) (FeedEvent, bool) {
	d := it.Data
	ev := FeedEvent{
		ID: it.ID, Group: strx.Trim(ostr(get(d, "group"))), Title: strx.Trim(ostr(get(d, "title"))),
		Note: strx.Trim(ostr(get(d, "note"))), From: oneLine(ostr(get(d, "from"))), Text: strx.Trim(ostr(get(d, "text"))),
	}
	at, ok := oiso(get(d, "at"))
	if !ok || ev.Group == "" || ev.Title == "" || ev.Text == "" || strx.Len16(ev.Group) > maxGroupChars {
		return FeedEvent{}, false
	}
	ev.At = at
	ev.Title = strx.Slice16(ev.Title, maxTitleChars)
	return ev, true
}

// StageEvents stages a page's event items; returns how many were.
func StageEvents(q db.Q, c Connector, items []ChangeItem, ctx StageContext) (int, error) {
	staged := 0
	written := map[string]bool{}
	for _, it := range items {
		if it.Kind != KindEvent {
			continue
		}
		key := eventKeyPrefix + it.ID
		ev, ok := ParseEvent(it)
		if it.Deleted || !ok {
			if err := unstage(q, c, key, ""); err != nil {
				return staged, err
			}
			continue
		}
		day, ok := LocalDay(ev.At, ctx.Timezone)
		if !ok {
			continue
		}
		// The first sync only reaches back so far.
		if !ctx.CaughtUp && ctx.BackfillFrom != "" && day < ctx.BackfillFrom {
			continue
		}
		// That group's day is written already; it doesn't change.
		sid := EventDaySourceID(c.Provider, ev.Group, day)
		done, seen := written[sid]
		if !seen {
			_, found, err := memories.FindBySourceID(q, c.WorkspaceID, c.Container, sid)
			if err != nil {
				return staged, err
			}
			done, written[sid] = found, found
		}
		if done {
			continue
		}
		payload := jsonx.Stringify(ev)
		if _, err := q.Exec(`INSERT INTO app_connector_staging (connector_id, day, item_key, payload) VALUES (?, ?, ?, ?)
			ON CONFLICT (connector_id, item_key) DO UPDATE SET day = ?, payload = ? WHERE app_connector_staging.written_at IS NULL`, c.ID, day, key, payload, day, payload); err != nil {
			return staged, err
		}
		staged++
	}
	return staged, nil
}

func eventDays(q db.Q, c Connector, today string) ([]string, error) {
	rows, err := q.Query(`SELECT DISTINCT day FROM app_connector_staging WHERE connector_id = ? AND written_at IS NULL AND day < ? AND item_key LIKE 'ev:%' ORDER BY day`, c.ID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func dayEventLines(q db.Q, c Connector, day string) ([]FeedEvent, error) {
	rows, err := q.Query(`SELECT payload FROM app_connector_staging WHERE connector_id = ? AND day = ? AND written_at IS NULL AND item_key LIKE 'ev:%'`, c.ID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedEvent
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		var e FeedEvent
		if json.Unmarshal([]byte(p), &e) == nil && e.Group != "" {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

// EventGroupDay is one group's lines on one day, oldest first.
type EventGroupDay struct {
	Group, Title, Note string
	Events             []FeedEvent
}

// GroupEvents splits a day's lines by group, groups in title order.
func GroupEvents(events []FeedEvent) []EventGroupDay {
	by := map[string]*EventGroupDay{}
	var order []string
	for _, e := range events {
		g, ok := by[e.Group]
		if !ok {
			g = &EventGroupDay{Group: e.Group}
			by[e.Group] = g
			order = append(order, e.Group)
		}
		g.Events = append(g.Events, e)
	}
	out := make([]EventGroupDay, 0, len(order))
	for _, k := range order {
		g := by[k]
		sort.SliceStable(g.Events, func(i, j int) bool {
			if g.Events[i].At != g.Events[j].At {
				return g.Events[i].At < g.Events[j].At
			}
			return g.Events[i].ID < g.Events[j].ID
		})
		// The newest line names the group, so a renamed chat reads right.
		last := g.Events[len(g.Events)-1]
		g.Title, g.Note = last.Title, last.Note
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Group < out[j].Group
	})
	return out
}

// EventDigest is one group's day: its title and content.
func EventDigest(day string, g EventGroupDay, tz string) (string, string) {
	dateText := dates.Format(dates.Dated{Date: day, Precision: dates.Day})
	t, _ := time.Parse("2006-01-02", day)
	head := fmt.Sprintf("%s on %s, %s (%s time). %s.", g.Title, t.Weekday(), dateText, tz, plural(len(g.Events), "entry", "entries"))
	if g.Note != "" {
		head += " " + oneLine(g.Note)
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n")
	size := strx.Len16(head) + 1
	for i, e := range g.Events {
		line := "\n" + localTime(e.At, tz) + " "
		if e.From != "" {
			line += e.From + ": "
		}
		line += oneLine(e.Text)
		n := strx.Len16(line)
		if size+n > MaxDigestChars {
			b.WriteString(fmt.Sprintf("\n(%s cut for length.)", plural(len(g.Events)-i, "more entry", "more entries")))
			break
		}
		b.WriteString(line)
		size += n
	}
	return g.Title + " on " + dateText, b.String()
}

// closeEventDays writes one digest per group for every day that is over.
// It only touches event rows, so it runs before the older per-app closers.
func closeEventDays(d *db.DB, c Connector, tz string, now int64) (int, error) {
	days, err := eventDays(d, c, dates.TodayIn(tz, now))
	if err != nil {
		return 0, err
	}
	written := 0
	for _, day := range days {
		err := d.Tx(func(tx db.Q) error {
			events, err := dayEventLines(tx, c, day)
			if err != nil {
				return err
			}
			for _, g := range GroupEvents(events) {
				title, content := EventDigest(day, g, tz)
				meta := jsonx.ObjectOf("source", c.Provider, "kind", "event_day", "group", g.Group, "day", day, "entries", len(g.Events))
				ok, err := writeDoc(tx, c, docInput{EventDaySourceID(c.Provider, g.Group, day), title, content, day, meta}, now)
				if err != nil {
					return err
				}
				if ok {
					written++
				}
			}
			_, err = tx.Exec(`DELETE FROM app_connector_staging WHERE connector_id = ? AND day = ? AND item_key LIKE 'ev:%'`, c.ID, day)
			return err
		})
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
