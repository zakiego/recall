package httpapi

// connector-fns.ts, oauth-fns.ts, and the Gmail connect routes
// (src/server/connectors/oauth.ts: /app/connectors/google/start|callback).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

var appProviders = []string{"oslo", "finansial", "whatsapp"}

// friendly is connector-fns.ts friendly(): errors the page can show as they are.
func friendly(err error) error {
	var ce *connectors.ConnectorError
	var ge *connectors.GoogleError
	switch {
	case errors.As(err, &ce):
		return rpcFail(ce.Msg)
	case errors.As(err, &ge):
		switch ge.Code {
		case "rate_limited":
			return rpcFail("Gmail is rate limiting this account. Wait a minute and try again.")
		case "not_found":
			return rpcFail("Gmail couldn't find that. It may have been deleted.")
		}
		return rpcFail("Gmail didn't answer (HTTP " + strconv.Itoa(ge.Status) + "). Try again in a moment.")
	}
	return err
}

func appFriendly(err error) error {
	var ae *apps.Error
	if errors.As(err, &ae) {
		return rpcFail(ae.Msg)
	}
	return err
}

func init() {
	registerRPC(
		RPC{Name: "getConnectors", Method: "GET", Input: zod.Object(zod.F("container", rpcContainer)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			configured := connectors.Configured()
			canManage := viewer.RoleAtLeast(m.Role, users.Admin)
			uid := m.Viewer.User.ID
			missing := []string{}
			if canManage && !configured {
				missing = connectors.MissingKeys()
			}
			var gmail any
			mine, ok, err := connectors.GetConnection(c.DB(), m.WS, uid)
			if err != nil {
				return nil, err
			}
			if ok {
				gmail = jsonx.ObjectOf("email", mine.AccountEmail, "status", mine.Status, "connectedAt", mine.CreatedAt, "lastUsedAt", mine.LastUsedAt)
			}
			conns := []connectors.Connection{}
			if canManage {
				if conns, err = connectors.ListConnections(c.DB(), m.WS, uid); err != nil {
					return nil, err
				}
			}
			imports, err := connectors.ListImports(c.DB(), m.WS, c.Str("container"), uid, 30)
			if err != nil {
				return nil, err
			}
			views := make([]any, len(appProviders))
			for i, p := range appProviders {
				if views[i], err = apps.ConnectorView(c.DB(), m.WS, p); err != nil {
					return nil, err
				}
			}
			return jsonx.ObjectOf("configured", configured, "missing", missing, "canManage", canManage, "gmail", gmail, "connections", conns,
				"imports", imports, "maxImport", connectors.MaxImport, "apps", views, "appSecretConfigured", apps.SecretConfigured()), nil
		}},
		RPC{Name: "searchGmailMessages", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("q", zod.String().Trim().Max(500)),
			zod.F("pageToken", zod.Optional(zod.String().Max(200))),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			res, err := connectors.Search(context.WithoutCancel(c.R.Context()), c.DB(), connectors.SearchInput{WorkspaceID: m.WS, UserID: m.Viewer.User.ID, Container: c.Str("container"), Q: c.Str("q"), PageToken: c.Str("pageToken")}, c.S.google())
			if err != nil {
				return nil, friendly(err)
			}
			return res, nil
		}},
		RPC{Name: "importGmailMessages", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("messages", zod.Array(zod.Object(
				zod.F("id", zod.String().Regex(connectors.MessageID, `^[A-Za-z0-9_-]{6,64}$`)),
				zod.F("subject", zod.Optional(zod.Nullable(zod.String().Max(300)))),
			)).Min(1).Max(connectors.MaxImport)),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			msgsV, _ := c.Obj().Get("messages")
			var msgs []connectors.ImportMessage
			for _, x := range msgsV.([]any) {
				o := x.(*jsonx.Object)
				id, _ := o.Get("id")
				im := connectors.ImportMessage{ID: id.(string)}
				if s, ok := o.Get("subject"); ok {
					if str, ok := s.(string); ok {
						im.Subject = &str
					}
				}
				msgs = append(msgs, im)
			}
			r, err := connectors.Import(c.DB(), connectors.ImportInput{WorkspaceID: m.WS, UserID: m.Viewer.User.ID, Container: c.Str("container"), Messages: msgs})
			if err != nil {
				return nil, friendly(err)
			}
			return jsonx.ObjectOf("queued", r.Queued, "duplicates", r.Duplicates), nil
		}},
		RPC{Name: "disconnectGmail", Method: "POST", SameOrigin: true, Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			return connectors.Disconnect(context.WithoutCancel(c.R.Context()), c.DB(), m.WS, m.Viewer.User.ID, c.S.google())
		}},
		RPC{Name: "saveAppConnectorFn", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("provider", zod.Enum(appProviders...)),
			zod.F("container", rpcContainer),
			zod.F("baseUrl", zod.String().Trim().Min(1).Max(500)),
			zod.F("apiKey", zod.Optional(zod.Nullable(zod.String().Max(apps.MaxAPIKeyLen)))),
			zod.F("enabled", zod.Bool()),
			zod.F("thresholds", zod.Optional(zod.Record(zod.Number().Min(0).Max(1e15)).Keys(zod.String().Regex(regexp.MustCompile(`^[A-Z]{3}$`), `^[A-Z]{3}$`)))),
		), Fn: rpcSaveAppConnector},
		RPC{Name: "syncAppConnectorFn", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("provider", zod.Enum(appProviders...))), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			r, err := apps.Sync(context.WithoutCancel(c.R.Context()), c.DB(), m.WS, c.Str("provider"), apps.SyncDeps{})
			if err != nil {
				return nil, appFriendly(err)
			}
			return jsonx.ObjectOf("ok", r.OK, "items", r.Items, "documents", r.Documents, "staged", r.Staged, "caughtUp", r.CaughtUp, "error", r.Error), nil
		}},
		RPC{Name: "getConsentRequest", Method: "GET", Input: zod.Object(zod.F("query", zod.String().Max(8000))), Fn: rpcConsentRequest},
	)
}

