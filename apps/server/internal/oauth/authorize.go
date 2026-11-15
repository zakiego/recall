package oauth

// GET|POST /oauth2/authorize: authorizeEndpoint with Recall's options
// (loginPage and consentPage are the /app/oauth hand-offs, postLogin never
// redirects, the consent reference is the session's active workspace).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

var (
	promptTokens = map[string]bool{"none": true, "consent": true, "login": true, "create": true, "select_account": true}
	dpopJKT      = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	tokenWord    = regexp.MustCompile(`\b(token|id_token)\b`)
	// querySchemaKeys is authorizationQuerySchema's shape order.
	querySchemaKeys     = []string{"response_type", "request", "request_uri", "redirect_uri", "scope", "state", "client_id", "prompt", "display", "ui_locales", "max_age", "acr_values", "login_hint", "id_token_hint", "code_challenge", "code_challenge_method", "nonce", "claims", "dpop_jkt", "resource"}
	authorizeErrorCodes = map[string]any{
		"response_type": map[string]string{"invalid": "unsupported_response_type"},
		"resource":      map[string]string{"invalid": "invalid_target"},
	}
)

// redirectResult is what handleRedirect gives: a URL the browser goes to.
type redirectResult struct{ url string }

func strOf(o *jsonx.Object, k string) (string, bool) {
	v, ok := o.Get(k)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// parseAuthorizeQuery is authorizationQuerySchema.safeParse: the output
// (shape keys first, then the passthrough ones) or the first issue.
func parseAuthorizeQuery(in *jsonx.Object) (*jsonx.Object, *issue) {
	out := jsonx.NewMap[any]()
	str := func(k string) (string, bool, *issue) {
		v := get(in, k)
		if v == undefined {
			return "", false, nil
		}
		s, ok := v.(string)
		if !ok {
			return "", false, at(typeIssue("string", v), k)
		}
		return s, true, nil
	}
	for _, k := range querySchemaKeys {
		v := get(in, k)
		switch k {
		case "response_type", "code_challenge_method":
			s, ok, is := str(k)
			if is != nil {
				return nil, is
			}
			if !ok {
				continue
			}
			want := "code"
			if k == "code_challenge_method" {
				want = "S256"
			}
			if s != want {
				return nil, at(&issue{code: "invalid_value", values: []string{want}, message: `Invalid option: expected one of "` + want + `"`}, k)
			}
			out.Set(k, s)
		case "redirect_uri":
			if v == undefined {
				continue
			}
			val, is := safeURL(v)
			if is != nil {
				return nil, at(is, k)
			}
			out.Set(k, val)
		case "client_id":
			s, ok, is := str(k)
			if is != nil {
				return nil, is
			}
			if !ok {
				return nil, at(typeIssue("string", undefined), k)
			}
			if s == "" {
				return nil, at(&issue{code: "too_small", message: "client_id is required"}, k)
			}
			out.Set(k, s)
		case "prompt":
			s, ok, is := str(k)
			if is != nil {
				return nil, is
			}
			if !ok {
				continue
			}
			toks := []string{}
			for _, t := range strings.Split(s, " ") {
				if t = strings.TrimSpace(t); t != "" {
					toks = append(toks, t)
				}
			}
			if len(toks) == 0 {
				return nil, at(&issue{code: "custom", message: "prompt must include at least one value"}, k)
			}
			set := map[string]bool{}
			for _, t := range toks {
				if !promptTokens[t] {
					return nil, at(&issue{code: "custom", message: "unsupported prompt value: " + t}, k)
				}
				set[t] = true
			}
			if set["none"] && len(set) > 1 {
				return nil, at(&issue{code: "custom", message: "prompt=none cannot be combined with other prompt values"}, k)
			}
			out.Set(k, s)
		case "max_age":
			if v == undefined {
				continue
			}
			var n float64
			switch x := v.(type) {
			case float64:
				n = x
			case string:
				if strings.TrimSpace(x) == "" {
					return nil, at(tooSmallString(1), k)
				}
				n = strx.Number(x)
			default:
				return nil, at(&issue{code: "invalid_union", message: "Invalid input"}, k)
			}
			if n != math.Trunc(n) || math.IsInf(n, 0) || n < 0 {
				return nil, at(&issue{code: "custom", message: "max_age must be a non-negative integer"}, k)
			}
			out.Set(k, n)
		case "dpop_jkt":
			s, ok, is := str(k)
			if is != nil {
				return nil, is
			}
			if !ok {
				continue
			}
			if !dpopJKT.MatchString(s) {
				return nil, at(&issue{code: "invalid_format", message: "dpop_jkt must be a base64url-encoded SHA-256 JWK thumbprint"}, k)
			}
			out.Set(k, s)
		case "resource":
			if v == undefined {
				continue
			}
			if is := resourceParam(v); is != nil {
				return nil, at(is, k)
			}
			out.Set(k, v)
		case "claims":
			if v == undefined {
				continue
			}
			out.Set(k, v)
		default:
			s, ok, is := str(k)
			if is != nil {
				return nil, is
			}
			if ok {
				out.Set(k, s)
			}
		}
	}
	for _, k := range in.Keys() {
		if !contains(querySchemaKeys, k) {
			v, _ := in.Get(k)
			out.Set(k, v)
		}
	}
	return out, nil
}

// serializeQuery is serializeAuthorizationQuery.
func serializeQuery(q *jsonx.Object) *Params {
	p := &Params{}
	for _, k := range q.Keys() {
		v, _ := q.Get(k)
		switch x := v.(type) {
		case nil:
		case []any:
			for _, e := range x {
				p.Append(k, jsonx.JSString(e))
			}
		case *jsonx.Object:
			p.Set(k, jsonx.Stringify(x))
		default:
			p.Set(k, jsonx.JSString(x))
		}
	}
	return p
}

// queryOf is searchParamsToQuery.
func queryOf(p *Params) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for _, k := range p.Keys() {
		vals := p.GetAll(k)
		if len(vals) == 1 {
			o.Set(k, vals[0])
		} else {
			o.Set(k, strList(vals))
		}
	}
	return o
}

