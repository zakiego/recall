// Package jsre runs JavaScript regular expressions (without the u flag) the
// way V8 does, for the TS code's patterns that RE2 can't express (lookbehind,
// lookahead, lazy quantifiers). It uses github.com/dlclark/regexp2 in
// ECMAScript mode and feeds it UTF-16 code units, so \s, \d, \w and \b are the
// JS sets and every index is a JS string index.
package jsre

import (
	"strings"
	"unicode/utf16"

	"github.com/dlclark/regexp2"
)

// Regexp is a compiled JS regular expression.
type Regexp struct {
	re    *regexp2.Regexp
	ascii bool
}

// MustCompile compiles pattern with JS flags ("g" is accepted and ignored:
// FindAll is matchAll, Find is exec from 0; "i" is case-insensitive; "m" is
// multiline).
func MustCompile(pattern, flags string) *Regexp {
	opts := regexp2.RegexOptions(regexp2.ECMAScript)
	for _, f := range flags {
		switch f {
		case 'i':
			opts |= regexp2.IgnoreCase
		case 'm':
			opts |= regexp2.Multiline
		case 'g':
		default:
			panic("jsre: unsupported flag " + string(f))
		}
	}
	ascii := true
	for _, r := range pattern {
		if r >= 0x80 {
			ascii = false
		}
	}
	if !ascii && (strings.Contains(pattern, `\b`) || strings.Contains(pattern, `\w`) || strings.Contains(pattern, `\W`) || strings.Contains(pattern, `\B`)) {
		panic("jsre: a pattern with non-ASCII characters can't use \\b or \\w here: " + pattern)
	}
	return &Regexp{re: regexp2.MustCompile(pattern, opts), ascii: ascii}
}

// Units is a string as UTF-16 code units, one per rune slot (surrogates kept
// as their own values), which is how the matcher sees it.
type Units []rune

// ToUnits converts s to UTF-16 code units.
func ToUnits(s string) Units {
	u16 := utf16.Encode([]rune(s))
	out := make(Units, len(u16))
	for i, u := range u16 {
		out[i] = rune(u)
	}
	return out
}

// String converts code units back (a lone surrogate becomes U+FFFD).
func (u Units) String() string {
	u16 := make([]uint16, len(u))
	for i, r := range u {
		u16[i] = uint16(r)
	}
	return string(utf16.Decode(u16))
}

// Slice is u.slice(start, end) as a string.
func (u Units) Slice(start, end int) string { return u[start:end].String() }

// isJSSpace is the JS \s set.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// neutral is what the matcher sees in place of non-ASCII characters: a
// private-use code point, which regexp2 counts as neither a word character
// nor a space.
const neutral = 0xE000

// asciiView makes regexp2 see the input the way a JS regex without the u flag
// does: \w, \b and case folding are ASCII-only there, while regexp2 treats
// letters like "é", "中" or U+212A KELVIN SIGN as word characters and folds
// U+212A to "k". Every non-ASCII code unit that isn't JS whitespace becomes
// the neutral character; whitespace stays so \s still matches it. Patterns
// with non-ASCII characters of their own don't get the view (MustCompile
// refuses such a pattern if it also uses \b or \w).
func asciiView(u Units) Units {
	var out Units
	for i, r := range u {
		if r >= 0x80 && !isJSSpace(r) {
			if out == nil {
				out = append(Units(nil), u...)
			}
			out[i] = neutral
		}
	}
	if out == nil {
		return u
	}
	return out
}

// Match is one match: Index and End are UTF-16 indices; Groups holds each
// capture group's [start, end], or [-1, -1] when it didn't take part.
type Match struct {
	Index, End int
	Groups     [][2]int
	units      Units
}

// Text is the matched text.
func (m *Match) Text() string { return m.units.Slice(m.Index, m.End) }

// Group returns capture group n, and whether it took part.
func (m *Match) Group(n int) (string, bool) {
	g := m.Groups[n-1]
	if g[0] < 0 {
		return "", false
	}
	return m.units.Slice(g[0], g[1]), true
}

func (re *Regexp) input(u Units) Units {
	if re.ascii {
		return asciiView(u)
	}
	return u
}

func convert(m *regexp2.Match, u Units) *Match {
	out := &Match{Index: m.Index, End: m.Index + m.Length, units: u}
	groups := m.Groups()
	for _, g := range groups[1:] {
		if len(g.Captures) == 0 {
			out.Groups = append(out.Groups, [2]int{-1, -1})
		} else {
			c := g.Captures[len(g.Captures)-1]
			out.Groups = append(out.Groups, [2]int{c.Index, c.Index + c.Length})
		}
	}
	return out
}

// FindAll is s.matchAll(re) for a global regex.
func (re *Regexp) FindAll(u Units) []*Match {
	in := re.input(u)
	var out []*Match
	m, err := re.re.FindRunesMatch(in)
	for m != nil && err == nil {
		out = append(out, convert(m, u))
		m, err = re.re.FindNextMatch(m)
	}
	if err != nil {
		panic("jsre: " + err.Error())
	}
	return out
}

// Find is re.exec(s) for a non-global regex: the first match, or nil.
func (re *Regexp) Find(u Units) *Match {
	m, err := re.re.FindRunesMatch(re.input(u))
	if err != nil {
		panic("jsre: " + err.Error())
	}
	if m == nil {
		return nil
	}
	return convert(m, u)
}

// Test is re.test(s) for a non-global regex.
func (re *Regexp) Test(s string) bool { return re.Find(ToUnits(s)) != nil }

// ReplaceAll is s.replace(re, repl) with a global regex and a literal
// replacement (no $ patterns).
func (re *Regexp) ReplaceAll(s, repl string) string {
	u := ToUnits(s)
	ms := re.FindAll(u)
	if len(ms) == 0 {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, m := range ms {
		b.WriteString(u.Slice(pos, m.Index))
		b.WriteString(repl)
		pos = m.End
	}
	b.WriteString(u.Slice(pos, len(u)))
	return b.String()
}

// ReplaceFunc is s.replace(re, fn) with a global regex: each match is
// replaced by what fn returns for it.
func (re *Regexp) ReplaceFunc(s string, fn func(m *Match) string) string {
	u := ToUnits(s)
	ms := re.FindAll(u)
	if len(ms) == 0 {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, m := range ms {
		b.WriteString(u.Slice(pos, m.Index))
		b.WriteString(fn(m))
		pos = m.End
	}
	b.WriteString(u.Slice(pos, len(u)))
	return b.String()
}

// Split is s.split(re) for a pattern without capture groups.
func (re *Regexp) Split(s string) []string {
	u := ToUnits(s)
	ms := re.FindAll(u)
	out := make([]string, 0, len(ms)+1)
	pos := 0
	for _, m := range ms {
		if m.End == m.Index && (m.Index == 0 || m.Index == len(u) || m.Index == pos) {
			continue
		}
		out = append(out, u.Slice(pos, m.Index))
		pos = m.End
	}
	out = append(out, u.Slice(pos, len(u)))
	return out
}
