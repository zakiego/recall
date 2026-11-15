// Package ratelimit is rate-limit.ts: a fixed-window counter per key, in memory.
package ratelimit

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/httpx"
)

type window struct {
	start int64
	n     int
}

// Limiter allows max calls per key in each window.
type Limiter struct {
	mu       sync.Mutex
	max      int
	windowMs int64
	hits     map[string]*window
}

// New returns a limiter of max calls per windowMs.
func New(max int, windowMs int64) *Limiter {
	return &Limiter{max: max, windowMs: windowMs, hits: map[string]*window{}}
}

// Take returns seconds to wait, or 0 when the call is allowed.
func (l *Limiter) Take(key string, now int64) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cur, ok := l.hits[key]
	if !ok || cur.start <= now-l.windowMs {
		l.hits[key] = &window{start: now, n: 1}
		if len(l.hits) > 10_000 {
			l.prune(now)
		}
		return 0
	}
	if cur.n >= l.max {
		return int(math.Ceil(float64(cur.start+l.windowMs-now) / 1000))
	}
	cur.n++
	return 0
}

func (l *Limiter) prune(now int64) {
	for k, v := range l.hits {
		if v.start <= now-l.windowMs {
			delete(l.hits, k)
		}
	}
}

// AskMax and AskWindowMs: every ask is a gateway call. Generous for an agent
// asking in bursts, but a leaked or looping key can't run up the model bill.
const (
	AskMax      = 60
	AskWindowMs = 10 * 60_000
)

// AskLimiter is the process-wide ask limiter.
var AskLimiter = New(AskMax, AskWindowMs)

// LimitAsk returns a 429 with Retry-After once keyID has used up its asks.
func LimitAsk(keyID string, l *Limiter) error {
	if l == nil {
		l = AskLimiter
	}
	wait := l.Take(keyID, time.Now().UnixMilli())
	if wait > 0 {
		return httpx.WithHeaders(429, "rate_limited", fmt.Sprintf("Too many asks with this API key. Try again in %d s.", wait), map[string]string{"retry-after": strconv.Itoa(wait)})
	}
	return nil
}
