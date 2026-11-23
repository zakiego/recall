package httpapi_test

// Regressions from the M6 reviews (docs/GO-PORT.md M6 review log).

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/users"
)

// oauthEnv is a signed-in user with a workspace and a registered public client.
type oauthEnv struct {
	*env
	clientID string
	cookie   string
}

const reviewRedirect = "https://claude.ai/api/mcp/auth_callback"

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	t.Setenv("SESSION_SECRET", "review-session-secret-0123456789abcdefgh")
	e := newEnv(t)
	u := mustUser(t, e, "zane")
	ws, _ := users.CreateWorkspace(e.d, "Home", "ws_home", "")
	users.AddMember(e.d, ws.ID, u.ID, users.Owner)
	reg := e.authDo("POST", "/api/auth/oauth2/register", map[string]any{"client_name": "Claude", "redirect_uris": []string{reviewRedirect}, "token_endpoint_auth_method": "none"})
	if reg.status != 201 {
		t.Fatal(reg.raw)
	}
	in := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "zane", "password": authPW})
	return &oauthEnv{env: e, clientID: reg.body["client_id"].(string), cookie: in.jar()}
}

const reviewVerifier = "review-verifier-0123456789-abcdefghijklmnopqrstuvwxyz"

func (o *oauthEnv) authorize(t *testing.T, extra url.Values) authRes {
	t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {o.clientID}, "redirect_uri": {reviewRedirect}, "code_challenge": {oauthChallenge(reviewVerifier)}, "code_challenge_method": {"S256"}, "state": {"s"}, "scope": {"claudeai offline_access"}}
	for k, v := range extra {
		q[k] = v
	}
	return o.authDo("GET", "/api/auth/oauth2/authorize?"+q.Encode(), nil, "cookie", o.cookie, "accept", "application/json")
}

func oauthChallenge(v string) string {
	return strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(b64sha(v)), "=")
}

// consent approves the request authorize sent to the consent page and returns the code.
func (o *oauthEnv) consent(t *testing.T, res authRes) string {
	t.Helper()
	u, _ := res.body["url"].(string)
	if !strings.HasPrefix(u, "/app/oauth/approve?") {
		return codeFrom(t, u)
	}
	c := o.authDo("POST", "/api/auth/oauth2/consent", map[string]any{"accept": true, "oauth_query": strings.SplitN(u, "?", 2)[1]}, "cookie", o.cookie)
	if c.status != 200 {
		t.Fatal(c.raw)
	}
	return codeFrom(t, c.body["url"].(string))
}

func codeFrom(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("no code in %q", raw)
	}
	return u.Query().Get("code")
}

