package httpapi_test

// testdata/rest.json from testdata/gen/rest-goldens.ts: the TS route
// handlers' responses to 104 requests on a copy of the seeded database. The
// same requests go over real HTTP to the Go handler on its own copy.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

type restStep struct {
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Key       string            `json:"key"`
	Headers   map[string]string `json:"headers"`
	Body      *string           `json:"body"`
	Multipart *struct {
		Fields map[string]string `json:"fields"`
		File   *struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"file"`
	} `json:"multipart"`
}

type restGolden struct {
	Now   int64             `json:"now"`
	Keys  map[string]string `json:"keys"`
	Setup []string          `json:"setup"`
	Refs  map[string]string `json:"refs"`
	Steps []struct {
		Step    restStep          `json:"step"`
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"steps"`
	Tables  map[string]string `json:"tables"`
	Vectors map[string]string `json:"vectors"`
}

// norm is the generator's normText, with ids by first appearance.
type norm struct {
	now    int64
	seen   map[string]string
	counts map[string]int
}

var (
	idRe     = regexp.MustCompile(`\b(mem|doc|chk)_[0-9a-z]{16}\b`)
	timingRe = regexp.MustCompile(`"timingMs":\d+`)
	clockRe  = regexp.MustCompile(`("(?:createdAt|updatedAt|validFrom|validTo|lastActivity|lastMentionedAt|lastUsedAt|runAfter)":)(\d{13})`)
	cursorRe = regexp.MustCompile(`"nextCursor":"([A-Za-z0-9_-]+)"`)
	bareRe   = regexp.MustCompile(`\b(\d{13})\b`)
	isoRe    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z`)
)

func (n *norm) text(s string) string {
	s = idRe.ReplaceAllStringFunc(s, func(id string) string {
		if v, ok := n.seen[id]; ok {
			return v
		}
		p := id[:strings.IndexByte(id, '_')]
		n.counts[p]++
		v := fmt.Sprintf("%s_%d", p, n.counts[p])
		n.seen[id] = v
		return v
	})
	s = timingRe.ReplaceAllString(s, `"timingMs":"<ms>"`)
	s = isoRe.ReplaceAllStringFunc(s, func(m string) string {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", m); err == nil && t.UnixMilli() >= n.now {
			return "<now-iso>"
		}
		return m
	})
	return clockRe.ReplaceAllStringFunc(s, func(m string) string {
		p := clockRe.FindStringSubmatch(m)
		if v, _ := strconv.ParseInt(p[2], 10, 64); v >= n.now {
			return p[1] + `"<now>"`
		}
		return m
	})
}

func (n *norm) body(s string) string {
	s = cursorRe.ReplaceAllStringFunc(s, func(m string) string {
		raw, _ := base64.RawURLEncoding.DecodeString(cursorRe.FindStringSubmatch(m)[1])
		return `"nextCursor":"cursor(` + string(raw) + `)"`
	})
	return n.text(s)
}

func replayVectors(t *testing.T, vectors map[string]string) {
	embed.Override = func(texts []string, kind embed.Kind) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, text := range texts {
			b64, ok := vectors[string(kind)+":"+text]
			if !ok {
				return nil, fmt.Errorf("the TS run never embedded %s %q", kind, text)
			}
			raw, _ := base64.StdEncoding.DecodeString(b64)
			v := make([]float32, len(raw)/4)
			for k := range v {
				v[k] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*k:]))
			}
			out[i] = v
		}
		return out, nil
	}
	t.Cleanup(func() { embed.Override = nil })
}

func copyFile(t *testing.T, from, to string) {
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRESTMatchesTS(t *testing.T) {
	var g restGolden
	testutil.LoadJSON(t, "rest.json", &g)
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("RECALL_JOBS", "off")
	t.Setenv("RECALL_URL", "https://recall.test")
	file := filepath.Join(dir, "recall.db")
	copyFile(t, filepath.Join(testutil.Root(), "testdata", "search", "seeded.db"), file)
	d, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	replayVectors(t, g.Vectors)
	for _, sql := range g.Setup {
		if _, err := d.Exec(sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	refs := map[string]string{}
	for name, sql := range g.Refs {
		var id string
		if err := d.QueryRow(sql).Scan(&id); err != nil {
			t.Fatal(name, err)
		}
		refs[name] = id
	}
	// The TS clock was fixed and SQLite's ran on: an advancing clock from
	// the fixed instant keeps the order of what each request wrote.
	start := time.Now()
	s := &httpapi.Server{DB: d, Now: func() int64 { return g.Now + time.Since(start).Milliseconds() }}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	n := &norm{now: g.Now, seen: map[string]string{}, counts: map[string]int{}}
	var raw []string
	cursorRef := regexp.MustCompile(`\{cursor:(\d+)\}`)
	refRe := regexp.MustCompile(`\{(\w+)\}`)
	fill := func(s string) string {
		s = refRe.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := refs[m[1:len(m)-1]]; ok {
				return v
			}
			return m
		})
		return cursorRef.ReplaceAllStringFunc(s, func(m string) string {
			i, _ := strconv.Atoi(cursorRef.FindStringSubmatch(m)[1])
			var prev struct {
				NextCursor string `json:"nextCursor"`
			}
			json.Unmarshal([]byte(raw[i]), &prev)
			return url.QueryEscape(prev.NextCursor)
		})
	}
	for i, gs := range g.Steps {
		st := gs.Step
		label := fmt.Sprintf("step %d %s %s", i, st.Method, st.Path)
		var body io.Reader
		headers := http.Header{}
		for k, v := range st.Headers {
			headers.Set(k, v)
		}
		switch st.Key {
		case "none":
		case "bad":
			headers.Set("authorization", "Bearer rcl_notarealkey")
		case "":
			headers.Set("authorization", "Bearer "+g.Keys["alpha"])
		default:
			headers.Set("authorization", "Bearer "+g.Keys[st.Key])
		}
		switch {
		case st.Multipart != nil:
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			keys := make([]string, 0, len(st.Multipart.Fields))
			for k := range st.Multipart.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				mw.WriteField(k, st.Multipart.Fields[k])
			}
			if f := st.Multipart.File; f != nil {
				h := textproto.MIMEHeader{}
				// undici's serializer leaves an empty filename out.
				disp := `form-data; name="file"`
				if f.Name != "" {
					disp += fmt.Sprintf(`; filename=%q`, f.Name)
				}
				h.Set("Content-Disposition", disp)
				typ := f.Type
				if typ == "" {
					typ = "application/octet-stream"
				}
				h.Set("Content-Type", typ)
				pw, _ := mw.CreatePart(h)
				pw.Write([]byte(f.Text))
			}
			mw.Close()
			body = &buf
			headers.Set("content-type", mw.FormDataContentType())
		case st.Body != nil:
			body = strings.NewReader(fill(*st.Body))
			if headers.Get("content-type") == "" {
				headers.Set("content-type", "application/json")
			}
		}
		req, err := http.NewRequest(st.Method, srv.URL+fill(st.Path), body)
		if err != nil {
			t.Fatal(label, err)
		}
		req.Header = headers
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(label, err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		raw = append(raw, string(b))
		if res.StatusCode != gs.Status {
			t.Fatalf("%s: status %d, want %d\n%s", label, res.StatusCode, gs.Status, b)
		}
		got := map[string]string{}
		for k := range res.Header {
			lk := strings.ToLower(k)
			if lk == "date" || lk == "content-length" {
				continue
			}
			got[lk] = strings.Join(res.Header.Values(k), ", ")
		}
		if fmt.Sprint(got) != fmt.Sprint(gs.Headers) {
			t.Fatalf("%s: headers\n got %v\nwant %v", label, got, gs.Headers)
		}
		if nb := n.body(string(b)); nb != gs.Body && !sameWithin(nb, gs.Body) {
			t.Fatalf("%s: body\n got %s\nwant %s", label, nb, gs.Body)
		}
	}
	queries := map[string]string{
		"documents": "SELECT id, workspace_id, container, source_type, title, url, content, status, metadata, source_id, created_at FROM documents ORDER BY rowid",
		"memories":  "SELECT id, workspace_id, container, fact, keywords, kind, supersedes_id, valid_from, valid_to, review_note, event_date, event_precision, due_date, due_precision, created_at FROM memories ORDER BY rowid",
		"jobs":      "SELECT id, workspace_id, type, payload, status, run_after FROM jobs ORDER BY id",
		"api_keys":  "SELECT id, last_used_at FROM api_keys ORDER BY id",
	}
	for _, name := range []string{"documents", "memories", "jobs", "api_keys"} {
		got := n.text(bareRe.ReplaceAllStringFunc(rowsJSON(t, d, queries[name]), func(m string) string {
			if v, _ := strconv.ParseInt(m, 10, 64); v >= g.Now && v < 1e13 {
				return `"<now>"`
			}
			return m
		}))
		if got != g.Tables[name] {
			t.Errorf("table %s\n got %s\nwant %s", name, got, g.Tables[name])
		}
	}
}

// rowsJSON is JSON.stringify of better-sqlite3's raw rows.
func rowsJSON(t *testing.T, d *db.DB, query string) string {
	t.Helper()
	rows, err := d.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	b, err := jsonx.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sameWithin compares two JSON bodies exactly except "similarity", the
// cosine sqlite-vec computes, which differs in the last float bits from one
// platform to another (the ranks don't): equal within 1e-6.
func sameWithin(got, want string) bool {
	var g, w any
	if json.Unmarshal([]byte(got), &g) != nil || json.Unmarshal([]byte(want), &w) != nil {
		return false
	}
	return close(g, w, "")
}

func close(a, b any, key string) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !close(v, y[k], k) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !close(x[i], y[i], key) {
				return false
			}
		}
		return true
	case float64:
		y, ok := b.(float64)
		if key == "similarity" {
			return ok && math.Abs(x-y) <= 1e-6
		}
		return ok && x == y
	}
	return a == b
}
