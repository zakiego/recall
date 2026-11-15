// Package zone is the luxon and Intl layer for time zones: which names are
// valid (what Intl.DateTimeFormat accepts, case-insensitive, offset zones like
// "+08:00" included), the offset math luxon uses to turn a wall time into an
// instant (DST gaps shift forward), its day and hour arithmetic, ISO output and
// short zone names. Go's tzdata is embedded, so the binary needs no zoneinfo.
package zone

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

var cache sync.Map // name -> *time.Location

// Valid is luxon's IANAZone.isValidZone (and settings' isTimeZone): whether
// Intl.DateTimeFormat accepts the name.
func Valid(name string) bool {
	_, ok := Load(name)
	return ok
}

// Load returns the location for a zone name as Intl reads it: IANA names in any
// case, and the offset forms "+08:00", "+0800" and "+08".
func Load(name string) (*time.Location, bool) {
	if v, ok := cache.Load(name); ok {
		return v.(*time.Location), true
	}
	loc := resolve(name)
	if loc == nil {
		return nil, false
	}
	cache.Store(name, loc)
	return loc, true
}

// MustLoad is Load for names already checked; an unknown name falls back to UTC.
func MustLoad(name string) *time.Location {
	if loc, ok := Load(name); ok {
		return loc
	}
	return time.UTC
}

func resolve(name string) *time.Location {
	if canonical, ok := names[strings.ToLower(name)]; ok {
		loc, err := time.LoadLocation(canonical)
		if err != nil {
			return nil
		}
		return loc
	}
	if off, ok := parseOffset(name); ok {
		return time.FixedZone(name, off*60)
	}
	return nil
}

