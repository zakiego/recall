package oauth

// POST /oauth2/consent, POST /oauth2/continue and the sign-in after hook
// that resumes an authorization once the user has signed in.

import (
	"net/http"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// validationErr is better-call's VALIDATION_ERROR for a plain endpoint.
func validationErr(issues ...string) *apiErr {
	return oerr(400, "message", strings.Join(issues, "; "), "code", "VALIDATION_ERROR")
}

var errUnauthorized = oerr(401, "message", "Unauthorized", "code", "UNAUTHORIZED")

func removePrompt(p *Params, prompt string) *Params {
	next := p.Clone()
	cur, ok := next.Get("prompt")
	if !ok {
		return next
	}
	parts := strings.Split(cur, " ")
	for i, v := range parts {
		if v == prompt {
			parts = append(parts[:i], parts[i+1:]...)
			if len(parts) > 0 {
				next.Set("prompt", strings.Join(parts, " "))
			} else {
				next.Delete("prompt")
			}
			break
		}
	}
	return next
}

func sessionFresh(createdAt, issuedAt int64) bool {
	return issuedAt != 0 && createdAt >= issuedAt
}

func (s *Server) consent(c *call) (*reply, error) {
	body := c.body
	if body == nil {
		body = jsonx.NewMap[any]()
	}
	accept := get(body, "accept")
	var problems []string
	if _, ok := accept.(bool); !ok {
		problems = append(problems, "[body.accept] Invalid input: expected boolean, received "+received(accept))
	}
	var bodyScope *string
	if v := get(body, "scope"); v != undefined {
		sc, ok := v.(string)
		if !ok {
			problems = append(problems, "[body.scope] Invalid input: expected string, received "+received(v))
		}
		bodyScope = &sc
	}
	if v := get(body, "oauth_query"); v != undefined {
		if _, ok := v.(string); !ok {
			problems = append(problems, "[body.oauth_query] Invalid input: expected string, received "+received(v))
		}
	}
	if len(problems) > 0 {
		return nil, validationErr(problems...)
	}
	if c.session == nil {
		return nil, errUnauthorized
	}
	if c.state == nil {
		return nil, descErr(400, "missing oauth query", "invalid_request")
	}
	query := ParseParams(c.state.query)
	original := []string{}
	if sc, ok := query.Get("scope"); ok {
		original = strings.Split(sc, " ")
	}
	clientID, _ := query.Get("client_id")
	if clientID == "" {
		return nil, descErr(400, "client_id is required", "invalid_client")
	}
	var requested []string
	if bodyScope != nil {
		requested = strings.Split(*bodyScope, " ")
		for _, sc := range requested {
			if !contains(original, sc) {
				return nil, descErr(400, "Scope not originally requested", "invalid_request")
			}
		}
	}
	if accept != true {
		redirect, _ := query.Get("redirect_uri")
		state, _ := query.Get("state")
		return &reply{body: jsonx.ObjectOf("redirect", true, "url", formatErrorURL(redirect, "access_denied", "User denied access", state, s.issuer(), "query"))}, nil
	}
	sess := c.session
	promptText, _ := query.Get("prompt")
	hasLogin := parsePrompt(promptText)["login"]
	loginOK := hasLogin && sessionFresh(sess.Session.CreatedAt, c.state.issuedAt)
	c.forceJSON = true
	if hasLogin && !loginOK {
		return s.authorize(c, queryOf(query))
	}
	ws := sess.Session.ActiveOrganizationID
	if ws == nil || *ws == "" {
		return nil, errNoWorkspace
	}
	scopes := original
	if requested != nil {
		scopes = requested
	}
	found, err := s.findConsent(clientID, sess.User.ID, *ws)
	if err != nil {
		return nil, err
	}
	iat := s.Now() / 1000
	resources := query.GetAll("resource")
	var resJSON any
	if len(resources) > 0 {
		resJSON = jsonList(resources)
	}
	if found != nil {
		// An update leaves resources alone when the request named none.
		_, err = s.DB.Exec(`UPDATE oauth_consent SET resources = coalesce(?, resources), scopes = ?, requested_user_info_claims = '[]', updated_at = ? WHERE id = ?`, resJSON, jsonList(scopes), iat*1000, found.id)
	} else {
		_, err = s.DB.Exec(`INSERT INTO oauth_consent (id, client_id, user_id, reference_id, resources, requested_user_info_claims, scopes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, '[]', ?, ?, ?)`,
			newID(), clientID, sess.User.ID, *ws, resJSON, jsonList(scopes), iat*1000, iat*1000)
	}
	if err != nil {
		return nil, err
	}
	if requested != nil {
		query.Set("scope", strings.Join(scopes, " "))
	}
	next := removePrompt(query, "consent")
	if loginOK {
		next = removePrompt(next, "login")
		next.Delete("max_age")
	}
	return s.authorize(c, queryOf(next))
}

func (s *Server) continueEndpoint(c *call) (*reply, error) {
	body := c.body
	if body == nil {
		body = jsonx.NewMap[any]()
	}
	var problems []string
	for _, k := range []string{"selected", "created", "postLogin"} {
		if v := get(body, k); v != undefined {
			if _, ok := v.(bool); !ok {
				problems = append(problems, "[body."+k+"] Invalid input: expected boolean, received "+received(v))
			}
		}
	}
	if v := get(body, "oauth_query"); v != undefined {
		if _, ok := v.(string); !ok {
			problems = append(problems, "[body.oauth_query] Invalid input: expected string, received "+received(v))
		}
	}
	if len(problems) > 0 {
		return nil, validationErr(problems...)
	}
	if c.session == nil {
		return nil, errUnauthorized
	}
	var drop string
	switch {
	case get(body, "selected") == true:
		drop = "select_account"
	case get(body, "created") == true:
		drop = "create"
	case get(body, "postLogin") == true:
	default:
		return nil, descErr(400, "Missing parameters", "invalid_request")
	}
	if c.state == nil {
		return nil, descErr(400, "missing oauth query", "invalid_request")
	}
	q := ParseParams(c.state.query)
	if drop != "" {
		q = removePrompt(q, drop)
	}
	c.forceJSON = true
	return s.authorize(c, queryOf(q))
}

// ResumeAfterSignIn is the oauth-provider after hook on a sign-in that set
// a session cookie while an oauth_query was verified: the authorization
// runs again for the new session and its answer replaces the sign-in's.
// It writes the response and reports whether it did.
func (s *Server) ResumeAfterSignIn(w http.ResponseWriter, r *http.Request, st *SignInState, sess *auth.Found) bool {
	if st == nil || st.st == nil || st.st.query == "" || sess == nil {
		return false
	}
	c := &call{r: r, header: r.Header.Clone(), session: sess}
	mode := strings.ToLower(r.Header.Get("sec-fetch-mode"))
	acceptHdr := strings.ToLower(r.Header.Get("accept"))
	if !(mode == "navigate" || mode == "" && (strings.Contains(acceptHdr, "text/html") || strings.Contains(acceptHdr, "application/xhtml+xml"))) {
		c.forceJSON = true
	}
	q := removePrompt(ParseParams(st.st.query), "login")
	if sessionFresh(sess.Session.CreatedAt, st.st.issuedAt) {
		q.Delete("max_age")
	}
	rep, err := s.authorize(c, queryOf(q))
	if err != nil {
		if ae, ok := err.(*apiErr); ok {
			if ae.status == 302 {
				write(w, 302, nil, true, ae.headers)
				return true
			}
			writeErr(w, ae, nil)
			return true
		}
		write(w, 500, nil, true)
		return true
	}
	write(w, 200, rep.body, rep.empty, rep.headers)
	return true
}
