package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
)

// users.test.ts "better auth" and the Go build's own dashboard routes.

const authOrigin = "http://localhost:4800"
const authPW = "a long enough password"

type authRes struct {
	status  int
	body    map[string]any
	raw     string
	cookies []*http.Cookie
	header  http.Header
}

// authDo sends a request like users.test.ts authReq: JSON, Origin
// RECALL_URL and a client IP. headers are pairs; a "" value removes one.
func (e *env) authDo(method, path string, body any, headers ...string) authRes {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("origin", authOrigin)
	req.Header.Set("x-forwarded-for", "203.0.113.7")
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := authRes{status: res.StatusCode, raw: string(raw), cookies: res.Cookies(), header: res.Header}
	json.Unmarshal(raw, &out.body)
	return out
}

func (r authRes) cookie(name string) *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (r authRes) jar() string {
	parts := []string{}
	for _, c := range r.cookies {
		if c.MaxAge >= 0 {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

func mustUser(t *testing.T, e *env, username string) users.User {
	t.Helper()
	u, err := users.CreateWithPassword(e.d, username, authPW, "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSignInByUsernameWithLegacyHash(t *testing.T) {
	e := newEnv(t)
	hash, err := auth.HashLegacyPassword("old admin password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.BootstrapUsers(e.d, &hash); err != nil {
		t.Fatal(err)
	}
	res := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "zaki", "password": "old admin password"})
	if res.status != 200 {
		t.Fatal(res.status, res.raw)
	}
	c := res.cookie(auth.CookieName)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Fatalf("cookie %+v", c)
	}
	if !strings.Contains(res.header.Get("set-cookie"), "SameSite=Lax") {
		t.Fatal(res.header.Get("set-cookie"))
	}
	v, err := viewer.FromToken(e.d, c.Value, e.s.Now())
	if err != nil || v == nil || v.Workspace == nil || v.Workspace.ID != "ws_zaki" || v.Workspace.Role != "owner" {
		t.Fatalf("viewer %+v %v", v, err)
	}
	if u := res.body["user"].(map[string]any); u["username"] != "zaki" {
		t.Fatal(res.body)
	}
	wrong := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "zaki", "password": "nope nope nope"})
	if wrong.status != 401 || wrong.body["code"] != "INVALID_USERNAME_OR_PASSWORD" || wrong.cookie(auth.CookieName) != nil {
		t.Fatal(wrong.status, wrong.raw)
	}
	unknown := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "nobody", "password": "nope nope nope"})
	if unknown.status != 401 || unknown.raw != wrong.raw {
		t.Fatal(unknown.status, unknown.raw)
	}
}

func TestNoOpenSignup(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/auth/sign-up/email", "/api/auth/sign-in/email", "/api/auth/update-user", "/api/auth/passkey/generate-authenticate-options"} {
		res := e.authDo("POST", p, map[string]any{"email": "x@example.com", "password": authPW, "name": "x", "username": "intruder"})
		if res.status != 404 || res.body["code"] != "NOT_FOUND" {
			t.Fatal(p, res.status, res.raw)
		}
	}
	var n int
	e.d.QueryRow("SELECT count(*) FROM user").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestSignInRateLimitPerClientIP(t *testing.T) {
	e := newEnv(t)
	mustUser(t, e, "target")
	statuses := []int{}
	for i := 0; i < 7; i++ {
		statuses = append(statuses, e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "target", "password": "wrong password!"}, "cf-connecting-ip", "198.51.100.9").status)
	}
	for _, s := range statuses[:5] {
		if s != 401 {
			t.Fatal(statuses)
		}
	}
	last := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "target", "password": authPW}, "cf-connecting-ip", "198.51.100.9")
	if statuses[6] != 429 || last.status != 429 || last.header.Get("X-Retry-After") == "" || last.body["message"] != "Too many requests. Please try again later." {
		t.Fatal(statuses, last.status, last.raw)
	}
	other := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "target", "password": authPW}, "cf-connecting-ip", "198.51.100.10")
	if other.status != 200 {
		t.Fatal(other.status, other.raw)
	}
}

