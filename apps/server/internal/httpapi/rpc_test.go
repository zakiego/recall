package httpapi_test

import (
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
)

func TestRPCFramework(t *testing.T) {
	if n := len(httpapi.RPCNames()); n != 48 {
		t.Fatalf("%d server functions, want 48", n)
	}
	e := newEnv(t)
	if r := e.authDo("GET", "/app/api/rpc/nope", nil); r.status != 404 || r.body["code"] != "NOT_FOUND" {
		t.Fatal(r.status, r.raw)
	}
	if r := e.authDo("POST", "/app/api/rpc/getSession", map[string]any{}); r.status != 405 || r.body["code"] != "METHOD_NOT_ALLOWED" || r.header.Get("allow") != "GET" {
		t.Fatal(r.status, r.raw)
	}
	if r := e.authDo("GET", "/app/api/rpc/getContainers", nil); r.status != 401 || r.body["error"] != "UNAUTHORIZED" {
		t.Fatal(r.status, r.raw)
	}
	if r := e.authDo("GET", "/app/api/rpc/getChain?input=%7Bnope", nil); r.status != 400 || r.body["code"] != "VALIDATION" {
		t.Fatal(r.status, r.raw)
	}
	if r := e.authDo("GET", "/app/api/rpc/getSession", nil); r.status != 200 || r.header.Get("cache-control") != "no-store" {
		t.Fatal(r.status, r.raw)
	}
}

// Regressions from the M7 review.
func TestRPCRenewsAndClearsTheSessionCookie(t *testing.T) {
	g := newGmailEnv(t)
	e := &env{t: t, d: g.s.DB, s: g.s, srv: g.srv}
	// A day later the session is extended and its cookie sent again.
	g.now += 25 * 60 * 60 * 1000
	r := e.authDo("GET", "/app/api/rpc/getContainers", nil, "cookie", g.cookies["zaki"])
	if r.status != 200 || r.cookie(auth.CookieName) == nil || r.cookie(auth.CookieName).MaxAge <= 0 {
		t.Fatal(r.status, r.header)
	}
	// A cookie for no session is cleared.
	r = e.authDo("GET", "/app/api/rpc/getSession", nil, "cookie", auth.CookieName+"=nope")
	if c := r.cookie(auth.CookieName); c == nil || c.MaxAge >= 0 {
		t.Fatal(r.header)
	}
}

func TestRPCTakesLargeTextAndNormalizesConnectorPaths(t *testing.T) {
	g := newGmailEnv(t)
	e := &env{t: t, d: g.s.DB, s: g.s, srv: g.srv}
	big := strings.Repeat("界", 900_000) // 2.7 MB as UTF-8
	r := e.authDo("POST", "/app/api/rpc/addText", map[string]any{"container": "inbox", "kind": "text", "value": big}, "cookie", g.cookies["zaki"])
	if r.status != 200 {
		t.Fatal(r.status, r.raw[:min(len(r.raw), 200)])
	}
	if res := g.get("/app/connectors/google/start/?c=inbox", g.cookies["zaki"]); res.status != 302 || !strings.HasPrefix(res.location, "https://accounts.google.com/") {
		t.Fatal(res)
	}
}
