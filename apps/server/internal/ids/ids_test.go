package ids_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

var idPattern = regexp.MustCompile(`^(mem|doc|chk|key|usr|acc|mbr|ws|inv|con|per)_[0-9abcdefghjkmnpqrstvwxyz]{16}$`)

// Ids the TS build made and ids Go makes have the same form.
func TestIDsHaveTheTSForm(t *testing.T) {
	var g struct {
		IDs []string `json:"ids"`
	}
	testutil.LoadJSON(t, "m1.json", &g)
	if len(g.IDs) != 220 {
		t.Fatalf("%d ids", len(g.IDs))
	}
	for _, id := range g.IDs {
		if !idPattern.MatchString(id) {
			t.Errorf("TS id %q", id)
		}
		prefix := id[:strings.Index(id, "_")]
		if got := ids.New(prefix); !idPattern.MatchString(got) || !strings.HasPrefix(got, prefix+"_") {
			t.Errorf("Go id %q", got)
		}
	}
}

// Each byte maps to ALPHABET[b & 31].
func TestFromBytes(t *testing.T) {
	b := []byte{0, 1, 31, 32, 33, 255, 128, 10, 18, 19, 20, 21, 26, 27, 30, 64}
	if got := ids.FromBytes("mem", b); got != "mem_01z01z0ajkmntvy0" {
		t.Fatalf("%s", got)
	}
}
