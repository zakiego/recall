package sensitive_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/sensitive"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type golden struct {
	Sensitive []struct {
		Text   string           `json:"text"`
		Hits   []sensitive.Hit  `json:"hits"`
		Kinds  []sensitive.Kind `json:"kinds"`
		Masked string           `json:"masked"`
		Count  int              `json:"count"`
	} `json:"sensitive"`
	Luhn []struct {
		D  string `json:"d"`
		OK bool   `json:"ok"`
	} `json:"luhn"`
	NIK []struct {
		D  string `json:"d"`
		OK bool   `json:"ok"`
	} `json:"nik"`
	IBAN []struct {
		S  string `json:"s"`
		OK bool   `json:"ok"`
	} `json:"iban"`
	Entropy []struct {
		S string   `json:"s"`
		H *float64 `json:"h"`
	} `json:"entropy"`
}

// findSensitive, sensitiveKinds and maskSecrets on about 3500 texts: the
// sensitive.test.ts examples, near misses and a fuzz corpus with Unicode noise.
func TestSensitiveMatchesTS(t *testing.T) {
	var g golden
	testutil.LoadJSON(t, "text.json", &g)
	if len(g.Sensitive) < 3000 {
		t.Fatal("golden incomplete")
	}
	bad := 0
	for _, c := range g.Sensitive {
		hits := sensitive.Find(c.Text, nil)
		kinds := sensitive.Kinds(c.Text)
		masked, count := sensitive.Mask(c.Text)
		if fmt.Sprint(hits) != fmt.Sprint(nilToEmpty(c.Hits)) || fmt.Sprint(kinds) != fmt.Sprint(nilToEmptyK(c.Kinds)) || masked != c.Masked || count != c.Count {
			bad++
			if bad <= 10 {
				t.Errorf("%q:\n hits %v want %v\n kinds %v want %v\n masked %q (%d) want %q (%d)", c.Text, hits, c.Hits, kinds, c.Kinds, masked, count, c.Masked, c.Count)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d texts differ", bad, len(g.Sensitive))
	}
}

func nilToEmpty(h []sensitive.Hit) []sensitive.Hit {
	if h == nil {
		return []sensitive.Hit{}
	}
	return h
}

func nilToEmptyK(k []sensitive.Kind) []sensitive.Kind {
	if k == nil {
		return []sensitive.Kind{}
	}
	return k
}

func TestCheckersMatchTS(t *testing.T) {
	var g golden
	testutil.LoadJSON(t, "text.json", &g)
	for _, c := range g.Luhn {
		if sensitive.Luhn(c.D) != c.OK {
			t.Errorf("luhn %q", c.D)
		}
	}
	for _, c := range g.NIK {
		if sensitive.NikShaped(c.D) != c.OK {
			t.Errorf("nik %q", c.D)
		}
	}
	for _, c := range g.IBAN {
		if sensitive.IbanValid(c.S) != c.OK {
			t.Errorf("iban %q", c.S)
		}
	}
	for _, c := range g.Entropy {
		got := sensitive.Entropy(c.S)
		if c.H == nil {
			if !math.IsNaN(got) {
				t.Errorf("entropy %q = %v, want NaN", c.S, got)
			}
			continue
		}
		if got != *c.H {
			t.Errorf("entropy %q = %v, want %v", c.S, got, *c.H)
		}
	}
}
