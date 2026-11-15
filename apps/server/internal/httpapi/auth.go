package httpapi

// /api/auth: the routes that replace Better Auth's handler (better-auth.ts).
// The dashboard ships with the server, so its routes are the Go build's own
// (the SPA calls them in M8); only the OAuth server under /api/auth/oauth2
// keeps Better Auth's paths and JSON, because clients cache them.
//
//	GET  /api/auth/session                       null | {session, user}
//	POST /api/auth/sign-in/username              {username, password} -> {session, user}, sets the cookie
//	POST /api/auth/sign-out                      {success: true}
//	POST /api/auth/change-password               {currentPassword, newPassword, revokeOtherSessions?} -> {session, user}
//	GET  /api/auth/passkey/register-options      PublicKeyCredentialCreationOptionsJSON (signed in)
//	POST /api/auth/passkey/register              {response, name?} -> passkey item (signed in, fresh session)
//	GET  /api/auth/passkey/authenticate-options  PublicKeyCredentialRequestOptionsJSON
//	POST /api/auth/passkey/authenticate          {response} -> {session, user}, sets the cookie
//
// Errors are {code, message} with the status (Better Auth's shape). Every
// request is rate limited per client IP and path; a write must not come from
// another origin (crossOriginWrite); `/api/auth/organization*` stays 404.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

type authRouteDef struct {
	method string
	fn     func(s *Server, w http.ResponseWriter, r *http.Request) error
}

var authRoutes = map[string]authRouteDef{
	"/session":                      {http.MethodGet, (*Server).authSession},
	"/sign-in/username":             {http.MethodPost, (*Server).authSignInUsername},
	"/sign-out":                     {http.MethodPost, (*Server).authSignOut},
	"/change-password":              {http.MethodPost, (*Server).authChangePassword},
	"/passkey/register-options":     {http.MethodGet, (*Server).authRegisterOptions},
	"/passkey/register":             {http.MethodPost, (*Server).authRegister},
	"/passkey/authenticate-options": {http.MethodGet, (*Server).authAuthenticateOptions},
	"/passkey/authenticate":         {http.MethodPost, (*Server).authAuthenticate},
	"/error":                        {http.MethodGet, (*Server).authErrorPage},
	"/ok":                           {http.MethodGet, (*Server).authOK},
}

// authErr is a refused auth request: {code, message} with the status.
type authErr = auth.Error

func authError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message}, nil)
}

var slashes = regexp.MustCompile(`/{2,}`)

// blockedAuthPath is blockedAuthPath: the organization plugin's endpoints.
func blockedAuthPath(r *http.Request) bool {
	p, err := url.PathUnescape(r.URL.EscapedPath())
	if err != nil {
		return true
	}
	p = strings.ToLower(slashes.ReplaceAllString(p, "/"))
	if !strings.HasPrefix(p, "/api/auth/") {
		return false
	}
	rest := p[len("/api/auth"):]
	return rest == "/organization" || strings.HasPrefix(rest, "/organization/")
}

