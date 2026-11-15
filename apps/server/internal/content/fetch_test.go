package content_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/content"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Fake DNS: nothing here touches the network.
var dns = map[string][]string{
	"example.com":          {"93.184.215.14"},
	"cdn.example.com":      {"2606:4700::6810:85e5"},
	"localhost":            {"127.0.0.1", "::1"},
	"intranet.example.com": {"10.0.0.5"},
	"mixed.example.com":    {"93.184.215.14", "192.168.1.10"},
	"metadata.example.com": {"169.254.169.254"},
	"ula.example.com":      {"fd12:3456::1"},
	"empty.example.com":    {},
}

func resolve(_ context.Context, host string) ([]string, error) {
	found, ok := dns[host]
	if !ok {
		return nil, jserr.New("Error", "getaddrinfo ENOTFOUND "+host)
	}
	return found, nil
}

type fake struct {
	calls   []string
	handler func(u *whatwg.URL) *http.Response
}

func (f *fake) fetch(_ context.Context, u *whatwg.URL, addrs []string) (*http.Response, error) {
	f.calls = append(f.calls, u.Href())
	return f.handler(u), nil
}

func page(body string, ct string) *http.Response {
	h := http.Header{}
	if ct != "" {
		h.Set("content-type", ct)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func redirect(location string, status int) *http.Response {
	h := http.Header{}
	h.Set("location", location)
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(""))}
}

func TestFetchesAPublicPage(t *testing.T) {
	f := &fake{handler: func(*whatwg.URL) *http.Response { return page("hello from the web", "text/plain") }}
	r, err := content.FetchURL(context.Background(), "https://example.com/notes.txt", f.fetch, resolve)
	if err != nil || r.Text != "hello from the web" || len(f.calls) != 1 || *r.Title != "notes.txt" || r.Mime != "text/plain" {
		t.Fatalf("%+v %v %v", r, err, f.calls)
	}
}

func TestRefusesPrivateAddressesWithoutFetching(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:8793/v1/chat",
		"http://localhost:4800/v1/documents",
		"http://[::1]:8793/",
		"http://[::ffff:127.0.0.1]:8793/",
		"http://2130706433:8793/",
		"http://0x7f.1/",
		"http://intranet.example.com/",
		"http://mixed.example.com/",
		"http://metadata.example.com/latest/meta-data/",
		"http://169.254.169.254/latest/meta-data/",
		"http://ula.example.com/",
	} {
		f := &fake{handler: func(*whatwg.URL) *http.Response { return page("secret", "text/plain") }}
		_, err := content.FetchURL(context.Background(), u, f.fetch, resolve)
		if err == nil || !strings.Contains(err.Error(), "private or local address") || len(f.calls) != 0 {
			t.Errorf("%s: %v %v", u, err, f.calls)
		}
	}
	_, err := content.FetchURL(context.Background(), "http://localhost:4800/x", (&fake{}).fetch, resolve)
	if err.Error() != "refusing to fetch localhost:4800: it points to a private or local address" {
		t.Fatalf("%q", err)
	}
}

func TestRedirects(t *testing.T) {
	f := &fake{handler: func(*whatwg.URL) *http.Response { return redirect("http://127.0.0.1:8793/v1/models", 302) }}
	if _, err := content.FetchURL(context.Background(), "https://example.com/go", f.fetch, resolve); err == nil || !strings.Contains(err.Error(), "private or local address") || len(f.calls) != 1 {
		t.Fatalf("into private: %v %v", err, f.calls)
	}
	f = &fake{handler: func(u *whatwg.URL) *http.Response {
		switch u.Href() {
		case "https://example.com/a":
			return redirect("https://cdn.example.com/b", 301)
		case "https://cdn.example.com/b":
			return redirect("/c", 307)
		}
		return page("landed on "+u.Href(), "text/plain")
	}}
	r, err := content.FetchURL(context.Background(), "https://example.com/a", f.fetch, resolve)
	if err != nil || r.Text != "landed on https://cdn.example.com/c" || len(f.calls) != 3 {
		t.Fatalf("relative: %+v %v %v", r, err, f.calls)
	}
	n := 0
	f = &fake{handler: func(*whatwg.URL) *http.Response { n++; return redirect(fmt.Sprintf("https://example.com/%d", n), 302) }}
	_, err = content.FetchURL(context.Background(), "https://example.com/0", f.fetch, resolve)
	if err == nil || err.Error() != "fetch example.com redirected more than 5 times" || len(f.calls) != content.MaxRedirects+1 {
		t.Fatalf("too many: %v %d", err, len(f.calls))
	}
	f = &fake{handler: func(*whatwg.URL) *http.Response { return redirect("file:///etc/passwd", 302) }}
	if _, err := content.FetchURL(context.Background(), "https://example.com/", f.fetch, resolve); err == nil || !strings.Contains(err.Error(), "only http and https") {
		t.Fatalf("protocol: %v", err)
	}
	f = &fake{handler: func(*whatwg.URL) *http.Response { return page("x", "text/plain") }}
	if _, err := content.FetchURL(context.Background(), "ftp://example.com/x", f.fetch, resolve); err == nil || !strings.Contains(err.Error(), "only http and https") || len(f.calls) != 0 {
		t.Fatalf("ftp: %v", err)
	}
}

