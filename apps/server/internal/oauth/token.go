package oauth

// POST /oauth2/token: authorization_code (PKCE) and refresh_token, opaque
// tokens stored hashed, refresh tokens rotated with reuse detection.

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

var tokenErrorCodes = map[string]any{
	"grant_type": map[string]string{"missing": "invalid_request", "invalid": "unsupported_grant_type"},
	"resource":   map[string]string{"invalid": "invalid_target"},
}

// tokenBody is the token endpoint body after its schema.
type tokenBody struct {
	grantType, clientID, clientSecret, assertion, assertionType string
	code, verifier, redirectURI, refreshToken                   string
	scope                                                       *string
	resource                                                    []string
}

func parseTokenBody(o *jsonx.Object, raw []byte) (*tokenBody, *issue) {
	if o == nil {
		o = jsonx.NewMap[any]()
	}
	b := &tokenBody{}
	gt := get(o, "grant_type")
	s, ok := gt.(string)
	if !ok {
		return nil, at(typeIssue("string", gt), "grant_type")
	}
	b.grantType = strings.TrimSpace(s)
	if b.grantType == "" {
		return nil, at(tooSmallString(1), "grant_type")
	}
	for _, f := range []struct {
		k   string
		dst *string
	}{{"client_id", &b.clientID}, {"client_secret", &b.clientSecret}, {"client_assertion", &b.assertion}, {"client_assertion_type", &b.assertionType}, {"code", &b.code}, {"code_verifier", &b.verifier}} {
		v, is := optString(get(o, f.k))
		if is != nil {
			return nil, at(is, f.k)
		}
		*f.dst = v
	}
	if v := get(o, "redirect_uri"); v != undefined {
		u, is := safeURL(v)
		if is != nil {
			return nil, at(is, "redirect_uri")
		}
		b.redirectURI = u
	}
	v, is := optString(get(o, "refresh_token"))
	if is != nil {
		return nil, at(is, "refresh_token")
	}
	b.refreshToken = v
	if v := get(o, "resource"); v != undefined {
		if is := resourceParam(v); is != nil {
			return nil, at(is, "resource")
		}
		b.resource = resourceList(v)
	}
	if v := get(o, "scope"); v != undefined {
		sc, is := optString(v)
		if is != nil {
			return nil, at(is, "scope")
		}
		b.scope = &sc
	}
	// extractRepeatedResourceFromForm: a repeated form resource is a list.
	if raw != nil {
		if rs := nonEmpty(ParseParams(string(raw)).GetAll("resource")); len(rs) > 1 {
			b.resource = rs
		}
	}
	return b, nil
}

// credentials is extractClientCredentials' result.
type credentials struct {
	clientID, secret, method string
	assertion                bool
}

var (
	basicPrefix   = regexp.MustCompile(`(?i)^Basic +`)
	basicPattern  = regexp.MustCompile(`(?i)^Basic +(.*)$`)
	authSchemeTok = regexp.MustCompile("^([!#$%&'*+\\-.^_`|~0-9A-Za-z]+)(?:\\s|$)")
)

func invalidClient(desc, method, scheme string) *apiErr {
	if scheme == "" && method == "client_secret_basic" {
		scheme = "Basic"
	}
	if scheme != "" {
		e := descErr(401, desc, "invalid_client")
		e.headers = map[string]string{"WWW-Authenticate": scheme}
		return e
	}
	return descErr(400, desc, "invalid_client")
}

