package apps

// oslo.ts: Oslo's feed to one digest document per local day (tasks
// completed, projects created or completed, each with its link). A day is
// written only after it is over; until then its events wait in
// app_connector_staging.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zone"
)

// DefaultTimezone is the day boundary when nothing else says.
const DefaultTimezone = "Asia/Makassar"

// OsloEvent is one line of a digest; At is ISO.
type OsloEvent struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	At      string `json:"at"`
	Project string `json:"project,omitempty"`
}

// OsloSourceID is osloSourceId.
func OsloSourceID(day string) string { return "oslo:day:" + day }

func zoneOf(tz string) *time.Location {
	if !dates.IsTimezone(tz) {
		tz = DefaultTimezone
	}
	// Intl's reading of the name: any case, and offsets like "+08:00".
	return zone.MustLoad(tz)
}

// LocalDay is localDay: the calendar date of an instant in a time zone.
func LocalDay(iso, tz string) (string, bool) {
	t, ok := dates.JSParse(iso)
	if !ok {
		return "", false
	}
	return time.UnixMilli(t).In(zoneOf(tz)).Format("2006-01-02"), true
}

func localTime(iso, tz string) string {
	t, _ := dates.JSParse(iso)
	return time.UnixMilli(t).In(zoneOf(tz)).Format("15:04")
}

func ostr(v any) string {
	if s, ok := v.(string); ok {
		return strx.Trim(s)
	}
	return ""
}

func oiso(v any) (string, bool) {
	s := ostr(v)
	if s == "" {
		return "", false
	}
	_, ok := dates.JSParse(s)
	return s, ok
}

// --- staging -----------------------------------------------------------------------

// StageContext is the staging context: the time zone, whether the feed was
// read to the end before, the backfill window's first day ("" for none).
type StageContext struct {
	Timezone     string
	CaughtUp     bool
	BackfillFrom string
	Now          int64
}

func dayWritten(q db.Q, c Connector, day string) (bool, error) {
	_, found, err := memories.FindBySourceID(q, c.WorkspaceID, c.Container, OsloSourceID(day))
	return found, err
}

// unstage drops an item's unwritten events, except keep.
func unstage(q db.Q, c Connector, prefix, keep string) error {
	query := `DELETE FROM app_connector_staging WHERE connector_id = ? AND substr(item_key, 1, ?) = ? AND written_at IS NULL`
	args := []any{c.ID, strx.Len16(prefix), prefix}
	if keep != "" {
		query += ` AND item_key <> ?`
		args = append(args, keep)
	}
	_, err := q.Exec(query, args...)
	return err
}

func stage(q db.Q, c Connector, ctx StageContext, key string, ev OsloEvent) (bool, error) {
	day, ok := LocalDay(ev.At, ctx.Timezone)
	if !ok {
		return false, nil
	}
	// The first sync only reaches back so far.
	if !ctx.CaughtUp && ctx.BackfillFrom != "" && day < ctx.BackfillFrom {
		return false, nil
	}
	// That day's digest is out already; it doesn't change.
	if done, err := dayWritten(q, c, day); err != nil || done {
		return false, err
	}
	payload := jsonx.Stringify(ev)
	_, err := q.Exec(`INSERT INTO app_connector_staging (connector_id, day, item_key, payload) VALUES (?, ?, ?, ?)
		ON CONFLICT (connector_id, item_key) DO UPDATE SET payload = ? WHERE app_connector_staging.written_at IS NULL`, c.ID, day, key, payload, payload)
	return err == nil, err
}