func TestFetchErrorsAndKinds(t *testing.T) {
	cases := []struct {
		url, want string
		res       *http.Response
	}{
		{"https://example.com/x", "fetch example.com returned 404", &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}},
		{"https://example.com:8443/x", "fetch example.com:8443 returned 500", &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}},
		{"https://example.com/img.png", "unsupported content type: image/png", page("x", "image/png; q=1")},
		{"https://empty.example.com/", "empty.example.com has no address", nil},
		{"https://nope.example.com/", "getaddrinfo ENOTFOUND nope.example.com", nil},
		{"not a url", "Invalid URL", nil},
		// new URL() runs before the scheme check.
		{"ftp://[bad", "Invalid URL", nil},
		{"mailto:someone@example.com", "only http and https URLs are supported", nil},
	}
	for _, c := range cases {
		f := &fake{handler: func(*whatwg.URL) *http.Response { return c.res }}
		_, err := content.FetchURL(context.Background(), c.url, f.fetch, resolve)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.url, err, c.want)
		}
	}
	// No content type: HTML. A text file is titled by its last path segment or the host.
	f := &fake{handler: func(*whatwg.URL) *http.Response {
		return page("<html><head><title>T</title></head><body><article><p>"+strings.Repeat("Words in an article. ", 40)+"</p></article></body></html>", "")
	}}
	r, err := content.FetchURL(context.Background(), "https://example.com/", f.fetch, resolve)
	if err != nil || r.Title == nil || !strings.HasPrefix(r.Text, "Words in an article.") {
		t.Fatalf("html: %+v %v", r, err)
	}
	f = &fake{handler: func(*whatwg.URL) *http.Response { return page("plain", "text/plain") }}
	r, _ = content.FetchURL(context.Background(), "https://example.com/", f.fetch, resolve)
	if *r.Title != "example.com" {
		t.Fatalf("title %q", *r.Title)
	}
	f = &fake{handler: func(*whatwg.URL) *http.Response {
		return page(strings.Repeat("x", content.MaxFetchBytes+1), "text/plain")
	}}
	if _, err := content.FetchURL(context.Background(), "https://example.com/big", f.fetch, resolve); err == nil || err.Error() != "page is larger than 15 MB" {
		t.Fatalf("cap: %v", err)
	}
}

// The real fetch connects to the checked address, not to a second lookup.
func TestDefaultFetchDialsTheCheckedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("user-agent") != "Mozilla/5.0 (compatible; recall/1.0; +https://recall.zakiego.com)" || r.Header.Get("accept-language") != "*" || r.Header.Get("sec-fetch-mode") != "cors" || r.ProtoMajor != 1 {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("content-type", "text/plain")
		fmt.Fprint(w, "pinned "+r.Host)
	}))
	defer srv.Close()
	su, _ := url.Parse(srv.URL)
	u, err := whatwg.ParseURL("http://pinned.example:" + su.Port() + "/x")
	if err != nil {
		t.Fatal(err)
	}
	res, err := content.DefaultFetch(context.Background(), u, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "pinned pinned.example:"+su.Port() {
		t.Fatalf("%q", b)
	}
}