func (s *Server) authRoute(w http.ResponseWriter, r *http.Request) {
	appURL := config.Load().URL
	if blockedAuthPath(r) {
		authError(w, 404, "NOT_FOUND", "Not found")
		return
	}
	oauthPath := strings.HasPrefix(strings.ToLower(r.URL.Path), "/api/auth/oauth2/") || strings.EqualFold(r.URL.Path, "/api/auth/.well-known/oauth-authorization-server")
	// The OAuth endpoints run Better Auth's own cookie origin check, after routing.
	if auth.CrossOriginWrite(r, appURL) || !oauthPath && cookieWriteWithoutOrigin(r, appURL) {
		authError(w, 403, "INVALID_ORIGIN", "Invalid origin")
		return
	}
	path := strings.TrimRight(strings.TrimPrefix(r.URL.Path, "/api/auth"), "/")
	s.oauth().Seed()
	ip := auth.ClientIP(r.Header)
	if config.Load().AuthRateLimit {
		if allowed, retry := s.Limiter.Allow(auth.RateLimitIP(ip), path); !allowed {
			w.Header().Set("X-Retry-After", strconv.FormatInt(retry, 10))
			// Better Auth's 429 has a message and no code.
			writeJSON(w, 429, map[string]string{"message": "Too many requests. Please try again later."}, nil)
			return
		}
	}
	if strings.HasPrefix(path, "/oauth2/") || path == "/.well-known/oauth-authorization-server" {
		// Only the endpoints that read the session refresh it.
		var f *auth.Found
		var err error
		switch path {
		case "/oauth2/register", "/oauth2/authorize", "/oauth2/consent", "/oauth2/continue":
			f, err = auth.GetSession(s.DB, auth.TokenFrom(r), true, s.Now())
		}
		if err != nil {
			log.Printf("[recall] auth %s failed: %v", path, err)
			authError(w, 500, "INTERNAL_SERVER_ERROR", "Internal error")
			return
		}
		if f != nil && f.Refreshed {
			http.SetCookie(w, auth.SessionCookie(appURL, f.Token))
		}
		s.oauth().Handle(w, r, path, f)
		return
	}
	if s.RP == nil && strings.HasPrefix(path, "/passkey/") {
		authError(w, auth.ErrPasskeysOff.Status, auth.ErrPasskeysOff.Code, auth.ErrPasskeysOff.Message)
		return
	}
	route, ok := authRoutes[path]
	if !ok {
		authError(w, 404, "NOT_FOUND", "Not found")
		return
	}
	if r.Method != route.method {
		authError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	if err := route.fn(s, w, r); err != nil {
		var ae *authErr
		if errors.As(err, &ae) {
			authError(w, ae.Status, ae.Code, ae.Message)
			return
		}
		if oauth.WriteError(w, err) {
			return
		}
		log.Printf("[recall] auth %s failed: %v", path, err)
		authError(w, 500, "INTERNAL_SERVER_ERROR", "Internal error")
	}
}

// SessionBody is a signed-in session: {session, user}.
type SessionBody struct {
	Session SessionInfo `json:"session"`
	User    viewer.User `json:"user"`
}

// SessionInfo is the session part of SessionBody.
type SessionInfo struct {
	ID                   string  `json:"id"`
	ExpiresAt            int64   `json:"expiresAt"`
	CreatedAt            int64   `json:"createdAt"`
	ActiveOrganizationID *string `json:"activeOrganizationId"`
}

func sessionBody(f *auth.Found) SessionBody {
	name := f.User.Name
	if f.User.Username != nil && *f.User.Username != "" {
		name = *f.User.Username
	}
	if f.User.DisplayUsername != nil && *f.User.DisplayUsername != "" {
		name = *f.User.DisplayUsername
	}
	return SessionBody{
		Session: SessionInfo{ID: f.Session.ID, ExpiresAt: f.Session.ExpiresAt, CreatedAt: f.Session.CreatedAt, ActiveOrganizationID: f.Session.ActiveOrganizationID},
		User:    viewer.User{ID: f.User.ID, Username: name, Name: f.User.Name},
	}
}

func (s *Server) authSession(w http.ResponseWriter, r *http.Request) error {
	appURL := config.Load().URL
	token := auth.TokenFrom(r)
	f, err := auth.GetSession(s.DB, token, true, s.Now())
	if err != nil {
		return err
	}
	if f == nil {
		if token != "" {
			http.SetCookie(w, auth.ClearCookie(appURL, auth.CookieName))
		}
		writeJSON(w, 200, nil, nil)
		return nil
	}
	if f.Refreshed {
		http.SetCookie(w, auth.SessionCookie(appURL, token))
	}
	writeJSON(w, 200, sessionBody(f), nil)
	return nil
}

// authErrorPage is Better Auth's /error in production: a 302 to the site
// root with the error (codes outside [A-Za-z0-9_'-] become UNKNOWN).
func (s *Server) authErrorPage(w http.ResponseWriter, r *http.Request) error {
	q := oauth.ParseParams(r.URL.RawQuery)
	code, _ := q.Get("error")
	if code == "" || !safeErrorCode.MatchString(code) {
		code = "UNKNOWN"
	}
	p := &oauth.Params{}
	p.Set("error", code)
	if d, _ := q.Get("error_description"); d != "" {
		p.Set("error_description", d)
	}
	writeRaw(w, 302, nil, map[string]string{"location": "/?" + p.String()})
	return nil
}

var safeErrorCode = regexp.MustCompile(`^['A-Za-z0-9_-]+$`)

// authOK is Better Auth's /ok.
func (s *Server) authOK(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]bool{"ok": true}, nil)
	return nil
}

