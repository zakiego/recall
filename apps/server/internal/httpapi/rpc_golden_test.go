package httpapi_test

// Replays testdata/rpc.json (testdata/gen/rpc-goldens.ts: the real
// fns.ts, connector-fns.ts and oauth-fns.ts) through /app/api/rpc/<name>.
// Random values pair as in TestOAuthGolden; a time written at or after the
// fixed clock matches any such time (TS also wrote SQLite's real clock).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
	"github.com/zakiego/recall/apps/server/internal/users"
)

var rpcTables = map[string]string{
	"organization":      "SELECT * FROM organization ORDER BY rowid",
	"member":            "SELECT * FROM member ORDER BY rowid",
	"session":           "SELECT id, user_id, active_organization_id FROM session ORDER BY rowid",
	"invitation":        "SELECT * FROM invitation ORDER BY rowid",
	"api_keys":          "SELECT * FROM api_keys ORDER BY rowid",
	"memories":          "SELECT * FROM memories ORDER BY rowid",
	"documents":         "SELECT * FROM documents ORDER BY rowid",
	"jobs":              "SELECT id, workspace_id, type, payload, status, attempts, run_after, created_at FROM jobs ORDER BY rowid",
	"people":            "SELECT * FROM people ORDER BY rowid",
	"reminder_settings": "SELECT * FROM reminder_settings ORDER BY rowid",
	"passkey":           "SELECT * FROM passkey ORDER BY rowid",
	"app_connectors":    "SELECT * FROM app_connectors ORDER BY rowid",
}

func queryRows(t *testing.T, d *db.DB, q string) []any {
	t.Helper()
	rs, err := d.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	list := []any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rs.Scan(ptrs...)
		row := jsonx.NewMap[any]()
		for i, c := range cols {
			switch v := vals[i].(type) {
			case int64:
				row.Set(c, float64(v))
			case float64:
				row.Set(c, v)
			case []byte:
				row.Set(c, string(v))
			default:
				row.Set(c, v)
			}
		}
		list = append(list, row)
	}
	return list
}

type rpcGolden struct {
	Now             int64             `json:"now"`
	Origin          string            `json:"origin"`
	Secret          string            `json:"secret"`
	ConnectorSecret string            `json:"connectorSecret"`
	Password        string            `json:"password"`
	Vectors         map[string]string `json:"vectors"`
	Setup           struct {
		Users      []struct{ Username, Password string }
		Workspaces []struct{ ID, Name string }
		Members    []struct{ Workspace, User, Role string }
	} `json:"setup"`
	Steps []*jsonx.Object `json:"steps"`
}

