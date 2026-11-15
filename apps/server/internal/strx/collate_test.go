package strx

// testdata/collation.json from testdata/gen/collation-goldens.ts.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/testutil"
)

func TestLocaleCompareMatchesICU(t *testing.T) {
	var g struct {
		CharTable []string `json:"charTable"`
		Pairs     [][3]any `json:"pairs"`
	}
	testutil.LoadJSON(t, "collation.json", &g)
	for i, row := range g.CharTable {
		for j, want := range strings.Split(row, ",") {
			w, _ := strconv.Atoi(want)
			if got := LocaleCompare(string(rune(i)), string(rune(j))); got != w {
				t.Errorf("%q vs %q: %d, want %d", rune(i), rune(j), got, w)
			}
		}
	}
	for _, p := range g.Pairs {
		a, b, want := p[0].(string), p[1].(string), int(p[2].(float64))
		if got := LocaleCompare(a, b); got != want {
			t.Errorf("%q vs %q: %d, want %d", a, b, got, want)
		}
	}
}
