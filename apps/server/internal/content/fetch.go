package content

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Anyone with an API key can ask for a URL, and the text lands in search. So
// the server must never fetch its own loopback (other services listen on
// 127.0.0.1), the LAN, or cloud metadata. Every hop is resolved and checked,
// and redirects are followed by hand so a public page can't bounce us inward.
// Unlike the TS build, the connection goes to the addresses that were
// checked (no second DNS lookup between the check and the connect).

const (
	MaxRedirects   = 5
	FetchTimeoutMs = 30_000
)

// Resolve looks a host name up.
type Resolve func(ctx context.Context, host string) ([]string, error)

// SystemResolve uses the system resolver, with Node's error texts
// ("getaddrinfo ENOTFOUND host").
func SystemResolve(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return nil, jserr.New("Error", "getaddrinfo ENOTFOUND "+host)
		}
		if errors.As(err, &dns) && dns.IsTimeout {
			return nil, jserr.New("Error", "getaddrinfo EAI_AGAIN "+host)
		}
		return nil, jserr.New("Error", "getaddrinfo EAI_FAIL "+host)
	}
	return addrs, nil
}

var (
	blockedV4 = prefixes("0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24",
		"192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4")
	blockedV6 = prefixes("::/96", "::ffff:0:0/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8")
)

