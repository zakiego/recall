package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- same origin ---------------------------------------------------------------------

// CrossOriginWrite is better-auth.ts crossOriginWrite: a write to /api/auth
// marked cross-site by the browser, or whose Origin isn't RECALL_URL's.
func CrossOriginWrite(r *http.Request, appURL string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if r.Header.Get("sec-fetch-site") == "cross-site" {
		return true
	}
	origin := r.Header.Get("origin")
	app, _ := originOf(appURL)
	return origin != "" && origin != app
}

// SameOrigin is session.ts checkSameOrigin for dashboard writes: refused
// when the browser says cross-site, or when an Origin header is there and
// its host is neither RECALL_URL's nor the host the request was sent to.
func SameOrigin(h http.Header, host, appURL string) bool {
	if h.Get("sec-fetch-site") == "cross-site" {
		return false
	}
	origin := h.Get("origin")
	if origin == "" {
		return true
	}
	// new URL(origin).host: normalized for http(s), as written otherwise.
	var originHost string
	if w, err := whatwg.ParseURL(origin); err == nil && w.Host != "" {
		originHost = w.HostPort()
	} else {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" {
			return false
		}
		originHost = u.Host
	}
	if originHost == hostOf(appURL) {
		return true
	}
	if fh := h.Get("x-forwarded-host"); fh != "" {
		host = fh
	}
	return host != "" && originHost == host
}

// originOf is new URL(raw).origin (lowercase host, default port dropped).
func originOf(raw string) (string, bool) {
	u, err := whatwg.ParseURL(raw)
	if err != nil {
		return "", false
	}
	return u.Origin(), true
}

// AppOrigin is new URL(RECALL_URL).origin.
func AppOrigin(appURL string) string {
	o, _ := originOf(appURL)
	return o
}

func hostOf(raw string) string {
	u, err := whatwg.ParseURL(raw)
	if err != nil {
		return ""
	}
	return u.HostPort()
}

// RateLimitIP is Better Auth's getIP for the limiter: an IPv4 address as
// is, an IPv4-mapped IPv6 address as IPv4, other IPv6 addresses as their
// /64 network, anything else one shared bucket (production).
func RateLimitIP(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || a.Zone() != "" {
		return "no-trusted-ip"
	}
	if a.Is4() || a.Is4In6() {
		return a.Unmap().String()
	}
	b := a.As16()
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		if i < 4 {
			parts[i] = fmt.Sprintf("%02x%02x", b[2*i], b[2*i+1])
		} else {
			parts[i] = "0000"
		}
	}
	return strings.Join(parts, ":")
}

// --- rate limits ---------------------------------------------------------------------

// Rule is a rate limit window (seconds) and the requests allowed in it.
type Rule struct {
	Window int64
	Max    int
}

// DefaultRule and PathRules are better-auth.ts rateLimit: 100 per 60 s,
// sign-in, passkey sign-in and password changes tighter. Paths are relative
// to /api/auth and are the Go endpoints (better-auth.ts named Better Auth's).
var (
	DefaultRule = Rule{Window: 60, Max: 100}
	PathRules   = map[string]Rule{
		"/sign-in/username":             {Window: 60, Max: 5},
		"/passkey/authenticate":         {Window: 60, Max: 5},
		"/passkey/authenticate-options": {Window: 60, Max: 10},
		"/change-password":              {Window: 60, Max: 5},
		// @better-auth/oauth-provider's rules.
		"/oauth2/token":              {Window: 60, Max: 20},
		"/oauth2/authorize":          {Window: 60, Max: 30},
		"/oauth2/introspect":         {Window: 60, Max: 100},
		"/oauth2/revoke":             {Window: 60, Max: 30},
		"/oauth2/register":           {Window: 60, Max: 5},
		"/oauth2/client/rotate-keys": {Window: 60, Max: 5},
		"/oauth2/userinfo":           {Window: 60, Max: 60},
	}
)

type bucket struct {
	count       int
	lastRequest int64
	expiresAt   int64
}

// Limiter is Better Auth's memory rate limiter: one bucket per client IP and
// path. A bucket's count resets once a full window passed since its last
// counted request.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	Rules   map[string]Rule
	Default Rule
	// Unix ms; default time.Now.
	Clock func() int64
}

func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, Rules: PathRules, Default: DefaultRule}
}

const maxBuckets = 100_000

// Allow counts one request; when refused it returns the seconds to wait.
func (l *Limiter) Allow(ip, path string) (bool, int64) {
	now := time.Now().UnixMilli()
	if l.Clock != nil {
		now = l.Clock()
	}
	rule, ok := l.Rules[path]
	if !ok {
		rule = l.Default
	}
	window := rule.Window * 1000
	key := ip + "|" + path
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if now >= b.expiresAt {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) > maxBuckets {
		for k := range l.buckets {
			delete(l.buckets, k)
			if len(l.buckets) <= maxBuckets {
				break
			}
		}
	}
	b := l.buckets[key]
	switch {
	case b == nil:
		l.buckets[key] = &bucket{count: 1, lastRequest: now, expiresAt: now + window}
	case now-b.lastRequest >= window:
		b.count, b.lastRequest, b.expiresAt = 1, now, now+window
	case b.count >= rule.Max:
		retry := (b.lastRequest + window - now + 999) / 1000
		return false, retry
	default:
		b.count++
		b.lastRequest, b.expiresAt = now, now+window
	}
	return true, 0
}