// formatErrorURL appends error, error_description, state and iss.
func formatErrorURL(base, code, desc, state, iss, mode string) string {
	p := &Params{}
	p.Append("error", code)
	p.Append("error_description", desc)
	if state != "" {
		p.Append("state", state)
	}
	if iss != "" {
		p.Append("iss", iss)
	}
	if mode == "fragment" {
		return base + "#" + p.String()
	}
	return appendQueryParams(base, p)
}

// appendQueryParams is @better-auth/core appendQueryParams.
func appendQueryParams(input string, p *Params) string {
	q := p.String()
	if q == "" {
		return input
	}
	relative := strings.HasPrefix(input, "/")
	target := input
	if relative {
		target = "https://better-auth.invalid" + input
	}
	u, err := parseJSURL(target)
	if err != nil {
		return input
	}
	sep := "&"
	if strings.HasSuffix(u.search, "&") {
		sep = ""
	}
	search := q
	if u.search != "" {
		search = strings.TrimPrefix(u.search, "?") + sep + q
	}
	out := u.withQuery(search)
	if relative {
		return strings.TrimPrefix(out, "https://better-auth.invalid")
	}
	return out
}

func (s *Server) errorURL(code, desc string) string {
	return formatErrorURL(s.baseURL()+"/error", code, desc, "", "", "query")
}

// wantsJSON is handleRedirect's test: a browser fetch, or Accept JSON.
func (c *call) wantsJSON() bool {
	return c.forceJSON || c.r.Header.Get("sec-fetch-mode") == "cors" || strings.Contains(c.header.Get("accept"), "application/json")
}

