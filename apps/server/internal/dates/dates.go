// Package dates is src/lib/dates.ts: calendar dates on facts, when something
// happened (event) and when it ends or is due (due). A date is stored as a
// full ISO date plus a precision: "2027-12" becomes {2027-12-01, month} and
// "2027" becomes {2027-01-01, year}.
package dates

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zone"
)

// Precisions.
const (
	Day   = "day"
	Month = "month"
	Year  = "year"
)

// Dated is a date with its precision.
type Dated struct {
	Date      string `json:"date"`
	Precision string `json:"precision"`
}

const (
	minYear = 1900
	maxYear = 2200
)

var dateRE = regexp.MustCompile(`^(\d{4})(?:-(\d{2})(?:-(\d{2})(?:[T ][\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)?)?)?$`)

func validDay(y, m, d int) bool {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

// Parse is parseDate: "2026-11-05", "2026-11" or "2026", optionally with a
// time part that is ignored. Anything else is nil, including impossible dates.
func Parse(input string) *Dated {
	s := strx.Trim(input)
	m := dateRE.FindStringSubmatchIndex(s)
	if m == nil {
		return nil
	}
	group := func(i int) (string, bool) {
		if m[2*i] < 0 {
			return "", false
		}
		return s[m[2*i]:m[2*i+1]], true
	}
	ys, _ := group(1)
	y, _ := strconv.Atoi(ys)
	if y < minYear || y > maxYear {
		return nil
	}
	ms, ok := group(2)
	if !ok {
		return &Dated{ys + "-01-01", Year}
	}
	mo, _ := strconv.Atoi(ms)
	if mo < 1 || mo > 12 {
		return nil
	}
	ds, ok := group(3)
	if !ok {
		return &Dated{ys + "-" + ms + "-01", Month}
	}
	d, _ := strconv.Atoi(ds)
	if !validDay(y, mo, d) {
		return nil
	}
	return &Dated{ys + "-" + ms + "-" + ds, Day}
}

// String is toDateString: "2027-12" for a month, "2027" for a year.
func (d Dated) String() string {
	switch d.Precision {
	case Year:
		return d.Date[:4]
	case Month:
		return d.Date[:7]
	}
	return d.Date
}

var isoDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// FromColumns is dated(): the two stored columns, nil when either is missing or broken.
func FromColumns(date, precision *string) *Dated {
	if date == nil || *date == "" || precision == nil || (*precision != Day && *precision != Month && *precision != Year) {
		return nil
	}
	if !isoDay.MatchString(*date) {
		return nil
	}
	return &Dated{*date, *precision}
}

var months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// Format is formatDate: "Nov 5, 2026", "Dec 2027" or "2027".
func Format(d Dated) string {
	parts := strings.Split(d.Date, "-")
	y := parts[0]
	if d.Precision == Year {
		return y
	}
	m, _ := strconv.Atoi(parts[1])
	month := "undefined"
	if m >= 1 && m <= 12 {
		month = months[m-1]
	}
	if d.Precision == Month {
		return month + " " + y
	}
	day, _ := strconv.Atoi(parts[2])
	return fmt.Sprintf("%s %d, %s", month, day, y)
}

func utc(iso string) int64 {
	p := strings.Split(iso, "-")
	y, _ := strconv.Atoi(p[0])
	m, _ := strconv.Atoi(p[1])
	d, _ := strconv.Atoi(p[2])
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

// AddDays is addDays.
func AddDays(iso string, days int) string {
	return time.UnixMilli(utc(iso) + int64(days)*86_400_000).UTC().Format("2006-01-02")
}

// DaysBetween is daysBetween: whole days from a to b (b later is positive).
func DaysBetween(a, b string) int {
	return int(math.Floor(float64(utc(b)-utc(a))/86_400_000 + 0.5))
}

// IsTimezone is isTimezone: what Intl.DateTimeFormat accepts.
func IsTimezone(tz string) bool { return zone.Valid(tz) }

func loc(tz string) *time.Location {
	if !IsTimezone(tz) {
		tz = "UTC"
	}
	return zone.MustLoad(tz)
}

// TodayIn is todayIn: the calendar date in a time zone (UTC when the zone is
// unknown), as YYYY-MM-DD.
func TodayIn(tz string, nowMs int64) string {
	return time.UnixMilli(nowMs).In(loc(tz)).Format("2006-01-02")
}

// HourIn is hourIn: the hour 0 to 23 in a time zone.
func HourIn(tz string, nowMs int64) int {
	return time.UnixMilli(nowMs).In(loc(tz)).Hour()
}

// RelativeDays is relativeDays: "today", "tomorrow", "in 12 days", "yesterday", "3 days ago".
func RelativeDays(days int) string {
	switch days {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	case -1:
		return "yesterday"
	}
	if days > 0 {
		return fmt.Sprintf("in %d days", days)
	}
	return fmt.Sprintf("%d days ago", -days)
}

// ISODate is new Date(ms).toISOString().slice(0, 10).
func ISODate(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }

// utcJS is utc() as JS computes it for any text: the "-" parts through
// Number() into Date.UTC, NaN when a part is missing or not a number.
func utcJS(iso string) float64 {
	p := strings.Split(iso, "-")
	if len(p) < 3 {
		return math.NaN()
	}
	var n [3]float64
	for i := range n {
		n[i] = strx.Number(p[i])
		if math.IsNaN(n[i]) || math.IsInf(n[i], 0) {
			return math.NaN()
		}
		n[i] = math.Trunc(n[i])
	}
	y := n[0]
	if y >= 0 && y <= 99 {
		y += 1900
	}
	t := time.Date(int(y), time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, int(n[1])-1, int(n[2])-1)
	ms := float64(t.UnixMilli())
	if math.Abs(ms) > 8.64e15 {
		return math.NaN()
	}
	return ms
}

// DaysBetweenJS is daysBetween for any stored text: ok false where JS
// gets NaN (a date that isn't YYYY-MM-DD), which JSON writes as null.
func DaysBetweenJS(a, b string) (int, bool) {
	d := (utcJS(b) - utcJS(a)) / 86_400_000
	if math.IsNaN(d) {
		return 0, false
	}
	return int(math.Floor(d + 0.5)), true
}
