package auth

import (
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/model"
)

// Passkeys (@better-auth/passkey 1.7.18 on @simplewebauthn/server 13) with
// go-webauthn, reading and writing the rows Better Auth wrote:
//
//	public_key     the COSE public key, standard base64 with padding
//	credential_id  the credential id, base64url without padding
//	counter        the signature counter
//	device_type    "singleDevice" or "multiDevice" (the backup eligible flag)
//	backed_up      0 or 1 (the backup state flag)
//	transports     the response's transports joined with "," ("" when none)
//	aaguid         the authenticator's AAGUID as a lowercase UUID string
//	created_at     Unix ms
//
// Ceremony state lives in the `verification` table for 5 minutes, keyed by a
// random token in the ChallengeCookie (Better Auth used a signed cookie for the
// same token). The user handle sent at registration is random and never
// stored, as in Better Auth, so a login is matched by credential id alone.

const (
	// ChallengeCookie holds the ceremony's verification token.
	ChallengeCookie = "recall_passkey"
	// ChallengeTTLMs is the plugin's MAX_AGE_IN_SECONDS (300).
	ChallengeTTLMs  int64 = 300_000
	RPName                = "Recall"
	ceremonyTimeout       = 60 * time.Second
)

// Error is a refused auth request: Better Auth's {code, message} and status.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	ErrChallengeNotFound = &Error{http.StatusBadRequest, "CHALLENGE_NOT_FOUND", "Challenge not found"}
	// Every passkey route answers this while the relying party id can't be
	// one (an IP address).
	ErrPasskeysOff          = &Error{http.StatusServiceUnavailable, "PASSKEYS_UNAVAILABLE", "Passkeys need RECALL_URL (or PASSKEY_RP_ID) to use a host name (like localhost or recall.example.com), not an IP address. Change it and restart the server."}
	ErrNotAllowedToRegister = &Error{http.StatusUnauthorized, "YOU_ARE_NOT_ALLOWED_TO_REGISTER_THIS_PASSKEY", "You are not allowed to register this passkey"}
	ErrFailedRegistration   = &Error{http.StatusBadRequest, "FAILED_TO_VERIFY_REGISTRATION", "Failed to verify registration"}
	ErrPasskeyNotFound      = &Error{http.StatusUnauthorized, "PASSKEY_NOT_FOUND", "Passkey not found"}
	ErrAuthFailed           = &Error{http.StatusBadRequest, "AUTHENTICATION_FAILED", "Authentication failed"}
	ErrUnauthorized         = &Error{http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized"}
	ErrSessionNotFresh      = &Error{http.StatusForbidden, "SESSION_NOT_FRESH", "Session is not fresh"}
	ErrFailedUpdate         = &Error{http.StatusInternalServerError, "FAILED_TO_UPDATE_PASSKEY", "Failed to update passkey"}
	ErrBadName              = &Error{http.StatusBadRequest, "VALIDATION_ERROR", "Passkey names can't be empty."}
)

// Passkey is a `passkey` row as stored.
type Passkey struct {
	ID           string  `json:"id"`
	Name         *string `json:"name"`
	PublicKey    string  `json:"-"`
	UserID       string  `json:"userId"`
	CredentialID string  `json:"credentialID"`
	Counter      int64   `json:"counter"`
	DeviceType   string  `json:"deviceType"`
	BackedUp     bool    `json:"backedUp"`
	Transports   *string `json:"transports"`
	CreatedAt    *int64  `json:"createdAt"`
	AAGUID       *string `json:"aaguid"`
}

// PasskeyCols is the column list ScanPasskey reads.
const PasskeyCols = "id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid"

func ScanPasskey(row model.Scanner) (Passkey, error) {
	var p Passkey
	err := row.Scan(&p.ID, &p.Name, &p.PublicKey, &p.UserID, &p.CredentialID, &p.Counter, &p.DeviceType, &p.BackedUp, &p.Transports, &p.CreatedAt, &p.AAGUID)
	return p, err
}

// decodeB64 is @better-auth/utils base64.decode: either alphabet, padding optional.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// Credential turns a stored row into what go-webauthn verifies against.
func (p Passkey) Credential() (webauthn.Credential, error) {
	id, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.CredentialID, "="))
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("credential_id: %w", err)
	}
	pk, err := decodeB64(p.PublicKey)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("public_key: %w", err)
	}
	var transports []protocol.AuthenticatorTransport
	if p.Transports != nil && *p.Transports != "" {
		for _, t := range strings.Split(*p.Transports, ",") {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
	}
	var aaguid []byte
	if p.AAGUID != nil {
		aaguid, _ = hex.DecodeString(strings.ReplaceAll(*p.AAGUID, "-", ""))
	}
	counter := p.Counter
	if counter < 0 {
		counter = 0
	}
	return webauthn.Credential{
		ID:        id,
		PublicKey: pk,
		Transport: transports,
		Flags:     webauthn.CredentialFlags{UserPresent: true, BackupEligible: p.DeviceType == "multiDevice", BackupState: p.BackedUp},
		Authenticator: webauthn.Authenticator{
			AAGUID:    aaguid,
			SignCount: uint32(counter),
		},
	}, nil
}

