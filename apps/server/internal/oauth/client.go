package oauth

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

var errNoRows = sql.ErrNoRows

// clientRow is an oauth_client row as the adapter returns it: JSON array
// columns parsed, NULL as nil.
type clientRow struct {
	ID                       string
	ClientID                 string
	ClientSecret             *string
	ClientDiscoveryID        *string
	Disabled                 *bool
	SkipConsent              *bool
	EnableEndSession         *bool
	SubjectType              *string
	Scopes                   []string
	UserID                   *string
	CreatedAt                *int64
	Name, URI, Icon          *string
	Contacts                 []string
	Tos, Policy              *string
	SoftwareID               *string
	SoftwareVersion          *string
	SoftwareStatement        *string
	RedirectURIs             []string
	PostLogoutRedirectURIs   []string
	BackchannelLogoutURI     *string
	BackchannelSessionReq    *bool
	TokenEndpointAuthMethod  *string
	ApplicationType          *string
	JWKS                     *string
	JWKSURI                  *string
	GrantTypes               []string
	ResponseTypes            []string
	RequirePKCE              *bool
	DPoPBoundAccessTokens    *bool
	ReferenceID              *string
	Metadata                 *string
	hasScopes, hasGrantTypes bool
}

const clientCols = `id, client_id, client_secret, client_discovery_id, disabled, skip_consent, enable_end_session, subject_type, scopes, user_id, created_at, name, uri, icon, contacts, tos, policy, software_id, software_version, software_statement, redirect_uris, post_logout_redirect_uris, backchannel_logout_uri, backchannel_logout_session_required, token_endpoint_auth_method, application_type, jwks, jwks_uri, grant_types, response_types, require_pkce, dpop_bound_access_tokens, reference_id, metadata`

func parseJSONList(raw *string) ([]string, bool) {
	if raw == nil {
		return nil, false
	}
	var out []string
	if json.Unmarshal([]byte(*raw), &out) != nil {
		return nil, false
	}
	if out == nil {
		out = []string{}
	}
	return out, true
}

func scanClient(row interface{ Scan(...any) error }) (*clientRow, error) {
	var c clientRow
	var scopes, contacts, redirects, postLogout, grants, responses *string
	err := row.Scan(&c.ID, &c.ClientID, &c.ClientSecret, &c.ClientDiscoveryID, &c.Disabled, &c.SkipConsent, &c.EnableEndSession, &c.SubjectType, &scopes, &c.UserID, &c.CreatedAt,
		&c.Name, &c.URI, &c.Icon, &contacts, &c.Tos, &c.Policy, &c.SoftwareID, &c.SoftwareVersion, &c.SoftwareStatement, &redirects, &postLogout, &c.BackchannelLogoutURI, &c.BackchannelSessionReq,
		&c.TokenEndpointAuthMethod, &c.ApplicationType, &c.JWKS, &c.JWKSURI, &grants, &responses, &c.RequirePKCE, &c.DPoPBoundAccessTokens, &c.ReferenceID, &c.Metadata)
	if err != nil {
		return nil, err
	}
	c.Scopes, c.hasScopes = parseJSONList(scopes)
	c.Contacts, _ = parseJSONList(contacts)
	c.RedirectURIs, _ = parseJSONList(redirects)
	c.PostLogoutRedirectURIs, _ = parseJSONList(postLogout)
	c.GrantTypes, c.hasGrantTypes = parseJSONList(grants)
	c.ResponseTypes, _ = parseJSONList(responses)
	return &c, nil
}

// getClient is getClient without discoveries: a row whose
// client_discovery_id is set is unknown here.
func (s *Server) getClient(clientID string) (*clientRow, error) {
	c, err := scanClient(s.DB.QueryRow(`SELECT `+clientCols+` FROM oauth_client WHERE client_id = ?`, clientID))
	if err == errNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.ClientDiscoveryID != nil {
		return nil, nil
	}
	return c, nil
}

func (c *clientRow) disabled() bool { return c.Disabled != nil && *c.Disabled }

