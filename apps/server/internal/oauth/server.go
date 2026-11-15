package oauth

// The authorization server: @better-auth/oauth-provider 1.7.18 with
// Recall's options (better-auth.ts oauthProvider(...)). Paths, JSON, rows
// and error texts are Better Auth's, because connected clients (claude.ai)
// cached them and their tokens must keep working across the cutover.
// docs/GO-PORT.md M6 lists what is left out.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Lifetimes (seconds) and Recall's provider options.
const (
	CodeExpiresIn         = 600
	AccessTokenExpiresIn  = 3600
	RefreshTokenExpiresIn = 2592000
	ResourceName          = "Recall MCP"
)

// GrantTypes are the grants the server issues.
var GrantTypes = []string{"authorization_code", "refresh_token"}

// Server answers /api/auth/oauth2/* and the metadata documents.
type Server struct {
	DB *db.DB
	// Now is the clock in ms.
	Now func() int64
	// URL is RECALL_URL.
	URL string
	// Secret signs the authorization query and encrypts client secrets.
	Secret string

	seedOnce sync.Mutex
	seeded   bool
}

var (
	randomSecretOnce sync.Once
	randomSecret     string
)

// SecretFor is ctx.secret: SESSION_SECRET when it has at least 32
// characters, otherwise a random value for this process.
func SecretFor(sessionSecret string) string {
	if strx.Len16(sessionSecret) >= 32 {
		return sessionSecret
	}
	randomSecretOnce.Do(func() {
		b := make([]byte, 32)
		rand.Read(b)
		randomSecret = base64.StdEncoding.EncodeToString(b)
	})
	return randomSecret
}

func (s *Server) baseURL() string { return s.URL + AuthBasePath }

// issuer is validateIssuerUrl(baseURL): https unless the host is loopback,
// no query or fragment, no trailing slash.
func (s *Server) issuer() string {
	u, err := whatwg.ParseURL(s.baseURL())
	if err != nil {
		return s.baseURL()
	}
	if u.Scheme != "https" && !isLoopbackHost(u.HostPort()) && (u.Scheme == "http" || u.Scheme == "ws" || u.Scheme == "wss" || u.Scheme == "ftp") {
		u.Scheme = "https"
		if u.Port == "443" {
			u.Port = ""
		}
	}
	u.RawQuery, u.ForceQuery, u.Fragment = "", false, ""
	return strings.TrimSuffix(u.Href(), "/")
}

func (s *Server) resource() string { return MCPResource(s.URL) }

// --- responses ---------------------------------------------------------------

// apiErr is an APIError: a status, a JSON body (nil for none) and headers.
type apiErr struct {
	status  int
	body    *jsonx.Object
	headers map[string]string
}

func (e *apiErr) Error() string { return jsonx.Stringify(e.body) }

func oerr(status int, kv ...any) *apiErr { return &apiErr{status: status, body: jsonx.ObjectOf(kv...)} }

// descErr is the {error_description, error} order most throw sites use.
func descErr(status int, desc, code string) *apiErr {
	return oerr(status, "error_description", desc, "error", code)
}

// errDesc is the {error, error_description} order.
func errDesc(status int, code, desc string) *apiErr {
	return oerr(status, "error", code, "error_description", desc)
}

var noStore = map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache"}

// reply is what a handler answers: a body (nil writes nothing), the
// status and extra headers. A redirect is status 302 with Location.
type reply struct {
	status  int
	body    any
	empty   bool
	headers map[string]string
}

func write(w http.ResponseWriter, status int, body any, empty bool, headers ...map[string]string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	for _, m := range headers {
		for k, v := range m {
			h.Set(k, v)
		}
	}
	w.WriteHeader(status)
	if empty {
		return
	}
	b, err := jsonx.Marshal(body)
	if err != nil {
		log.Printf("[recall] oauth: %v", err)
		return
	}
	w.Write(b)
}

func writeErr(w http.ResponseWriter, e *apiErr, extra map[string]string) {
	write(w, e.status, e.body, e.body == nil, extra, e.headers)
}

// --- routing -------------------------------------------------------------------

type endpoint struct {
	methods []string
	media   []string // allowed body media types; nil is application/json
	noStore bool
	fn      func(s *Server, c *call) (*reply, error)
}

// call is one request as the handlers see it.
type call struct {
	r       *http.Request
	header  http.Header
	body    *jsonx.Object // nil without a body
	rawBody []byte
	query   *jsonx.Object
	session *auth.Found
	// state is the oauth_query the before hook verified.
	state *oauthState
	// forceJSON: a handler set Accept to application/json.
	forceJSON bool
	// nonObject is the JSON type of a body that isn't an object.
	nonObject string
}

type oauthState struct {
	query     string
	issuedAt  int64
	postLogin string
}