// handleRedirect answers {redirect, url} to a fetch, else a 302.
func (c *call) handleRedirect(url string) (*reply, error) {
	if c.wantsJSON() {
		return &reply{body: jsonx.ObjectOf("redirect", true, "url", url)}, nil
	}
	return nil, &apiErr{status: 302, headers: map[string]string{"Location": url}}
}

// stripLoopbackPort is stripLoopbackRedirectPort; ok false is undefined.
func stripLoopbackPort(uri string) (string, bool) {
	u, err := parseJSURL(uri)
	if err != nil {
		return "", false
	}
	if u.scheme != "http" || !(isLoopbackIP(u.hostname) || u.hostname == "localhost") {
		return "", false
	}
	i := strings.Index(uri, "://")
	if i < 0 {
		return "", false
	}
	start := i + 3
	end := len(uri)
	if j := strings.IndexAny(uri[start:], "/?#"); j >= 0 {
		end = start + j
	}
	authority := uri[start:end]
	var portStart int
	if strings.HasPrefix(authority, "[") {
		cb := strings.Index(authority, "]")
		if cb < 0 {
			return "", false
		}
		if cb+1 >= len(authority) || authority[cb+1] != ':' {
			return uri, true
		}
		portStart = cb + 1
	} else {
		portStart = strings.LastIndex(authority, ":")
		if portStart < 0 {
			return uri, true
		}
	}
	port := authority[portStart+1:]
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return uri[:start+portStart] + uri[end:], true
}

// findRegisteredRedirect is findRegisteredRedirectUri.
func findRegisteredRedirect(registered []string, requested string) bool {
	if registered == nil || requested == "" {
		return false
	}
	u, err := parseJSURL(requested)
	if err != nil || strings.Contains(requested, "#") || u.username != "" || u.password != "" {
		return false
	}
	without, ok := stripLoopbackPort(requested)
	for _, r := range registered {
		if r == requested {
			return true
		}
		if ok {
			if w, ok2 := stripLoopbackPort(r); ok2 && w == without {
				return true
			}
		}
	}
	return false
}

// redirectOnError is authorizeRedirectOnError: to the client's redirect
// URI when it is registered, else the error page.
func (s *Server) redirectOnError(c *call, raw *jsonx.Object, code, desc string) (*reply, error) {
	clientID, _ := strOf(raw, "client_id")
	redirect, _ := strOf(raw, "redirect_uri")
	if clientID != "" && redirect != "" {
		cl, err := s.getClient(clientID)
		if err == nil && cl != nil && !cl.disabled() && findRegisteredRedirect(cl.RedirectURIs, redirect) {
			state, _ := strOf(raw, "state")
			mode := "query"
			if rm, _ := strOf(raw, "response_mode"); rm == "fragment" {
				mode = "fragment"
			} else if rm != "query" {
				if rt, _ := strOf(raw, "response_type"); rt != "" && tokenWord.MatchString(rt) {
					mode = "fragment"
				}
			}
			return c.handleRedirect(formatErrorURL(redirect, code, desc, state, s.issuer(), mode))
		}
	}
	return c.handleRedirect(s.errorURL(code, desc))
}

func parsePrompt(p string) map[string]bool {
	set := map[string]bool{}
	for _, t := range strings.Split(p, " ") {
		t = strings.TrimSpace(t)
		if promptTokens[t] {
			set[t] = true
		}
	}
	return set
}

func resourceList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		if len(x) == 0 {
			return nil
		}
		out := make([]string, len(x))
		for i, e := range x {
			out[i], _ = e.(string)
		}
		return out
	}
	return nil
}

// ErrNoWorkspace is consentReferenceId's refusal.
var errNoWorkspace = oerr(403, "message", "Join a workspace before connecting an app.")

func (s *Server) authorizeEndpoint(c *call) (*reply, error) {
	src := c.query
	if c.r.Method == http.MethodPost {
		src = c.body
		if src == nil {
			src = jsonx.NewMap[any]()
		}
	}
	return s.authorize(c, src)
}

