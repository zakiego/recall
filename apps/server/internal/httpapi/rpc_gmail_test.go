package httpapi_test

// connectors.test.ts "configuration" and "connect": the Gmail connect
// routes /app/connectors/google/start and /callback.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
)

type gmailEnv struct {
	t       *testing.T
	s       *httpapi.Server
	srv     *httptest.Server
	ws      map[string]string
	ids     map[string]string
	cookies map[string]string
	now     int64
}

func newGmailEnv(t *testing.T) *gmailEnv {
	t.Helper()
	for k, v := range map[string]string{"GOOGLE_CLIENT_ID": "test-client.apps.googleusercontent.com", "GOOGLE_CLIENT_SECRET": "test-client-secret", "CONNECTOR_SECRET": "test-connector-secret-0123456789abcdefghijklmnop", "RECALL_URL": "http://localhost:4800", "RECALL_AUTH_RATE_LIMIT": "off", "DATA_DIR": t.TempDir()} {
		t.Setenv(k, v)
	}
	d := testkit.OpenDB(t)
	g := &gmailEnv{t: t, ws: map[string]string{}, ids: map[string]string{}, cookies: map[string]string{}, now: 1_800_000_000_000}
	g.s = &httpapi.Server{DB: d, Now: func() int64 { return g.now }}
	g.srv = httptest.NewServer(g.s.Handler())
	t.Cleanup(g.srv.Close)
	a, _ := users.CreateWorkspace(d, "Alpha", "", "")
	b, _ := users.CreateWorkspace(d, "Beta", "", "")
	g.ws["a"], g.ws["b"] = a.ID, b.ID
	e := &env{t: t, d: d, s: g.s, srv: g.srv}
	for _, x := range []struct{ name, ws, role string }{{"zaki", a.ID, users.Owner}, {"eva", a.ID, users.Admin}, {"mallory", b.ID, users.Owner}} {
		u, err := users.CreateWithPassword(d, x.name, "correct horse battery staple", "")
		if err != nil {
			t.Fatal(err)
		}
		g.ids[x.name] = u.ID
		users.AddMember(d, x.ws, u.ID, x.role)
		r := e.authDo("POST", "/api/auth/sign-in/username", map[string]any{"username": x.name, "password": "correct horse battery staple"})
		g.cookies[x.name] = r.jar()
	}
	return g
}

type gmailRes struct {
	status   int
	location string
	cookies  []string
}

func (g *gmailEnv) get(path, cookie string, headers ...string) gmailRes {
	g.t.Helper()
	req, _ := http.NewRequest("GET", g.srv.URL+path, nil)
	if cookie != "" {
		req.Header.Set("cookie", cookie)
	}
	req.Header.Set("sec-fetch-site", "same-origin")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return gmailRes{status: res.StatusCode, location: res.Header.Get("location"), cookies: res.Header.Values("set-cookie")}
}

func (g *gmailEnv) start(name string, headers ...string) gmailRes {
	return g.get("/app/connectors/google/start?c=inbox", g.cookies[name], headers...)
}

// connect runs the flow for a user; tweak changes the state, cookie or query.
func (g *gmailEnv) connect(name string, fake *connectors.FakeGoogle, state, cookie, query string) gmailRes {
	g.t.Helper()
	g.s.Google = fake.Do
	st := g.start(name)
	if st.status != 302 {
		g.t.Fatal(st)
	}
	u, _ := url.Parse(st.location)
	if state == "" {
		state = u.Query().Get("state")
	}
	stateCookie := strings.SplitN(st.cookies[0], ";", 2)[0]
	if cookie == "" {
		cookie = g.cookies[name] + "; " + stateCookie
	}
	if query == "" {
		query = "code=4%2F0Afake&state=" + state + "&scope=" + url.QueryEscape(connectors.GmailScope)
	}
	return g.get("/app/connectors/google/callback?"+query, cookie)
}

func (g *gmailEnv) connections() int {
	var n int
	g.s.DB.QueryRow(`SELECT count(*) FROM connector_accounts`).Scan(&n)
	return n
}

func TestGmailStartNotConfigured(t *testing.T) {
	g := newGmailEnv(t)
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("CONNECTOR_SECRET", "too-short")
	if r := g.start("zaki"); r.location != "/app/connectors?gmail=not_configured&c=inbox" {
		t.Fatal(r)
	}
}

