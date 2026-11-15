// Package nodefetch is the real HTTP client for the calls the TS build made
// with Node's global fetch (undici): Google, Gmail, Resend and the app feeds.
// It follows redirects the way undici does and fails with the errors fetch
// fails with, so stored messages read the same.
package nodefetch

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/zakiego/recall/apps/server/internal/jserr"
)

// maxRedirects is undici's limit.
const maxRedirects = 20

var transport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Node's fetch ignores HTTP(S)_PROXY unless NODE_USE_ENV_PROXY is set.
	t.Proxy = nil
	return t
}()

var client = &http.Client{
	Transport: transport,
	// undici: at most 20 redirects, and a redirect to another origin
	// (scheme, host and port) drops the credentials.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("redirect count exceeded")
		}
		if origin(req) != origin(via[len(via)-1]) {
			req.Header.Del("Authorization")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Cookie")
		}
		return nil
	},
}

func origin(r *http.Request) string {
	port := r.URL.Port()
	if port == "" {
		switch r.URL.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return r.URL.Scheme + "://" + r.URL.Hostname() + ":" + port
}

// Do sends req like fetch: a network failure is "TypeError: fetch failed",
// the request context's deadline "TimeoutError: The operation was aborted
// due to timeout", and the same holds while the body is read.
func Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	res, err := client.Do(req)
	if err != nil {
		return nil, Error(ctx, err)
	}
	res.Body = &body{res.Body, ctx}
	return res, nil
}

// Error is what fetch rejects with for err while ctx is req's context.
func Error(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &jserr.Error{Name: "TimeoutError", Message: "The operation was aborted due to timeout", Cause: err}
	}
	if ctx.Err() != nil {
		if c := context.Cause(ctx); c != nil && c != context.Canceled {
			return c
		}
		return &jserr.Error{Name: "AbortError", Message: "This operation was aborted", Cause: err}
	}
	return &jserr.Error{Name: "TypeError", Message: "fetch failed", Cause: err}
}

type body struct {
	io.ReadCloser
	ctx context.Context
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		if b.ctx.Err() != nil {
			return n, Error(b.ctx, err)
		}
		return n, &jserr.Error{Name: "TypeError", Message: "terminated", Cause: err}
	}
	return n, err
}