// authorize is authorizeEndpoint for the parameters raw.
func (s *Server) authorize(c *call, raw *jsonx.Object) (*reply, error) {
	reqObj, hasReq := strOf(raw, "request")
	_ = reqObj
	reqURI, hasReqURI := strOf(raw, "request_uri")
	_ = reqURI
	if hasReq && hasReqURI {
		return s.redirectOnError(c, raw, "invalid_request", "request and request_uri cannot be used together")
	}
	if hasReq {
		return s.redirectOnError(c, raw, "request_not_supported", "request object not supported")
	}
	if hasReqURI {
		if id, _ := strOf(raw, "client_id"); id == "" {
			return s.redirectOnError(c, raw, "invalid_request", "client_id is required")
		}
		return s.redirectOnError(c, raw, "request_uri_not_supported", "request_uri not supported")
	}
	q, is := parseAuthorizeQuery(raw)
	if is != nil {
		code, desc := mapIssue(is, authorizeErrorCodes, "invalid_request")
		return s.redirectOnError(c, raw, code, desc)
	}
	if _, ok := q.Get("response_type"); !ok {
		return s.redirectOnError(c, q, "invalid_request", "response_type is required")
	}
	promptText, _ := strOf(q, "prompt")
	prompts := parsePrompt(promptText)
	promptNone := prompts["none"]
	if prompts["select_account"] {
		return c.handleRedirect(s.errorURL("unsupported_prompt_select_account", "unsupported prompt type"))
	}
	clientID, _ := strOf(q, "client_id")
	cl, err := s.getClient(clientID)
	if err != nil {
		return nil, err
	}
	if cl == nil {
		return c.handleRedirect(s.errorURL("invalid_client", "client not found"))
	}
	if cl.disabled() {
		return c.handleRedirect(s.errorURL("client_disabled", "client is disabled"))
	}
	if !cl.allowsGrant("authorization_code") {
		return c.handleRedirect(s.errorURL("unauthorized_client", "client is not authorized to use the authorization_code grant"))
	}
	redirect, _ := strOf(q, "redirect_uri")
	if redirect == "" || !findRegisteredRedirect(cl.RedirectURIs, redirect) {
		return c.handleRedirect(s.errorURL("invalid_redirect", "invalid redirect uri"))
	}
	state, _ := strOf(q, "state")
	fail := func(code, desc string) (*reply, error) {
		return c.handleRedirect(formatErrorURL(redirect, code, desc, state, s.issuer(), "query"))
	}
	var scopes []string
	if sc, ok := strOf(q, "scope"); ok {
		scopes = []string{}
		for _, x := range strings.Split(sc, " ") {
			if x != "" {
				scopes = append(scopes, x)
			}
		}
		valid := cl.Scopes
		if !cl.hasScopes {
			valid = Scopes
		}
		bad := []string{}
		for _, x := range scopes {
			if !contains(valid, x) {
				bad = append(bad, x)
			}
		}
		if len(bad) > 0 {
			return fail("invalid_scope", "The following scopes are invalid: "+strings.Join(bad, ", "))
		}
	}
	if scopes == nil {
		scopes = cl.Scopes
		if !cl.hasScopes {
			scopes = Scopes
		}
		if scopes == nil {
			scopes = []string{}
		}
		q.Set("scope", strings.Join(scopes, " "))
	}
	openid := contains(scopes, "openid")
	if _, ok := q.Get("claims"); ok && !openid {
		return fail("invalid_request", "openid scope must be requested when using the claims parameter")
	}
	if openid {
		return fail("invalid_request", "claims must be a valid Claims request object")
	}
	if rv, ok := q.Get("resource"); ok {
		if _, e := s.resourcePolicy(cl.ClientID, rv, scopes); e != nil {
			code, _ := strOf(e.body, "error")
			desc, _ := strOf(e.body, "error_description")
			if code == "" {
				code = "invalid_target"
			}
			if desc == "" {
				desc = "requested resource invalid"
			}
			return fail(code, desc)
		}
	}
	nonce, _ := strOf(q, "nonce")
	challenge, _ := strOf(q, "code_challenge")
	method, _ := strOf(q, "code_challenge_method")
	if why := cl.pkceRequired(scopes, nonce); why != "" && (challenge == "" || method == "") {
		return fail("invalid_request", why)
	}
	if challenge != "" || method != "" {
		if challenge == "" || method == "" {
			return fail("invalid_request", "code_challenge and code_challenge_method must both be provided")
		}
		if method != "S256" {
			return fail("invalid_request", "invalid code_challenge method, only S256 is supported")
		}
	}
	rv, _ := q.Get("resource")
	requested := resourceList(rv)
	if requested == nil {
		requested = []string{}
	}
	sess := c.session
	now := s.Now()
	maxAgeV, hasMaxAge := q.Get("max_age")
	satisfied := false
	if sess != nil && hasMaxAge {
		ma := maxAgeV.(float64)
		satisfied = ma != 0 && float64(now-sess.Session.CreatedAt) <= ma*1000
	}
	if sess == nil || (hasMaxAge && !satisfied) || prompts["login"] || prompts["create"] {
		if promptNone {
			return fail("login_required", "authentication required")
		}
		return s.redirectWithPrompt(c, q, LoginHandoff, "")
	}
	if satisfied {
		q.Delete("max_age")
	}
	if prompts["consent"] {
		return s.redirectWithPrompt(c, q, ConsentHandoff, sess.Session.ID)
	}
	ws := sess.Session.ActiveOrganizationID
	if ws == nil || *ws == "" {
		return nil, errNoWorkspace
	}
	if derefB(cl.SkipConsent) {
		return s.issueCode(c, q, cl.ClientID, sess, *ws, requested)
	}
	consent, err := s.findConsent(cl.ClientID, sess.User.ID, *ws)
	if err != nil {
		return nil, err
	}
	need := consent == nil
	if !need {
		for _, sc := range scopes {
			if !contains(consent.scopes, sc) {
				need = true
			}
		}
		for _, r := range requested {
			if !contains(consent.resources, r) {
				need = true
			}
		}
	}
	if need {
		if promptNone {
			return fail("consent_required", "End-User consent is required")
		}
		return s.redirectWithPrompt(c, q, ConsentHandoff, sess.Session.ID)
	}
	return s.issueCode(c, q, cl.ClientID, sess, *ws, requested)
}

