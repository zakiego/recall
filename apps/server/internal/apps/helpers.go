package apps

// Small helpers the generic feed shares: reading item fields, local days and
// times, and the staging table.

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zone"
)

// DefaultTimezone is the day boundary when nothing else says.
const DefaultTimezone = "Asia/Makassar"

// StageContext is the staging context: the time zone, whether the feed was
// read to the end before, the backfill window's first day ("" for none).
type StageContext struct {
	Timezone     string
	CaughtUp     bool
	BackfillFrom string
	Now          int64
}

func zoneOf(tz string) *time.Location {
	if !dates.IsTimezone(tz) {
		tz = DefaultTimezone
	}
	// Intl's reading of the name: any case, and offsets like "+08:00".
	return zone.MustLoad(tz)
}

// LocalDay is the calendar date of an instant in a time zone.
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

func get(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
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

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// unstage drops an item's unwritten staged rows, except keep.
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
