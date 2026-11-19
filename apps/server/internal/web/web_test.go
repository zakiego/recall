package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/web"
)

func get(t *testing.T, h http.Handler, p string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	return rec.Result()
}

func body(r *http.Response) string { b, _ := io.ReadAll(r.Body); return string(b) }

func TestEmbeddedPlaceholderServesIndex(t *testing.T) {
	h := web.Handler("")
	for _, p := range []string{"/app", "/app/", "/app/connectors", "/app/people/per_1"} {
		res := get(t, h, p)
		if res.StatusCode != 200 || !strings.Contains(body(res), "Recall") || res.Header.Get("cache-control") != "no-cache" {
			t.Fatalf("%s: %d %q", p, res.StatusCode, res.Header.Get("cache-control"))
		}
	}
}

func TestPagesRefuseFraming(t *testing.T) {
	h := web.Handler("")
	for _, p := range []string{"/app/", "/app/oauth/consent", "/app/assets/gone-old.js"} {
		res := get(t, h, p)
		if res.Header.Get("x-frame-options") != "DENY" || res.Header.Get("content-security-policy") != "frame-ancestors 'none'" || res.Header.Get("x-content-type-options") != "nosniff" {
			t.Fatalf("%s: %v", p, res.Header)
		}
	}
}

func TestDirServesAssetsWithLongCacheAndFallsBackToIndex(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte("console.log(1)"), 0o644)
	os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("<svg/>"), 0o644)
	h := web.Handler(dir)

	js := get(t, h, "/app/assets/index-abc123.js")
	if js.StatusCode != 200 || body(js) != "console.log(1)" || js.Header.Get("cache-control") != "public, max-age=31536000, immutable" || !strings.Contains(js.Header.Get("content-type"), "javascript") {
		t.Fatalf("asset: %d %v", js.StatusCode, js.Header)
	}
	icon := get(t, h, "/app/favicon.svg")
	if icon.StatusCode != 200 || icon.Header.Get("cache-control") != "no-cache" {
		t.Fatalf("favicon: %d %v", icon.StatusCode, icon.Header)
	}
	page := get(t, h, "/app/settings")
	if page.StatusCode != 200 || body(page) != "<html>spa</html>" || page.Header.Get("content-type") != "text/html; charset=utf-8" {
		t.Fatalf("spa: %d", page.StatusCode)
	}
	if res := get(t, h, "/app/assets/gone-old.js"); res.StatusCode != 404 {
		t.Fatalf("missing asset: %d", res.StatusCode)
	}
	// No way out of the directory.
	if res := get(t, h, "/app/../../etc/passwd"); res.StatusCode != 200 || body(res) != "<html>spa</html>" {
		t.Fatalf("traversal: %d", res.StatusCode)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/app/", nil))
	if rec.Code != 405 {
		t.Fatalf("post: %d", rec.Code)
	}
}
