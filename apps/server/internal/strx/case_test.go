package strx

import (
	"encoding/json"
	"testing"
)

// toLowerCase and toUpperCase as Node computes them, on crafted sequences
// (final sigma in every context, multi-code-point mappings) and on every
// code point alone.
func TestCaseMatchesNode(t *testing.T) {
	var f struct {
		Unicode   string           `json:"unicode"`
		Lower     map[string][]int `json:"lower"`
		Sequences [][3][]int       `json:"sequences"`
	}
	if err := json.Unmarshal(caseJSON, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Lower) < 1400 || len(f.Sequences) < 20 {
		t.Fatalf("case.json looks incomplete: %d lower, %d sequences", len(f.Lower), len(f.Sequences))
	}
	for _, s := range f.Sequences {
		in, lower, upper := runesToString(s[0]), runesToString(s[1]), runesToString(s[2])
		if got := Lower(in); got != lower {
			t.Errorf("Lower(%+q) = %+q, want %+q", in, got, lower)
		}
		if got := Upper(in); got != upper {
			t.Errorf("Upper(%+q) = %+q, want %+q", in, got, upper)
		}
	}
	bad := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		want := string(cp)
		if v, ok := f.Lower[itoa(int(cp))]; ok {
			want = runesToString(v)
		}
		if cp == 'Σ' {
			want = "σ"
		}
		if got := Lower(string(cp)); got != want {
			bad++
			if bad < 10 {
				t.Errorf("Lower(U+%04X) = %+q, want %+q", cp, got, want)
			}
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