func TestRPCMatchesTS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testutil.Root(), "testdata", "rpc.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g rpcGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"RECALL_URL": g.Origin, "SESSION_SECRET": g.Secret, "CONNECTOR_SECRET": g.ConnectorSecret, "RECALL_JOBS": "off", "DATA_DIR": t.TempDir()} {
		t.Setenv(k, v)
	}
	replayVectors(t, g.Vectors)
	file := filepath.Join(t.TempDir(), "recall.db")
	copyFile(t, filepath.Join(testutil.Root(), "testdata", "search", "seeded.db"), file)
	d, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := g.Now
	lim := auth.NewLimiter()
	lim.Clock = func() int64 { return now }
	s := &httpapi.Server{DB: d, Now: func() int64 { return now }, Limiter: lim}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
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
	if _, err := d.Exec("UPDATE member SET created_at = created_at + rowid"); err != nil {
		t.Fatal(err)
	}
	jars := map[string]string{}
	for _, u := range g.Setup.Users {
		body, _ := json.Marshal(map[string]string{"username": u.Username, "password": u.Password})
		req, _ := http.NewRequest("POST", srv.URL+"/api/auth/sign-in/username", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("origin", g.Origin)
		req.Header.Set("x-forwarded-for", "198.51.100.1")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("sign-in %s: %v %v", u.Username, res.StatusCode, err)
		}
		res.Body.Close()
		for _, c := range res.Cookies() {
			if c.Name == auth.CookieName {
				jars[u.Username] = c.Name + "=" + c.Value
			}
		}
	}
	p := &pairs{ts2go: map[string]string{}, go2ts: map[string]string{}, now: float64(g.Now)}
	// Rows as of before the first step, so a step compares only what TS saw change.
	for i, st := range g.Steps {
		nameV, _ := st.Get("name")
		if q, ok := st.Get("sql"); ok && q != nil {
			if _, err := d.Exec(p.toGo(q.(string))); err != nil {
				t.Fatalf("step %d %q: %v", i, nameV, err)
			}
			continue
		}
		fn, _ := st.Get("fn")
		name := fmt.Sprintf("step %d %q (%s)", i, nameV, fn)
		hv, _ := st.Get("headers")
		headers := map[string]string{}
		if ho, ok := hv.(*jsonx.Object); ok {
			for _, k := range ho.Keys() {
				v, _ := ho.Get(k)
				headers[k] = p.toGo(v.(string))
			}
		}
		input, hasInput := st.Get("input")
		mp, _ := st.Get("multipart")
		method := "GET"
		if _, ok := headers["origin"]; ok {
			method = "POST"
		}
		var body io.Reader
		path := "/app/api/rpc/" + fn.(string)
		ctype := ""
		switch {
		case mp != nil:
			method = "POST"
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			for _, k := range mp.(*jsonx.Object).Keys() {
				v, _ := mp.(*jsonx.Object).Get(k)
				if f, ok := v.(*jsonx.Object); ok {
					fname, _ := f.Get("name")
					ftype, _ := f.Get("type")
					content, _ := f.Get("content")
					h := make(map[string][]string)
					h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="%s"; filename="%s"`, k, fname)}
					h["Content-Type"] = []string{ftype.(string)}
					part, _ := w.CreatePart(h)
					part.Write([]byte(content.(string)))
				} else {
					w.WriteField(k, fmt.Sprint(v))
				}
			}
			w.Close()
			body, ctype = &buf, w.FormDataContentType()
		case method == "POST":
			if hasInput && input != nil {
				body = strings.NewReader(p.toGo(jsonx.Stringify(input)))
				ctype = "application/json"
			}
		default:
			if hasInput && input != nil {
				path += "?input=" + url.QueryEscape(p.toGo(jsonx.Stringify(input)))
			}
		}
		req, _ := http.NewRequest(method, srv.URL+path, body)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if h, ok := headers["host"]; ok {
			req.Host = h
		}
		if ctype != "" {
			req.Header.Set("content-type", ctype)
		}
		cookies := []string{}
		if as, _ := st.Get("as"); as != nil && as != "" {
			cookies = append(cookies, jars[as.(string)])
		}
		if ck, _ := st.Get("cookie"); ck != nil {
			cookies = append(cookies, ck.(string))
		}
		if len(cookies) > 0 {
			req.Header.Set("cookie", strings.Join(cookies, "; "))
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		rb, _ := io.ReadAll(res.Body)
		res.Body.Close()
		got, err := jsonx.Parse(rb)
		if err != nil {
			t.Fatalf("%s: %d %s", name, res.StatusCode, rb)
		}
		want, _ := st.Get("res")
		wo := want.(*jsonx.Object)
		if ok, has := wo.Get("ok"); has {
			if res.StatusCode != 200 {
				t.Fatalf("%s: status %d, want 200\n   %s", name, res.StatusCode, rb)
			}
			if err := p.match(ok, got, name); err != nil {
				t.Fatal(err)
			}
		} else {
			ev, _ := wo.Get("error")
			e := ev.(*jsonx.Object)
			ename, _ := e.Get("name")
			emsg, _ := e.Get("message")
			gotO, _ := got.(*jsonx.Object)
			code, _ := gotO.Get("code")
			gmsg, _ := gotO.Get("error")
			wantStatus := map[any]int{"UnauthorizedError": 401, "ForbiddenError": 403, "ZodError": 400}[ename]
			if wantStatus != 0 && res.StatusCode != wantStatus || wantStatus == 0 && res.StatusCode < 400 {
				t.Fatalf("%s: status %d for TS %v\n   %s", name, res.StatusCode, ename, rb)
			}
			if ename == "ZodError" && code != "VALIDATION" {
				t.Fatalf("%s: %s", name, rb)
			}
			if err := p.match(emsg, gmsg, name+" error"); err != nil {
				t.Fatal(err)
			}
		}
		if rows, ok := st.Get("rows"); ok && rows != nil {
			ro := rows.(*jsonx.Object)
			for _, table := range ro.Keys() {
				w, _ := ro.Get(table)
				if err := p.match(w, queryRows(t, d, rpcTables[table]), name+" rows."+table); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Logf("%d steps, %d paired values", len(g.Steps), len(p.ts2go))
}
