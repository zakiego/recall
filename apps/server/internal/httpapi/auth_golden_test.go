package httpapi_test

// Replays testdata/auth.json (testdata/gen/auth-goldens.ts: the real
// Better Auth oauth-provider) against the Go server. Random values (client
// ids, codes, tokens, row ids, signatures) are paired with Go's by
// position the first time they differ; after that a pair must stay the
// same both ways, and requests carry the Go side of every pair.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
)

var tokenRe = regexp.MustCompile(`[A-Za-z0-9_%+\-]+`)

// pairs maps TS random values to Go's and back.
type pairs struct {
	ts2go, go2ts map[string]string
	// now, when set, makes any two times at or after it (ms, within a
	// year) equal: TS wrote SQLite's real clock where Go writes the fixed one.
	now float64
}

func (p *pairs) pair(ts, gov string) error {
	if g, ok := p.ts2go[ts]; ok {
		if g != gov {
			return fmt.Errorf("TS %q was Go %q, now %q", ts, g, gov)
		}
		return nil
	}
	if t, ok := p.go2ts[gov]; ok {
		return fmt.Errorf("Go %q was TS %q, now TS %q", gov, t, ts)
	}
	p.ts2go[ts], p.go2ts[gov] = gov, ts
	return nil
}

func (p *pairs) prefixPaired(a, b string) bool {
	for t, g := range p.ts2go {
		if strings.HasPrefix(t, a) && strings.HasPrefix(g, b) {
			return true
		}
	}
	return false
}