func prefixes(ps ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// isIP is Node's net.isIP: 4, 6 or 0. Zones ("fe80::1%en0") count as IPv6.
func isIP(s string) int {
	a, err := netip.ParseAddr(s)
	if err != nil || strings.Contains(s, "/") {
		return 0
	}
	if a.Is4() {
		if strings.Count(s, ".") != 3 {
			return 0
		}
		return 4
	}
	return 6
}

// IsPrivateAddress is isPrivateAddress: anything that isn't a public IP
// address (a name counts as private).
func IsPrivateAddress(address string) bool {
	family := isIP(address)
	if family == 0 {
		return true
	}
	a := netip.MustParseAddr(address).WithZone("")
	list := blockedV6
	if family == 4 {
		list = blockedV4
	}
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func onlyHTTP() error { return jserr.New("Error", "only http and https URLs are supported") }

func invalidURL() error { return jserr.New("TypeError", "Invalid URL") }

// parseURL is new URL(raw, base), then assertPublicUrl's scheme check: a
// URL that doesn't parse is "Invalid URL" whatever its scheme.
func parseURL(raw string, base *whatwg.URL) (*whatwg.URL, error) {
	var u *whatwg.URL
	var err error
	if base == nil {
		u, err = whatwg.ParseURL(raw)
	} else {
		u, err = base.Resolve(raw)
	}
	if err != nil {
		return nil, invalidURL()
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, onlyHTTP()
	}
	return u, nil
}

// checked resolves the URL's host and returns its addresses once all of them
// are public (fetch may connect to any of them).
func checked(ctx context.Context, u *whatwg.URL, resolve Resolve) ([]string, error) {
	host := strings.TrimSuffix(strings.TrimPrefix(u.Host, "["), "]")
	var addrs []string
	if isIP(host) != 0 {
		addrs = []string{host}
	} else {
		var err error
		if addrs, err = resolve(ctx, host); err != nil {
			return nil, err
		}
	}
	if len(addrs) == 0 {
		return nil, jserr.New("Error", u.HostPort()+" has no address")
	}
	for _, a := range addrs {
		if IsPrivateAddress(a) {
			return nil, jserr.New("Error", "refusing to fetch "+u.HostPort()+": it points to a private or local address")
		}
	}
	return addrs, nil
}

// Fetch makes one request to u, connecting only to addrs (already checked).
// Redirects are not followed. Tests replace it.
type Fetch func(ctx context.Context, u *whatwg.URL, addrs []string) (*http.Response, error)

// DefaultFetch is the real Fetch: a GET with the TS build's headers, dialing
// the checked addresses in order.
func DefaultFetch(ctx context.Context, u *whatwg.URL, addrs []string) (*http.Response, error) {
	port := u.Port
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			for _, a := range addrs {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(a, port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
		// undici's fetch speaks HTTP/1.1 only.
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.Href(), nil)
	if err != nil {
		return nil, jserr.New("TypeError", "fetch failed")
	}
	req.Header.Set("user-agent", "Mozilla/5.0 (compatible; recall/1.0; +https://recall.zakiego.com)")
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5")
	// Headers undici's fetch adds on its own (accept-encoding is Go's gzip).
	req.Header.Set("accept-language", "*")
	req.Header.Set("sec-fetch-mode", "cors")
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &jserr.Error{Name: "TypeError", Message: "fetch failed", Cause: err}
	}
	return res, nil
}

func timeoutErr() error {
	return jserr.New("TimeoutError", "The operation was aborted due to timeout")
}

// FetchURL is fetchUrl. fetch and resolve may be nil for the real ones.
func FetchURL(ctx context.Context, rawURL string, fetch Fetch, resolve Resolve) (Resolved, error) {
	if fetch == nil {
		fetch = DefaultFetch
	}
	if resolve == nil {
		resolve = SystemResolve
	}
	// AbortSignal.timeout(FETCH_TIMEOUT_MS) starts here and is passed to fetch
	// only: it covers the requests and the body read, not the DNS checks or
	// the parsing (though their time counts).
	sctx, cancel := context.WithTimeout(ctx, FetchTimeoutMs*time.Millisecond)
	defer cancel()
	return fetchURL(ctx, sctx, rawURL, fetch, resolve)
}

// signalErr is the error a fetch or body read gives once the timeout signal
// fired.
func signalErr(sctx context.Context, err error) error {
	if errors.Is(sctx.Err(), context.DeadlineExceeded) {
		return timeoutErr()
	}
	return err
}

func fetchURL(ctx, sctx context.Context, rawURL string, fetch Fetch, resolve Resolve) (Resolved, error) {
	u, err := parseURL(rawURL, nil)
	if err != nil {
		return Resolved{}, err
	}
	var res *http.Response
	for hop := 0; ; hop++ {
		addrs, err := checked(ctx, u, resolve)
		if err != nil {
			return Resolved{}, err
		}
		res, err = fetch(sctx, u, addrs)
		if err != nil {
			return Resolved{}, signalErr(sctx, err)
		}
		location := ""
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			location = res.Header.Get("location")
		}
		if location == "" {
			break
		}
		res.Body.Close()
		if hop >= MaxRedirects {
			return Resolved{}, jserr.New("Error", fmt.Sprintf("fetch %s redirected more than %d times", u.HostPort(), MaxRedirects))
		}
		if u, err = parseURL(location, u); err != nil {
			return Resolved{}, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Resolved{}, jserr.New("Error", fmt.Sprintf("fetch %s returned %d", u.HostPort(), res.StatusCode))
	}
	mime := ""
	hasMime := false
	if ct, ok := res.Header["Content-Type"]; ok && len(ct) > 0 {
		mime = strx.Trim(strings.Split(strings.Join(ct, ", "), ";")[0])
		hasMime = true
	}
	body, err := readCapped(res.Body)
	if err != nil {
		return Resolved{}, signalErr(sctx, err)
	}
	last := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if IsPdf(mime, u.Path) {
		r, err := PDFToText(body)
		if err != nil {
			return Resolved{}, err
		}
		if r.Title == nil {
			name, err := url.PathUnescape(last)
			if err != nil || !utf8.ValidString(name) {
				return Resolved{}, jserr.New("URIError", "URI malformed")
			}
			if name == "" {
				name = u.HostPort()
			}
			r.Title = &name
		}
		return r, nil
	}
	if !hasMime || mime == "" || strings.Contains(mime, "html") {
		return HTMLToText(strx.TextDecode(body), rawURL)
	}
	if IsTextLike(mime, u.Path) {
		title := last
		if title == "" {
			title = u.HostPort()
		}
		return Resolved{Title: &title, Text: strx.Trim(strx.TextDecode(body)), Mime: mime}, nil
	}
	return Resolved{}, jserr.New("Error", "unsupported content type: "+mime)
}

func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxFetchBytes+1))
	if err != nil {
		return nil, &jserr.Error{Name: "TypeError", Message: "terminated", Cause: err}
	}
	if len(b) > MaxFetchBytes {
		return nil, jserr.New("Error", fmt.Sprintf("page is larger than %d MB", MaxFetchBytes/1024/1024))
	}
	return b, nil
}
