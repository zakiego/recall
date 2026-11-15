package oauth

// POST /oauth2/revoke, POST /oauth2/introspect, /oauth2/userinfo and the
// RFC 8414 metadata.

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

var authSchemeRe = regexp.MustCompile("^([A-Za-z][A-Za-z0-9!#$%&'*+.^_`|~-]*)\\s+([\\s\\S]+)$")

// parseTokenAuthorization is parseAccessTokenAuthorization: the scheme
// (Bearer, DPoP or Unknown) and the token; ok false for a blank value.
func parseTokenAuthorization(v string) (scheme, token string, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", "", false
	}
	m := authSchemeRe.FindStringSubmatch(v)
	if m == nil {
		return "Unknown", v, true
	}
	switch strings.ToLower(m[1]) {
	case "bearer":
		return "Bearer", strings.TrimSpace(m[2]), true
	case "dpop":
		return "DPoP", strings.TrimSpace(m[2]), true
	}
	return "Unknown", v, true
}

// stripScheme is stripAccessTokenAuthorizationScheme.
func stripScheme(tok string) string {
	if _, t, ok := parseTokenAuthorization(tok); ok {
		return t
	}
	return tok
}

func tokenField(c *call) (string, string, *apiErr) {
	v := get(c.body, "token")
	tok, ok := v.(string)
	if !ok {
		is := at(typeIssue("string", v), "token")
		code, desc := mapIssue(is, nil, "invalid_request")
		return "", "", errDesc(400, code, desc)
	}
	hint, _ := get(c.body, "token_type_hint").(string)
	if hint != "access_token" && hint != "refresh_token" {
		hint = ""
	}
	return stripScheme(tok), hint, nil
}

type accessRow struct {
	id, clientID         string
	userID, sessionID    *string
	resources            []string
	scopes               []string
	expiresAt, createdAt int64
	revoked              *int64
}

func (s *Server) findAccess(hash string) (*accessRow, error) {
	var a accessRow
	var res *string
	var scopes string
	var exp, created *int64
	err := s.DB.QueryRow(`SELECT id, client_id, user_id, session_id, resources, scopes, expires_at, created_at, revoked FROM oauth_access_token WHERE token = ?`, hash).
		Scan(&a.id, &a.clientID, &a.userID, &a.sessionID, &res, &scopes, &exp, &created, &a.revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.resources, _ = parseJSONList(res)
	a.scopes, _ = parseJSONList(&scopes)
	if exp != nil {
		a.expiresAt = *exp
	}
	if created != nil {
		a.createdAt = *created
	}
	return &a, nil
}

// errNullBody is a handler returning null: 200 with body null.
var nullReply = &reply{body: nil}

func (s *Server) revoke(c *call) (*reply, error) {
	if c.body == nil {
		c.body = jsonx.NewMap[any]()
	}
	tok, hint, e := tokenField(c)
	if e != nil {
		return nil, &validationFailure{e}
	}
	cr, e := s.clientCredentials(c)
	if e != nil {
		return nil, e
	}
	if cr == nil || cr.clientID == "" {
		return nil, descErr(401, "missing required credentials", "invalid_client")
	}
	if tok == "" {
		return nil, descErr(400, "missing a required token for introspection", "invalid_request")
	}
	cl, err := s.validateClient(cr, nil, "")
	if err != nil {
		return nil, err
	}
	if hint == "" || hint == "access_token" {
		rep, err := s.revokeAccess(cl.ClientID, tok)
		if err == nil {
			return rep, nil
		}
		var ae *apiErr
		if !errors.As(err, &ae) || hint == "access_token" {
			return nil, err
		}
	}
	if hint == "" || hint == "refresh_token" {
		rep, err := s.revokeRefresh(cl.ClientID, tok)
		if err == nil {
			return rep, nil
		}
		var ae *apiErr
		if !errors.As(err, &ae) || hint == "refresh_token" {
			return nil, err
		}
	}
	return nil, descErr(400, "token not found", "invalid_request")
}

func (s *Server) revokeAccess(clientID, tok string) (*reply, error) {
	if !strings.HasPrefix(tok, AccessTokenPrefix) {
		return nil, descErr(400, "Invalid access token", "invalid_request")
	}
	a, err := s.findAccess(HashToken(strings.Replace(tok, AccessTokenPrefix, "", 1)))
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, descErr(400, "Invalid access token", "invalid_request")
	}
	if a.clientID != clientID {
		return nullReply, nil
	}
	if _, err := s.DB.Exec(`DELETE FROM oauth_access_token WHERE id = ?`, a.id); err != nil {
		return nil, err
	}
	return &reply{empty: true}, nil
}