// clientCredentials is normalizeClientAuthenticationParameters plus
// extractClientCredentials (nil when none were sent).
func (s *Server) clientCredentials(c *call) (*credentials, *apiErr) {
	body := c.body
	clientID := ""
	fields := map[string]string{}
	if body != nil {
		if v, ok := body.Get("client_id"); ok && v != "" {
			s, ok := v.(string)
			if !ok {
				return nil, errDesc(400, "invalid_request", "client_id must be a string")
			}
			clientID = s
		}
		for _, f := range []string{"client_secret", "client_assertion", "client_assertion_type"} {
			if v, ok := body.Get(f); ok && v != "" {
				s, ok := v.(string)
				if !ok {
					return nil, errDesc(400, "invalid_request", f+" must be a string")
				}
				fields[f] = s
			}
		}
	}
	authz := c.r.Header.Get("authorization")
	if strings.Contains(strings.ToLower(c.r.Header.Get("content-type")), "application/x-www-form-urlencoded") && c.rawBody != nil {
		p := ParseParams(string(c.rawBody))
		ids := nonEmpty(p.GetAll("client_id"))
		if len(ids) > 1 {
			return nil, errDesc(400, "invalid_request", "client_id must not be repeated")
		}
		clientID = ""
		if len(ids) == 1 {
			clientID = ids[0]
		}
		for _, f := range []string{"client_secret", "client_assertion", "client_assertion_type"} {
			vals := nonEmpty(p.GetAll(f))
			if len(vals) > 1 {
				return nil, errDesc(400, "invalid_request", f+" must not be repeated")
			}
			if len(vals) == 1 {
				fields[f] = vals[0]
			}
		}
	}
	_, hasSecret := fields["client_secret"]
	_, a1 := fields["client_assertion"]
	_, a2 := fields["client_assertion_type"]
	hasAssertion := a1 || a2
	if authz != "" && (hasSecret || hasAssertion) || hasSecret && hasAssertion {
		return nil, errDesc(400, "invalid_request", "A request must use only one client authentication method")
	}
	if hasAssertion {
		if !a1 || !a2 {
			return nil, descErr(400, "client_assertion and client_assertion_type must both be provided", "invalid_client")
		}
		// private_key_jwt is not supported by the Go build (docs/GO-PORT.md M6).
		return nil, descErr(400, "invalid client assertion", "invalid_client")
	}
	if authz != "" && !basicPrefix.MatchString(authz) {
		m := authSchemeTok.FindStringSubmatch(authz)
		if m == nil {
			return nil, errDesc(400, "invalid_request", "Invalid authorization header format")
		}
		return nil, invalidClient("unsupported authorization scheme", "", m[1])
	}
	if authz != "" {
		id, secret, ok := decodeBasic(authz)
		if !ok {
			e := descErr(401, "invalid authorization header format", "invalid_client")
			e.headers = map[string]string{"WWW-Authenticate": "Basic"}
			return nil, e
		}
		return &credentials{clientID: id, secret: secret, method: "client_secret_basic"}, nil
	}
	if clientID != "" && fields["client_secret"] != "" {
		return &credentials{clientID: clientID, secret: fields["client_secret"], method: "client_secret_post"}, nil
	}
	if clientID != "" {
		return &credentials{clientID: clientID, method: "none"}, nil
	}
	return nil, nil
}

