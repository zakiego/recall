package strx

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// Case mappings of the Node that runs the TS build (testdata/gen/unicode-nfkc.mjs
// writes case.json): Go's unicode tables are an older Unicode version, and
// toLowerCase maps some code points to more than one.
//
//go:embed case.json
var caseJSON []byte

type caseTables struct {
	lower     map[rune]string
	upper     map[rune]string
	ignorable map[rune]bool
	cased     map[rune]bool
}

var (
	caseOnce sync.Once
	caseTab  *caseTables
)

func cases() *caseTables {
	caseOnce.Do(func() {
		var f struct {
			Lower     map[string][]int `json:"lower"`
			Upper     map[string][]int `json:"upper"`
			Ignorable []int            `json:"ignorable"`
			Cased     []int            `json:"cased"`
		}
		if err := json.Unmarshal(caseJSON, &f); err != nil {
			panic("strx: case.json: " + err.Error())
		}
		conv := func(m map[string][]int) map[rune]string {
			out := make(map[rune]string, len(m))
			for k, v := range m {
				cp, err := strconv.Atoi(k)
				if err != nil {
					panic("strx: case.json: " + k)
				}
				rs := make([]rune, len(v))
				for i, x := range v {
					rs[i] = rune(x)
				}
				out[rune(cp)] = string(rs)
			}
			return out
		}
		t := &caseTables{lower: conv(f.Lower), upper: conv(f.Upper), ignorable: map[rune]bool{}, cased: map[rune]bool{}}
		for _, c := range f.Ignorable {
			t.ignorable[rune(c)] = true
		}
		for _, c := range f.Cased {
			t.cased[rune(c)] = true
		}
		caseTab = t
	})
	return caseTab
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Lower is String.prototype.toLowerCase.
func Lower(s string) string {
	if isASCII(s) {
		return strings.ToLower(s)
	}
	t := cases()
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		if r == 'Σ' {
			if finalSigma(t, runes, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
			continue
		}
		if m, ok := t.lower[r]; ok {
			b.WriteString(m)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// finalSigma is ICU's rule: skipping case-ignorable characters, a cased
// letter comes before the sigma and none comes after it.
func finalSigma(t *caseTables, runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		c := runes[j]
		if t.ignorable[c] {
			continue
		}
		before = t.cased[c] || c == 'Σ'
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		c := runes[j]
		if t.ignorable[c] {
			continue
		}
		return !(t.cased[c] || c == 'Σ')
	}
	return true
}

// Upper is String.prototype.toUpperCase.
func Upper(s string) string {
	if isASCII(s) {
		return strings.ToUpper(s)
	}
	t := cases()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if m, ok := t.upper[r]; ok {
			b.WriteString(m)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Slice16Range is s.slice(start, end) in UTF-16 code units (negative or
// out-of-range bounds clamp like JS). A cut through a surrogate pair leaves
// U+FFFD for the half.
func Slice16Range(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	if start < 0 {
		start = max(len(u)+start, 0)
	}
	if end < 0 {
		end = max(len(u)+end, 0)
	}
	start, end = min(start, len(u)), min(end, len(u))
	if start >= end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}