func (s *Server) revokeRefresh(clientID, tok string) (*reply, error) {
	if !strings.HasPrefix(tok, RefreshTokenPrefix) {
		return nil, descErr(400, "refresh token not found", "invalid_token")
	}
	rt, err := s.findRefresh(HashToken(strings.Replace(tok, RefreshTokenPrefix, "", 1)))
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, descErr(400, "token not found", "invalid_request")
	}
	if rt.revoked != nil {
		if err := s.invalidateFamily(clientID, rt.userID); err != nil {
			return nil, err
		}
		return nil, descErr(400, "refresh token revoked", "invalid_request")
	}
	if rt.clientID != clientID {
		return nullReply, nil
	}
	iat := s.Now() / 1000
	res, err := s.DB.Exec(`UPDATE oauth_refresh_token SET revoked = ? WHERE id = ? AND revoked IS NULL`, iat*1000, rt.id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if err := s.invalidateFamily(clientID, rt.userID); err != nil {
			return nil, err
		}
		return nil, descErr(400, "refresh token revoked", "invalid_request")
	}
	if _, err := s.DB.Exec(`DELETE FROM oauth_access_token WHERE refresh_id = ?`, rt.id); err != nil {
		return nil, err
	}
	return &reply{empty: true}, nil
}

func (s *Server) introspect(c *call) (*reply, error) {
	if c.body == nil {
		c.body = jsonx.NewMap[any]()
	}
	tok, hint, e := tokenField(c)
	if e != nil {
		return nil, &validationFailure{e}
	}
	cr, e := s.clientCredentials(c)
	if e != nil {
		return nil, e
	}
	if cr == nil || cr.clientID == "" || cr.secret == "" {
		return nil, descErr(401, "missing required credentials", "invalid_client")
	}
	if tok == "" {
		return nil, descErr(400, "missing a required token for introspection", "invalid_request")
	}
	cl, err := s.validateClient(cr, nil, "")
	if err != nil {
		return nil, err
	}
	inactive := &reply{body: jsonx.ObjectOf("active", false)}
	if (hint == "" || hint == "access_token") && strings.HasPrefix(tok, AccessTokenPrefix) {
		a, err := s.findAccess(HashToken(strings.Replace(tok, AccessTokenPrefix, "", 1)))
		if err != nil {
			return nil, err
		}
		if a != nil {
			if out, err := s.introspectAccess(cl.ClientID, a); err != nil || out != nil {
				if out == nil {
					return inactive, err
				}
				return &reply{body: out}, nil
			}
			return inactive, nil
		}
	}
	if (hint == "" || hint == "refresh_token") && strings.HasPrefix(tok, RefreshTokenPrefix) {
		rt, err := s.findRefresh(HashToken(strings.Replace(tok, RefreshTokenPrefix, "", 1)))
		if err != nil {
			return nil, err
		}
		if rt != nil && rt.clientID == cl.ClientID && rt.expiresAt >= s.Now() && rt.revoked == nil {
			out := jsonx.ObjectOf("active", true, "client_id", rt.clientID, "iss", s.baseURL(), "sub", rt.userID)
			if rt.sessionID != nil && s.sessionAlive(*rt.sessionID) {
				out.Set("sid", *rt.sessionID)
			}
			out.Set("exp", rt.expiresAt/1000)
			var created int64
			s.DB.QueryRow(`SELECT created_at FROM oauth_refresh_token WHERE id = ?`, rt.id).Scan(&created)
			out.Set("iat", created/1000)
			out.Set("scope", strings.Join(rt.scopes, " "))
			out.Set("token_type", "Bearer")
			return &reply{body: out}, nil
		}
	}
	return inactive, nil
}

func (s *Server) sessionAlive(id string) bool {
	var exp int64
	err := s.DB.QueryRow(`SELECT expires_at FROM session WHERE id = ?`, id).Scan(&exp)
	return err == nil && exp >= s.Now()
}

