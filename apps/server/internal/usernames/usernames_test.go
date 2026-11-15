package usernames_test

import (
	"testing"

	"github.com/zakiego/recall/apps/server/internal/testutil"
	"github.com/zakiego/recall/apps/server/internal/usernames"
)

func TestUsernamesMatchTS(t *testing.T) {
	var g struct {
		Usernames []struct {
			Input      string  `json:"input"`
			Normalized string  `json:"normalized"`
			Problem    *string `json:"problem"`
			Email      string  `json:"email"`
		} `json:"usernames"`
	}
	testutil.LoadJSON(t, "m1.json", &g)
	if len(g.Usernames) < 10 {
		t.Fatal("golden incomplete")
	}
	for _, c := range g.Usernames {
		want := ""
		if c.Problem != nil {
			want = *c.Problem
		}
		if got := usernames.Problem(c.Input); got != want {
			t.Errorf("Problem(%q) = %q, want %q", c.Input, got, want)
		}
		if got := usernames.Normalize(c.Input); got != c.Normalized {
			t.Errorf("Normalize(%q) = %q", c.Input, got)
		}
		if got := usernames.PlaceholderEmail(c.Input); got != c.Email {
			t.Errorf("PlaceholderEmail(%q) = %q", c.Input, got)
		}
	}
}