func TestSignInRefusedFromAnotherOrigin(t *testing.T) {
	e := newEnv(t)
	mustUser(t, e, "target")
	body := map[string]any{"username": "target", "password": authPW}
	res := e.authDo("POST", "/api/auth/sign-in/username", body, "origin", "https://evil.example")
	if res.status != 403 || res.body["code"] != "INVALID_ORIGIN" || len(res.cookies) != 0 {
		t.Fatal(res.status, res.raw)
	}
	if res := e.authDo("POST", "/api/auth/sign-in/username", body, "origin", "", "sec-fetch-site", "cross-site"); res.status != 403 {
		t.Fatal(res.status)
	}
	ok := e.authDo("POST", "/api/auth/sign-in/username", body)
	if ok.status != 200 {
		t.Fatal(ok.status, ok.raw)
	}
	if res := e.authDo("POST", "/api/auth/sign-out", map[string]any{}, "cookie", ok.jar(), "origin", "https://evil.example"); res.status != 403 {
		t.Fatal(res.status)
	}
	// With a session cookie, a write must name its origin.
	if res := e.authDo("POST", "/api/auth/sign-out", map[string]any{}, "cookie", ok.jar(), "origin", ""); res.status != 403 {
		t.Fatal(res.status)
	}
	if res := e.authDo("POST", "/api/auth/sign-out", map[string]any{}, "cookie", ok.jar(), "origin", "", "referer", authOrigin+"/app/settings"); res.status != 200 {
		t.Fatal(res.status, res.raw)
	}
	// A read is not a write.
	if res := e.authDo("GET", "/api/auth/session", nil, "origin", "https://evil.example"); res.status != 200 {
		t.Fatal(res.status)
	}
}

func TestOrganizationEndpointsHidden(t *testing.T) {
	e := newEnv(t)
	u := mustUser(t, e, "mallory")
	ws, err := users.CreateWorkspace(e.d, "Team", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m, err := users.AddMember(e.d, ws.ID, u.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	signed := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "mallory", "password": authPW})
	if signed.status != 200 {
		t.Fatal(signed.status, signed.raw)
	}
	calls := []struct {
		path string
		body any
	}{
		{"/organization/create", map[string]any{"name": "Mine", "slug": "zaki-typo"}},
		{"/organization/update", map[string]any{"organizationId": ws.ID, "data": map[string]any{"name": "Renamed", "slug": "zaki"}}},
		{"/organization/update-member-role", map[string]any{"memberId": m.ID, "role": []string{"member", "admin"}}},
		{"/organization/invite-member", map[string]any{"email": "x@example.com", "role": "owner"}},
		{"/organization/set-active", map[string]any{"organizationId": ws.ID}},
		{"/organization/list", nil},
		{"//organization/create", map[string]any{"name": "Slashes"}},
		{"/%6Frganization/create", map[string]any{"name": "Encoded"}},
		{"/ORGANIZATION/create", map[string]any{"name": "Upper"}},
	}
	for _, c := range calls {
		method := "POST"
		if c.body == nil {
			method = "GET"
		}
		if res := e.authDo(method, "/api/auth"+c.path, c.body, "cookie", signed.jar()); res.status != 404 {
			t.Fatal(c.path, res.status)
		}
	}
	var name, slug string
	e.d.QueryRow("SELECT name, slug FROM organization").Scan(&name, &slug)
	if name != "Team" || slug != "team" {
		t.Fatal(name, slug)
	}
	if role, _ := users.GetRole(e.d, u.ID, ws.ID); role != "admin" {
		t.Fatal(role)
	}
}

func TestPasskeyChallengesStoredServerSide(t *testing.T) {
	e := newEnv(t)
	res := e.authDo("GET", "/api/auth/passkey/authenticate-options", nil)
	if res.status != 200 || res.body["rpId"] != "localhost" {
		t.Fatal(res.status, res.raw)
	}
	challenge, _ := res.body["challenge"].(string)
	if challenge == "" || res.cookie(auth.ChallengeCookie) == nil {
		t.Fatal(res.raw)
	}
	rows, _ := e.d.Query("SELECT value FROM verification")
	defer rows.Close()
	found := false
	for rows.Next() {
		var v string
		rows.Scan(&v)
		found = found || strings.Contains(v, challenge)
	}
	if !found {
		t.Fatal("challenge not stored")
	}
}