// introspectAccess is the active view of an access token, nil when it is
// inactive or the caller may not see it.
func (s *Server) introspectAccess(caller string, a *accessRow) (*jsonx.Object, error) {
	if a.expiresAt < s.Now() || a.revoked != nil {
		return nil, nil
	}
	cl, err := s.getClient(a.clientID)
	if err != nil || cl == nil || cl.disabled() {
		return nil, err
	}
	if a.sessionID != nil && !s.sessionAlive(*a.sessionID) {
		return nil, nil
	}
	if caller != a.clientID {
		linked := false
		for _, r := range a.resources {
			var n int
			s.DB.QueryRow(`SELECT count(*) FROM oauth_client_resource WHERE client_id = ? AND resource_id = ?`, caller, r).Scan(&n)
			linked = linked || n > 0
		}
		if !linked {
			return nil, nil
		}
	}
	for _, r := range a.resources {
		row, err := s.getResource(r)
		if err != nil || row == nil {
			return nil, err
		}
	}
	out := jsonx.ObjectOf("active", true, "iss", s.baseURL())
	switch len(a.resources) {
	case 0:
	case 1:
		out.Set("aud", a.resources[0])
	default:
		out.Set("aud", strList(a.resources))
	}
	out.Set("client_id", a.clientID)
	out.Set("azp", a.clientID)
	if a.userID != nil {
		out.Set("sub", *a.userID)
	}
	if a.sessionID != nil {
		out.Set("sid", *a.sessionID)
	}
	out.Set("exp", a.expiresAt/1000)
	out.Set("iat", a.createdAt/1000)
	out.Set("scope", strings.Join(a.scopes, " "))
	out.Set("token_type", "Bearer")
	return out, nil
}

func (s *Server) userinfo(c *call) (*reply, error) {
	_, tok, fromHeader := parseTokenAuthorization(c.r.Header.Get("authorization"))
	bodyTok := ""
	if c.r.Method == http.MethodPost {
		bodyTok, _ = get(c.body, "access_token").(string)
	}
	if fromHeader && bodyTok != "" {
		return nil, descErr(400, "Multiple access token transport methods are not allowed", "invalid_request")
	}
	if !fromHeader {
		tok = bodyTok
	}
	if tok == "" {
		return nil, descErr(401, "access token not found", "invalid_request")
	}
	bad := descErr(401, "Invalid access token", "invalid_token")
	bad.headers = map[string]string{"WWW-Authenticate": `Bearer error="invalid_token", error_description="Invalid access token"`}
	if !strings.HasPrefix(tok, AccessTokenPrefix) {
		return nil, bad
	}
	a, err := s.findAccess(HashToken(strings.Replace(tok, AccessTokenPrefix, "", 1)))
	if err != nil {
		return nil, err
	}
	if a == nil || a.expiresAt < s.Now() || a.revoked != nil {
		return nil, bad
	}
	if cl, err := s.getClient(a.clientID); err != nil || cl == nil || cl.disabled() {
		return nil, bad
	}
	if a.sessionID != nil && !s.sessionAlive(*a.sessionID) {
		return nil, bad
	}
	return nil, descErr(400, "Missing required scope", "invalid_scope")
}

const metadataCache = "public, max-age=15, stale-while-revalidate=15, stale-if-error=86400"

var signingAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// Metadata is oauthAuthorizationServerMetadata for Recall's options.
func (s *Server) Metadata() *jsonx.Object {
	base := s.baseURL()
	algs := strList(signingAlgs)
	authMethods := strList([]string{"client_secret_basic", "client_secret_post", "private_key_jwt"})
	return jsonx.ObjectOf(
		"scopes_supported", strList(Scopes),
		"issuer", s.issuer(),
		"authorization_endpoint", base+"/oauth2/authorize",
		"token_endpoint", base+"/oauth2/token",
		"registration_endpoint", base+"/oauth2/register",
		"introspection_endpoint", base+"/oauth2/introspect",
		"revocation_endpoint", base+"/oauth2/revoke",
		"response_types_supported", strList([]string{"code"}),
		"response_modes_supported", strList([]string{"query"}),
		"grant_types_supported", strList(GrantTypes),
		"token_endpoint_auth_methods_supported", strList(supportedAuthMethods),
		"token_endpoint_auth_signing_alg_values_supported", algs,
		"introspection_endpoint_auth_methods_supported", authMethods,
		"introspection_endpoint_auth_signing_alg_values_supported", algs,
		"revocation_endpoint_auth_methods_supported", authMethods,
		"revocation_endpoint_auth_signing_alg_values_supported", algs,
		"code_challenge_methods_supported", strList([]string{"S256"}),
		"authorization_response_iss_parameter_supported", true,
		"dpop_signing_alg_values_supported", strList([]string{"EdDSA", "ES256", "ES512", "PS256", "RS256"}),
		"backchannel_logout_supported", false,
		"backchannel_logout_session_supported", false,
	)
}

// MetadataCache is the Cache-Control of the metadata documents.
const MetadataCache = metadataCache