func readAuthJSON(r *http.Request, into any) error {
	if ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("content-type"), ";")[0])); ct != "application/json" {
		return &authErr{Status: 415, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Content-Type must be application/json"}
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || json.Unmarshal(b, into) != nil {
		return &authErr{Status: 400, Code: "VALIDATION_ERROR", Message: "body must be a JSON object"}
	}
	return nil
}

var errBadCredentials = &authErr{Status: 401, Code: "INVALID_USERNAME_OR_PASSWORD", Message: "Invalid username or password"}

func (s *Server) authSignInUsername(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Username   *string `json:"username"`
		Password   *string `json:"password"`
		OAuthQuery *string `json:"oauth_query"`
	}
	if err := readAuthJSON(r, &body); err != nil {
		return err
	}
	st, err := s.oauthState(body.OAuthQuery)
	if err != nil {
		return err
	}
	if body.Username == nil || body.Password == nil || *body.Username == "" || *body.Password == "" {
		return &authErr{Status: 400, Code: "VALIDATION_ERROR", Message: "username and password are required"}
	}
	if strx.Len16(*body.Password) > auth.MaxPasswordLength {
		return &authErr{Status: 400, Code: "PASSWORD_TOO_LONG", Message: "Password too long"}
	}
	u, ok, err := users.VerifyCredentials(s.DB, *body.Username, *body.Password)
	if err != nil {
		return err
	}
	if !ok {
		return errBadCredentials
	}
	return s.signIn(w, r, u.ID, st)
}

func (s *Server) authSignOut(w http.ResponseWriter, r *http.Request) error {
	if err := auth.DeleteSession(s.DB, auth.TokenFrom(r)); err != nil {
		return err
	}
	http.SetCookie(w, auth.ClearCookie(config.Load().URL, auth.CookieName))
	writeJSON(w, 200, map[string]bool{"success": true}, nil)
	return nil
}

func (s *Server) signedIn(r *http.Request) (*auth.Found, error) {
	f, err := auth.GetSession(s.DB, auth.TokenFrom(r), false, s.Now())
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, auth.ErrUnauthorized
	}
	return f, nil
}

// authChangePassword is changePassword: the current password checks, the
// new one is held to the same rules as a new user's, and with
// revokeOtherSessions every other session ends and this one is replaced.
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) error {
	f, err := s.signedIn(r)
	if err != nil {
		return err
	}
	var body struct {
		CurrentPassword     *string `json:"currentPassword"`
		NewPassword         *string `json:"newPassword"`
		RevokeOtherSessions bool    `json:"revokeOtherSessions"`
	}
	if err := readAuthJSON(r, &body); err != nil {
		return err
	}
	if body.CurrentPassword == nil || body.NewPassword == nil {
		return &authErr{Status: 400, Code: "VALIDATION_ERROR", Message: "currentPassword and newPassword are required"}
	}
	if p := users.PasswordProblem(*body.NewPassword); p != "" {
		code := "PASSWORD_TOO_SHORT"
		if strx.Len16(*body.NewPassword) > auth.MaxPasswordLength {
			code = "PASSWORD_TOO_LONG"
		}
		return &authErr{Status: 400, Code: code, Message: p}
	}
	ok, err := users.CheckPassword(s.DB, f.User.ID, *body.CurrentPassword)
	if err != nil {
		return err
	}
	if !ok {
		return &authErr{Status: 400, Code: "INVALID_PASSWORD", Message: "Invalid password"}
	}
	hash, err := auth.HashPassword(*body.NewPassword)
	if err != nil {
		return err
	}
	if err := users.SetPassword(s.DB, f.User.ID, hash, s.Now()); err != nil {
		return err
	}
	if !body.RevokeOtherSessions {
		writeJSON(w, 200, sessionBody(f), nil)
		return nil
	}
	if err := auth.DeleteOtherSessions(s.DB, f.User.ID, f.Session.ID); err != nil {
		return err
	}
	if err := auth.DeleteSession(s.DB, f.Token); err != nil {
		return err
	}
	return s.signIn(w, r, f.User.ID, nil)
}

