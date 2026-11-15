package dates

import (
	"math"
	"time"
)

// JSParse is Date.parse: V8's DateParser (src/date/dateparser-inl.h), the
// ECMAScript date-time format first and then the legacy parser for the rest
// ("Tue, 10 Nov 2026 08:00:00 GMT", "11/10/2026", "Nov 10 2026 8:00 PM",
// even a bare "12", which is Dec 1, 2001). A date-only ISO string is UTC; a
// form without an offset is local time. ok false is NaN.
func JSParse(s string) (int64, bool) {
	var out [8]float64
	if !parseDate([]rune(s), &out) {
		return 0, false
	}
	day := makeDay(out[outYear], out[outMonth], out[outDay])
	tm := out[outHour]*3_600_000 + out[outMinute]*60_000 + out[outSecond]*1000 + out[outMs]
	date := day*86_400_000 + tm
	const maxTime = 8.64e15
	if math.IsNaN(out[outOffset]) {
		if date < -(maxTime+864_000_000) || date > maxTime+864_000_000 {
			return 0, false
		}
		date = localToUTC(date)
	} else {
		date -= out[outOffset] * 1000
		if date < -maxTime || date > maxTime {
			return 0, false
		}
	}
	if math.Abs(date) > maxTime {
		return 0, false
	}
	return int64(date), true
}

const (
	outYear = iota
	outMonth
	outDay
	outHour
	outMinute
	outSecond
	outMs
	outOffset
)

// makeDay is MakeDay(year, month, date): days since the epoch.
func makeDay(year, month, date float64) float64 {
	ym := year + math.Floor(month/12)
	mn := math.Mod(month, 12)
	if mn < 0 {
		mn += 12
	}
	first := time.Date(int(ym), time.Month(int(mn)+1), 1, 0, 0, 0, 0, time.UTC)
	return float64(first.Unix()/86_400) + date - 1
}

// localToUTC is DateCache::ToUTC for a local wall time in ms.
func localToUTC(ms float64) float64 {
	t := time.UnixMilli(int64(ms)).UTC()
	local := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	return float64(local.UnixMilli())
}

const none = math.MaxInt32

// --- tokens -------------------------------------------------------------------------

type tokTag int

const (
	tagInvalid tokTag = iota
	tagUnknown
	tagNumber
	tagSymbol
	tagWhiteSpace
	tagKeyword
	tagEnd
)

const (
	kwInvalid = iota
	kwMonth
	kwTimeZone
	kwTimeSep
	kwAmPm
)

type token struct {
	tag    tokTag
	length int
	value  int
	kwType int
}

func (t token) isNumber() bool           { return t.tag == tagNumber }
func (t token) isSymbol(c rune) bool     { return t.tag == tagSymbol && t.value == int(c) }
func (t token) isWhiteSpace() bool       { return t.tag == tagWhiteSpace }
func (t token) isEnd() bool              { return t.tag == tagEnd }
func (t token) isKeyword() bool          { return t.tag == tagKeyword }
func (t token) isFixedNumber(n int) bool { return t.isNumber() && t.length == n }
func (t token) isAsciiSign() bool        { return t.tag == tagSymbol && (t.value == '+' || t.value == '-') }
func (t token) asciiSign() int           { return 44 - t.value }
func (t token) isKeywordType(k int) bool { return t.tag == tagKeyword && t.kwType == k }
func (t token) isKeywordZ() bool {
	return t.tag == tagKeyword && t.kwType == kwTimeZone && t.length == 1 && t.value == 0
}
func between(x, lo, hi int) bool             { return x >= lo && x <= hi }
func isMonth(x int) bool                     { return between(x, 1, 12) }
func isDay(x int) bool                       { return between(x, 1, 31) }
func isHour(x int) bool                      { return between(x, 0, 23) }
func isMinute(x int) bool                    { return between(x, 0, 59) }
func isSecond(x int) bool                    { return between(x, 0, 59) }
func isHour12(x int) bool                    { return between(x, 0, 12) }
func isMillisecond(x int) bool               { return between(x, 0, 999) }
func (t token) keyword() (kwType, value int) { return t.kwType, t.value }