func (p *pairs) matchString(ts, gov string) error {
	if ts == gov {
		return nil
	}
	tt := tokenRe.FindAllStringIndex(ts, -1)
	gt := tokenRe.FindAllStringIndex(gov, -1)
	if len(tt) != len(gt) {
		return fmt.Errorf("TS %q\n   Go %q", ts, gov)
	}
	// The text between tokens must be the same.
	sep := func(s string, idx [][]int) []string {
		out, last := []string{}, 0
		for _, ix := range idx {
			out = append(out, s[last:ix[0]])
			last = ix[1]
		}
		return append(out, s[last:])
	}
	if strings.Join(sep(ts, tt), "|") != strings.Join(sep(gov, gt), "|") {
		return fmt.Errorf("TS %q\n   Go %q", ts, gov)
	}
	for i := range tt {
		a, b := ts[tt[i][0]:tt[i][1]], gov[gt[i][0]:gt[i][1]]
		if a == b {
			continue
		}
		if len(a) < 16 || len(b) < 16 {
			// A prefix of a paired value (an API key's shown prefix).
			if len(a) == len(b) && len(a) >= 8 && p.prefixPaired(a, b) {
				continue
			}
			// The parts of a sealed value (v1.<nonce>.<ciphertext>.<tag>) are random.
			if strings.HasPrefix(ts, "v1.") && strings.HasPrefix(gov, "v1.") && len(a) == len(b) {
				if err := p.pair(a, b); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("token %q vs %q in\n   TS %q\n   Go %q", a, b, ts, gov)
		}
		if err := p.pair(a, b); err != nil {
			return err
		}
	}
	return nil
}

func (p *pairs) match(ts, gov any, where string) error {
	switch t := ts.(type) {
	case *jsonx.Object:
		g, ok := gov.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("%s: TS object, Go %s", where, jsonx.Stringify(gov))
		}
		if strings.Join(t.Keys(), ",") != strings.Join(g.Keys(), ",") {
			return fmt.Errorf("%s: keys\n   TS %v\n   Go %v", where, t.Keys(), g.Keys())
		}
		for _, k := range t.Keys() {
			a, _ := t.Get(k)
			b, _ := g.Get(k)
			if err := p.match(a, b, where+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		g, ok := gov.([]any)
		if !ok || len(g) != len(t) {
			return fmt.Errorf("%s:\n   TS %s\n   Go %s", where, jsonx.Stringify(ts), jsonx.Stringify(gov))
		}
		for i := range t {
			if err := p.match(t[i], g[i], fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
		return nil
	case string:
		g, ok := gov.(string)
		if !ok {
			return fmt.Errorf("%s: TS %q, Go %v", where, t, gov)
		}
		if err := p.matchString(t, g); err != nil {
			return fmt.Errorf("%s: %v", where, err)
		}
		return nil
	}
	if _, ok := ts.(float64); ok && p.now > 0 && strings.HasSuffix(where, ".timingMs") {
		return nil // wall-clock time
	}
	if a, ok := ts.(float64); ok && p.now > 0 {
		if b, ok := gov.(float64); ok && a >= p.now && b >= p.now && a < p.now+366*86400000 && b < p.now+366*86400000 {
			return nil
		}
	}
	if jsonx.Stringify(ts) != jsonx.Stringify(gov) {
		return fmt.Errorf("%s: TS %s, Go %s", where, jsonx.Stringify(ts), jsonx.Stringify(gov))
	}
	return nil
}

// toGo replaces every paired TS value in s by Go's.
func (p *pairs) toGo(s string) string {
	return tokenRe.ReplaceAllStringFunc(s, func(t string) string {
		if g, ok := p.ts2go[t]; ok {
			return g
		}
		return t
	})
}

type goldenReq struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
	Jar     string            `json:"jar"`
}

type authGolden struct {
	Now      int64  `json:"now"`
	Origin   string `json:"origin"`
	Secret   string `json:"secret"`
	Password string `json:"password"`
	Setup    struct {
		Users      []struct{ Username, Password string }
		Workspaces []struct{ ID, Name string }
		Members    []struct{ Workspace, User, Role string }
	} `json:"setup"`
	Steps []*jsonx.Object `json:"steps"`
}

var keepHeaders = []string{"content-type", "location", "cache-control", "pragma", "www-authenticate", "x-retry-after", "access-control-allow-origin", "allow"}

func goRows(t *testing.T, e *env) *jsonx.Object {
	t.Helper()
	out := jsonx.NewMap[any]()
	for _, table := range []string{"oauth_resource", "oauth_client", "oauth_client_resource", "oauth_consent", "oauth_access_token", "oauth_refresh_token", "verification"} {
		where := ""
		if table == "verification" {
			where = " WHERE value LIKE '%authorization_code%'"
		}
		rs, err := e.d.Query("SELECT * FROM " + table + where + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		list := []any{}
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			row := jsonx.NewMap[any]()
			for i, c := range cols {
				switch v := vals[i].(type) {
				case int64:
					row.Set(c, float64(v))
				case []byte:
					row.Set(c, string(v))
				default:
					row.Set(c, v)
				}
			}
			list = append(list, row)
		}
		rs.Close()
		out.Set(table, list)
	}
	return out
}

func TestOAuthGolden(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	var g authGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECALL_URL", g.Origin)
	t.Setenv("SESSION_SECRET", g.Secret)
	t.Setenv("RECALL_AUTH_RATE_LIMIT", "on")
	now := g.Now
	d := testkit.OpenDB(t)
	lim := auth.NewLimiter()
	lim.Clock = func() int64 { return now }
	s := &httpapi.Server{DB: d, Now: func() int64 { return now }, Limiter: lim}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	e := &env{t: t, d: d, s: s, srv: srv}
	ids := map[string]string{}
	for _, u := range g.Setup.Users {
		row, err := users.CreateWithPassword(d, u.Username, u.Password, "")
		if err != nil {
			t.Fatal(err)
		}
		ids[u.Username] = row.ID
	}
	for _, w := range g.Setup.Workspaces {
		if _, err := users.CreateWorkspace(d, w.Name, w.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range g.Setup.Members {
		if _, err := users.AddMember(d, m.Workspace, ids[m.User], m.Role); err != nil {
			t.Fatal(err)
		}
	}
	p := &pairs{ts2go: map[string]string{}, go2ts: map[string]string{}}
	jars := map[string]string{}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path string, headers map[string]string, body *string, jar string) (int, http.Header, string) {
		var rd io.Reader
		if body != nil {
			rd = strings.NewReader(p.toGo(*body))
		}
		req, _ := http.NewRequest(method, srv.URL+p.toGo(path), rd)
		for k, v := range headers {
			if strings.EqualFold(k, "authorization") && strings.HasPrefix(v, "Basic ") {
				dec, _ := base64.StdEncoding.DecodeString(v[6:])
				v = "Basic " + base64.StdEncoding.EncodeToString([]byte(p.toGo(string(dec))))
			}
			req.Header.Set(k, p.toGo(v))
		}
		if jar != "" {
			req.Header.Set("cookie", jars[jar])
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}
	compareRes := func(name string, want *jsonx.Object, status int, h http.Header, body string, withCookie bool) {
		wantStatus, _ := want.Get("status")
		if float64(status) != wantStatus.(float64) {
			t.Fatalf("%s: status %d, want %v\n   body %s", name, status, wantStatus, body)
		}
		wh, _ := want.Get("headers")
		gotH := jsonx.NewMap[any]()
		keys := append([]string(nil), keepHeaders...)
		if withCookie {
			keys = append(keys, "set-cookie")
		}
		for _, k := range keys {
			if k == "set-cookie" {
				gotH.Set(k, strings.Join(h.Values("Set-Cookie"), "\n"))
				continue
			}
			if v := h.Get(k); v != "" {
				gotH.Set(k, v)
			}
		}
		// Header order doesn't matter: compare sorted.
		sorted := func(o *jsonx.Object) *jsonx.Object {
			ks := o.Keys()
			sort.Strings(ks)
			out := jsonx.NewMap[any]()
			for _, k := range ks {
				v, _ := o.Get(k)
				out.Set(k, v)
			}
			return out
		}
		if err := p.match(sorted(wh.(*jsonx.Object)), sorted(gotH), name+" headers"); err != nil {
			t.Fatal(err)
		}
		wantBody, _ := want.Get("body")
		isJSON, _ := want.Get("json")
		var got any = body
		if isJSON == true {
			got, err = jsonx.Parse([]byte(body))
			if err != nil {
				t.Fatalf("%s: body not JSON: %q", name, body)
			}
		}
		if err := p.match(wantBody, got, name+" body"); err != nil {
			t.Fatal(err)
		}
	}
	for i, st := range g.Steps {
		kind, _ := st.Get("kind")
		nameV, _ := st.Get("name")
		name := fmt.Sprintf("step %d %q", i, nameV)
		switch kind {
		case "http":
			rv, _ := st.Get("req")
			var r goldenReq
			if err := json.Unmarshal([]byte(jsonx.Stringify(rv)), &r); err != nil {
				t.Fatal(err)
			}
			status, h, body := send(r.Method, r.Path, r.Headers, r.Body, r.Jar)
			res, _ := st.Get("res")
			compareRes(name, res.(*jsonx.Object), status, h, body, strings.HasPrefix(r.Path, "/app/oauth/"))
		case "signIn":
			user, _ := st.Get("user")
			jar, _ := st.Get("jar")
			b := map[string]any{"username": user, "password": g.Password}
			if q, ok := st.Get("oauthQuery"); ok && q != nil {
				b["oauth_query"] = p.toGo(q.(string))
			}
			bodyText := jsonx.Stringify(b)
			status, h, body := send("POST", "/api/auth/sign-in/username", map[string]string{"content-type": "application/json", "origin": g.Origin, "x-forwarded-for": fmt.Sprintf("198.51.100.%d", i%250)}, &bodyText, "")
			if status != 200 {
				t.Fatalf("%s: sign-in %d %s", name, status, body)
			}
			for _, c := range (&http.Response{Header: h}).Cookies() {
				if c.Name == auth.CookieName {
					jars[jar.(string)] = c.Name + "=" + c.Value
				}
			}
			if res, ok := st.Get("res"); ok && res != nil {
				compareRes(name, res.(*jsonx.Object), status, h, body, false)
			}
		case "caller":
			tok, _ := st.Get("token")
			c, err := oauth.MCPCaller(d, p.toGo(tok.(string)), now)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if c != nil {
				got = jsonx.ObjectOf("workspaceId", c.WorkspaceID, "keyId", c.KeyID)
			}
			want, _ := st.Get("res")
			if err := p.match(want, got, name); err != nil {
				t.Fatal(err)
			}
		case "sql":
			q, _ := st.Get("sql")
			stmt := p.toGo(q.(string))
			if _, err := d.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case "advance":
			ms, _ := st.Get("ms")
			now += int64(ms.(float64))
		default:
			t.Fatalf("%s: kind %v", name, kind)
		}
		if want, ok := st.Get("rows"); ok && want != nil {
			if err := p.match(want, goRows(t, e), name+" rows"); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("%d steps, %d paired values", len(g.Steps), len(p.ts2go))
}