func (o *oauthEnv) form(t *testing.T, path string, f url.Values, headers ...string) (int, map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("POST", o.srv.URL+path, strings.NewReader(f.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	return res.StatusCode, out, res.Header
}

func (o *oauthEnv) tokens(t *testing.T, code string) map[string]any {
	t.Helper()
	status, body, _ := o.form(t, "/api/auth/oauth2/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {o.clientID}, "redirect_uri": {reviewRedirect}, "code_verifier": {reviewVerifier}})
	if status != 200 {
		t.Fatal(status, body)
	}
	return body
}

func TestPasskeyCookieCannotConsumeOtherVerificationRows(t *testing.T) {
	o := newOAuthEnv(t)
	code := o.consent(t, o.authorize(t, nil))
	var identifier string
	o.d.QueryRow(`SELECT identifier FROM verification WHERE value LIKE '%authorization_code%'`).Scan(&identifier)
	res := o.authDo("POST", "/api/auth/passkey/authenticate", map[string]any{"response": map[string]any{"id": "x"}}, "cookie", auth.ChallengeCookie+"="+identifier)
	if res.status != 400 || res.body["code"] != "CHALLENGE_NOT_FOUND" {
		t.Fatal(res.status, res.raw)
	}
	// The code is still there and still works.
	o.tokens(t, code)
}

func TestRefreshBoundToDPoPIsRefused(t *testing.T) {
	o := newOAuthEnv(t)
	tok := o.tokens(t, o.consent(t, o.authorize(t, nil)))
	o.d.Exec(`UPDATE oauth_refresh_token SET confirmation = '{"jkt":"abc"}'`)
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {o.clientID}}
	status, body, _ := o.form(t, "/api/auth/oauth2/token", refresh)
	if status != 400 || body["error"] != "invalid_dpop_proof" || body["error_description"] != "DPoP proof header is required" {
		t.Fatal(status, body)
	}
	// A proof the Go build can't verify doesn't unlock it either.
	status, body, _ = o.form(t, "/api/auth/oauth2/token", refresh, "DPoP", "eyJhbGciOiJFUzI1NiJ9.e30.sig")
	if status != 400 || body["error"] != "invalid_dpop_proof" {
		t.Fatal(status, body)
	}
}

func TestEmptyScopeGrantsNothing(t *testing.T) {
	o := newOAuthEnv(t)
	o.consent(t, o.authorize(t, nil))
	code := o.consent(t, o.authorize(t, url.Values{"scope": {""}}))
	status, body, _ := o.form(t, "/api/auth/oauth2/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {o.clientID}, "redirect_uri": {reviewRedirect}, "code_verifier": {reviewVerifier}})
	if status != 400 || body["error"] != "invalid_scope" || body["error_description"] != "client does not allow scope " {
		t.Fatal(status, body)
	}
}

func TestConsentUpdateKeepsResources(t *testing.T) {
	o := newOAuthEnv(t)
	o.consent(t, o.authorize(t, url.Values{"resource": {"http://localhost:4800/mcp"}}))
	o.consent(t, o.authorize(t, url.Values{"prompt": {"consent"}}))
	var res string
	o.d.QueryRow(`SELECT resources FROM oauth_consent`).Scan(&res)
	if res != `["http://localhost:4800/mcp"]` {
		t.Fatal(res)
	}
	// So a request for that resource needs no new consent.
	r := o.authorize(t, url.Values{"resource": {"http://localhost:4800/mcp"}})
	if u, _ := r.body["url"].(string); !strings.HasPrefix(u, reviewRedirect+"?code=") {
		t.Fatal(r.raw)
	}
}

func TestOAuthBodyAndTokenEdges(t *testing.T) {
	o := newOAuthEnv(t)
	// No body and no content type: the media type check still runs.
	req, _ := http.NewRequest("POST", o.srv.URL+"/api/auth/oauth2/token", nil)
	res, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 415 || !strings.Contains(string(b), "Content-Type is required") {
		t.Fatal(res.StatusCode, string(b))
	}
	// A JSON array on register is the OAuth envelope.
	r := o.authDo("POST", "/api/auth/oauth2/register", []any{})
	if r.status != 400 || r.body["error"] != "invalid_client_metadata" || r.body["error_description"] != "Invalid input: expected object, received array" {
		t.Fatal(r.raw)
	}
	// Userinfo without a token, and with two.
	r = o.authDo("GET", "/api/auth/oauth2/userinfo", nil)
	if r.status != 401 || r.body["error_description"] != "access token not found" || r.header.Get("WWW-Authenticate") != "" {
		t.Fatal(r.status, r.raw)
	}
	status, body, _ := o.form(t, "/api/auth/oauth2/userinfo", url.Values{"access_token": {"rco_x"}}, "authorization", "Bearer rco_y")
	if status != 400 || body["error_description"] != "Multiple access token transport methods are not allowed" {
		t.Fatal(status, body)
	}
	// Blank max_age.
	r = o.authorize(t, url.Values{"max_age": {""}})
	if u, _ := r.body["url"].(string); !strings.Contains(u, "max_age%3A+Too+small%3A+expected+string+to+have+%3E%3D1+characters") {
		t.Fatal(r.raw)
	}
	// Revoke trims the token like Better Auth.
	tok := o.tokens(t, o.consent(t, o.authorize(t, nil)))
	status, _, _ = o.form(t, "/api/auth/oauth2/revoke", url.Values{"token": {"  " + tok["access_token"].(string) + " "}, "client_id": {o.clientID}})
	if status != 200 {
		t.Fatal(status)
	}
	var n int
	o.d.QueryRow(`SELECT count(*) FROM oauth_access_token`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestSkipConsentClient(t *testing.T) {
	o := newOAuthEnv(t)
	o.d.Exec(`UPDATE oauth_client SET skip_consent = 1`)
	r := o.authorize(t, nil)
	if u, _ := r.body["url"].(string); !strings.HasPrefix(u, reviewRedirect+"?code=") {
		t.Fatal(r.raw)
	}
}

func TestSignInUnknownUserStillHashes(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("x", auth.MaxPasswordLength+1)
	r := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "nobody", "password": long})
	if r.status != 400 || r.body["code"] != "PASSWORD_TOO_LONG" {
		t.Fatal(r.status, r.raw)
	}
}

func b64sha(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestRateLimitGroupsAnIPv6Network(t *testing.T) {
	e := newEnv(t)
	mustUser(t, e, "target")
	var last authRes
	for i := 1; i <= 6; i++ {
		last = e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "target", "password": "wrong password!"}, "cf-connecting-ip", "2001:db8:1:2::"+strings.Repeat("1", 1)+string(rune('0'+i)))
	}
	if last.status != 429 {
		t.Fatal(last.status, last.raw)
	}
	if got := auth.RateLimitIP("2001:db8:1:2::16"); got != "2001:0db8:0001:0002:0000:0000:0000:0000" {
		t.Fatal(got)
	}
	if got := auth.RateLimitIP("::ffff:203.0.113.9"); got != "203.0.113.9" {
		t.Fatal(got)
	}
	if got := auth.RateLimitIP("local"); got != "no-trusted-ip" {
		t.Fatal(got)
	}
}

func TestAuthErrorAndOKPages(t *testing.T) {
	e := newEnv(t)
	r := e.authDo("GET", "/api/auth/error?error=invalid_client&error_description=client+not+found", nil)
	if r.status != 302 || r.header.Get("Location") != "/?error=invalid_client&error_description=client+not+found" {
		t.Fatal(r.status, r.header)
	}
	r = e.authDo("GET", "/api/auth/error?error=%3Cscript%3E", nil)
	if r.header.Get("Location") != "/?error=UNKNOWN" {
		t.Fatal(r.header.Get("Location"))
	}
	if r := e.authDo("GET", "/api/auth/ok", nil); r.status != 200 || r.raw != `{"ok":true}` {
		t.Fatal(r.raw)
	}
}

func TestOriginsAreNormalized(t *testing.T) {
	t.Setenv("RECALL_URL", "http://LocalHost:4800")
	e := newEnv(t)
	mustUser(t, e, "zane")
	// The browser sends the normalized origin.
	if r := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "zane", "password": authPW}); r.status != 200 {
		t.Fatal(r.status, r.raw)
	}
}