type consentRow struct {
	id        string
	scopes    []string
	resources []string
}

func (s *Server) findConsent(clientID, userID, ref string) (*consentRow, error) {
	var c consentRow
	var scopes string
	var resources *string
	err := s.DB.QueryRow(`SELECT id, scopes, resources FROM oauth_consent WHERE client_id = ? AND user_id = ? AND reference_id = ? LIMIT 1`, clientID, userID, ref).Scan(&c.id, &scopes, &resources)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.scopes, _ = parseJSONList(&scopes)
	c.resources, _ = parseJSONList(resources)
	return &c, nil
}

// redirectWithPrompt is redirectWithPromptCode: page?<signed query>. A
// consent redirect carries ba_pl, the session it was made for.
func (s *Server) redirectWithPrompt(c *call, q *jsonx.Object, page, sessionID string) (*reply, error) {
	now := s.Now()
	p := serializeQuery(q)
	p.Set("exp", itoa(now/1000+CodeExpiresIn))
	p.Set(issuedAtParam, itoa(now))
	p.Delete("sig")
	p.Delete(postLoginParam)
	if sessionID != "" {
		p.Set(postLoginParam, sessionID)
	}
	setSignedNames(p)
	p.Set("sig", makeSignature(canonicalize(p).String(), s.Secret))
	return c.handleRedirect(page + "?" + p.String())
}