func rpcSaveAppConnector(c *Call) (any, error) {
	m, err := c.RequireMember(users.Admin)
	if err != nil {
		return nil, err
	}
	provider := c.Str("provider")
	enabled, _ := c.Obj().Get("enabled")
	in := apps.SaveInput{WorkspaceID: m.WS, Provider: provider, Container: c.Str("container"), BaseURL: c.Str("baseUrl"), Enabled: enabled == true, Now: c.Now()}
	uid := m.Viewer.User.ID
	in.UserID = &uid
	// Write-only: omitted keeps the stored key, "" keeps it too, null removes it.
	var newKey *string
	if v, ok := c.Opt("apiKey"); ok {
		if s, isStr := v.(string); isStr {
			if t := strx.Trim(s); t != "" {
				newKey = &t
				in.APIKey.Set, in.APIKey.Value = true, &t
			}
		} else {
			in.APIKey.Set = true
		}
	}
	existing, found, err := apps.Get(c.DB(), m.WS, provider)
	if err != nil {
		return nil, err
	}
	if enabled == true && newKey == nil && !(found && existing.APIKey != nil) {
		return nil, rpcFail("Paste an API key to turn syncing on.")
	}
	if provider == "finansial" {
		if th, ok := c.Opt("thresholds"); ok {
			in.Settings = jsonx.ObjectOf("thresholds", th)
		}
	}
	if _, err := apps.Save(c.DB(), in); err != nil {
		return nil, appFriendly(err)
	}
	// Turning a connector on arms the hourly sync (a no-op if it's queued).
	if _, _, err := apps.Schedule(c.DB(), c.Now()); err != nil {
		return nil, err
	}
	return apps.ConnectorView(c.DB(), m.WS, provider)
}

