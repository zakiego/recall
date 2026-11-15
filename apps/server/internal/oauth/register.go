package oauth

// POST /oauth2/register (RFC 7591), registerEndpoint with Recall's options:
// dynamic and unauthenticated registration allowed, scopes always the
// server's, resources defaulted to /mcp, client secrets encrypted.

import (
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

var registerErrorCodes = map[string]any{
	"redirect_uris":             "invalid_redirect_uri",
	"post_logout_redirect_uris": "invalid_redirect_uri",
	"software_statement":        "invalid_software_statement",
	"resources":                 "invalid_target",
}

// regBody is the parsed registration request (unknown keys dropped).
type regBody struct {
	redirectURIs, postLogout     []string
	hasRedirects, hasPostLogout  bool
	scope, clientName            *string
	clientURI, logoURI           *string
	contacts                     []string
	tosURI, policyURI            *string
	softwareID, softwareVersion  *string
	softwareStatement            *string
	backchannelURI               *string
	backchannelSessionRequired   *bool
	authMethod                   *string
	jwks                         any
	jwksURI                      *string
	grantTypes                   []string
	hasGrantTypes                bool
	responseTypes                []string
	hasResponseTypes             bool
	applicationType, subjectType *string
	dpopBound                    *bool
	resources                    []string
}

func strField(o *jsonx.Object, k string, dst **string) *issue {
	v := get(o, k)
	if v == undefined {
		return nil
	}
	s, is := optString(v)
	if is != nil {
		return at(is, k)
	}
	*dst = &s
	return nil
}

func boolField(o *jsonx.Object, k string, dst **bool) *issue {
	v := get(o, k)
	if v == undefined {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		return at(typeIssue("boolean", v), k)
	}
	*dst = &b
	return nil
}

func urlList(o *jsonx.Object, k string) ([]string, bool, *issue) {
	v := get(o, k)
	if v == undefined {
		return nil, false, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, at(typeIssue("array", v), k)
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, is := safeURL(e)
		if is != nil {
			return nil, false, at(at(is, i), k)
		}
		out[i] = s
	}
	if len(arr) < 1 {
		return nil, false, at(tooSmallArray(1), k)
	}
	return out, true, nil
}

func enumField(o *jsonx.Object, k string, values []string, dst **string) *issue {
	v := get(o, k)
	if v == undefined {
		return nil
	}
	s, ok := v.(string)
	if !ok || !contains(values, s) {
		q := make([]string, len(values))
		for i, x := range values {
			q[i] = `"` + x + `"`
		}
		return at(&issue{code: "invalid_value", values: values, message: "Invalid option: expected one of " + strings.Join(q, "|")}, k)
	}
	*dst = &s
	return nil
}

// parseRegistration is clientRegistrationRequestSchema.
func parseRegistration(o *jsonx.Object) (*regBody, *issue) {
	b := &regBody{}
	var is *issue
	if b.redirectURIs, b.hasRedirects, is = urlList(o, "redirect_uris"); is != nil {
		return nil, is
	}
	if is = strField(o, "scope", &b.scope); is != nil {
		return nil, is
	}
	if is = strField(o, "client_name", &b.clientName); is != nil {
		return nil, is
	}
	if b.clientName != nil && strings.TrimSpace(*b.clientName) == "" {
		return nil, at(&issue{code: "custom", message: "client_name cannot be empty"}, "client_name")
	}
	for _, f := range []struct {
		k   string
		dst **string
	}{{"client_uri", &b.clientURI}, {"logo_uri", &b.logoURI}} {
		if is = strField(o, f.k, f.dst); is != nil {
			return nil, is
		}
	}
	if v := get(o, "contacts"); v != undefined {
		arr, ok := v.([]any)
		if !ok {
			return nil, at(typeIssue("array", v), "contacts")
		}
		for i, e := range arr {
			s, ok := e.(string)
			if !ok {
				return nil, at(at(typeIssue("string", e), i), "contacts")
			}
			if s == "" {
				return nil, at(at(tooSmallString(1), i), "contacts")
			}
			b.contacts = append(b.contacts, s)
		}
		if len(arr) < 1 {
			return nil, at(tooSmallArray(1), "contacts")
		}
	}
	for _, f := range []struct {
		k   string
		dst **string
	}{{"tos_uri", &b.tosURI}, {"policy_uri", &b.policyURI}, {"software_id", &b.softwareID}, {"software_version", &b.softwareVersion}, {"software_statement", &b.softwareStatement}} {
		if is = strField(o, f.k, f.dst); is != nil {
			return nil, is
		}
	}
	if b.postLogout, b.hasPostLogout, is = urlList(o, "post_logout_redirect_uris"); is != nil {
		return nil, is
	}
	if v := get(o, "backchannel_logout_uri"); v != undefined {
		s, is := safeURL(v)
		if is != nil {
			return nil, at(is, "backchannel_logout_uri")
		}
		b.backchannelURI = &s
	}
	if is = boolField(o, "backchannel_logout_session_required", &b.backchannelSessionRequired); is != nil {
		return nil, is
	}
	if v := get(o, "token_endpoint_auth_method"); v != undefined {
		s, ok := v.(string)
		if !ok {
			return nil, at(typeIssue("string", v), "token_endpoint_auth_method")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, at(tooSmallString(1), "token_endpoint_auth_method")
		}
		b.authMethod = &s
	}
	if v := get(o, "jwks"); v != undefined {
		jo, ok := v.(*jsonx.Object)
		if !ok {
			return nil, at(typeIssue("object", v), "jwks")
		}
		keys := get(jo, "keys")
		arr, ok := keys.([]any)
		if !ok {
			return nil, at(at(typeIssue("array", keys), "keys"), "jwks")
		}
		for i, e := range arr {
			if _, ok := e.(*jsonx.Object); !ok {
				return nil, at(at(at(typeIssue("record", e), i), "keys"), "jwks")
			}
		}
		if len(arr) < 1 {
			return nil, at(at(tooSmallArray(1), "keys"), "jwks")
		}
		b.jwks = v
	}
	if is = strField(o, "jwks_uri", &b.jwksURI); is != nil {
		return nil, is
	}
	if v := get(o, "grant_types"); v != undefined {
		arr, ok := v.([]any)
		if !ok {
			return nil, at(typeIssue("array", v), "grant_types")
		}
		for i, e := range arr {
			s, ok := e.(string)
			if !ok {
				return nil, at(at(typeIssue("string", e), i), "grant_types")
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, at(at(tooSmallString(1), i), "grant_types")
			}
			b.grantTypes = append(b.grantTypes, s)
		}
		if len(arr) < 1 {
			return nil, at(tooSmallArray(1), "grant_types")
		}
		b.hasGrantTypes = true
	}
	if v := get(o, "response_types"); v != undefined {
		arr, ok := v.([]any)
		if !ok {
			return nil, at(typeIssue("array", v), "response_types")
		}
		b.responseTypes = []string{}
		for i, e := range arr {
			if s, ok := e.(string); !ok || s != "code" {
				return nil, at(at(&issue{code: "invalid_value", values: []string{"code"}, message: `Invalid option: expected one of "code"`}, i), "response_types")
			}
			b.responseTypes = append(b.responseTypes, "code")
		}
		b.hasResponseTypes = true
	}
	if is = enumField(o, "application_type", []string{"web", "native"}, &b.applicationType); is != nil {
		return nil, is
	}
	if is = enumField(o, "subject_type", []string{"public", "pairwise"}, &b.subjectType); is != nil {
		return nil, is
	}
	if is = boolField(o, "dpop_bound_access_tokens", &b.dpopBound); is != nil {
		return nil, is
	}
	if v := get(o, "resources"); v != undefined {
		arr, ok := v.([]any)
		if !ok {
			return nil, at(typeIssue("array", v), "resources")
		}
		for i, e := range arr {
			if is := resourceURI(e); is != nil {
				return nil, at(at(is, i), "resources")
			}
			b.resources = append(b.resources, e.(string))
		}
	}
	if v := get(o, "skip_consent"); v != undefined {
		return nil, at(&issue{code: "invalid_type", expected: "never", message: "skip_consent cannot be set during dynamic client registration"}, "skip_consent")
	}
	return b, nil
}

var (
	reverseDomainScheme = regexp.MustCompile(`(?i)^[a-z](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	nonPrivateSchemes   = map[string]bool{"http": true, "https": true, "ws": true, "wss": true, "file": true, "ftp": true, "mailto": true, "javascript": true, "data": true, "vbscript": true, "blob": true, "about": true, "view-source": true}
	localhostDots       = regexp.MustCompile(`(?i)^localhost\.+$`)
	rawHTTPAuthority    = regexp.MustCompile(`(?i)^http://([^/?#]*)`)
)

func isNonHTTP(uri string) bool {
	u, err := parseJSURL(uri)
	return err == nil && u.scheme != "http" && u.scheme != "https"
}

func rawHTTPHostname(uri string) string {
	m := rawHTTPAuthority.FindStringSubmatch(uri)
	if m == nil || m[1] == "" {
		return ""
	}
	hp := m[1][strings.LastIndex(m[1], "@")+1:]
	if strings.HasPrefix(hp, "[") {
		end := strings.Index(hp, "]")
		if end < 0 {
			return ""
		}
		return strings.ToLower(hp[:end+1])
	}
	return strings.ToLower(strings.Split(hp, ":")[0])
}

func isExactLoopbackHTTP(uri string) bool {
	h := rawHTTPHostname(uri)
	return h == "localhost" || h == "127.0.0.1" || h == "[::1]"
}

func isNativePrivateUse(u *jsURL) bool {
	if u.special {
		return false
	}
	ssp := strings.TrimPrefix(u.raw[len(u.scheme)+1:], "")
	if u.host == "" && strings.HasPrefix(ssp, "/") && !strings.HasPrefix(ssp, "//") && reverseDomainScheme.MatchString(u.scheme) {
		return true
	}
	return !nonPrivateSchemes[u.scheme] && u.host != "" && strings.HasPrefix(u.pathname, "/")
}

// validateClientRedirectURI is validateClientRedirectUri.
func validateClientRedirectURI(uri, appType string) *apiErr {
	bad := func(d string) *apiErr { return errDesc(400, "invalid_redirect_uri", d) }
	u, err := parseJSURL(uri)
	if err != nil {
		return bad("redirect URI must be an absolute URI: " + uri)
	}
	if strings.Contains(uri, "#") || u.username != "" || u.password != "" {
		return bad("redirect URI must not include credentials or a fragment: " + uri)
	}
	isHTTP, isHTTPS := u.scheme == "http", u.scheme == "https"
	if localhostDots.MatchString(u.hostname) {
		return bad("redirect URI localhost must not include trailing dots: " + uri)
	}
	loopback := isLoopbackIP(u.hostname) || u.hostname == "localhost"
	if appType == "web" {
		if !isHTTPS || loopback {
			return bad("web clients require https redirect URIs on non-loopback hosts: " + uri)
		}
		return nil
	}
	if isHTTPS {
		if loopback {
			return bad("native clients must not use https loopback redirect URIs: " + uri)
		}
		return nil
	}
	if isHTTP {
		if !isExactLoopbackHTTP(uri) {
			return bad("native clients may use http only on the exact loopback hosts localhost, 127.0.0.1, or [::1]: " + uri)
		}
		return nil
	}
	if !isNativePrivateUse(u) {
		return bad("native private-use redirect URI schemes must not use a reserved scheme; they must be an authority-free reverse-domain URI or a custom-scheme URI with a host and a path: " + uri)
	}
	return nil
}

var supportedAuthMethods = []string{"none", "client_secret_basic", "client_secret_post", "private_key_jwt"}

func (s *Server) register(c *call) (*reply, error) {
	o := c.body
	if o == nil {
		o = jsonx.NewMap[any]()
	}
	b, is := parseRegistration(o)
	if is != nil {
		code, desc := mapIssue(is, registerErrorCodes, "invalid_client_metadata")
		return nil, &validationFailure{errDesc(400, code, desc)}
	}
	if c.session == nil {
		// authorizeInitialAccessToken: Recall has no validator for one.
		if h := c.r.Header.Get("authorization"); h != "" {
			parts := strings.Fields(h)
			if len(parts) > 0 && strings.EqualFold(parts[0], "bearer") {
				hdr := map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache"}
				if len(parts) != 2 {
					hdr["WWW-Authenticate"] = `Bearer error="invalid_request"`
					return nil, &apiErr{status: 400, body: jsonx.ObjectOf("error", "invalid_request", "error_description", "Malformed initial access token Authorization header"), headers: hdr}
				}
				hdr["WWW-Authenticate"] = `Bearer error="invalid_token"`
				return nil, &apiErr{status: 401, body: jsonx.ObjectOf("error", "invalid_token", "error_description", "Invalid initial access token"), headers: hdr}
			}
		}
	}
	// Grants the server doesn't issue are dropped.
	grants := b.grantTypes
	hasGrants := b.hasGrantTypes
	if len(b.grantTypes) > 0 {
		mutual := []string{}
		for _, g := range b.grantTypes {
			if contains(GrantTypes, g) {
				mutual = append(mutual, g)
			}
		}
		if len(mutual) == 0 {
			return nil, errDesc(400, "invalid_client_metadata", "no mutually supported grant_type among "+strings.Join(b.grantTypes, ", "))
		}
		grants = mutual
	}
	responseTypes, hasResponse := b.responseTypes, b.hasResponseTypes
	if contains(b.grantTypes, "authorization_code") && !contains(grants, "authorization_code") {
		responseTypes, hasResponse = nil, false
	}
	scope := strings.Join(Scopes, " ")
	if b.scope != nil && *b.scope != "" {
		scope = *b.scope
	}
	// applyOAuthClientRegistrationDefaults with application type "infer".
	authMethod := "client_secret_basic"
	if b.authMethod != nil {
		authMethod = *b.authMethod
	}
	if !hasGrants {
		grants, hasGrants = []string{"authorization_code"}, true
	}
	if len(grants) == 0 {
		return nil, errDesc(400, "invalid_client_metadata", "grant_types must contain at least one grant type")
	}
	if !hasResponse && contains(grants, "authorization_code") {
		responseTypes, hasResponse = []string{"code"}, true
	}
	appType := "web"
	if b.applicationType != nil {
		appType = *b.applicationType
	} else {
		for _, u := range b.redirectURIs {
			if isNonHTTP(u) || isExactLoopbackHTTP(u) {
				appType = "native"
			}
		}
	}
	// checkOAuthClient.
	if !contains(supportedAuthMethods, authMethod) {
		return nil, errDesc(400, "invalid_client_metadata", "unsupported token_endpoint_auth_method "+authMethod)
	}
	if contains(grants, "authorization_code") && len(b.redirectURIs) == 0 {
		return nil, errDesc(400, "invalid_redirect_uri", "Redirect URIs are required for authorization_code and implicit grant types")
	}
	for _, u := range b.redirectURIs {
		if e := validateClientRedirectURI(u, appType); e != nil {
			return nil, e
		}
	}
	for _, g := range grants {
		if !contains(GrantTypes, g) {
			return nil, errDesc(400, "invalid_client_metadata", "unsupported grant_type "+g)
		}
	}
	if contains(grants, "authorization_code") && !contains(responseTypes, "code") {
		return nil, errDesc(400, "invalid_client_metadata", "When 'authorization_code' grant type is used, 'code' response type must be included")
	}
	if !contains(grants, "authorization_code") && contains(responseTypes, "code") {
		return nil, errDesc(400, "invalid_client_metadata", "When 'code' response type is used, 'authorization_code' grant type must be included")
	}
	if b.subjectType != nil && *b.subjectType == "pairwise" {
		return nil, errDesc(400, "invalid_client_metadata", "pairwise subject_type requires server pairwiseSecret configuration")
	}
	for _, sc := range strings.Split(scope, " ") {
		if sc != "" && !contains(Scopes, sc) {
			return nil, errDesc(400, "invalid_scope", "cannot request scope "+sc)
		}
	}
	if b.jwks != nil || (b.jwksURI != nil && *b.jwksURI != "") {
		if e := s.checkJWKS(b); e != nil {
			return nil, e
		}
	}
	if authMethod == "private_key_jwt" && b.jwks == nil && (b.jwksURI == nil || *b.jwksURI == "") {
		return nil, errDesc(400, "invalid_client_metadata", "private_key_jwt requires either jwks or jwks_uri")
	}
	if b.backchannelURI != nil {
		return nil, errDesc(400, "invalid_client_metadata", "backchannel_logout_uri requires the jwt plugin (disableJwtPlugin must be false)")
	}
	// Credentials.
	clientID := randomString(32, lower+upper)
	var secret, stored *string
	if authMethod != "none" && authMethod != "private_key_jwt" {
		v := randomString(32, lower+upper)
		enc, err := encryptSecret(s.Secret, v)
		if err != nil {
			return nil, err
		}
		secret, stored = &v, &enc
	}
	// Resources: requested ones must be the default (/mcp).
	for _, r := range b.resources {
		if r != "" && r != s.resource() {
			return nil, errDesc(400, "invalid_target", "requested resource "+r+" is not allowed for client registration")
		}
	}
	resources := []string{s.resource()}
	for _, r := range b.resources {
		if r != "" && !contains(resources, r) {
			resources = append(resources, r)
		}
	}
	for _, r := range resources {
		row, err := s.getResource(r)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, errDesc(400, "invalid_target", "requested resource "+r+" does not exist")
		}
		if row.Disabled {
			return nil, errDesc(400, "invalid_target", "requested resource "+r+" is disabled")
		}
	}
	iat := s.Now() / 1000
	var userID any
	if c.session != nil {
		userID = c.session.User.ID
	}
	var row *clientRow
	err := s.DB.Tx(func(tx db.Q) error {
		id := newID()
		_, err := tx.Exec(`INSERT INTO oauth_client (id, client_id, client_secret, client_discovery_id, scopes, client_credentials_scopes, user_id, created_at, updated_at, name, uri, icon, contacts, tos, policy, software_id, software_version, software_statement, redirect_uris, post_logout_redirect_uris, backchannel_logout_session_required, token_endpoint_auth_method, application_type, jwks, jwks_uri, grant_types, response_types, subject_type, dpop_bound_access_tokens)
			VALUES (?, ?, ?, NULL, ?, '[]', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, clientID, nullStr(stored), jsonList(Scopes), userID, iat*1000, iat*1000, nullStr(b.clientName), nullStr(b.clientURI), nullStr(b.logoURI),
			nullList(b.contacts, b.contacts != nil), nullStr(b.tosURI), nullStr(b.policyURI), nullStr(b.softwareID), nullStr(b.softwareVersion), nullStr(b.softwareStatement),
			jsonList(b.redirectURIs), nullList(b.postLogout, b.hasPostLogout), nullBool(b.backchannelSessionRequired), authMethod, appType, nullJSON(b.jwks), nullStr(b.jwksURI),
			nullList(grants, hasGrants), nullList(responseTypes, hasResponse), nullStr(b.subjectType), boolOr(b.dpopBound, false))
		if err != nil {
			return err
		}
		now := s.Now()
		for _, r := range resources {
			if _, err := tx.Exec(`INSERT INTO oauth_client_resource (id, client_id, resource_id, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, newID(), clientID, r, now); err != nil {
				return err
			}
		}
		row, err = scanClient(tx.QueryRow(`SELECT `+clientCols+` FROM oauth_client WHERE id = ?`, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	body := row.schemaToOAuth(secret)
	body.Set("resources", strList(resources))
	return &reply{status: 201, body: body}, nil
}

// checkJWKS is the jwks / jwks_uri part of checkOAuthClient. The key set's
// contents are only checked for shape (see docs/GO-PORT.md M6 gaps).
func (s *Server) checkJWKS(b *regBody) *apiErr {
	if b.jwks != nil && b.jwksURI != nil && *b.jwksURI != "" {
		return errDesc(400, "invalid_client_metadata", "jwks and jwks_uri are mutually exclusive")
	}
	if b.jwksURI != nil && *b.jwksURI != "" {
		u, err := parseJSURL(*b.jwksURI)
		if err != nil {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must be a valid URL")
		}
		if u.scheme != "https" {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must use HTTPS")
		}
		if u.username != "" || u.password != "" {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must not contain credentials")
		}
		if strings.Contains(*b.jwksURI, "#") {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must not include a fragment component")
		}
		if isPrivateHostname(u.hostname) {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must not point to a private or reserved address")
		}
		app, err := parseJSURL(s.URL)
		if err != nil || !u.special || u.w.Origin() != app.w.Origin() {
			return errDesc(400, "invalid_client_metadata", "jwks_uri must belong to a trusted origin or the Client ID Metadata Document origin")
		}
	}
	return nil
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullBool(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolOr(p *bool, d bool) bool {
	if p == nil {
		return d
	}
	return *p
}

func jsonList(l []string) string {
	if l == nil {
		l = []string{}
	}
	return jsonx.Stringify(strList(l))
}

func nullList(l []string, ok bool) any {
	if !ok {
		return nil
	}
	return jsonList(l)
}

func nullJSON(v any) any {
	if v == nil {
		return nil
	}
	return jsonx.Stringify(v)
}