var endpoints = map[string]endpoint{
	"/oauth2/register":   {methods: []string{"POST"}, noStore: true, fn: (*Server).register},
	"/oauth2/authorize":  {methods: []string{"GET", "POST"}, media: []string{"application/x-www-form-urlencoded"}, fn: (*Server).authorizeEndpoint},
	"/oauth2/consent":    {methods: []string{"POST"}, fn: (*Server).consent},
	"/oauth2/continue":   {methods: []string{"POST"}, fn: (*Server).continueEndpoint},
	"/oauth2/token":      {methods: []string{"POST"}, media: []string{"application/x-www-form-urlencoded"}, noStore: true, fn: (*Server).token},
	"/oauth2/revoke":     {methods: []string{"POST"}, media: []string{"application/x-www-form-urlencoded"}, fn: (*Server).revoke},
	"/oauth2/introspect": {methods: []string{"POST"}, media: []string{"application/x-www-form-urlencoded"}, noStore: true, fn: (*Server).introspect},
	"/oauth2/userinfo":   {methods: []string{"GET", "POST"}, media: []string{"application/x-www-form-urlencoded"}, noStore: true, fn: (*Server).userinfo},
}

// Handle answers a request for path (relative to /api/auth) that Recall's
// wrapper and the rate limiter already let through. sess is the caller's
// session (nil when signed out).
func (s *Server) Handle(w http.ResponseWriter, r *http.Request, path string, sess *auth.Found) {
	s.seed()
	if path == "/.well-known/oauth-authorization-server" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		h := map[string]string{"Cache-Control": metadataCache}
		if r.Method == http.MethodHead {
			write(w, 200, nil, true, h)
			return
		}
		write(w, 200, s.Metadata(), false, h)
		return
	}
	ep, ok := endpoints[path]
	allowed := false
	for _, m := range ep.methods {
		allowed = allowed || m == r.Method
	}
	if !ok || !allowed {
		w.WriteHeader(404)
		return
	}
	c := &call{r: r, header: r.Header.Clone(), session: sess}
	if err := s.readBody(c, ep.media); err != nil {
		writeErr(w, err, nil)
		return
	}
	if err := originCheck(r, s.URL); err != nil {
		writeErr(w, err, nil)
		return
	}
	c.query = jsonx.NewMap[any]()
	for _, kv := range rawQuery(r.URL.RawQuery) {
		c.query.Set(kv.k, kv.v)
	}
	if c.body != nil {
		if q, ok := c.body.Get("oauth_query"); ok && jsonx.Truthy(q) {
			st, err := s.beforeHook(q)
			if err != nil {
				writeErr(w, err, nil)
				return
			}
			c.state = st
		}
	}
	if c.nonObject != "" {
		msg := "Invalid input: expected object, received " + c.nonObject
		if path == "/oauth2/register" {
			writeErr(w, errDesc(400, "invalid_client_metadata", msg), nil)
		} else {
			writeErr(w, oerr(400, "message", "[body] "+msg, "code", "VALIDATION_ERROR"), nil)
		}
		return
	}
	rep, err := ep.fn(s, c)
	var extra map[string]string
	if ep.noStore {
		extra = noStore
	}
	if err != nil {
		var vf *validationFailure
		if errors.As(err, &vf) {
			writeErr(w, vf.apiErr, nil)
			return
		}
		var ae *apiErr
		if errors.As(err, &ae) {
			// noStore headers are set before the handler runs, so they are on its errors.
			if ae.status == 302 {
				write(w, 302, nil, true, ae.headers)
				return
			}
			writeErr(w, ae, extra)
			return
		}
		log.Printf("[recall] oauth %s: %v", path, err)
		write(w, 500, nil, true, extra)
		return
	}
	if rep.status == 0 {
		rep.status = 200
	}
	write(w, rep.status, rep.body, rep.empty, extra, rep.headers)
}

type kv struct {
	k string
	v any
}

// rawQuery is better-call's query object: repeated keys become arrays.
func rawQuery(raw string) []kv {
	p := ParseParams(raw)
	out := []kv{}
	for _, k := range p.Keys() {
		vals := p.GetAll(k)
		if len(vals) == 1 {
			out = append(out, kv{k, vals[0]})
		} else {
			arr := make([]any, len(vals))
			for i, v := range vals {
				arr[i] = v
			}
			out = append(out, kv{k, arr})
		}
	}
	return out
}

// readBody is better-call's getBody with the endpoint's media types.
func (s *Server) readBody(c *call, media []string) *apiErr {
	if media == nil {
		media = []string{"application/json"}
	}
	if c.r.Method == http.MethodGet || c.r.Method == http.MethodHead || c.r.Body == nil {
		return nil
	}
	// The TS request always had a body on a POST (withClientIp rebuilds
	// it), so an empty one still meets the media type and JSON checks.
	raw, err := io.ReadAll(io.LimitReader(c.r.Body, 1<<20))
	if err != nil {
		return nil
	}
	c.rawBody = raw
	ct := c.r.Header.Get("content-type")
	lct := strings.ToLower(ct)
	base := strings.TrimSpace(strings.Split(lct, ";")[0])
	ok := false
	for _, m := range media {
		ok = ok || base == m
	}
	if !ok {
		if lct == "" {
			return oerr(415, "message", "Content-Type is required. Allowed types: "+strings.Join(media, ", "), "code", "UNSUPPORTED_MEDIA_TYPE")
		}
		return oerr(415, "message", `Content-Type "`+ct+`" is not allowed. Allowed types: `+strings.Join(media, ", "), "code", "UNSUPPORTED_MEDIA_TYPE")
	}
	if strings.Contains(lct, "application/x-www-form-urlencoded") {
		obj := jsonx.NewMap[any]()
		for _, kv := range ParseParams(string(raw)).Entries() {
			obj.Set(kv[0], kv[1])
		}
		c.body = obj
		return nil
	}
	v, err := jsonx.Parse(bytes.TrimSpace(raw))
	if err != nil {
		return oerr(400, "message", "Invalid JSON in request body", "code", "BAD_REQUEST")
	}
	if o, ok := v.(*jsonx.Object); ok {
		c.body = o
	} else {
		c.body = nil
		c.rawBody = nil
		c.nonObject = jsType(v)
	}
	return nil
}

