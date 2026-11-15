package ratelimit_test

import (
	"errors"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

// rate-limit.test.ts "lets a key ask up to the limit, then throws a 429 with Retry-After".
func TestLimitAsk(t *testing.T) {
	l := ratelimit.New(2, 60_000)
	if err := ratelimit.LimitAsk("key_a", l); err != nil {
		t.Fatal(err)
	}
	if err := ratelimit.LimitAsk("key_a", l); err != nil {
		t.Fatal(err)
	}
	err := ratelimit.LimitAsk("key_a", l)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 429 || he.Type != "rate_limited" || he.Headers["retry-after"] != "60" ||
		he.Message != "Too many asks with this API key. Try again in 60 s." {
		t.Fatalf("%#v", err)
	}
	if err := ratelimit.LimitAsk("key_b", l); err != nil {
		t.Fatal(err)
	}
	if ratelimit.AskLimiter == nil || ratelimit.AskMax != 60 || ratelimit.AskWindowMs != 600_000 {
		t.Fatal("ask limit changed")
	}
}

// rate-limit.test.ts "opens again once the window passes", plus the TS trace.
func TestTakeMatchesTS(t *testing.T) {
	l := ratelimit.New(1, 1_000)
	if l.Take("k", 0) != 0 || l.Take("k", 400) != 1 || l.Take("k", 1_000) != 0 {
		t.Fatal("window")
	}
	var g struct {
		RateLimit []struct {
			Key  string `json:"key"`
			Now  int64  `json:"now"`
			Wait int    `json:"wait"`
		} `json:"rateLimit"`
	}
	testutil.LoadJSON(t, "m1.json", &g)
	l = ratelimit.New(3, 10_000)
	for i, s := range g.RateLimit {
		if got := l.Take(s.Key, s.Now); got != s.Wait {
			t.Errorf("step %d: %d, want %d", i, got, s.Wait)
		}
	}
}