func TestSessionSignOutAndChangePassword(t *testing.T) {
	e := newEnv(t)
	u := mustUser(t, e, "eva")
	ws, _ := users.CreateWorkspace(e.d, "Eva", "", "")
	users.AddMember(e.d, ws.ID, u.ID, "owner")

	if res := e.authDo("GET", "/api/auth/session", nil); res.status != 200 || res.raw != "null" {
		t.Fatal(res.status, res.raw)
	}
	a := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "EVA", "password": authPW})
	b := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "eva", "password": authPW})
	if a.status != 200 || b.status != 200 {
		t.Fatal(a.raw, b.raw)
	}
	s := e.authDo("GET", "/api/auth/session", nil, "cookie", a.jar())
	sess := s.body["session"].(map[string]any)
	if s.status != 200 || sess["activeOrganizationId"] != ws.ID || s.body["user"].(map[string]any)["username"] != "eva" {
		t.Fatal(s.raw)
	}

	// Wrong current password, then a too-short new one.
	if res := e.authDo("POST", "/api/auth/change-password", map[string]any{"currentPassword": "not it at all", "newPassword": "another long password"}, "cookie", a.jar()); res.status != 400 || res.body["code"] != "INVALID_PASSWORD" {
		t.Fatal(res.status, res.raw)
	}
	if res := e.authDo("POST", "/api/auth/change-password", map[string]any{"currentPassword": authPW, "newPassword": "short"}, "cookie", a.jar()); res.status != 400 || res.body["code"] != "PASSWORD_TOO_SHORT" {
		t.Fatal(res.status, res.raw)
	}
	if res := e.authDo("POST", "/api/auth/change-password", map[string]any{"currentPassword": authPW, "newPassword": "x"}); res.status != 401 {
		t.Fatal(res.status, res.raw)
	}
	// revokeOtherSessions: b ends, a gets a new token, the old one stops.
	ch := e.authDo("POST", "/api/auth/change-password", map[string]any{"currentPassword": authPW, "newPassword": "another long password", "revokeOtherSessions": true}, "cookie", a.jar())
	if ch.status != 200 || ch.cookie(auth.CookieName) == nil {
		t.Fatal(ch.status, ch.raw)
	}
	for _, jar := range []string{a.jar(), b.jar()} {
		if res := e.authDo("GET", "/api/auth/session", nil, "cookie", jar); res.raw != "null" {
			t.Fatal(res.raw)
		}
	}
	if res := e.authDo("GET", "/api/auth/session", nil, "cookie", ch.jar()); res.status != 200 || res.raw == "null" {
		t.Fatal(res.raw)
	}
	if _, ok, _ := users.VerifyCredentials(e.d, "eva", "another long password"); !ok {
		t.Fatal("new password does not verify")
	}

	out := e.authDo("POST", "/api/auth/sign-out", map[string]any{}, "cookie", ch.jar())
	if out.status != 200 || out.body["success"] != true {
		t.Fatal(out.raw)
	}
	if c := out.cookie(auth.CookieName); c == nil || c.MaxAge >= 0 {
		t.Fatalf("%+v", c)
	}
	if res := e.authDo("GET", "/api/auth/session", nil, "cookie", ch.jar()); res.raw != "null" {
		t.Fatal(res.raw)
	}
	if res := e.authDo("GET", "/api/auth/sign-out", nil); res.status != 405 {
		t.Fatal(res.status)
	}
}

func TestOAuthHandoffKeepsTheSignedQuery(t *testing.T) {
	e := newEnv(t)
	res := e.authDo("GET", "/app/oauth/sign-in?client_id=abc&ba_param=1&ba_param=2&sig=x%2By", nil)
	if res.status != 302 || res.header.Get("location") != "/app/login?oauth=1" || res.header.Get("cache-control") != "no-store" {
		t.Fatal(res.status, res.header)
	}
	if got := res.header.Get("set-cookie"); got != "recall_oauth_query=client_id%3Dabc%26ba_param%3D1%26ba_param%3D2%26sig%3Dx%252By; Path=/app; Max-Age=600; SameSite=Lax" {
		t.Fatal(got)
	}
	res = e.authDo("GET", "/app/oauth/approve", nil)
	if res.status != 302 || res.header.Get("location") != "/app/oauth/consent" || res.header.Get("set-cookie") != "" {
		t.Fatal(res.status, res.header)
	}
}