func jsType(v any) string {
	switch v.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "number"
}

// originCheck is Better Auth's originCheck for a write that carries
// cookies: Origin (or Referer) must be RECALL_URL's origin.
func originCheck(r *http.Request, appURL string) *apiErr {
	if r.Method != http.MethodPost || r.Header.Get("cookie") == "" {
		return nil
	}
	from := r.Header.Get("origin")
	if from == "" {
		from = r.Header.Get("referer")
	}
	if from == "" || from == "null" {
		return oerr(403, "message", "Missing or null Origin", "code", "MISSING_OR_NULL_ORIGIN")
	}
	u, err := whatwg.ParseURL(from)
	app, err2 := whatwg.ParseURL(appURL)
	if err != nil || err2 != nil || u.Origin() != app.Origin() {
		return oerr(403, "message", "Invalid origin", "code", "INVALID_ORIGIN")
	}
	return nil
}

// beforeHook verifies oauth_query and keeps it as the request's state.
func (s *Server) beforeHook(q any) (*oauthState, *apiErr) {
	query, _ := q.(string)
	if query == "" || !VerifyQuery(query, s.Secret, s.Now()) {
		return nil, oerr(400, "error", "invalid_signature")
	}
	p := ParseParams(query)
	st := &oauthState{issuedAt: signedIssuedAt(query)}
	st.postLogin, _ = p.Get(postLoginParam)
	p.Delete("sig")
	p.Delete("exp")
	p.Delete(issuedAtParam)
	p.Delete(postLoginParam)
	st.query = p.String()
	return st, nil
}

// VerifyHook is the before hook for Recall's own sign-in endpoints: the
// state of a valid oauth_query, or the 400 invalid_signature error.
func (s *Server) VerifyHook(query string) (*SignInState, error) {
	st, err := s.beforeHook(query)
	if err != nil {
		return nil, err
	}
	return &SignInState{st: st}, nil
}

// SignInState is a verified oauth_query on a sign-in request.
type SignInState struct{ st *oauthState }

// --- resources ------------------------------------------------------------------

type resourceRow struct {
	ID, Identifier string
	AllowedScopes  *string
	Disabled       bool
	AccessTTL      *int64
	RefreshTTL     *int64
	DPoPRequired   bool
}

// Seed is seedResourcesOnce, run on the first /api/auth request.
func (s *Server) Seed() { s.seed() }

// WriteError writes an error the OAuth code returned; false when err isn't one.
func WriteError(w http.ResponseWriter, err error) bool {
	var ae *apiErr
	if !errors.As(err, &ae) {
		return false
	}
	writeErr(w, ae, nil)
	return true
}

// seed is seedResourcesOnce: the configured resource is inserted when
// missing (insert-only).
func (s *Server) seed() {
	s.seedOnce.Lock()
	defer s.seedOnce.Unlock()
	if s.seeded {
		return
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM oauth_resource WHERE identifier = ?`, s.resource()).Scan(&n); err != nil {
		log.Printf("[recall] oauth resource seed: %v", err)
		return
	}
	if n == 0 {
		now := s.Now()
		if _, err := s.DB.Exec(`INSERT INTO oauth_resource (id, identifier, name, custom_claims, dpop_bound_access_tokens_required, disabled, created_at, updated_at, policy_version, metadata) VALUES (?, ?, ?, 'null', 0, 0, ?, ?, 1, 'null')`,
			newID(), s.resource(), ResourceName, now, now); err != nil {
			log.Printf("[recall] oauth resource seed: %v", err)
			return
		}
	}
	s.seeded = true
}

func (s *Server) getResource(identifier string) (*resourceRow, error) {
	s.seed()
	var r resourceRow
	var disabled *bool
	var dpop *bool
	err := s.DB.QueryRow(`SELECT id, identifier, allowed_scopes, disabled, access_token_ttl, refresh_token_ttl, dpop_bound_access_tokens_required FROM oauth_resource WHERE identifier = ?`, identifier).
		Scan(&r.ID, &r.Identifier, &r.AllowedScopes, &disabled, &r.AccessTTL, &r.RefreshTTL, &dpop)
	r.DPoPRequired = dpop != nil && *dpop
	if errors.Is(err, errNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Disabled = disabled != nil && *disabled
	return &r, nil
}