// parseOffset reads "+HH", "+HHMM" and "+HH:MM" (or "-"), hours 00..23.
func parseOffset(s string) (int, bool) {
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	body := s[1:]
	var hh, mm string
	switch {
	case len(body) == 2:
		hh = body
	case len(body) == 4:
		hh, mm = body[:2], body[2:]
	case len(body) == 5 && body[2] == ':':
		hh, mm = body[:2], body[3:]
	default:
		return 0, false
	}
	for _, c := range hh + mm {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	h, _ := strconv.Atoi(hh)
	m := 0
	if mm != "" {
		m, _ = strconv.Atoi(mm)
	}
	if h > 23 || m > 59 {
		return 0, false
	}
	off := h*60 + m
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

// IsFixedUTC is luxon's FixedOffsetZone.utcInstance: the names "utc" and "gmt"
// in any case. ISO output ends in "Z" only for these.
func IsFixedUTC(name string) bool {
	l := strings.ToLower(name)
	return l == "utc" || l == "gmt"
}

// OffsetMin is the zone's offset in minutes at ms (luxon's zone.offset).
func OffsetMin(loc *time.Location, ms int64) int {
	_, off := time.UnixMilli(ms).In(loc).Zone()
	return off / 60
}

// Parts is a wall time.
type Parts struct {
	Year, Month, Day, Hour, Minute, Second, Millisecond int
}

// LocalTS is Date.UTC over the parts (fields overflow like JS).
func LocalTS(p Parts) int64 {
	return time.Date(p.Year, time.Month(p.Month), p.Day, p.Hour, p.Minute, p.Second, p.Millisecond*1e6, time.UTC).UnixMilli()
}

// PartsOf is the wall time of ms in loc.
func PartsOf(ms int64, loc *time.Location) Parts {
	t := time.UnixMilli(ms).In(loc)
	return Parts{t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond() / 1e6}
}

// FixOffset is luxon's fixOffset: the instant for a local timestamp given a
// guessed offset. A wall time in a DST gap comes out shifted forward by the gap.
func FixOffset(localTS int64, guess int, loc *time.Location) (int64, int) {
	utcGuess := localTS - int64(guess)*60_000
	o2 := OffsetMin(loc, utcGuess)
	if guess == o2 {
		return utcGuess, guess
	}
	utcGuess -= int64(o2-guess) * 60_000
	o3 := OffsetMin(loc, utcGuess)
	if o2 == o3 {
		return utcGuess, o2
	}
	return localTS - int64(min(o2, o3))*60_000, max(o2, o3)
}

// FromParts is DateTime.fromObject(parts, { zone }).toMillis(). luxon guesses
// with the offset at the real current time; the result only depends on that
// guess for a wall time that happens twice, and callers resolve those first.
func FromParts(p Parts, loc *time.Location) int64 {
	ts := LocalTS(p)
	ms, _ := FixOffset(ts, OffsetMin(loc, ts), loc)
	return ms
}

// StartOfDay is DateTime.fromMillis(ms, { zone }).startOf("day").toMillis().
func StartOfDay(ms int64, loc *time.Location) int64 {
	p := PartsOf(ms, loc)
	out, _ := FixOffset(LocalTS(Parts{Year: p.Year, Month: p.Month, Day: p.Day}), OffsetMin(loc, ms), loc)
	return out
}

// StartOfHour is startOf("hour").
func StartOfHour(ms int64, loc *time.Location) int64 {
	p := PartsOf(ms, loc)
	out, _ := FixOffset(LocalTS(Parts{Year: p.Year, Month: p.Month, Day: p.Day, Hour: p.Hour}), OffsetMin(loc, ms), loc)
	return out
}

// PlusDays is DateTime.plus({ days: n }): calendar days, keeping the wall time,
// with the current offset as luxon's guess.
func PlusDays(ms int64, loc *time.Location, n int) int64 {
	p := PartsOf(ms, loc)
	p.Day += n
	out, _ := FixOffset(LocalTS(p), OffsetMin(loc, ms), loc)
	return out
}

// NextDay is startOf("day").plus({ days: 1 }), the budget's daily rollover.
func NextDay(ms int64, loc *time.Location) int64 {
	return PlusDays(StartOfDay(ms, loc), loc, 1)
}

// NextHour is startOf("hour").plus({ hours: 1 }).
func NextHour(ms int64, loc *time.Location) int64 {
	return StartOfHour(ms, loc) + 3600_000
}

// ISO is luxon's toISO(): "2026-11-11T09:00:00.000+08:00", or "...Z" for the
// fixed UTC zone.
func ISO(ms int64, name string, loc *time.Location) string {
	t := time.UnixMilli(ms).In(loc)
	base := t.Format("2006-01-02T15:04:05.000")
	if IsFixedUTC(name) {
		return base + "Z"
	}
	_, off := t.Zone()
	return base + formatOffset(off/60)
}

func formatOffset(min int) string {
	sign := "+"
	if min < 0 {
		sign = "-"
		min = -min
	}
	return fmt.Sprintf("%s%02d:%02d", sign, min/60, min%60)
}

func gmtName(off int, zeroPlus bool) string {
	if off == 0 {
		if zeroPlus {
			return "GMT+0"
		}
		return "GMT"
	}
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	if off%60 != 0 {
		return fmt.Sprintf("GMT%s%d:%02d", sign, off/60, off%60)
	}
	return fmt.Sprintf("GMT%s%d", sign, off/60)
}

// ShortName is luxon's offsetNameShort in the en-US locale: "EST", "PDT",
// "UTC" or "GMT+8".
func ShortName(ms int64, name string) string {
	if IsFixedUTC(name) {
		return "UTC"
	}
	loc, ok := Load(name)
	if !ok {
		return name
	}
	t := time.UnixMilli(ms).In(loc)
	_, off := t.Zone()
	canonical := names[strings.ToLower(name)]
	if pair, ok := shortNames[canonical]; ok {
		if strings.HasPrefix(pair[0], "GMT") && strings.HasPrefix(pair[1], "GMT") {
			return gmtName(off/60, true)
		}
		if t.IsDST() {
			return pair[1]
		}
		return pair[0]
	}
	return gmtName(off/60, false)
}