func (c *clientRow) authMethod() string {
	if c.TokenEndpointAuthMethod == nil {
		return ""
	}
	return *c.TokenEndpointAuthMethod
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// allowsGrant is clientAllowsGrant.
func (c *clientRow) allowsGrant(grant string) bool {
	allowed := c.GrantTypes
	if len(allowed) == 0 {
		allowed = []string{"authorization_code"}
	}
	if grant == "refresh_token" && contains(allowed, "authorization_code") {
		return true
	}
	return contains(allowed, grant)
}

// pkceRequired is isPKCERequired: the reason, or "".
func (c *clientRow) pkceRequired(scopes []string, nonce string) string {
	if c.authMethod() == "none" {
		return "pkce is required for public clients"
	}
	if contains(scopes, "offline_access") && !(contains(scopes, "openid") && nonce != "") {
		return "pkce or OIDC nonce is required when requesting offline_access scope"
	}
	if c.RequirePKCE == nil || *c.RequirePKCE {
		return "pkce is required for this client"
	}
	return ""
}

func optStr(o *jsonx.Object, k string, v *string) {
	if v != nil {
		o.Set(k, *v)
	}
}

func optBool(o *jsonx.Object, k string, v *bool) {
	if v != nil {
		o.Set(k, *v)
	}
}

func strList(l []string) []any {
	out := make([]any, len(l))
	for i, v := range l {
		out[i] = v
	}
	return out
}

// schemaToOAuth is schemaToOAuth: the RFC 7591 view of a client row.
// secret replaces the stored (encrypted) one.
func (c *clientRow) schemaToOAuth(secret *string) *jsonx.Object {
	o := jsonx.NewMap[any]()
	if c.Metadata != nil && *c.Metadata != "" {
		if m, err := jsonx.ParseObject([]byte(*c.Metadata)); err == nil {
			for _, k := range m.Keys() {
				if !reservedMetadataKey(k) {
					v, _ := m.Get(k)
					o.Set(k, v)
				}
			}
		}
	}
	o.Set("client_id", c.ClientID)
	if secret != nil {
		o.Set("client_secret", *secret)
		o.Set("client_secret_expires_at", 0)
	} else {
		o.Delete("client_secret")
		o.Delete("client_secret_expires_at")
	}
	if c.hasScopes {
		o.Set("scope", strings.Join(c.Scopes, " "))
	} else {
		o.Delete("scope")
	}
	setOrDelete := func(k string, v any, ok bool) {
		if ok {
			o.Set(k, v)
		} else {
			o.Delete(k)
		}
	}
	setOrDelete("user_id", deref(c.UserID), c.UserID != nil)
	if c.CreatedAt != nil {
		o.Set("client_id_issued_at", roundSeconds(*c.CreatedAt))
	} else {
		o.Delete("client_id_issued_at")
	}
	setOrDelete("client_name", deref(c.Name), c.Name != nil)
	setOrDelete("client_uri", deref(c.URI), c.URI != nil)
	setOrDelete("logo_uri", deref(c.Icon), c.Icon != nil)
	setOrDelete("contacts", strList(c.Contacts), c.Contacts != nil)
	setOrDelete("tos_uri", deref(c.Tos), c.Tos != nil)
	setOrDelete("policy_uri", deref(c.Policy), c.Policy != nil)
	if c.JWKS != nil && *c.JWKS != "" {
		v, _ := jsonx.Parse([]byte(*c.JWKS))
		o.Set("jwks", v)
	} else {
		o.Delete("jwks")
	}
	setOrDelete("jwks_uri", deref(c.JWKSURI), c.JWKSURI != nil)
	setOrDelete("software_id", deref(c.SoftwareID), c.SoftwareID != nil)
	setOrDelete("software_version", deref(c.SoftwareVersion), c.SoftwareVersion != nil)
	setOrDelete("software_statement", deref(c.SoftwareStatement), c.SoftwareStatement != nil)
	ru := c.RedirectURIs
	if ru == nil {
		ru = []string{}
	}
	o.Set("redirect_uris", strList(ru))
	setOrDelete("post_logout_redirect_uris", strList(c.PostLogoutRedirectURIs), c.PostLogoutRedirectURIs != nil)
	setOrDelete("backchannel_logout_uri", deref(c.BackchannelLogoutURI), c.BackchannelLogoutURI != nil)
	setOrDelete("backchannel_logout_session_required", derefB(c.BackchannelSessionReq), c.BackchannelSessionReq != nil)
	setOrDelete("token_endpoint_auth_method", deref(c.TokenEndpointAuthMethod), c.TokenEndpointAuthMethod != nil)
	setOrDelete("grant_types", strList(c.GrantTypes), c.GrantTypes != nil)
	setOrDelete("response_types", strList(c.ResponseTypes), c.ResponseTypes != nil)
	setOrDelete("application_type", deref(c.ApplicationType), c.ApplicationType != nil)
	setOrDelete("disabled", derefB(c.Disabled), c.Disabled != nil)
	setOrDelete("skip_consent", derefB(c.SkipConsent), c.SkipConsent != nil)
	setOrDelete("enable_end_session", derefB(c.EnableEndSession), c.EnableEndSession != nil)
	setOrDelete("require_pkce", derefB(c.RequirePKCE), c.RequirePKCE != nil)
	setOrDelete("dpop_bound_access_tokens", derefB(c.DPoPBoundAccessTokens), c.DPoPBoundAccessTokens != nil)
	setOrDelete("subject_type", deref(c.SubjectType), c.SubjectType != nil)
	setOrDelete("reference_id", deref(c.ReferenceID), c.ReferenceID != nil)
	return o
}

// reservedMetadataKey is stripReservedOAuthClientMetadataExtensions: the
// OAuth fields a stored metadata blob can't override.
func reservedMetadataKey(k string) bool {
	switch k {
	case "client_id", "client_secret", "client_secret_expires_at", "scope", "user_id", "client_id_issued_at", "client_name", "client_uri", "logo_uri", "contacts", "tos_uri", "policy_uri", "jwks", "jwks_uri", "software_id", "software_version", "software_statement", "redirect_uris", "post_logout_redirect_uris", "backchannel_logout_uri", "backchannel_logout_session_required", "token_endpoint_auth_method", "grant_types", "response_types", "application_type", "disabled", "skip_consent", "enable_end_session", "require_pkce", "dpop_bound_access_tokens", "subject_type", "reference_id":
		return true
	}
	return false
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefB(p *bool) bool { return p != nil && *p }

// roundSeconds is Math.round(ms / 1000).
func roundSeconds(ms int64) int64 {
	q := ms / 1000
	r := ms % 1000
	if r < 0 {
		r += 1000
		q--
	}
	if r >= 500 {
		q++
	}
	return q
}
