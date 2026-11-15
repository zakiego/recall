package nodefetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/jserr"
)

// undici drops the credentials on a redirect to another origin, and keeps
// them on the same origin.
func TestRedirectsDropAuthorizationAcrossOrigins(t *testing.T) {
	var seen []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
	}))
	defer target.Close()
	var src *httptest.Server
	src = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, src.URL+"/landed", http.StatusFound)
		case "/landed":
			seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		default:
			http.Redirect(w, r, target.URL+"/other", http.StatusFound)
		}
	}))
	defer src.Close()
	for _, path := range []string{"/same", "/cross"} {
		req, _ := http.NewRequest("GET", src.URL+path, nil)
		req.Header.Set("Authorization", "Bearer k")
		res, err := Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if len(seen) != 2 || seen[0] != "/landed Bearer k" || seen[1] != "/other " {
		t.Fatal(seen)
	}
}

func TestErrorsAreFetchErrors(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1:1/", nil)
	if _, err := Do(req); jserr.Format(err) != "TypeError: fetch failed" {
		t.Fatal(err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, "GET", slow.URL, nil)
	if _, err := Do(req); jserr.Format(err) != "TimeoutError: The operation was aborted due to timeout" {
		t.Fatal(err)
	}
}
