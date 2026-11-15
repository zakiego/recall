package strx

// NFKC as Node's String.prototype.normalize("NFKC") computes it, from data
// exported from that Node (testdata/gen/unicode-nfkc.mjs writes nfkc.json).
// golang.org/x/text is not used here: its tables are an older Unicode version
// than Node 24's ICU, and it applies the stream-safe rule (a U+034F after 30
// combining marks), which ICU does not. Both changed token ids.

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed nfkc.json
var nfkcJSON []byte

type nfkcData struct {
	decomp  map[rune][]rune
	ccc     map[rune]uint8
	compose map[[2]rune]rune
}

var (
	nfkcOnce sync.Once
	nfkcTab  *nfkcData
)

type nfkcFile struct {
	Unicode string           `json:"unicode"`
	Decomp  map[string][]int `json:"decomp"`
	CCC     map[string]int   `json:"ccc"`
	Compose [][3]int         `json:"compose"`
}

func nfkcTables() *nfkcData {
	nfkcOnce.Do(func() {
		var f nfkcFile
		if err := json.Unmarshal(nfkcJSON, &f); err != nil {
			panic("embed: nfkc.json: " + err.Error())
		}
		d := &nfkcData{
			decomp:  make(map[rune][]rune, len(f.Decomp)),
			ccc:     make(map[rune]uint8, len(f.CCC)),
			compose: make(map[[2]rune]rune, len(f.Compose)),
		}
		for k, v := range f.Decomp {
			cp, err := strconv.Atoi(k)
			if err != nil {
				panic("embed: nfkc.json: bad code point " + k)
			}
			rs := make([]rune, len(v))
			for i, x := range v {
				rs[i] = rune(x)
			}
			d.decomp[rune(cp)] = rs
		}
		for k, v := range f.CCC {
			cp, err := strconv.Atoi(k)
			if err != nil || v < 1 || v > 255 {
				panic("embed: nfkc.json: bad ccc entry " + k)
			}
			d.ccc[rune(cp)] = uint8(v)
		}
		for _, c := range f.Compose {
			d.compose[[2]rune{rune(c[0]), rune(c[1])}] = rune(c[2])
		}
		nfkcTab = d
	})
	return nfkcTab
}

// Hangul syllables are composed and decomposed by arithmetic (Unicode 3.12).
const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulLCount = 19
	hangulVCount = 21
	hangulTCount = 28
	hangulNCount = hangulVCount * hangulTCount
	hangulSCount = hangulLCount * hangulNCount
)

// NFKC is s.normalize("NFKC") in Node.
func NFKC(s string) string {
	// Fast path: ASCII is already NFKC.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	t := nfkcTables()
	dec := nfkd(t, s)

	// Canonical composition.
	out := make([]rune, 0, len(dec))
	starter := -1
	var lastClass int
	for _, ch := range dec {
		chClass := int(t.ccc[ch])
		if starter >= 0 && (lastClass < chClass || lastClass == 0) {
			if c, ok := composePair(t, out[starter], ch); ok {
				out[starter] = c
				continue
			}
		}
		if chClass == 0 {
			starter = len(out)
		}
		lastClass = chClass
		out = append(out, ch)
	}
	return string(out)
}

// NFKD is s.normalize("NFKD") in Node.
func NFKD(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return string(nfkd(nfkcTables(), s))
		}
	}
	return s
}

// nfkd is the full compatibility decomposition in canonical order.
func nfkd(t *nfkcData, s string) []rune {
	// Full compatibility decomposition.
	dec := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= hangulSBase && r < hangulSBase+hangulSCount {
			si := r - hangulSBase
			dec = append(dec, hangulLBase+si/hangulNCount, hangulVBase+(si%hangulNCount)/hangulTCount)
			if ti := si % hangulTCount; ti != 0 {
				dec = append(dec, hangulTBase+ti)
			}
			continue
		}
		if m, ok := t.decomp[r]; ok {
			dec = append(dec, m...)
			continue
		}
		dec = append(dec, r)
	}

	// Canonical ordering: each run of non-starters, stably by class.
	for i := 0; i < len(dec); {
		if t.ccc[dec[i]] == 0 {
			i++
			continue
		}
		j := i
		for j < len(dec) && t.ccc[dec[j]] != 0 {
			j++
		}
		run := dec[i:j]
		sort.SliceStable(run, func(a, b int) bool { return t.ccc[run[a]] < t.ccc[run[b]] })
		i = j
	}

	return dec
}

func composePair(t *nfkcData, a, b rune) (rune, bool) {
	// L + V -> LV
	if a >= hangulLBase && a < hangulLBase+hangulLCount && b >= hangulVBase && b < hangulVBase+hangulVCount {
		return hangulSBase + ((a-hangulLBase)*hangulVCount+(b-hangulVBase))*hangulTCount, true
	}
	// LV + T -> LVT
	if a >= hangulSBase && a < hangulSBase+hangulSCount && (a-hangulSBase)%hangulTCount == 0 &&
		b > hangulTBase && b < hangulTBase+hangulTCount {
		return a + (b - hangulTBase), true
	}
	c, ok := t.compose[[2]rune{a, b}]
	return c, ok
}

// NFKCJoin normalizes each part and joins them with sep.
func NFKCJoin(parts []string, sep string) string {
	for i, p := range parts {
		parts[i] = NFKC(p)
	}
	return strings.Join(parts, sep)
}
