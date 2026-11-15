package httpapi

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/health"
	"github.com/zakiego/recall/apps/server/internal/mcp"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
)

// Server holds what the handlers need. Now is the clock in ms (tests fix
// it); AskLimiter is the per-key ask limit (nil is the process-wide one).
type Server struct {
	DB         *db.DB
	Now        func() int64
	AskLimiter *ratelimit.Limiter
	// MCP answers /mcp for a caller that passed the API key or OAuth check.
	MCP func(w http.ResponseWriter, r *http.Request, caller *oauth.Caller)
	// RP runs passkey ceremonies (auth.NewRP; nil when passkeys are off).
	RP *auth.RP
	// Limiter is the /api/auth rate limiter (Better Auth's rateLimit).
	Limiter *auth.Limiter
	// Web serves the dashboard under /app (web.Handler); nil answers 404 there.
	Web http.Handler
	// Google answers the Gmail connector's calls (connectors.GoogleDo() when nil).
	Google connectors.Do
	// OAuth is the authorization server (made from the config when nil).
	OAuth     *oauth.Server
	oauthOnce sync.Once
}

// Handler is the HTTP handler for every route the server answers.
func (s *Server) Handler() http.Handler {
	if s.Now == nil {
		s.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if s.MCP == nil {
		s.MCP = func(w http.ResponseWriter, r *http.Request, c *oauth.Caller) {
			(&mcp.Server{DB: s.DB, WS: c.WorkspaceID, KeyID: c.KeyID, AskLimiter: s.AskLimiter, Now: s.Now}).Serve(w, r)
		}
	}
	if s.Limiter == nil {
		s.Limiter = auth.NewLimiter()
	}
	if s.RP == nil {
		cfg := config.Load()
		if rp, err := auth.NewRP(cfg.URL, cfg.PasskeyRpID); err == nil {
			s.RP = rp
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/{splat...}", s.authServerMetadata)
	mux.HandleFunc("GET /oauth/sign-in", s.oauthHandoff(oauth.LoginPage))
	mux.HandleFunc("GET /oauth/approve", s.oauthHandoff(oauth.ConsentPage))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status, body, err := health.Check(s.DB)
		if err != nil {
			writeRaw(w, 500, []byte("Internal Server Error"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
			return
		}
		writeRaw(w, status, body, map[string]string{"content-type": "application/json"})
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{splat...}", s.protectedResource)
	mcp := func(w http.ResponseWriter, r *http.Request) {
		caller, err := oauth.MCPCaller(s.DB, bearer(r), s.Now())
		if err != nil {
			// The TS route throws, and the server answers 500.
			log.Printf("[recall] mcp caller: %v", err)
			writeRaw(w, 500, []byte("Internal Server Error"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
			return
		}
		if caller == nil {
			body, headers := oauth.Unauthorized("")
			headers["content-type"] = "application/json"
			writeRaw(w, 401, body, headers)
			return
		}
		s.MCP(w, r, caller)
	}
	mux.HandleFunc("GET /mcp", mcp)
	mux.HandleFunc("POST /mcp", mcp)
	mux.HandleFunc("DELETE /mcp", mcp)
	s.routesV1(mux)
	mux.HandleFunc("/api/rpc/{name}", s.rpc)
	mux.HandleFunc("GET /connectors/google/start", s.gmailStart)
	mux.HandleFunc("GET /connectors/google/callback", s.gmailCallback)
	if s.Web != nil {
		mux.Handle("/app/", s.Web)
		mux.Handle("/app", s.Web)
	}
	// Dashboard pages that lived at the root before /app (legacyDashboardPath).
	mux.HandleFunc("/", legacyRedirect)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := apiPath(r.URL.Path); ok && p != r.URL.Path {
			r.URL.Path, r.URL.RawPath = p, ""
		}
		// Better Auth reads its own paths (ServeMux would redirect "//").
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			s.authRoute(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// apiPath is how TanStack's router reads a path to an API route: the /app
// basepath stripped when present, trailing slashes trimmed, and static
// segments matched without case (an id or splat keeps its case).
func apiPath(p string) (string, bool) {
	if p == "/app" || strings.HasPrefix(p, "/app/") {
		p = p[len("/app"):]
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	head := strings.ToLower(segs[0])
	switch head {
	case "api":
		if len(segs) > 1 {
			segs[1] = strings.ToLower(segs[1])
		}
	case "oauth":
		// The hand-offs only; /app/oauth/consent is a dashboard page.
		if len(segs) != 2 {
			return "", false
		}
		switch segs[1] = strings.ToLower(segs[1]); segs[1] {
		case "sign-in", "approve":
		default:
			return "", false
		}
	case "connectors":
		// The Gmail connect routes only; /app/connectors is a dashboard page.
		if len(segs) != 3 || strings.ToLower(segs[1]) != "google" {
			return "", false
		}
		for i := range segs {
			segs[i] = strings.ToLower(segs[i])
		}
		if segs[2] != "start" && segs[2] != "callback" {
			return "", false
		}
	case "health", "mcp":
		if len(segs) != 1 {
			return "", false
		}
	case ".well-known":
		if len(segs) > 1 {
			segs[1] = strings.ToLower(segs[1])
		}
	case "v1":
		for i := 1; i < len(segs); i++ {
			if i == 2 {
				switch strings.ToLower(segs[1]) {
				case "documents", "memories", "people":
					continue
				}
			}
			segs[i] = strings.ToLower(segs[i])
		}
	default:
		return "", false
	}
	segs[0] = head
	return "/" + strings.Join(segs, "/"), true
}

// protectedResource is the RFC 9728 metadata for /mcp, at the root and in
// the path-inserted form.
func (s *Server) protectedResource(w http.ResponseWriter, r *http.Request) {
	splat := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"), "/")
	if splat != "" && splat != "mcp" {
		writeRaw(w, 404, []byte("Not found"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
		return
	}
	writeJSON(w, 200, oauth.ProtectedResourceMetadata(config.Load().URL), map[string]string{"cache-control": "public, max-age=300", "access-control-allow-origin": "*"})
}

// legacyPages are the dashboard pages that used to live at the root.
var legacyPages = []string{"/login", "/search", "/ask", "/graph", "/add", "/keys", "/settings", "/invite",
	// Pages the TS router also rendered at the root (its basepath only
	// stripped /app when present); they go to /app as well.
	"/people", "/upcoming", "/connect", "/connectors", "/agent-instructions", "/oauth/consent"}

// legacyRedirect is start.ts's legacyRedirect: a GET or HEAD of "/" or an old
// dashboard path answers 302 to the same page under /app, query kept (a
// temporary redirect on purpose, see src/lib/app-path.ts). Anything else is
// a 404.
func legacyRedirect(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	legacy := p == "/"
	for _, page := range legacyPages {
		legacy = legacy || p == page || strings.HasPrefix(p, page+"/")
	}
	if !legacy || r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	to := "/app" + r.URL.EscapedPath()
	if p == "/" {
		to = "/app/"
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		to += "?" + r.URL.RawQuery
	}
	w.Header().Set("location", to)
	w.WriteHeader(http.StatusFound)
}