// issueCode is redirectWithAuthorizationCode.
func (s *Server) issueCode(c *call, q *jsonx.Object, clientID string, sess *auth.Found, ws string, requested []string) (*reply, error) {
	code := randomString(32, lower+upper+digits)
	iat := s.Now() / 1000
	value := jsonx.ObjectOf(
		"type", "authorization_code",
		"query", q,
		"userId", sess.User.ID,
		"sessionId", sess.Session.ID,
		"referenceId", ws,
		"authTime", sess.Session.CreatedAt,
		"resource", strList(requested),
	)
	if _, err := s.DB.Exec(`INSERT INTO verification (id, identifier, value, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		newID(), HashToken(code), jsonx.Stringify(value), (iat+CodeExpiresIn)*1000, iat*1000, iat*1000); err != nil {
		return nil, err
	}
	redirect, _ := strOf(q, "redirect_uri")
	u, err := parseJSURL(redirect)
	if err != nil {
		return nil, err
	}
	pairs := [][2]string{{"code", code}}
	if st, _ := strOf(q, "state"); st != "" {
		pairs = append(pairs, [2]string{"state", st})
	}
	pairs = append(pairs, [2]string{"iss", s.issuer()})
	return c.handleRedirect(u.setParams(pairs...))
}

// resourcePolicy is resolveResourcePolicy for Recall: every resource must
// be configured, enabled and linked to the client; allowed_scopes narrow.
// It returns the effective scopes.
func (s *Server) resourcePolicy(clientID string, resource any, scopes []string) (*policy, *apiErr) {
	list := resourceList(resource)
	effective := append([]string(nil), scopes...)
	if list == nil {
		return &policy{scopes: effective}, nil
	}
	uniq := []string{}
	for _, r := range list {
		if !contains(uniq, r) {
			uniq = append(uniq, r)
		}
	}
	userinfo := s.baseURL() + "/oauth2/userinfo"
	var rows []*resourceRow
	for _, id := range uniq {
		if id == userinfo {
			continue
		}
		row, err := s.getResource(id)
		if err != nil {
			return nil, &apiErr{status: 500}
		}
		if row == nil {
			return nil, errDesc(400, "invalid_target", "requested resource "+id+" is not configured")
		}
		if row.Disabled {
			return nil, errDesc(400, "invalid_target", "requested resource "+id+" is disabled")
		}
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		linked := map[string]bool{}
		rs, err := s.DB.Query(`SELECT resource_id FROM oauth_client_resource WHERE client_id = ?`, clientID)
		if err != nil {
			return nil, &apiErr{status: 500}
		}
		for rs.Next() {
			var id string
			rs.Scan(&id)
			linked[id] = true
		}
		rs.Close()
		missing := []string{}
		for _, r := range rows {
			if !linked[r.Identifier] {
				missing = append(missing, r.Identifier)
			}
		}
		if len(missing) > 0 {
			return nil, errDesc(400, "invalid_target", "client "+clientID+" is not linked to resource(s) "+strings.Join(missing, ", "))
		}
	}
	for _, r := range rows {
		if r.AllowedScopes == nil {
			continue
		}
		var allowed []string
		json.Unmarshal([]byte(*r.AllowedScopes), &allowed)
		inter := []string{}
		for _, sc := range effective {
			if contains(allowed, sc) {
				inter = append(inter, sc)
			}
		}
		if len(inter) == 0 {
			return nil, errDesc(400, "invalid_scope", "none of the requested scopes are allowed for resource "+r.Identifier)
		}
		effective = inter
	}
	out := &policy{scopes: effective}
	for _, r := range rows {
		out.dpop = out.dpop || r.DPoPRequired
		if r.AccessTTL != nil && (out.accessTTL == nil || *r.AccessTTL < *out.accessTTL) {
			out.accessTTL = r.AccessTTL
		}
		if r.RefreshTTL != nil && (out.refreshTTL == nil || *r.RefreshTTL < *out.refreshTTL) {
			out.refreshTTL = r.RefreshTTL
		}
	}
	return out, nil
}

// policy is resolveResourcePolicy's result for opaque tokens.
type policy struct {
	scopes                []string
	accessTTL, refreshTTL *int64
	dpop                  bool
}
