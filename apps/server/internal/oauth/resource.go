// Package oauth is oauth.ts. This file is its resource-server half (M5):
// the discovery document for /mcp, the 401 challenge, and turning an access
// token into a workspace. The authorization server comes with M6.
package oauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// Token prefixes, so tokens are easy to tell apart from API keys (rcl_).
const (
	AccessTokenPrefix  = "rco_"
	RefreshTokenPrefix = "rcr_"
	// AuthBasePath is Better Auth's base path (AUTH_BASE_PATH).
	AuthBasePath = "/api/auth"
)

// Scopes is OAUTH_SCOPES: nothing is gated by scope yet.
var Scopes = []string{"mcp", "offline_access", "claudeai"}

// MCPResource is mcpResource.
func MCPResource(url string) string { return url + "/mcp" }

// Issuer is oauthIssuer.
func Issuer(url string) string { return url + AuthBasePath }

// ProtectedResourceMetadataURL is protectedResourceMetadataUrl.
func ProtectedResourceMetadataURL(url string) string {
	return url + "/.well-known/oauth-protected-resource/mcp"
}

// ProtectedResourceMetadata is protectedResourceMetadata (RFC 9728).
func ProtectedResourceMetadata(url string) *jsonx.Object {
	return jsonx.ObjectOf(
		"resource", MCPResource(url),
		"authorization_servers", []string{Issuer(url)},
		"scopes_supported", Scopes,
		"bearer_methods_supported", []string{"header"},
		"resource_name", "Recall",
		"resource_documentation", url+"/docs/mcp",
	)
}

// Unauthorized is mcpUnauthorized: the JSON-RPC error body and the headers
// of the 401 that points OAuth clients at the metadata.
func Unauthorized(message string) ([]byte, map[string]string) {
	if message == "" {
		message = "missing or invalid API key or access token"
	}
	body := jsonx.Stringify(jsonx.ObjectOf("jsonrpc", "2.0", "error", jsonx.ObjectOf("code", -32001, "message", message), "id", nil))
	return []byte(body), map[string]string{
		"www-authenticate":              `Bearer realm="recall", resource_metadata="` + ProtectedResourceMetadataURL(config.Load().URL) + `"`,
		"access-control-expose-headers": "www-authenticate",
	}
}

// storedTokenHash is the plugin's hash: SHA-256, base64url, no padding.
func storedTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func parseList(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return nil
	}
	parsed, err := jsonx.Parse([]byte(v.String))
	if err != nil {
		return nil
	}
	list, ok := parsed.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TokenAuth is OAuthTokenAuth.
type TokenAuth struct {
	TokenID     string
	ClientID    string
	UserID      string
	WorkspaceID string
}

// VerifyAccessToken is verifyOAuthAccessToken: nil unless the token is
// unexpired and unrevoked, its client enabled, it was issued for this
// resource, and its user is still a member of the workspace picked at
// consent. now is ms.
func VerifyAccessToken(q db.Q, token string, now int64) (*TokenAuth, error) {
	if !strings.HasPrefix(token, AccessTokenPrefix) {
		return nil, nil
	}
	var id, clientID string
	var userID, referenceID, resources sql.NullString
	var expiresAt, revoked sql.NullInt64
	var disabled sql.NullInt64
	err := q.QueryRow(`SELECT t.id, t.client_id, t.user_id, t.reference_id, t.resources, t.expires_at, t.revoked, c.disabled
		FROM oauth_access_token t INNER JOIN oauth_client c ON c.client_id = t.client_id WHERE t.token = ?`,
		storedTokenHash(token[len(AccessTokenPrefix):])).Scan(&id, &clientID, &userID, &referenceID, &resources, &expiresAt, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// drizzle's timestamp_ms: a revoked time of 0 is still a Date, and truthy.
	if revoked.Valid || (disabled.Valid && disabled.Int64 != 0) || !userID.Valid || userID.String == "" || !referenceID.Valid || referenceID.String == "" {
		return nil, nil
	}
	if !expiresAt.Valid || expiresAt.Int64 <= now {
		return nil, nil
	}
	if list := parseList(resources); len(list) > 0 {
		want := MCPResource(config.Load().URL)
		found := false
		for _, r := range list {
			found = found || r == want
		}
		if !found {
			return nil, nil
		}
	}
	var member string
	err = q.QueryRow(`SELECT id FROM member WHERE user_id = ? AND organization_id = ?`, userID.String, referenceID.String).Scan(&member)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &TokenAuth{TokenID: id, ClientID: clientID, UserID: userID.String, WorkspaceID: referenceID.String}, nil
}

// Caller is who is calling /mcp.
type Caller struct {
	WorkspaceID string
	KeyID       string
}

// MCPCaller is mcpCaller: an API key (rcl_) or an OAuth access token (rco_).
func MCPCaller(q db.Q, token string, now int64) (*Caller, error) {
	key, err := auth.VerifyAPIKey(q, token, now)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return &Caller{WorkspaceID: key.WorkspaceID, KeyID: key.ID}, nil
	}
	t, err := VerifyAccessToken(q, token, now)
	if err != nil || t == nil {
		return nil, err
	}
	return &Caller{WorkspaceID: t.WorkspaceID, KeyID: "oauth:" + t.ClientID + ":" + t.UserID}, nil
}
