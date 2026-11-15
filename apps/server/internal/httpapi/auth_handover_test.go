package httpapi_test

// The M6 extra gate: rows Better Auth wrote (testdata/auth-handover.json,
// from testdata/gen/auth-goldens.ts) loaded into a Go database. A connector
// connected before the cutover must keep working: its access token reaches
// /mcp, its refresh token is redeemed, an unused code is exchanged, a
// confidential client's encrypted secret still checks, and a consent query
// Better Auth signed is accepted.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

type handover struct {
	Now    int64                       `json:"now"`
	Tables map[string][]map[string]any `json:"tables"`
	Values map[string]string           `json:"values"`
}

func TestOAuthHandoverFromBetterAuth(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/auth-handover.json")
	if err != nil {
		t.Fatal(err)
	}
	var h handover
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	var g struct {
		Origin, Secret, Password string
	}
	gr, _ := os.ReadFile("../../testdata/auth.json")
	if err := json.Unmarshal(gr, &g); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECALL_URL", g.Origin)
	t.Setenv("SESSION_SECRET", g.Secret)
	now := h.Now
	d := testkit.OpenDB(t)
	for _, table := range []string{"user", "account", "organization", "member", "session", "oauth_resource", "oauth_client", "oauth_client_resource", "oauth_consent", "oauth_refresh_token", "oauth_access_token", "verification"} {
		for _, row := range h.Tables[table] {
			cols, marks, vals := []string{}, []string{}, []any{}
			for k, v := range row {
				if f, ok := v.(float64); ok {
					v = int64(f)
				}
				cols = append(cols, `"`+k+`"`)
				marks = append(marks, "?")
				vals = append(vals, v)
			}
			if _, err := d.Exec(`INSERT INTO "`+table+`" (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`)`, vals...); err != nil {
				t.Fatalf("%s: %v", table, err)
			}
		}
	}
	lim := auth.NewLimiter()
	lim.Clock = func() int64 { return now }
	srv := httptest.NewServer((&httpapi.Server{DB: d, Now: func() int64 { return now }, Limiter: lim}).Handler())
	t.Cleanup(srv.Close)
	v := h.Values
	post := func(path string, form url.Values, headers ...string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(form.Encode()))
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
		return res.StatusCode, out
	}

	// The access token Better Auth issued reaches /mcp in its workspace.
	c, err := oauth.MCPCaller(d, v["accessToken"], now)
	if err != nil || c == nil || c.WorkspaceID != "ws_home" {
		t.Fatalf("caller %+v %v", c, err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("authorization", "Bearer "+v["accessToken"])
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("/mcp: %v %v", res.StatusCode, err)
	}
	res.Body.Close()

	// Its refresh token is redeemed, and the new access token works too.
	status, body := post("/api/auth/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {v["refreshToken"]}, "client_id": {v["claudeClientId"]}})
	if status != 200 || !strings.HasPrefix(body["access_token"].(string), "rco_") {
		t.Fatalf("refresh: %d %v", status, body)
	}
	if c, _ := oauth.MCPCaller(d, body["access_token"].(string), now); c == nil || c.WorkspaceID != "ws_home" {
		t.Fatal("refreshed token")
	}

	// A code Better Auth issued is exchanged.
	status, body = post("/api/auth/oauth2/token", url.Values{"grant_type": {"authorization_code"}, "code": {v["code"]}, "redirect_uri": {v["redirect"]}, "client_id": {v["claudeClientId"]}, "code_verifier": {v["verifier"]}, "resource": {v["resource"]}})
	if status != 200 || body["refresh_token"] == nil {
		t.Fatalf("code: %d %v", status, body)
	}

	// The confidential client's secret, encrypted by Better Auth, checks.
	status, body = post("/api/auth/oauth2/introspect", url.Values{"token": {v["confAccessToken"]}, "client_id": {v["confClientId"]}, "client_secret": {v["confClientSecret"]}})
	if status != 200 || body["active"] != true {
		t.Fatalf("introspect: %d %v", status, body)
	}
	status, body = post("/api/auth/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {v["confRefreshToken"]}, "client_id": {v["confClientId"]}, "client_secret": {"wrong"}})
	if status != 400 || body["error_description"] != "invalid client_secret" {
		t.Fatalf("wrong secret: %d %v", status, body)
	}

	// A consent query Better Auth signed is accepted from a Go session.
	signIn, _ := http.NewRequest("POST", srv.URL+"/api/auth/sign-in/username", strings.NewReader(`{"username":"zaki","password":"`+g.Password+`"}`))
	signIn.Header.Set("content-type", "application/json")
	signIn.Header.Set("origin", g.Origin)
	res, err = http.DefaultClient.Do(signIn)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("sign-in: %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	var cookie string
	for _, ck := range res.Cookies() {
		if ck.Name == auth.CookieName {
			cookie = ck.Name + "=" + ck.Value
		}
	}
	consentBody, _ := json.Marshal(map[string]any{"accept": true, "oauth_query": v["consentQuery"]})
	creq, _ := http.NewRequest("POST", srv.URL+"/api/auth/oauth2/consent", strings.NewReader(string(consentBody)))
	creq.Header.Set("content-type", "application/json")
	creq.Header.Set("origin", g.Origin)
	creq.Header.Set("cookie", cookie)
	res, err = http.DefaultClient.Do(creq)
	if err != nil {
		t.Fatal(err)
	}
	var consent map[string]any
	json.NewDecoder(res.Body).Decode(&consent)
	res.Body.Close()
	u, _ := url.Parse(consent["url"].(string))
	if res.StatusCode != 200 || u.Query().Get("code") == "" {
		t.Fatalf("consent: %d %v", res.StatusCode, consent)
	}
}