func TestGmailStartSendsToGoogle(t *testing.T) {
	g := newGmailEnv(t)
	r := g.start("zaki")
	loc, _ := url.Parse(r.location)
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Fatal(r.location)
	}
	q := loc.Query()
	for k, v := range map[string]string{"client_id": "test-client.apps.googleusercontent.com", "redirect_uri": "http://localhost:4800/app/connectors/google/callback", "response_type": "code", "scope": connectors.GmailScope, "access_type": "offline", "prompt": "consent", "code_challenge_method": "S256"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q", k, q.Get(k))
		}
	}
	if len(q.Get("code_challenge")) != 43 {
		t.Fatal(q.Get("code_challenge"))
	}
	set := r.cookies[0]
	if !strings.HasPrefix(set, "recall.gmail_oauth=v1.") || !strings.Contains(set, "HttpOnly") || !strings.Contains(set, "SameSite=Lax") || !strings.Contains(set, "Path=/app/connectors/google") {
		t.Fatal(set)
	}
}

func TestGmailStartSignedOutAndCrossSite(t *testing.T) {
	g := newGmailEnv(t)
	if r := g.get("/app/connectors/google/start?c=inbox", ""); !strings.HasPrefix(r.location, "/app/login") {
		t.Fatal(r)
	}
	r := g.start("zaki", "sec-fetch-site", "cross-site")
	if r.location != "/app/connectors?gmail=failed&c=inbox" || len(r.cookies) != 0 {
		t.Fatal(r)
	}
}

func TestGmailConnectStoresSealedTokens(t *testing.T) {
	g := newGmailEnv(t)
	r := g.connect("zaki", connectors.NewFakeGoogle(connectors.FakeOptions{}), "", "", "")
	if r.location != "/app/connectors?gmail=connected&c=inbox" || !strings.Contains(r.cookies[0], "Max-Age=0") {
		t.Fatal(r)
	}
	row, ok, _ := connectors.GetConnection(g.s.DB, g.ws["a"], g.ids["zaki"])
	if !ok || row.Provider != "gmail" || row.AccountEmail == nil || *row.AccountEmail != "zaki@example.com" || row.Status != "active" || row.Scope != connectors.GmailScope || !strings.HasPrefix(row.Tokens, "v1.") || strings.Contains(row.Tokens, "fake-") {
		t.Fatalf("%+v", row)
	}
	if _, ok, _ := connectors.GetConnection(g.s.DB, g.ws["b"], g.ids["zaki"]); ok {
		t.Fatal("other workspace")
	}
	if _, ok, _ := connectors.GetConnection(g.s.DB, g.ws["a"], g.ids["eva"]); ok {
		t.Fatal("other user")
	}
}

func TestGmailCallbackRefusals(t *testing.T) {
	g := newGmailEnv(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	if r := g.connect("zaki", fake, "forged", "", ""); !strings.Contains(r.location, "gmail=failed") {
		t.Fatal(r)
	}
	if r := g.connect("zaki", fake, "", g.cookies["zaki"], ""); !strings.Contains(r.location, "gmail=failed") {
		t.Fatal(r)
	}
	st := g.start("zaki")
	u, _ := url.Parse(st.location)
	state := u.Query().Get("state")
	stateCookie := strings.SplitN(st.cookies[0], ";", 2)[0]
	// Eva's browser finishing a flow Zaki started.
	if r := g.get("/app/connectors/google/callback?code=x&state="+state, g.cookies["eva"]+"; "+stateCookie); !strings.Contains(r.location, "gmail=failed") {
		t.Fatal(r)
	}
	g.now += 11 * 60_000
	if r := g.get("/app/connectors/google/callback?code=x&state="+state, g.cookies["zaki"]+"; "+stateCookie); !strings.Contains(r.location, "gmail=expired") {
		t.Fatal(r)
	}
	if g.connections() != 0 {
		t.Fatal(g.connections())
	}
}

func TestGmailConsentRefused(t *testing.T) {
	g := newGmailEnv(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	if r := g.connect("zaki", fake, "", "", "error=access_denied"); !strings.Contains(r.location, "gmail=failed") {
		t.Fatal(r)
	}
	st := g.start("zaki")
	u, _ := url.Parse(st.location)
	r := g.get("/app/connectors/google/callback?error=access_denied&state="+u.Query().Get("state"), g.cookies["zaki"]+"; "+strings.SplitN(st.cookies[0], ";", 2)[0])
	if r.location != "/app/connectors?gmail=denied&c=inbox" || g.connections() != 0 {
		t.Fatal(r)
	}
}

func TestGmailReconnectRevokesOldGrant(t *testing.T) {
	g := newGmailEnv(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	g.connect("zaki", fake, "", "", "")
	g.connect("zaki", fake, "", "", "")
	if strings.Join(fake.RevokedTokens, ",") != "fake-refresh-1" || g.connections() != 1 {
		t.Fatal(fake.RevokedTokens, g.connections())
	}
}