func nonEmpty(l []string) []string {
	out := []string{}
	for _, v := range l {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func decodeBasic(h string) (string, string, bool) {
	m := basicPattern.FindStringSubmatch(h)
	if m == nil {
		return "", "", false
	}
	raw, err := lenientBase64(m[1])
	if err != nil {
		return "", "", false
	}
	decoded := strx.TextDecode(raw)
	i := strings.Index(decoded, ":")
	if i < 0 || i == 0 || i == len(decoded)-1 {
		return "", "", false
	}
	return formURLDecode(decoded[:i]), formURLDecode(decoded[i+1:]), true
}

// validateClient is validateClientCredentials.
func (s *Server) validateClient(cr *credentials, scopes []string, grant string) (*clientRow, error) {
	cl, err := s.getClient(cr.clientID)
	if err != nil {
		return nil, err
	}
	if cl == nil {
		return nil, invalidClient("client not found", cr.method, "")
	}
	if cl.disabled() {
		return nil, invalidClient("client is disabled", cr.method, "")
	}
	registered := cl.authMethod()
	if registered == "" {
		registered = "client_secret_basic"
	}
	if cr.method != "" && registered != cr.method {
		return nil, invalidClient("client registered for "+registered+" cannot use "+cr.method, cr.method, "")
	}
	if cl.authMethod() == "private_key_jwt" {
		return nil, descErr(400, "client registered for private_key_jwt must use client_assertion", "invalid_client")
	}
	if cl.authMethod() != "none" && cr.secret == "" {
		return nil, invalidClient("client secret must be provided", cr.method, "")
	}
	if cr.secret != "" && cl.ClientSecret == nil {
		return nil, invalidClient("public client, client secret should not be received", cr.method, "")
	}
	if cr.secret != "" {
		plain, err := decryptSecret(s.Secret, *cl.ClientSecret)
		if err != nil || subtle.ConstantTimeCompare([]byte(plain), []byte(cr.secret)) != 1 {
			return nil, invalidClient("invalid client_secret", cr.method, "")
		}
	}
	if scopes != nil && cl.hasScopes {
		for _, sc := range scopes {
			if !contains(cl.Scopes, sc) {
				return nil, descErr(400, "client does not allow scope "+sc, "invalid_scope")
			}
		}
	}
	if grant != "" && !cl.allowsGrant(grant) {
		return nil, descErr(400, "client is not authorized to use grant type "+grant, "unauthorized_client")
	}
	return cl, nil
}

func (s *Server) token(c *call) (*reply, error) {
	b, is := parseTokenBody(c.body, c.rawBody)
	if is != nil {
		code, desc := mapIssue(is, tokenErrorCodes, "invalid_request")
		// Validation runs before the handler, so without the no-store headers.
		return nil, &validationFailure{errDesc(400, code, desc)}
	}
	switch b.grantType {
	case "authorization_code":
		return s.codeGrant(c, b)
	case "refresh_token":
		return s.refreshGrant(c, b)
	}
	return nil, descErr(400, "unsupported grant_type "+b.grantType, "unsupported_grant_type")
}

// validationFailure is an error from schema validation (no no-store headers).
type validationFailure struct{ *apiErr }

func (s *Server) codeGrant(c *call, b *tokenBody) (*reply, error) {
	cr, e := s.clientCredentials(c)
	if e != nil {
		return nil, e
	}
	if cr == nil || cr.clientID == "" {
		return nil, descErr(400, "client_id is required", "invalid_request")
	}
	if b.code == "" {
		return nil, descErr(400, "code is required", "invalid_request")
	}
	withSecret := cr.secret != ""
	withPKCE := b.verifier != ""
	if !withSecret && !withPKCE {
		return nil, descErr(400, "Either code_verifier or client_secret is required", "invalid_request")
	}
	cl, err := s.validateClient(cr, nil, "authorization_code")
	if err != nil {
		return nil, err
	}
	codeID := HashToken(b.code)
	value, err := s.consumeCode(codeID)
	if err != nil {
		return nil, err
	}
	if value == nil {
		s.DB.Exec(`DELETE FROM oauth_access_token WHERE authorization_code_id = ?`, codeID)
		s.DB.Exec(`DELETE FROM oauth_refresh_token WHERE authorization_code_id = ?`, codeID)
		return nil, descErr(400, "invalid code", "invalid_grant")
	}
	q, ok := get(value, "query").(*jsonx.Object)
	userID, okU := get(value, "userId").(string)
	sessionID, okS := get(value, "sessionId").(string)
	if typ, _ := get(value, "type").(string); typ != "authorization_code" || !ok || !okU || !okS {
		return nil, descErr(400, "malformed verification value", "invalid_grant")
	}
	if _, is := parseAuthorizeQuery(q); is != nil {
		return nil, descErr(400, "malformed verification value", "invalid_grant")
	}
	if qc, _ := strOf(q, "client_id"); qc != cr.clientID {
		return nil, descErr(400, "invalid client_id", "invalid_grant")
	}
	if bound, _ := strOf(q, "redirect_uri"); bound != "" {
		if b.redirectURI == "" {
			return nil, descErr(400, "redirect_uri is required", "invalid_request")
		}
		if bound != b.redirectURI {
			return nil, descErr(400, "redirect_uri mismatch", "invalid_grant")
		}
	} else if b.redirectURI != "" {
		return nil, descErr(400, "redirect_uri mismatch", "invalid_grant")
	}
	stored := resourceList(get(value, "resource"))
	if stored == nil {
		qr, _ := q.Get("resource")
		stored = resourceList(qr)
	}
	effective := b.resource
	if effective == nil {
		effective = stored
	}
	if b.resource != nil && stored != nil {
		for _, r := range b.resource {
			if !contains(stored, r) {
				return nil, descErr(400, "requested resource not authorized", "invalid_target")
			}
		}
	}
	scopeText, ok := strOf(q, "scope")
	if !ok {
		return nil, descErr(500, "verification scope unset", "invalid_scope")
	}
	scopes := strings.Split(scopeText, " ")
	if cl.hasScopes {
		for _, sc := range scopes {
			if !contains(cl.Scopes, sc) {
				return nil, descErr(400, "client does not allow scope "+sc, "invalid_scope")
			}
		}
	}
	nonce, _ := strOf(q, "nonce")
	if cl.pkceRequired(scopes, nonce) != "" {
		if !withPKCE {
			return nil, descErr(400, "PKCE is required for this client", "invalid_request")
		}
	}
	challenge, _ := strOf(q, "code_challenge")
	if challenge != "" || withPKCE {
		if challenge != "" && !withPKCE {
			return nil, descErr(400, "code_verifier required because PKCE was used in authorization", "invalid_request")
		}
		if challenge == "" && withPKCE {
			return nil, descErr(400, "code_verifier provided but PKCE was not used in authorization", "invalid_request")
		}
		method, _ := strOf(q, "code_challenge_method")
		if method != "S256" || codeChallenge(b.verifier) != challenge {
			return nil, descErr(400, "code verification failed", "invalid_grant")
		}
	}
	if userID == "" || !s.userExists(userID) {
		return nil, descErr(400, "missing user, user may have been deleted", "invalid_user")
	}
	var expires int64
	err = s.DB.QueryRow(`SELECT expires_at FROM session WHERE id = ?`, sessionID).Scan(&expires)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, sql.ErrNoRows) || expires < s.Now() {
		return nil, descErr(400, "session no longer exists", "invalid_request")
	}
	ref, _ := get(value, "referenceId").(string)
	var authTime any
	if at, ok := get(value, "authTime").(float64); ok {
		authTime = int64(at)
	}
	return s.issueTokens(c, cl, tokenParams{
		scopes: scopes, userID: userID, ref: ref, sessionID: &sessionID, authTime: authTime, codeID: codeID,
		resources: effective, originalResources: stored, dpopRequired: hasString(q, "dpop_jkt"),
	})
}

func hasString(o *jsonx.Object, k string) bool { v, _ := strOf(o, k); return v != "" }

func (s *Server) userExists(id string) bool {
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM user WHERE id = ?`, id).Scan(&n)
	return n > 0
}

// consumeCode is consumeVerificationValue: the newest row for the code is
// taken, every row for it deleted; expired is absent.
func (s *Server) consumeCode(identifier string) (*jsonx.Object, error) {
	var value string
	var expires int64
	found := false
	err := s.DB.Tx(func(tx db.Q) error {
		err := tx.QueryRow(`SELECT value, expires_at FROM verification WHERE identifier = ? ORDER BY created_at DESC LIMIT 1`, identifier).Scan(&value, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		_, err = tx.Exec(`DELETE FROM verification WHERE identifier = ?`, identifier)
		return err
	})
	if err != nil || !found || expires < s.Now() {
		return nil, err
	}
	o, err := jsonx.ParseObject([]byte(value))
	if err != nil {
		return nil, descErr(400, "malformed verification value", "invalid_grant")
	}
	return o, nil
}

type refreshRow struct {
	id, clientID, userID string
	sessionID, ref       *string
	codeID               *string
	resources            []string
	hasResources         bool
	scopes               []string
	expiresAt            int64
	revoked              *int64
	authTime             *int64
	confirmation         *string
}

func (s *Server) findRefresh(hash string) (*refreshRow, error) {
	var r refreshRow
	var resources *string
	var scopes string
	var expires *int64
	err := s.DB.QueryRow(`SELECT id, client_id, user_id, session_id, reference_id, authorization_code_id, resources, scopes, expires_at, revoked, auth_time, confirmation FROM oauth_refresh_token WHERE token = ?`, hash).
		Scan(&r.id, &r.clientID, &r.userID, &r.sessionID, &r.ref, &r.codeID, &resources, &scopes, &expires, &r.revoked, &r.authTime, &r.confirmation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.resources, r.hasResources = parseJSONList(resources)
	r.scopes, _ = parseJSONList(&scopes)
	if expires != nil {
		r.expiresAt = *expires
	}
	return &r, nil
}

// invalidateFamily is invalidateRefreshFamily.
func (s *Server) invalidateFamily(clientID, userID string) error {
	return s.DB.Tx(func(tx db.Q) error {
		if _, err := tx.Exec(`DELETE FROM oauth_access_token WHERE refresh_id IN (SELECT id FROM oauth_refresh_token WHERE client_id = ? AND user_id = ?)`, clientID, userID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM oauth_refresh_token WHERE client_id = ? AND user_id = ?`, clientID, userID)
		return err
	})
}