func challengeToken(r *http.Request) string {
	c, err := r.Cookie(auth.ChallengeCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) authRegisterOptions(w http.ResponseWriter, r *http.Request) error {
	f, err := s.signedIn(r)
	if err != nil {
		return err
	}
	v := f.Viewer()
	v.PasskeyName = r.URL.Query().Get("name")
	opts, token, err := s.RP.RegisterOptions(s.DB, v, s.Now())
	if err != nil {
		return err
	}
	http.SetCookie(w, auth.ChallengeCookieFor(config.Load().URL, token))
	writeJSON(w, 200, opts, nil)
	return nil
}

func isObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

func (s *Server) authRegister(w http.ResponseWriter, r *http.Request) error {
	f, err := s.signedIn(r)
	if err != nil {
		return err
	}
	var body struct {
		Response json.RawMessage `json:"response"`
		Name     *string         `json:"name"`
	}
	if err := readAuthJSON(r, &body); err != nil {
		return err
	}
	if !isObject(body.Response) {
		return &authErr{Status: 400, Code: "VALIDATION_ERROR", Message: "response is required"}
	}
	name := ""
	if body.Name != nil {
		name = *body.Name
	}
	pk, err := s.RP.Register(s.DB, f.Viewer(), challengeToken(r), body.Response, name, s.Now())
	http.SetCookie(w, auth.ClearCookie(config.Load().URL, auth.ChallengeCookie))
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": pk.ID, "name": pk.Name, "deviceType": pk.DeviceType, "backedUp": pk.BackedUp, "createdAt": pk.CreatedAt}, nil)
	return nil
}

func (s *Server) authAuthenticateOptions(w http.ResponseWriter, r *http.Request) error {
	f, err := auth.GetSession(s.DB, auth.TokenFrom(r), false, s.Now())
	if err != nil {
		return err
	}
	var v *auth.Viewer
	if f != nil {
		v = f.Viewer()
	}
	opts, token, err := s.RP.AuthenticateOptions(s.DB, v, s.Now())
	if err != nil {
		return err
	}
	http.SetCookie(w, auth.ChallengeCookieFor(config.Load().URL, token))
	writeJSON(w, 200, opts, nil)
	return nil
}

func (s *Server) authAuthenticate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Response   json.RawMessage `json:"response"`
		OAuthQuery *string         `json:"oauth_query"`
	}
	if err := readAuthJSON(r, &body); err != nil {
		return err
	}
	st, err := s.oauthState(body.OAuthQuery)
	if err != nil {
		return err
	}
	if !isObject(body.Response) {
		return &authErr{Status: 400, Code: "VALIDATION_ERROR", Message: "response is required"}
	}
	userID, err := s.RP.Authenticate(s.DB, challengeToken(r), body.Response, s.Now())
	http.SetCookie(w, auth.ClearCookie(config.Load().URL, auth.ChallengeCookie))
	if err != nil {
		return err
	}
	return s.signIn(w, r, userID, st)
}

// signIn starts a session for userID in their first workspace, sets the
// cookie and answers {session, user}.
// With st (a verified oauth_query) the OAuth authorization resumes for the
// new session and its answer is the response, as Better Auth's after hook.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, userID string, st *oauth.SignInState) error {
	token, _, err := auth.CreateSession(s.DB, userID, auth.ClientIP(r.Header), r.UserAgent(), s.Now())
	if err != nil {
		return &authErr{Status: 500, Code: "UNABLE_TO_CREATE_SESSION", Message: "Unable to create session"}
	}
	f, err := auth.GetSession(s.DB, token, false, s.Now())
	if err != nil || f == nil {
		return &authErr{Status: 500, Code: "UNABLE_TO_CREATE_SESSION", Message: "Unable to create session"}
	}
	http.SetCookie(w, auth.SessionCookie(config.Load().URL, token))
	if st != nil && s.oauth().ResumeAfterSignIn(w, r, st, f) {
		return nil
	}
	writeJSON(w, 200, sessionBody(f), nil)
	return nil
}

// oauthState is the oauth-provider before hook on a sign-in body.
func (s *Server) oauthState(q *string) (*oauth.SignInState, error) {
	if q == nil || *q == "" {
		return nil, nil
	}
	return s.oauth().VerifyHook(*q)
}

// oauthHandoff is the /app/oauth/sign-in and /app/oauth/approve routes.
func (s *Server) oauthHandoff(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeRaw(w, 302, nil, oauth.HandOff(r.URL.RawQuery, page, config.Load().URL))
	}
}

// cookieWriteWithoutOrigin is Better Auth's own origin check: a write that
// carries cookies must say where it comes from (Origin, else Referer), and
// that must be RECALL_URL's origin. Writes without cookies (an MCP client
// redeeming a code) pass.
func cookieWriteWithoutOrigin(r *http.Request, appURL string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if r.Header.Get("cookie") == "" {
		return false
	}
	from := r.Header.Get("origin")
	if from == "" {
		from = r.Header.Get("referer")
	}
	if from == "" || from == "null" {
		return true
	}
	u, err := whatwg.ParseURL(from)
	return err != nil || u.Origin() != auth.AppOrigin(appURL)
}
