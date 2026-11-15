package strx

import (
	"encoding/json"
	"strconv"
	"testing"
)

type nfkcChecks struct {
	Unicode   string           `json:"unicode"`
	Singles   map[string][]int `json:"singles"`
	Sequences [][2][]int       `json:"sequences"`
}

func runesToString(cps []int) string {
	rs := make([]rune, len(cps))
	for i, c := range cps {
		rs[i] = rune(c)
	}
	return string(rs)
}

// Every code point normalizes the way Node's normalize("NFKC") does, and so
// do the crafted sequences (long runs of marks, Hangul, Unicode 16 and 17
// characters).
func TestNFKCMatchesNode(t *testing.T) {
	var c nfkcChecks
	if err := json.Unmarshal(nfkcJSON, &c); err != nil {
		t.Fatal(err)
	}
	if c.Unicode == "" || len(c.Singles) < 4000 || len(c.Sequences) < 10 {
		t.Fatalf("nfkc.json looks incomplete: unicode %q, %d singles, %d sequences", c.Unicode, len(c.Singles), len(c.Sequences))
	}
	bad := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		in := string(cp)
		want := in
		if v, ok := c.Singles[strconv.Itoa(int(cp))]; ok {
			want = runesToString(v)
		}
		if got := NFKC(in); got != want {
			bad++
			if bad <= 10 {
				t.Errorf("U+%04X: got %q, want %q", cp, got, want)
			}
		}
	}
	for _, s := range c.Sequences {
		in, want := runesToString(s[0]), runesToString(s[1])
		if got := NFKC(in); got != want {
			bad++
			t.Errorf("%+q: got %+q, want %+q", in, got, want)
		}
	}
	if bad > 0 {
		t.Fatalf("%d differences from Node (Unicode %s)", bad, c.Unicode)
	}
}

// NFKD: every code point decomposes to Node's NFKD of it (the decomp table
// is that, Hangul is algorithmic), and the crafted sequences match Node.
func TestNFKDMatchesNode(t *testing.T) {
	var c struct {
		Decomp      map[string][]int `json:"decomp"`
		SequencesKD [][2][]int       `json:"sequencesKD"`
	}
	if err := json.Unmarshal(nfkcJSON, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Decomp) < 5000 || len(c.SequencesKD) < 10 {
		t.Fatalf("nfkc.json looks incomplete: %d decompositions, %d sequences", len(c.Decomp), len(c.SequencesKD))
	}
	bad := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if (cp >= 0xD800 && cp <= 0xDFFF) || (cp >= 0xAC00 && cp <= 0xD7A3) {
			continue
		}
		want := string(cp)
		if v, ok := c.Decomp[strconv.Itoa(int(cp))]; ok {
			want = runesToString(v)
		}
		if got := NFKD(string(cp)); got != want {
			bad++
			if bad <= 10 {
				t.Errorf("U+%04X: got %+q, want %+q", cp, got, want)
			}
		}
	}
	for _, s := range c.SequencesKD {
		in, want := runesToString(s[0]), runesToString(s[1])
		if got := NFKD(in); got != want {
			bad++
			t.Errorf("%+q: got %+q, want %+q", in, got, want)
		}
	}
	// Hangul: 각 is ᄀ ᅡ ᆨ.
	if got := NFKD("각"); got != "각" {
		t.Errorf("hangul: %+q", got)
	}
	if bad > 0 {
		t.Fatalf("%d differences from Node", bad)
	}
}

func TestMarkAndUpperLetter(t *testing.T) {
	for r, want := range map[rune][2]bool{
		'A': {false, true}, 'a': {false, false}, 'É': {false, true}, 'ǅ': {false, false}, 'Σ': {false, true},
		0x0301: {true, false}, 0x0903: {true, false}, 0x20DD: {true, false}, '1': {false, false}, 0x1E900: {false, true},
	} {
		if IsMark(r) != want[0] || IsUpperLetter(r) != want[1] {
			t.Errorf("U+%04X: mark %v upper %v", r, IsMark(r), IsUpperLetter(r))
		}
	}
}