// StageOsloItems is stageOsloItems: one page of Oslo items staged; returns
// how many events were.
func StageOsloItems(q db.Q, c Connector, items []ChangeItem, ctx StageContext) (int, error) {
	staged := 0
	count := func(ok bool, err error) error {
		if ok {
			staged++
		}
		return err
	}
	for _, it := range items {
		d := it.Data
		switch it.Kind {
		case "task":
			completedAt, okAt := oiso(get(d, "completedAt"))
			prefix := "task:" + it.ID + ":"
			// Deleted, reopened or cancelled: whatever waited for this task goes.
			if it.Deleted || ostr(get(d, "status")) != "done" || !okAt {
				if err := unstage(q, c, prefix, ""); err != nil {
					return staged, err
				}
				continue
			}
			day, ok := LocalDay(completedAt, ctx.Timezone)
			if !ok {
				continue
			}
			// One line per task per day; a later completion replaces an unwritten earlier one.
			key := prefix + day
			if err := unstage(q, c, prefix, key); err != nil {
				return staged, err
			}
			title := ostr(get(d, "title"))
			if title == "" {
				title = "(untitled task)"
			}
			if err := count(stage(q, c, ctx, key, OsloEvent{Type: "task", ID: it.ID, Title: title, URL: it.URL, At: completedAt, Project: ostr(get(d, "projectName"))})); err != nil {
				return staged, err
			}
		case "project":
			if it.Deleted {
				if err := unstage(q, c, "project-completed:"+it.ID+":", ""); err != nil {
					return staged, err
				}
				continue
			}
			name := ostr(get(d, "name"))
			if name == "" {
				name = "(untitled project)"
			}
			// Created: reported once ever; the first time Recall sees a project
			// after the backfill counts as its creation.
			createdKey := "project-created:" + it.ID
			var seen string
			err := q.QueryRow(`SELECT item_key FROM app_connector_staging WHERE connector_id = ? AND item_key = ?`, c.ID, createdKey).Scan(&seen)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return staged, err
			}
			if errors.Is(err, sql.ErrNoRows) {
				createdAt, okCreated := oiso(get(d, "createdAt"))
				at := it.UpdatedAt
				if okCreated {
					at = createdAt
				}
				ev := OsloEvent{Type: "project_created", ID: it.ID, Title: name, URL: it.URL, At: at}
				reportable := okCreated || ctx.CaughtUp
				done := false
				if reportable {
					if done, err = stage(q, c, ctx, createdKey, ev); err != nil {
						return staged, err
					}
				}
				if done {
					staged++
				} else {
					day, _ := LocalDay(ev.At, ctx.Timezone)
					if _, err := q.Exec(`INSERT INTO app_connector_staging (connector_id, day, item_key, payload, written_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
						c.ID, day, createdKey, jsonx.Stringify(ev), ctx.Now); err != nil {
						return staged, err
					}
				}
			}
			completedAt, okAt := oiso(get(d, "completedAt"))
			prefix := "project-completed:" + it.ID + ":"
			if !okAt {
				if err := unstage(q, c, prefix, ""); err != nil {
					return staged, err
				}
				continue
			}
			day, ok := LocalDay(completedAt, ctx.Timezone)
			if !ok {
				continue
			}
			key := prefix + day
			if err := unstage(q, c, prefix, key); err != nil {
				return staged, err
			}
			if err := count(stage(q, c, ctx, key, OsloEvent{Type: "project_completed", ID: it.ID, Title: name, URL: it.URL, At: completedAt})); err != nil {
				return staged, err
			}
		}
	}
	return staged, nil
}

// --- the digest --------------------------------------------------------------------

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// OsloDigest is osloDigest: the day's title and content.
func OsloDigest(day string, events []OsloEvent, tz string) (string, string) {
	sorted := append([]OsloEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if c := strx.LocaleCompare(sorted[i].At, sorted[j].At); c != 0 {
			return c < 0
		}
		return strx.LocaleCompare(sorted[i].ID, sorted[j].ID) < 0
	})
	var tasks, created, completed []OsloEvent
	for _, e := range sorted {
		switch e.Type {
		case "task":
			tasks = append(tasks, e)
		case "project_created":
			created = append(created, e)
		case "project_completed":
			completed = append(completed, e)
		}
	}
	line := func(e OsloEvent) string {
		s := "- " + e.Title
		if e.Project != "" {
			s += " (project: " + e.Project + ")"
		}
		s += ", at " + localTime(e.At, tz) + "."
		if e.URL != "" {
			s += " " + e.URL
		}
		return s
	}
	section := func(label string, list []OsloEvent) string {
		lines := []string{fmt.Sprintf("%s (%d):", label, len(list))}
		for _, e := range list {
			lines = append(lines, line(e))
		}
		return strings.Join(lines, "\n")
	}
	t, _ := time.Parse("2006-01-02", day)
	dateText := dates.Format(dates.Dated{Date: day, Precision: dates.Day})
	sections := []string{fmt.Sprintf("Activity in Oslo, the todo app, on %s, %s (%s time).", t.Weekday(), dateText, tz)}
	var counts []string
	if len(tasks) > 0 {
		sections = append(sections, section("Tasks completed", tasks))
		counts = append(counts, plural(len(tasks), "task", "tasks")+" done")
	}
	if len(created) > 0 {
		sections = append(sections, section("Projects created", created))
		counts = append(counts, plural(len(created), "project", "projects")+" started")
	}
	if len(completed) > 0 {
		sections = append(sections, section("Projects completed", completed))
		counts = append(counts, plural(len(completed), "project", "projects")+" finished")
	}
	return "Oslo on " + dateText + ": " + strings.Join(counts, ", "), strings.Join(sections, "\n\n")
}

// ClosedDays is closedDays: days with unwritten events that are over,
// oldest first.
func ClosedDays(q db.Q, c Connector, today string) ([]string, error) {
	rows, err := q.Query(`SELECT DISTINCT day FROM app_connector_staging WHERE connector_id = ? AND written_at IS NULL AND day < ? ORDER BY day`, c.ID, today)
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

// DayEvents is dayEvents.
func DayEvents(q db.Q, c Connector, day string) ([]OsloEvent, error) {
	rows, err := q.Query(`SELECT payload FROM app_connector_staging WHERE connector_id = ? AND day = ? AND written_at IS NULL`, c.ID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OsloEvent
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		var e OsloEvent
		json.Unmarshal([]byte(p), &e)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkDayWritten is markDayWritten: project-created keys stay, marked
// written, so a project is never reported as new again; the rest goes.
func MarkDayWritten(q db.Q, c Connector, day string, now int64) error {
	if _, err := q.Exec(`UPDATE app_connector_staging SET written_at = ? WHERE connector_id = ? AND day = ? AND written_at IS NULL AND item_key LIKE 'project-created:%'`, now, c.ID, day); err != nil {
		return err
	}
	_, err := q.Exec(`DELETE FROM app_connector_staging WHERE connector_id = ? AND day = ? AND item_key NOT LIKE 'project-created:%'`, c.ID, day)
	return err
}