func TestDashboardAuthTakesJSONOnly(t *testing.T) {
	e := newEnv(t)
	mustUser(t, e, "zane")
	r := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": "zane", "password": authPW}, "content-type", "text/plain")
	if r.status != 415 || r.body["code"] != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatal(r.status, r.raw)
	}
}

func TestReviewTwoOAuthEdges(t *testing.T) {
	o := newOAuthEnv(t)
	tok := o.tokens(t, o.consent(t, o.authorize(t, nil)))
	// A confirmation without a jkt doesn't bind the refresh token.
	o.d.Exec(`UPDATE oauth_refresh_token SET confirmation = 'null'`)
	status, body, _ := o.form(t, "/api/auth/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {o.clientID}})
	if status != 200 {
		t.Fatal(status, body)
	}
	// Userinfo with the token of a disabled client is an invalid token.
	o.d.Exec(`UPDATE oauth_client SET disabled = 1`)
	r := o.authDo("GET", "/api/auth/oauth2/userinfo", nil, "authorization", "Bearer "+body["access_token"].(string))
	if r.status != 401 || r.body["error"] != "invalid_token" {
		t.Fatal(r.status, r.raw)
	}
	// Consent reports every validation issue.
	r = o.authDo("POST", "/api/auth/oauth2/consent", map[string]any{"scope": 5}, "cookie", o.cookie)
	if r.status != 400 || r.body["message"] != "[body.accept] Invalid input: expected boolean, received undefined; [body.scope] Invalid input: expected string, received number" {
		t.Fatal(r.raw)
	}
}

// A passkey can only be added from a fresh session, checked before the prompt.
func TestRegisterOptionsNeedAFreshSession(t *testing.T) {
	g := newGmailEnv(t)
	e := &env{t: t, d: g.s.DB, s: g.s, srv: g.srv}
	if r := e.authDo("GET", "/api/auth/passkey/register-options?name=Laptop", nil, "cookie", g.cookies["zane"]); r.status != 200 || r.body["user"].(map[string]any)["name"] != "Laptop" || r.body["user"].(map[string]any)["displayName"] != "zane" {
		t.Fatal(r.status, r.raw)
	}
	g.now += 25 * 60 * 60 * 1000
	if r := e.authDo("GET", "/api/auth/passkey/register-options", nil, "cookie", g.cookies["zane"]); r.status != 403 || r.body["code"] != "SESSION_NOT_FRESH" {
		t.Fatal(r.status, r.raw)
	}
}