// hostOf is oauth-fns.ts hostOf: new URL(url).host, "" when it doesn't parse.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	if w, err := whatwg.ParseURL(raw); err == nil {
		if w.Host != "" {
			return w.HostPort()
		}
		if u, err := url.Parse(raw); err == nil {
			return u.Host
		}
	}
	return ""
}

func rpcConsentRequest(c *Call) (any, error) {
	v, err := c.Viewer()
	if err != nil {
		return nil, err
	}
	if v == nil {
		return jsonx.ObjectOf("ok", false, "problem", "Log in first, then start connecting again from the app.", "signedIn", false), nil
	}
	query := strings.TrimPrefix(c.Str("query"), "?")
	if query == "" || !oauth.VerifyQuery(query, oauth.SecretFor(config.Load().SessionSecret), c.Now()) {
		return jsonx.ObjectOf("ok", false, "problem", "This approval link is invalid or has expired. Start connecting again from the app.", "signedIn", true), nil
	}
	params := oauth.ParseParams(query)
	clientID, _ := params.Get("client_id")
	var name, uri *string
	var disabled *bool
	err = c.DB().QueryRow(`SELECT name, uri, disabled FROM oauth_client WHERE client_id = ?`, clientID).Scan(&name, &uri, &disabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err != nil || disabled != nil && *disabled {
		return jsonx.ObjectOf("ok", false, "problem", "The app asking for access isn't registered here.", "signedIn", true), nil
	}
	if len(v.Workspaces) == 0 {
		return jsonx.ObjectOf("ok", false, "problem", "You aren't in a workspace yet. Ask an owner for an invite link.", "signedIn", true), nil
	}
	redirect, _ := params.Get("redirect_uri")
	redirectHost := hostOf(redirect)
	clientName := "An app"
	switch {
	case name != nil && *name != "":
		clientName = *name
	case redirectHost != "":
		clientName = redirectHost
	}
	scope, _ := params.Get("scope")
	scopes := []string{}
	for _, s := range strings.Split(scope, " ") {
		if s != "" {
			scopes = append(scopes, s)
		}
	}
	wss := make([]any, len(v.Workspaces))
	for i, w := range v.Workspaces {
		wss[i] = jsonx.ObjectOf("id", w.ID, "name", w.Name)
	}
	var active any
	if v.Workspace != nil {
		active = v.Workspace.ID
	}
	return jsonx.ObjectOf("ok", true, "client", jsonx.ObjectOf("name", clientName, "uri", uri, "redirectHost", redirectHost), "scopes", scopes,
		"username", v.User.Username, "workspaces", wss, "activeWorkspaceId", active), nil
}

// --- Gmail connect routes -------------------------------------------------------------

const (
	gmailStateCookie  = "recall.gmail_oauth"
	gmailStateTTLMs   = 10 * 60_000
	gmailStateContext = "gmail-oauth-state"
	gmailCookiePath   = "/app/connectors/google"
)

type gmailState struct {
	State     string  `json:"state"`
	Verifier  string  `json:"verifier"`
	UserID    string  `json:"userId"`
	Workspace string  `json:"workspaceId"`
	Container *string `json:"container"`
	Exp       int64   `json:"exp"`
}

func connectorsPage(outcome string, container *string) string {
	p := &oauth.Params{}
	if outcome != "" {
		p.Set("gmail", outcome)
	}
	if container != nil {
		p.Set("c", *container)
	}
	if s := p.String(); s != "" {
		return "/app/connectors?" + s
	}
	return "/app/connectors"
}

func gmailCookie(value string, maxAge int) string {
	secure := ""
	if strings.HasPrefix(config.Load().URL, "https:") {
		secure = "; Secure"
	}
	return gmailStateCookie + "=" + value + "; Path=" + gmailCookiePath + "; HttpOnly; SameSite=Lax; Max-Age=" + strconv.Itoa(maxAge) + secure
}

func redirectTo(w http.ResponseWriter, location, cookie string) {
	w.Header().Set("location", location)
	w.Header().Set("cache-control", "no-store")
	if cookie != "" {
		w.Header().Add("set-cookie", cookie)
	}
	w.WriteHeader(302)
}

// gmailStart is handleStart.
func (s *Server) gmailStart(w http.ResponseWriter, r *http.Request) {
	var container *string
	if raw, ok := oauth.ParseParams(r.URL.RawQuery).Get("c"); ok {
		if t := strx.Trim(raw); t != "" && strx.Len16(t) <= memories.MaxContainerLen {
			container = &t
		}
	}
	if !auth.SameOrigin(r.Header, r.Host, config.Load().URL) {
		redirectTo(w, connectorsPage("failed", container), "")
		return
	}
	v, err := viewer.FromRequest(s.DB, r, s.Now())
	if err != nil {
		log.Printf("[recall:connectors] start: %v", err)
		writeRaw(w, 500, []byte("Internal Server Error"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
		return
	}
	if v == nil {
		redirectTo(w, "/app/login?next="+url.QueryEscape("/connectors"), "")
		return
	}
	if v.Workspace == nil {
		redirectTo(w, "/app/", "")
		return
	}
	if !connectors.Configured() {
		redirectTo(w, connectorsPage("not_configured", container), "")
		return
	}
	verifier, challenge := connectors.PKCEPair()
	b := make([]byte, 24)
	rand.Read(b)
	st := gmailState{State: base64.RawURLEncoding.EncodeToString(b), Verifier: verifier, UserID: v.User.ID, Workspace: v.Workspace.ID, Container: container, Exp: s.Now() + gmailStateTTLMs}
	raw, _ := json.Marshal(st)
	sealed, err := connectors.Seal(connectors.Secret(), string(raw), gmailStateContext)
	if err != nil {
		log.Printf("[recall:connectors] start: %v", err)
		writeRaw(w, 500, []byte("Internal Server Error"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
		return
	}
	location := connectors.AuthorizationURL(config.Load().GoogleClientID, st.State, challenge)
	if config.Load().GmailFake {
		p := &oauth.Params{}
		p.Set("code", "fake-code")
		p.Set("state", st.State)
		p.Set("scope", connectors.GmailScope)
		location = connectors.RedirectURI() + "?" + p.String()
	}
	redirectTo(w, location, gmailCookie(sealed, gmailStateTTLMs/1000))
}

// gmailCallback is handleCallback.
func (s *Server) gmailCallback(w http.ResponseWriter, r *http.Request) {
	q := oauth.ParseParams(r.URL.RawQuery)
	var st *gmailState
	if c, err := r.Cookie(gmailStateCookie); err == nil {
		if plain, err := connectors.Unseal(connectors.Secret(), c.Value, gmailStateContext); err == nil {
			var p gmailState
			if json.Unmarshal([]byte(plain), &p) == nil {
				st = &p
			}
		}
	}
	back := func(outcome string) {
		var container *string
		if st != nil {
			container = st.Container
		}
		redirectTo(w, connectorsPage(outcome, container), gmailCookie("", 0))
	}
	if !connectors.Configured() {
		back("not_configured")
		return
	}
	state, _ := q.Get("state")
	if st == nil || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(st.State)) != 1 {
		back("failed")
		return
	}
	if st.Exp < s.Now() {
		back("expired")
		return
	}
	if e, _ := q.Get("error"); e != "" {
		back("denied")
		return
	}
	v, err := viewer.FromRequest(s.DB, r, s.Now())
	if err != nil || v == nil || v.User.ID != st.UserID {
		back("failed")
		return
	}
	if role, err := users.GetRole(s.DB, st.UserID, st.Workspace); err != nil || role == "" {
		back("failed")
		return
	}
	code, _ := q.Get("code")
	if code == "" {
		back("failed")
		return
	}
	back(connectors.CompleteConnect(context.WithoutCancel(r.Context()), s.DB, st.Workspace, st.UserID, code, st.Verifier, s.google(), s.Now()))
}

// google is the Gmail connector's HTTP client.
func (s *Server) google() connectors.Do {
	if s.Google != nil {
		return s.Google
	}
	return connectors.GoogleDo()
}
