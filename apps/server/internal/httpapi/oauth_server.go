package httpapi

import (
	"net/http"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/oauth"
)

// oauth is the authorization server, made on first use.
func (s *Server) oauth() *oauth.Server {
	s.oauthOnce.Do(func() {
		if s.OAuth == nil {
			cfg := config.Load()
			s.OAuth = &oauth.Server{DB: s.DB, Now: s.Now, URL: cfg.URL, Secret: oauth.SecretFor(cfg.SessionSecret)}
		}
	})
	return s.OAuth
}

// authServerMetadata is RFC 8414 metadata at the root, also in the
// path-inserted form for the issuer.
func (s *Server) authServerMetadata(w http.ResponseWriter, r *http.Request) {
	splat := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/.well-known/oauth-authorization-server"), "/")
	if splat != "" && splat != "api/auth" {
		writeRaw(w, 404, []byte("Not found"), map[string]string{"content-type": "text/plain;charset=UTF-8"})
		return
	}
	s.oauth().Seed()
	writeJSON(w, 200, s.oauth().Metadata(), map[string]string{"access-control-allow-origin": "*", "cache-control": oauth.MetadataCache})
}