// UUIDString is simplewebauthn's convertAAGUIDToString.
func UUIDString(b []byte) string {
	if len(b) != 16 {
		b = make([]byte, 16)
	}
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// RowFromCredential is how Better Auth stored a verified registration.
func RowFromCredential(c *webauthn.Credential, transports []string) Passkey {
	deviceType := "singleDevice"
	if c.Flags.BackupEligible {
		deviceType = "multiDevice"
	}
	t := strings.Join(transports, ",")
	aaguid := UUIDString(c.Authenticator.AAGUID)
	return Passkey{
		PublicKey:    base64.StdEncoding.EncodeToString(c.PublicKey),
		CredentialID: base64.RawURLEncoding.EncodeToString(c.ID),
		Counter:      int64(c.Authenticator.SignCount),
		DeviceType:   deviceType,
		BackedUp:     c.Flags.BackupState,
		Transports:   &t,
		AAGUID:       &aaguid,
	}
}

// --- relying party -------------------------------------------------------------------

// RP is the relying party: rpID is PASSKEY_RP_ID or RECALL_URL's hostname,
// the origin RECALL_URL's origin, the name "Recall" (better-auth.ts).
//
// WebAuthn doesn't allow an IP address as the rpID: then WA is nil, Off says
// why, and every ceremony returns ErrPasskeysOff.
type RP struct {
	WA     *webauthn.WebAuthn
	ID     string
	Origin string
	Off    string
}

func NewRP(appURL, rpID string) (*RP, error) {
	u, err := url.Parse(appURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("RECALL_URL %q isn't a URL", appURL)
	}
	origin := AppOrigin(appURL)
	if origin == "" {
		origin = u.Scheme + "://" + u.Host
	}
	if rpID == "" {
		rpID = u.Hostname()
	}
	if net.ParseIP(rpID) != nil {
		return &RP{ID: rpID, Origin: origin, Off: fmt.Sprintf("the passkey relying party id %s is an IP address, and passkeys need a host name (like localhost)", rpID)}, nil
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: RPName,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: ceremonyTimeout, TimeoutUVD: ceremonyTimeout},
			Registration: webauthn.TimeoutConfig{Timeout: ceremonyTimeout, TimeoutUVD: ceremonyTimeout},
		},
	})
	if err != nil {
		return nil, err
	}
	return &RP{WA: wa, ID: rpID, Origin: origin}, nil
}