func (s *Server) refreshGrant(c *call, b *tokenBody) (*reply, error) {
	cr, e := s.clientCredentials(c)
	if e != nil {
		return nil, e
	}
	if cr == nil || cr.clientID == "" {
		return nil, descErr(400, "Missing required client_id", "invalid_request")
	}
	if b.refreshToken == "" {
		return nil, descErr(400, "Missing a required refresh_token for refresh_token grant", "invalid_request")
	}
	cl, err := s.validateClient(cr, nil, "refresh_token")
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(b.refreshToken, RefreshTokenPrefix) {
		return nil, descErr(400, "refresh token not found", "invalid_token")
	}
	raw := strings.Replace(b.refreshToken, RefreshTokenPrefix, "", 1)
	rt, err := s.findRefresh(HashToken(raw))
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, descErr(400, "session not found", "invalid_grant")
	}
	if rt.clientID != cr.clientID || rt.expiresAt < s.Now() {
		return nil, descErr(400, "invalid refresh token", "invalid_grant")
	}
	if b.resource != nil && rt.hasResources {
		for _, r := range b.resource {
			if !contains(rt.resources, r) {
				return nil, descErr(400, "requested resource invalid", "invalid_target")
			}
		}
	}
	scopes := rt.scopes
	if b.scope != nil {
		requested := strings.Split(*b.scope, " ")
		for _, sc := range requested {
			if !contains(rt.scopes, sc) {
				return nil, descErr(400, "unable to issue scope "+sc, "invalid_scope")
			}
		}
		scopes = requested
	}
	if cl.hasScopes {
		for _, sc := range scopes {
			if !contains(cl.Scopes, sc) {
				return nil, descErr(400, "client does not allow scope "+sc, "invalid_scope")
			}
		}
	}
	if rt.revoked != nil {
		if err := s.invalidateFamily(cr.clientID, rt.userID); err != nil {
			return nil, err
		}
		return nil, descErr(400, "invalid refresh token", "invalid_grant")
	}
	if !s.userExists(rt.userID) {
		return nil, descErr(400, "user not found", "invalid_request")
	}
	resources := b.resource
	if resources == nil && rt.hasResources {
		resources = rt.resources
	}
	var authTime any
	if rt.authTime != nil {
		authTime = *rt.authTime
	}
	ref := ""
	if rt.ref != nil {
		ref = *rt.ref
	}
	codeID := ""
	if rt.codeID != nil {
		codeID = *rt.codeID
	}
	return s.issueTokens(c, cl, tokenParams{
		scopes: scopes, userID: rt.userID, ref: ref, refNull: rt.ref == nil, sessionID: rt.sessionID, authTime: authTime,
		codeID: codeID, codeNull: rt.codeID == nil, resources: resources, existing: rt,
	})
}

