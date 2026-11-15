// Package strx holds the JavaScript string semantics the TS code relies on:
// lengths and slices in UTF-16 code units, the JS whitespace set (\s and
// String.prototype.trim), and the WHATWG UTF-8 decoder (TextDecoder).
package strx

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Len16 is the JS string length: UTF-16 code units.
func Len16(s string) int {
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

// Slice16 is s.slice(0, n) in JS. A cut through a surrogate pair leaves a lone
// surrogate in JS, which Node stores as U+FFFD; the same happens here.
func Slice16(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			if w == 2 && units+1 == n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// Tail16 is s.slice(-n) in JS (the last n UTF-16 code units).
func Tail16(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if n >= len(u) {
		return s
	}
	return string(utf16.Decode(u[len(u)-n:]))
}

// Compare16 orders two strings the way JS `<` does (UTF-16 code units).
func Compare16(a, b string) int {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}

// SpaceClass is the JS regex \s as an RE2 character class body.
const SpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// IsSpace reports whether r is in the JS whitespace set (\s, trim).
func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// Trim is String.prototype.trim.
func Trim(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// CollapseSpace is s.replace(/\s+/g, " ").
func CollapseSpace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeNewlines is s.replace(/\r\n?/g, "\n").
func NormalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// TextDecode is new TextDecoder("utf-8").decode(b): a leading BOM is dropped,
// then DecodeUTF8.
func TextDecode(b []byte) string {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return DecodeUTF8(b)
}

// DecodeUTF8 is Buffer.toString("utf8"): every maximal invalid subpart becomes
// one U+FFFD (WHATWG Encoding). A BOM is kept.
func DecodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	i := 0
	for i < len(b) {
		c := b[i]
		if c < 0x80 {
			out.WriteByte(c)
			i++
			continue
		}
		need := 0
		lower, upper := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			}
			if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			}
			if c == 0xF4 {
				upper = 0x8F
			}
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.Write(b[i:j])
		i = j
	}
	return out.String()
}

var jsDecimal = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// Number is JS Number(s) for a string: trimmed, "" is 0, decimal, exponent,
// 0x/0o/0b and Infinity forms; anything else is NaN.
func Number(s string) float64 {
	t := Trim(s)
	if t == "" {
		return 0
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(t) > 2 && t[0] == '0' {
		base := 0
		switch t[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := t[2:]
			if strings.ContainsAny(digits, "+-_") {
				return math.NaN()
			}
			n, ok := new(big.Int).SetString(digits, base)
			if !ok {
				return math.NaN()
			}
			// Rounded to the nearest double, like JS for long hex strings.
			f, _ := new(big.Float).SetInt(n).Float64()
			return f
		}
	}
	if !jsDecimal.MatchString(t) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && ne.Err == strconv.ErrRange {
			return f // ±Infinity on overflow, 0 on underflow, as in JS
		}
		return math.NaN()
	}
	return f
}
