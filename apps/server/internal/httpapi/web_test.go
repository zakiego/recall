package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/web"
)

// The dashboard under /app next to the API, and the old root paths.
func TestDashboardRoutesAndLegacyRedirects(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o644)
	s := &httpapi.Server{DB: testkit.OpenDB(t), Web: web.Handler(dir)}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) *http.Response {
		res, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	// Dashboard pages, including ones whose first segment is also an API route's.
	for _, p := range []string{"/app", "/app/", "/app/settings/keys", "/app/connectors", "/app/oauth/consent", "/app/people/per_1"} {
		if res := get(p); res.StatusCode != 200 || res.Header.Get("content-type") != "text/html; charset=utf-8" {
			t.Errorf("%s: %d %s", p, res.StatusCode, res.Header.Get("content-type"))
		}
	}
	// The OAuth hand-offs and the API stay the server's.
	if res := get("/app/oauth/approve"); res.StatusCode != 302 || res.Header.Get("location") != "/app/oauth/consent" {
		t.Errorf("approve: %d %s", res.StatusCode, res.Header.Get("location"))
	}
	if res := get("/app/api/rpc/getSession"); res.StatusCode != 200 || res.Header.Get("content-type") != "application/json" {
		t.Errorf("rpc: %d", res.StatusCode)
	}
	for p, want := range map[string]string{
		"/":                       "/app/",
		"/login?next=%2Fsearch":   "/app/login?next=%2Fsearch",
		"/settings/keys":          "/app/settings/keys",
		"/invite/rci_abc":         "/app/invite/rci_abc",
		"/search?c=work&q=coffee": "/app/search?c=work&q=coffee",
	} {
		if res := get(p); res.StatusCode != 302 || res.Header.Get("location") != want {
			t.Errorf("%s: %d %q", p, res.StatusCode, res.Header.Get("location"))
		}
	}
	for _, p := range []string{"/loginx", "/nope", "/favicon.ico"} {
		if res := get(p); res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
}

// Regressions from the M8 review.
func TestM8ReviewEdges(t *testing.T) {
	s := &httpapi.Server{DB: testkit.OpenDB(t)}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for p, want := range map[string]string{"/invite/a%2Fb": "/app/invite/a%2Fb", "/people/per_1": "/app/people/per_1", "/oauth/consent?x=1": "/app/oauth/consent?x=1"} {
		res, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 302 || res.Header.Get("location") != want {
			t.Errorf("%s: %d %q", p, res.StatusCode, res.Header.Get("location"))
		}
	}
}