type tokenParams struct {
	scopes            []string
	userID, ref       string
	refNull           bool
	sessionID         *string
	authTime          any
	codeID            string
	codeNull          bool
	resources         []string
	originalResources []string
	existing          *refreshRow
	dpopRequired      bool
}

// issueTokens is createUserTokens for opaque tokens.
func (s *Server) issueTokens(c *call, cl *clientRow, p tokenParams) (*reply, error) {
	iat := s.Now() / 1000
	var resource any
	if p.resources != nil {
		resource = strList(p.resources)
	}
	pol, e := s.resourcePolicy(cl.ClientID, resource, p.scopes)
	if e != nil {
		return nil, e
	}
	effective := pol.scopes
	exp := iat + AccessTokenExpiresIn
	if pol.accessTTL != nil && iat+*pol.accessTTL < exp {
		exp = iat + *pol.accessTTL
	}
	refreshExp := iat + RefreshTokenExpiresIn
	if pol.refreshTTL != nil && *pol.refreshTTL < RefreshTokenExpiresIn {
		refreshExp = iat + *pol.refreshTTL
	}
	var refreshResources []string
	switch {
	case p.existing != nil && p.existing.hasResources:
		refreshResources = p.existing.resources
	case p.originalResources != nil:
		refreshResources = p.originalResources
	default:
		refreshResources = p.resources
	}
	isRefresh := cl.allowsGrant("refresh_token") && (p.existing != nil && contains(p.existing.scopes, "offline_access") || contains(p.scopes, "offline_access"))
	// DPoP proofs aren't verified by the Go build; a binding that needs one
	// still refuses a request without the header (docs/GO-PORT.md M6).
	bound := p.existing != nil && confirmationJKT(p.existing.confirmation) != ""
	required := derefB(cl.DPoPBoundAccessTokens) || metadataDPoP(cl) || pol.dpop || p.dpopRequired || bound
	if required && c.r.Header.Get("DPoP") == "" {
		return nil, errDesc(400, "invalid_dpop_proof", "DPoP proof header is required")
	}
	if required {
		// Better Auth verified the proof and bound the tokens; the Go build
		// can't verify one, so a binding is refused rather than dropped.
		return nil, errDesc(400, "invalid_dpop_proof", "DPoP proofs are not supported")
	}
	ref := any(p.ref)
	if p.refNull {
		ref = nil
	}
	codeID := any(p.codeID)
	if p.codeNull {
		codeID = nil
	}
	var sid any
	if p.sessionID != nil {
		sid = *p.sessionID
	}
	var refreshToken, refreshID string
	accessRaw := randomString(32, upper+lower)
	err := s.DB.Tx(func(tx db.Q) error {
		if isRefresh {
			raw := randomString(32, upper+lower)
			if p.existing != nil {
				res, err := tx.Exec(`UPDATE oauth_refresh_token SET revoked = ?, rotated_at = ? WHERE id = ? AND revoked IS NULL`, iat*1000, iat*1000, p.existing.id)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return descErr(400, "invalid refresh token", "invalid_grant")
				}
			}
			refreshID = newID()
			var resJSON any
			if refreshResources != nil {
				resJSON = jsonList(refreshResources)
			}
			if _, err := tx.Exec(`INSERT INTO oauth_refresh_token (id, token, client_id, session_id, user_id, reference_id, authorization_code_id, resources, expires_at, created_at, auth_time, scopes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				refreshID, HashToken(raw), cl.ClientID, sid, p.userID, ref, codeID, resJSON, refreshExp*1000, iat*1000, p.authTime, jsonList(effective)); err != nil {
				return err
			}
			refreshToken = RefreshTokenPrefix + raw
		}
		var resJSON, rid any
		if p.resources != nil {
			resJSON = jsonList(p.resources)
		}
		if refreshID != "" {
			rid = refreshID
		}
		_, err := tx.Exec(`INSERT INTO oauth_access_token (id, token, client_id, session_id, user_id, reference_id, authorization_code_id, resources, refresh_id, expires_at, created_at, scopes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), HashToken(accessRaw), cl.ClientID, sid, p.userID, ref, codeID, resJSON, rid, exp*1000, iat*1000, jsonList(effective))
		return err
	})
	if err != nil {
		return nil, err
	}
	body := jsonx.ObjectOf("access_token", AccessTokenPrefix+accessRaw, "expires_in", exp-iat, "expires_at", exp, "token_type", "Bearer")
	if refreshToken != "" {
		body.Set("refresh_token", refreshToken)
	}
	body.Set("scope", strings.Join(effective, " "))
	return &reply{body: body}, nil
}

// metadataDPoP is the client's metadata dpop_bound_access_tokens flag.
func metadataDPoP(cl *clientRow) bool {
	if cl.Metadata == nil || *cl.Metadata == "" {
		return false
	}
	m, err := jsonx.ParseObject([]byte(*cl.Metadata))
	if err != nil {
		return false
	}
	v, _ := m.Get("dpop_bound_access_tokens")
	return v == true
}

// confirmationJKT is getConfirmationJkt: the jkt of a stored confirmation
// object, "" when there is none.
func confirmationJKT(raw *string) string {
	if raw == nil {
		return ""
	}
	o, err := jsonx.ParseObject([]byte(*raw))
	if err != nil {
		return ""
	}
	jkt, _ := o.Get("jkt")
	s, _ := jkt.(string)
	return s
}