var keywords = []struct {
	prefix string
	kwType int
	value  int
}{
	{"jan", kwMonth, 1}, {"feb", kwMonth, 2}, {"mar", kwMonth, 3}, {"apr", kwMonth, 4},
	{"may", kwMonth, 5}, {"jun", kwMonth, 6}, {"jul", kwMonth, 7}, {"aug", kwMonth, 8},
	{"sep", kwMonth, 9}, {"oct", kwMonth, 10}, {"nov", kwMonth, 11}, {"dec", kwMonth, 12},
	{"am\x00", kwAmPm, 0}, {"pm\x00", kwAmPm, 12},
	{"ut\x00", kwTimeZone, 0}, {"utc", kwTimeZone, 0}, {"z\x00\x00", kwTimeZone, 0}, {"gmt", kwTimeZone, 0},
	{"cdt", kwTimeZone, -5}, {"cst", kwTimeZone, -6}, {"edt", kwTimeZone, -4}, {"est", kwTimeZone, -5},
	{"mdt", kwTimeZone, -6}, {"mst", kwTimeZone, -7}, {"pdt", kwTimeZone, -7}, {"pst", kwTimeZone, -8},
	{"t\x00\x00", kwTimeSep, 0},
}

// isJSWhiteSpace is WhiteSpace or LineTerminator.
func isJSWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

type tokenizer struct {
	s    []rune
	pos  int
	next token
}

func newTokenizer(s []rune) *tokenizer {
	t := &tokenizer{s: s}
	t.next = t.scan()
	return t
}

func (t *tokenizer) ch() rune {
	if t.pos < len(t.s) {
		return t.s[t.pos]
	}
	return 0
}

func (t *tokenizer) peek() token { return t.next }

func (t *tokenizer) nextToken() token {
	r := t.next
	t.next = t.scan()
	return r
}

func (t *tokenizer) skipSymbol(c rune) bool {
	if t.next.isSymbol(c) {
		t.nextToken()
		return true
	}
	return false
}

func (t *tokenizer) scan() token {
	start := t.pos
	if t.pos >= len(t.s) {
		return token{tag: tagEnd}
	}
	c := t.ch()
	switch {
	case c >= '0' && c <= '9':
		n, i := 0, 0
		for t.ch() == '0' {
			t.pos++
		}
		for c := t.ch(); c >= '0' && c <= '9'; c = t.ch() {
			if i < 9 {
				n = n*10 + int(c-'0')
			}
			i++
			t.pos++
		}
		return token{tag: tagNumber, value: n, length: t.pos - start}
	case c == ':' || c == '-' || c == '+' || c == '.' || c == ')':
		t.pos++
		return token{tag: tagSymbol, value: int(c), length: 1}
	case c >= 'A' && !isJSWhiteSpace(c):
		var pre [3]rune
		n := 0
		for c := t.ch(); t.pos < len(t.s) && c >= 'A' && !isJSWhiteSpace(c); c = t.ch() {
			if n < 3 {
				pre[n] = c | 0x20
			}
			n++
			t.pos++
		}
		for _, k := range keywords {
			p := []rune(k.prefix)
			if pre[0] == p[0] && pre[1] == p[1] && pre[2] == p[2] && (n <= 3 || k.kwType == kwMonth) {
				return token{tag: tagKeyword, kwType: k.kwType, value: k.value, length: n}
			}
		}
		return token{tag: tagKeyword, kwType: kwInvalid, length: n}
	case isJSWhiteSpace(c):
		for t.pos < len(t.s) && isJSWhiteSpace(t.ch()) {
			t.pos++
		}
		return token{tag: tagWhiteSpace, length: t.pos - start}
	case c == '(':
		balance := 0
		for {
			switch t.ch() {
			case ')':
				balance--
			case '(':
				balance++
			}
			t.pos++
			if balance <= 0 || t.pos >= len(t.s) {
				break
			}
		}
		return token{tag: tagUnknown}
	}
	t.pos++
	return token{tag: tagUnknown}
}

// --- composers ----------------------------------------------------------------------

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	iso        bool
}

func (d *dayComposer) add(n int) bool {
	if d.index == 3 {
		return false
	}
	d.comp[d.index] = n
	d.index++
	return true
}