// waUser is the go-webauthn view of the user for one ceremony.
type waUser struct {
	handle      []byte
	name        string
	display     string
	credentials []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte   { return u.handle }
func (u *waUser) WebAuthnName() string { return u.name }
func (u *waUser) WebAuthnDisplayName() string {
	if u.display != "" {
		return u.display
	}
	return u.name
}
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// ListRows is every passkey of a user (no order).
func ListRows(q db.Q, userID string) ([]Passkey, error) {
	rows, err := q.Query("SELECT "+PasskeyCols+" FROM passkey WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		p, err := ScanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func credentialsOf(rows []Passkey) []webauthn.Credential {
	var out []webauthn.Credential
	for _, r := range rows {
		if c, err := r.Credential(); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func descriptors(rows []Passkey) []protocol.CredentialDescriptor {
	var out []protocol.CredentialDescriptor
	for _, c := range credentialsOf(rows) {
		out = append(out, c.Descriptor())
	}
	return out
}

// ceremony is what the verification row holds.
type ceremony struct {
	Type    string               `json:"type"`
	UserID  string               `json:"userId"`
	Session webauthn.SessionData `json:"session"`
}

func saveCeremony(q db.Q, c ceremony, now int64) (string, error) {
	token := RandomString(32, "")
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	_, err = q.Exec("INSERT INTO verification (id, identifier, value, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)", RandomString(32, ""), token, string(raw), now+ChallengeTTLMs, now, now)
	return token, err
}

// consumeCeremony is consumeVerificationValue: read and delete in one go.
func consumeCeremony(q db.Q, token, want string, now int64) (ceremony, error) {
	if token == "" {
		return ceremony{}, ErrChallengeNotFound
	}
	var value string
	var expiresAt int64
	// The cookie can only consume a ceremony row, never another kind of
	// verification (an authorization code, say).
	err := q.WQueryRow("DELETE FROM verification WHERE identifier = ? AND json_valid(value) AND json_extract(value, '$.type') = ? RETURNING value, expires_at", token, want).Scan(&value, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ceremony{}, ErrChallengeNotFound
	}
	if err != nil {
		return ceremony{}, err
	}
	var c ceremony
	if expiresAt < now || json.Unmarshal([]byte(value), &c) != nil || c.Type != want {
		return ceremony{}, ErrChallengeNotFound
	}
	return c, nil
}

// ChallengeCookieFor is the Set-Cookie for a ceremony token (5 minutes).
func ChallengeCookieFor(appURL, token string) *http.Cookie {
	return &http.Cookie{Name: ChallengeCookie, Value: token, Path: "/", MaxAge: int(ChallengeTTLMs / 1000), HttpOnly: true, Secure: Secure(appURL), SameSite: http.SameSiteLaxMode}
}

// --- registration --------------------------------------------------------------------

// RegisterOptions is generate-register-options for the signed-in user: it
// returns the PublicKeyCredentialCreationOptionsJSON for the browser and the
// ceremony token for the ChallengeCookie.
func (rp *RP) RegisterOptions(q db.Q, viewer *Viewer, now int64) (*protocol.PublicKeyCredentialCreationOptions, string, error) {
	if rp.WA == nil {
		return nil, "", ErrPasskeysOff
	}
	// freshSessionMiddleware ran on the options call, so the person hears it
	// before the device makes a passkey the server would then refuse.
	if now-viewer.SessionCreatedAt >= SessionFreshAgeMs {
		return nil, "", ErrSessionNotFresh
	}
	u, err := scanUser(q.QueryRow("SELECT "+userCols+" FROM user WHERE id = ?", viewer.User.ID))
	if err != nil {
		return nil, "", err
	}
	rows, err := ListRows(q, u.ID)
	if err != nil {
		return nil, "", err
	}
	// userName is the passkey's name when the page sent one, else the
	// user's name; the display name is the user's name (Better Auth's choice).
	display := u.Name
	if display == "" {
		display = u.ID
	}
	name := viewer.PasskeyName
	if name == "" {
		name = display
	}
	user := &waUser{handle: []byte(RandomString(32, "abcdefghijklmnopqrstuvwxyz0123456789")), name: name, display: display}
	creation, session, err := rp.WA.BeginRegistration(user,
		webauthn.WithExclusions(descriptors(rows)),
		webauthn.WithCredentialParameters([]protocol.CredentialParameter{
			{Type: protocol.PublicKeyCredentialType, Algorithm: -8},
			{Type: protocol.PublicKeyCredentialType, Algorithm: -7},
			{Type: protocol.PublicKeyCredentialType, Algorithm: -257},
		}),
	)
	if err != nil {
		return nil, "", err
	}
	token, err := saveCeremony(q, ceremony{Type: "registration", UserID: u.ID, Session: *session}, now)
	if err != nil {
		return nil, "", err
	}
	return &creation.Response, token, nil
}

// creationTransports reads response.response.transports from the raw JSON.
func creationTransports(raw json.RawMessage) []string {
	var body struct {
		Response struct {
			Transports []string `json:"transports"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &body)
	return body.Response.Transports
}

// VerifyRegistration checks a registration response against a ceremony
// session, without touching the database: the credential as it would be
// stored. Exported for the golden test.
func (rp *RP) VerifyRegistration(session webauthn.SessionData, response json.RawMessage) (Passkey, error) {
	if rp.WA == nil {
		return Passkey{}, ErrPasskeysOff
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return Passkey{}, ErrFailedRegistration
	}
	cred, err := rp.WA.CreateCredential(&waUser{handle: session.UserID}, session, parsed)
	if err != nil {
		return Passkey{}, ErrFailedRegistration
	}
	return RowFromCredential(cred, creationTransports(response)), nil
}

// Register is verify-registration: the signed-in user, a session younger
// than a day, the ceremony this browser started, then the new row.
func (rp *RP) Register(q db.Q, viewer *Viewer, challengeToken string, response json.RawMessage, name string, now int64) (Passkey, error) {
	if rp.WA == nil {
		return Passkey{}, ErrPasskeysOff
	}
	if viewer == nil {
		return Passkey{}, ErrUnauthorized
	}
	if now-viewer.SessionCreatedAt >= SessionFreshAgeMs {
		return Passkey{}, ErrSessionNotFresh
	}
	c, err := consumeCeremony(q, challengeToken, "registration", now)
	if err != nil {
		return Passkey{}, err
	}
	if c.UserID != viewer.User.ID {
		return Passkey{}, ErrNotAllowedToRegister
	}
	row, err := rp.VerifyRegistration(c.Session, response)
	if err != nil {
		return Passkey{}, err
	}
	var namePtr *string
	if n := strings.TrimSpace(name); n != "" {
		namePtr = &n
	}
	stored, err := ScanPasskey(q.WQueryRow("INSERT INTO passkey (id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+PasskeyCols,
		RandomString(32, ""), namePtr, row.PublicKey, viewer.User.ID, row.CredentialID, row.Counter, row.DeviceType, row.BackedUp, row.Transports, now, row.AAGUID))
	if err != nil {
		return Passkey{}, ErrFailedRegistration
	}
	return stored, nil
}

// --- authentication ------------------------------------------------------------------

// AuthenticateOptions is generate-authenticate-options. A signed-in caller
// gets its passkeys as allowCredentials; anyone else a discoverable login.
func (rp *RP) AuthenticateOptions(q db.Q, viewer *Viewer, now int64) (*protocol.PublicKeyCredentialRequestOptions, string, error) {
	if rp.WA == nil {
		return nil, "", ErrPasskeysOff
	}
	var opts []webauthn.LoginOption
	userID := ""
	if viewer != nil {
		userID = viewer.User.ID
		rows, err := ListRows(q, userID)
		if err != nil {
			return nil, "", err
		}
		if len(rows) > 0 {
			opts = append(opts, webauthn.WithAllowedCredentials(descriptors(rows)))
		}
	}
	assertion, session, err := rp.WA.BeginDiscoverableLogin(opts...)
	if err != nil {
		return nil, "", err
	}
	token, err := saveCeremony(q, ceremony{Type: "authentication", UserID: userID, Session: *session}, now)
	if err != nil {
		return nil, "", err
	}
	return &assertion.Response, token, nil
}

// VerifyAssertion checks an assertion against the stored row (and the other
// passkeys of the same user, for an allow list), without writing anything. It
// returns the new counter. Exported for the golden test.
func (rp *RP) VerifyAssertion(session webauthn.SessionData, row Passkey, others []Passkey, response json.RawMessage) (int64, error) {
	if rp.WA == nil {
		return 0, ErrPasskeysOff
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return 0, ErrAuthFailed
	}
	cred, err := row.Credential()
	if err != nil {
		return 0, ErrAuthFailed
	}
	creds := []webauthn.Credential{cred}
	for _, o := range others {
		if o.ID != row.ID {
			if c, err := o.Credential(); err == nil {
				creds = append(creds, c)
			}
		}
	}
	// The user handle Better Auth sent at registration was random and never
	// stored, so whatever the authenticator returns is the handle.
	user := &waUser{handle: parsed.Response.UserHandle, credentials: creds}
	session.UserID = parsed.Response.UserHandle
	out, err := rp.WA.ValidateLogin(user, session, parsed)
	if err != nil {
		return 0, ErrAuthFailed
	}
	// simplewebauthn refuses a counter that didn't go up (unless both are 0).
	if out.Authenticator.CloneWarning {
		return 0, ErrAuthFailed
	}
	return int64(out.Authenticator.SignCount), nil
}

// credentialIDOf reads the response's id.
func credentialIDOf(raw json.RawMessage) string {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &body)
	return body.ID
}

// Authenticate is verify-authentication: the passkey's owner, whose counter is
// updated. The caller then starts a session.
func (rp *RP) Authenticate(q db.Q, challengeToken string, response json.RawMessage, now int64) (string, error) {
	if rp.WA == nil {
		return "", ErrPasskeysOff
	}
	c, err := consumeCeremony(q, challengeToken, "authentication", now)
	if err != nil {
		return "", err
	}
	id := credentialIDOf(response)
	row, err := ScanPasskey(q.QueryRow("SELECT "+PasskeyCols+" FROM passkey WHERE credential_id = ?", id))
	if errors.Is(err, sql.ErrNoRows) || id == "" {
		return "", ErrPasskeyNotFound
	}
	if err != nil {
		return "", err
	}
	others, err := ListRows(q, row.UserID)
	if err != nil {
		return "", err
	}
	counter, err := rp.VerifyAssertion(c.Session, row, others, response)
	if err != nil {
		return "", err
	}
	if _, err := q.Exec("UPDATE passkey SET counter = ? WHERE id = ?", counter, row.ID); err != nil {
		return "", err
	}
	return row.UserID, nil
}

// --- management ----------------------------------------------------------------------

// Viewer is the signed-in caller the passkey ceremonies need.
type Viewer struct {
	SessionID string
	User      struct{ ID string }
	// When the session was created (the fresh-session check).
	SessionCreatedAt int64
	// PasskeyName is the name the page gave the new passkey (?name=).
	PasskeyName string
}
