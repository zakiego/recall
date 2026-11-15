// Package httpapi is the HTTP server: /health, the /v1 REST API, /mcp and
// the protected-resource metadata (M5), with the dashboard and auth routes
// added by later milestones. http.ts is the model for the helpers here.
package httpapi

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// MaxJSONBytes is MAX_JSON_BYTES.
const MaxJSONBytes = 10 * 1024 * 1024

// writeRaw writes body with the headers a TS handler's Response carried.
func writeRaw(w http.ResponseWriter, status int, body []byte, headers map[string]string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	w.Write(body)
}

// writeJSON is Response.json(body, { status, headers }).
func writeJSON(w http.ResponseWriter, status int, body any, headers map[string]string) {
	b, err := jsonx.Marshal(body)
	if err != nil {
		log.Printf("[recall] api error: %v", err)
		b, status = []byte(`{"error":{"type":"internal","message":"internal error"}}`), 500
	}
	h := map[string]string{"content-type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	writeRaw(w, status, b, h)
}

// jsonNoStore is json(): Response.json with cache-control: no-store.
func jsonNoStore(w http.ResponseWriter, status int, body any, headers map[string]string) {
	h := map[string]string{"cache-control": "no-store"}
	for k, v := range headers {
		h[k] = v
	}
	writeJSON(w, status, body, h)
}

// apiError is apiError: {"error":{"type","message"}}.
func apiError(w http.ResponseWriter, status int, typ, message string, headers map[string]string) {
	jsonNoStore(w, status, jsonx.ObjectOf("error", jsonx.ObjectOf("type", typ, "message", message)), headers)
}

func httpErr(status int, typ, message string) error { return httpx.New(status, typ, message) }

// readBodyCapped is readBodyCapped: a declared length over max is refused
// before reading, and a body that runs past it is cut off.
func readBodyCapped(r *http.Request, max int64) ([]byte, error) {
	tooLarge := httpErr(413, "too_large", "body is larger than "+strconv.FormatInt(max/1024/1024, 10)+" MB")
	if r.ContentLength > max {
		return nil, tooLarge
	}
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, tooLarge
	}
	return b, nil
}

// readJSON is readJson: the capped body, JSON.parse of its UTF-8 text, and
// the schema's safeParse, with zod's prettified issues on a 400.
func readJSON(r *http.Request, schema zod.Schema) (*jsonx.Object, error) {
	b, err := readBodyCapped(r, MaxJSONBytes)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Parse([]byte(strx.TextDecode(b)))
	if err != nil {
		return nil, httpErr(400, "invalid_json", "body must be JSON")
	}
	return validate(schema, v)
}

func validate(schema zod.Schema, v any) (*jsonx.Object, error) {
	out, err := zod.Parse(schema, v)
	if err != nil {
		var ze *zod.Error
		if errors.As(err, &ze) {
			return nil, httpErr(400, "invalid_request", zod.PrettifyError(ze.Issues))
		}
		return nil, err
	}
	o, _ := out.(*jsonx.Object)
	return o, nil
}

// queryObject is Object.fromEntries(new URL(request.url).searchParams).
func queryObject(r *http.Request) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for _, p := range whatwg.ParseQuery(r.URL.RawQuery) {
		o.Set(p[0], p[1])
	}
	return o
}

// parseQuery is parseQuery.
func parseQuery(r *http.Request, schema zod.Schema) (*jsonx.Object, error) {
	return validate(schema, queryObject(r))
}

// bearer is bearer(request): the token, or "" (null).
func bearer(r *http.Request) string {
	t, _ := auth.Bearer(r.Header)
	return t
}