func (d *dayComposer) write(out *[8]float64) bool {
	if d.index < 1 {
		return false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	year, month, day := 0, none, none
	if d.namedMonth == none {
		if d.iso || (d.index == 3 && !isDay(d.comp[0])) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day = d.comp[0], d.comp[1]
			if d.index == 3 {
				year = d.comp[2]
			}
		}
	} else {
		month = d.namedMonth
		if d.index == 1 {
			day = d.comp[0]
		} else if !isDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if year < -(1<<30) || year >= 1<<30 || !isMonth(month) || !isDay(day) {
		return false
	}
	out[outYear], out[outMonth], out[outDay] = float64(year), float64(month-1), float64(day)
	return true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *timeComposer) empty() bool { return t.index == 0 }

func (t *timeComposer) expecting(n int) bool {
	return (t.index == 1 && isMinute(n)) || (t.index == 2 && isSecond(n)) || (t.index == 3 && isMillisecond(n))
}

func (t *timeComposer) add(n int) bool {
	if t.index >= 4 {
		return false
	}
	t.comp[t.index] = n
	t.index++
	return true
}

func (t *timeComposer) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *timeComposer) write(out *[8]float64) bool {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	hour, minute, second, ms := t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != none {
		if !isHour12(hour) {
			return false
		}
		hour = hour%12 + t.hourOffset
	}
	if !isHour(hour) || !isMinute(minute) || !isSecond(second) || !isMillisecond(ms) {
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return false
		}
	}
	out[outHour], out[outMinute], out[outSecond], out[outMs] = float64(hour), float64(minute), float64(second), float64(ms)
	return true
}

type tzComposer struct{ sign, hour, minute int }

func (z *tzComposer) set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
	}
	z.hour, z.minute = hours*z.sign, 0
}

func (z *tzComposer) setSign(s int) {
	z.sign = 1
	if s < 0 {
		z.sign = -1
	}
}

func (z *tzComposer) expecting(n int) bool { return z.hour != none && z.minute == none && isMinute(n) }
func (z *tzComposer) isUTC() bool          { return z.hour == 0 && z.minute == 0 }
func (z *tzComposer) empty() bool          { return z.hour == none }

func (z *tzComposer) write(out *[8]float64) bool {
	if z.sign == none {
		out[outOffset] = math.NaN()
		return true
	}
	if z.hour == none {
		z.hour = 0
	}
	if z.minute == none {
		z.minute = 0
	}
	total := uint64(uint32(z.hour))*3600 + uint64(uint32(z.minute))*60
	if total > 1<<30-1 {
		return false
	}
	secs := int64(total)
	if z.sign < 0 {
		secs = -secs
	}
	out[outOffset] = float64(secs)
	return true
}

// readMilliseconds keeps the first three significant digits of a numeral.
func readMilliseconds(t token) int {
	n, length := t.value, t.length
	switch {
	case length == 1:
		n *= 100
	case length == 2:
		n *= 10
	case length > 3:
		if length > 9 {
			length = 9
		}
		factor := 1
		for length > 3 {
			factor *= 10
			length--
		}
		n /= factor
	}
	return n
}

// --- the parser ---------------------------------------------------------------------

func parseES5(sc *tokenizer, day *dayComposer, tm *timeComposer, tz *tzComposer) token {
	invalid := token{tag: tagInvalid}
	if sc.peek().isAsciiSign() {
		sign := sc.nextToken()
		if !sc.peek().isFixedNumber(6) {
			return sign
		}
		year := sc.nextToken().value
		if sign.asciiSign() < 0 && year == 0 {
			return sign
		}
		day.add(sign.asciiSign() * year)
	} else if sc.peek().isFixedNumber(4) {
		day.add(sc.nextToken().value)
	} else {
		return sc.nextToken()
	}
	if sc.skipSymbol('-') {
		if !sc.peek().isFixedNumber(2) || !isMonth(sc.peek().value) {
			return sc.nextToken()
		}
		day.add(sc.nextToken().value)
		if sc.skipSymbol('-') {
			if !sc.peek().isFixedNumber(2) || !isDay(sc.peek().value) {
				return sc.nextToken()
			}
			day.add(sc.nextToken().value)
		}
	}
	if !sc.peek().isKeywordType(kwTimeSep) {
		if !sc.peek().isEnd() {
			return sc.nextToken()
		}
	} else {
		sc.nextToken()
		if !sc.peek().isFixedNumber(2) || !between(sc.peek().value, 0, 24) {
			return invalid
		}
		hourIs24 := sc.peek().value == 24
		tm.add(sc.nextToken().value)
		if !sc.skipSymbol(':') {
			return invalid
		}
		if !sc.peek().isFixedNumber(2) || !isMinute(sc.peek().value) || (hourIs24 && sc.peek().value > 0) {
			return invalid
		}
		tm.add(sc.nextToken().value)
		if sc.skipSymbol(':') {
			if !sc.peek().isFixedNumber(2) || !isSecond(sc.peek().value) || (hourIs24 && sc.peek().value > 0) {
				return invalid
			}
			tm.add(sc.nextToken().value)
			if sc.skipSymbol('.') {
				if !sc.peek().isNumber() || (hourIs24 && sc.peek().value > 0) {
					return invalid
				}
				tm.add(readMilliseconds(sc.nextToken()))
			}
		}
		if sc.peek().isKeywordZ() {
			sc.nextToken()
			tz.set(0)
		} else if sc.peek().isSymbol('+') || sc.peek().isSymbol('-') {
			if sc.nextToken().value == '+' {
				tz.setSign(1)
			} else {
				tz.setSign(-1)
			}
			if sc.peek().isFixedNumber(4) {
				hm := sc.nextToken().value
				h, m := hm/100, hm%100
				if !isHour(h) || !isMinute(m) {
					return invalid
				}
				tz.hour, tz.minute = h, m
			} else {
				if !sc.peek().isFixedNumber(2) || !isHour(sc.peek().value) {
					return invalid
				}
				tz.hour = sc.nextToken().value
				if !sc.skipSymbol(':') {
					return invalid
				}
				if !sc.peek().isFixedNumber(2) || !isMinute(sc.peek().value) {
					return invalid
				}
				tz.minute = sc.nextToken().value
			}
		}
		if !sc.peek().isEnd() {
			return invalid
		}
	}
	if tz.empty() && tm.empty() {
		tz.set(0)
	}
	day.iso = true
	return token{tag: tagEnd}
}

func parseDate(s []rune, out *[8]float64) bool {
	sc := newTokenizer(s)
	day := &dayComposer{namedMonth: none}
	tm := &timeComposer{hourOffset: none}
	tz := &tzComposer{sign: none, hour: none, minute: none}
	next := parseES5(sc, day, tm, tz)
	if next.tag == tagInvalid {
		return false
	}
	hasReadNumber := day.index > 0
	for tok := next; !tok.isEnd(); tok = sc.nextToken() {
		switch {
		case tok.isNumber():
			hasReadNumber = true
			n := tok.value
			if sc.skipSymbol(':') {
				if sc.skipSymbol(':') {
					if !tm.empty() {
						return false
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return false
					}
					if sc.peek().isSymbol('.') {
						sc.nextToken()
					}
				}
			} else if sc.skipSymbol('.') && tm.expecting(n) {
				tm.add(n)
				if !sc.peek().isNumber() {
					return false
				}
				ms := readMilliseconds(sc.nextToken())
				if ms < 0 {
					return false
				}
				tm.addFinal(ms)
			} else if tz.expecting(n) {
				tz.minute = n
			} else if tm.expecting(n) {
				tm.addFinal(n)
				p := sc.peek()
				if !p.isEnd() && !p.isWhiteSpace() && !p.isKeywordZ() && !p.isAsciiSign() {
					return false
				}
			} else {
				if !day.add(n) {
					return false
				}
				sc.skipSymbol('-')
			}
		case tok.isKeyword():
			kw, value := tok.keyword()
			switch {
			case kw == kwAmPm && !tm.empty():
				tm.hourOffset = value
			case kw == kwMonth:
				day.namedMonth = value
				sc.skipSymbol('-')
			case kw == kwTimeZone && hasReadNumber:
				tz.set(value)
			default:
				if hasReadNumber {
					return false
				}
				if sc.peek().isNumber() {
					return false
				}
			}
		case tok.isAsciiSign() && (tz.isUTC() || !tm.empty()):
			tz.setSign(tok.asciiSign())
			n, length := 0, 0
			if sc.peek().isNumber() {
				nt := sc.nextToken()
				n, length = nt.value, nt.length
			}
			hasReadNumber = true
			switch {
			case sc.peek().isSymbol(':'):
				tz.hour, tz.minute = n, none
			case length == 1 || length == 2:
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				tz.hour, tz.minute = n/100, n%100
			default:
				return false
			}
		case (tok.isAsciiSign() || tok.isSymbol(')')) && hasReadNumber:
			return false
		}
	}
	return day.write(out) && tm.write(out) && tz.write(out)
}
