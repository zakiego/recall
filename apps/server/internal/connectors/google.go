package connectors

// google.ts: Google OAuth (web server flow with PKCE) and the few Gmail API
// calls the connector makes. Every call takes the Do to use, so tests and the
// dev fake run against fixtures. Errors carry status codes only, never mail
// content.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/nodefetch"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	GmailScope = "https://www.googleapis.com/auth/gmail.readonly"
	AuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL   = "https://oauth2.googleapis.com/token"
	RevokeURL  = "https://oauth2.googleapis.com/revoke"
	GmailAPI   = "https://gmail.googleapis.com/gmail/v1/users/me"
	// CallbackPath is under APP_BASE.
	CallbackPath  = "/app/connectors/google/callback"
	googleTimeout = 30 * time.Second
)

// Do makes an HTTP request (http.Client.Do, or a fake).
type Do func(*http.Request) (*http.Response, error)

// RealDo is the real HTTP client: Node's fetch semantics (nodefetch).
func RealDo(r *http.Request) (*http.Response, error) { return nodefetch.Do(r) }

// GoogleError is GoogleError. Code is "reauth", "not_found",
// "rate_limited", "upstream" or "bad_response".
type GoogleError struct {
	Msg    string
	Status int
	Code   string
}

func (e *GoogleError) Error() string  { return e.Msg }
func (e *GoogleError) JSName() string { return "GoogleError" }

// RedirectURI is redirectUri.
func RedirectURI() string { return config.Load().URL + CallbackPath }

// PKCEPair is pkcePair: a verifier and its S256 challenge.
func PKCEPair() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// FormEncode is URLSearchParams.toString(): pairs in order, the
// application/x-www-form-urlencoded byte serializer.
func FormEncode(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = formByte(p[0]) + "=" + formByte(p[1])
	}
	return strings.Join(parts, "&")
}

func formByte(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// EncodeURIComponent is JS encodeURIComponent for valid strings.
func EncodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// AuthorizationURL is authorizationUrl.
func AuthorizationURL(clientID, state, challenge string) string {
	return AuthURL + "?" + FormEncode([][2]string{
		{"client_id", clientID},
		{"redirect_uri", RedirectURI()},
		{"response_type", "code"},
		{"scope", GmailScope},
		// offline + consent: Google returns a refresh token every time, also on a reconnect.
		{"access_type", "offline"},
		{"prompt", "consent"},
		{"include_granted_scopes", "false"},
		{"state", state},
		{"code_challenge", challenge},
		{"code_challenge_method", "S256"},
	})
}

// Tokens is a stored grant; JSON in this key order is what gets sealed.
type Tokens struct {
	AccessToken  string  `json:"accessToken"`
	RefreshToken string  `json:"refreshToken"`
	ExpiresAt    float64 `json:"expiresAt"`
}

type tokenResponse struct {
	AccessToken  any `json:"access_token"`
	RefreshToken any `json:"refresh_token"`
	ExpiresIn    any `json:"expires_in"`
	Scope        any `json:"scope"`
	Error        any `json:"error"`
}

func postForm(ctx context.Context, do Do, url string, pairs [][2]string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, googleTimeout)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(FormEncode(pairs)))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	res, err := do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	res.Body = &cancelOnClose{res.Body, cancel}
	return res, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// readJSON is res.json().catch(() => ({})); null reports a body that is
// JSON null, whose properties JS can't read.
func readJSON(res *http.Response, into any) (null bool) {
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var v any
	if json.Unmarshal(b, &v) == nil && v == nil {
		return true
	}
	json.Unmarshal(b, into)
	return false
}

// nullRead is the TypeError reading a property of null throws.
func nullRead(prop string) error {
	return jserr.New("TypeError", "Cannot read properties of null (reading '"+prop+"')")
}

func ok(res *http.Response) bool { return res.StatusCode >= 200 && res.StatusCode <= 299 }

func str(v any) string {
	s, _ := v.(string)
	return s
}

// expiresIn is (json.expires_in ?? 3600), as the multiplication by 1000
// coerces it: a numeric string counts, anything else is NaN.
func expiresIn(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 3600
	case float64:
		return x
	case string:
		return strx.Number(x)
	case bool:
		if x {
			return 1
		}
		return 0
	}
	return math.NaN()
}

// Grant is exchangeCode's result: the tokens and the granted scope.
type Grant struct {
	Tokens
	Scope string `json:"scope"`
}

// ExchangeCode is exchangeCode.
func ExchangeCode(ctx context.Context, do Do, code, verifier string, now int64) (Grant, error) {
	c := config.Load()
	res, err := postForm(ctx, do, TokenURL, [][2]string{
		{"code", code}, {"client_id", c.GoogleClientID}, {"client_secret", c.GoogleClientSecret},
		{"redirect_uri", RedirectURI()}, {"grant_type", "authorization_code"}, {"code_verifier", verifier},
	})
	if err != nil {
		return Grant{}, err
	}
	var j tokenResponse
	if readJSON(res, &j) && ok(res) {
		return Grant{}, nullRead("access_token")
	}
	if !ok(res) || !jsonx.Truthy(j.AccessToken) {
		return Grant{}, &GoogleError{fmt.Sprintf("Google token exchange returned %d", res.StatusCode), res.StatusCode, "upstream"}
	}
	if !jsonx.Truthy(j.RefreshToken) {
		return Grant{}, &GoogleError{"Google returned no refresh token", res.StatusCode, "bad_response"}
	}
	scope := ""
	if j.Scope != nil {
		scope = str(j.Scope)
	}
	return Grant{Tokens: Tokens{AccessToken: str(j.AccessToken), RefreshToken: str(j.RefreshToken), ExpiresAt: float64(now) + expiresIn(j.ExpiresIn)*1000}, Scope: scope}, nil
}

// RefreshAccessToken is refreshAccessToken.
func RefreshAccessToken(ctx context.Context, do Do, refreshToken string, now int64) (Tokens, error) {
	c := config.Load()
	res, err := postForm(ctx, do, TokenURL, [][2]string{
		{"client_id", c.GoogleClientID}, {"client_secret", c.GoogleClientSecret}, {"refresh_token", refreshToken}, {"grant_type", "refresh_token"},
	})
	if err != nil {
		return Tokens{}, err
	}
	var j tokenResponse
	if readJSON(res, &j) {
		return Tokens{}, nullRead("error")
	}
	if j.Error == "invalid_grant" || res.StatusCode == 401 {
		return Tokens{}, &GoogleError{"Gmail access was revoked or expired", res.StatusCode, "reauth"}
	}
	if !ok(res) || !jsonx.Truthy(j.AccessToken) {
		return Tokens{}, &GoogleError{fmt.Sprintf("Google token refresh returned %d", res.StatusCode), res.StatusCode, "upstream"}
	}
	refresh := refreshToken
	if j.RefreshToken != nil {
		refresh = str(j.RefreshToken)
	}
	return Tokens{AccessToken: str(j.AccessToken), RefreshToken: refresh, ExpiresAt: float64(now) + expiresIn(j.ExpiresIn)*1000}, nil
}

// RevokeToken is revokeToken: true when Google confirmed, or the token was
// already dead (400).
func RevokeToken(ctx context.Context, do Do, token string) bool {
	res, err := postForm(ctx, do, RevokeURL, [][2]string{{"token", token}})
	if err != nil {
		return false
	}
	res.Body.Close()
	return ok(res) || res.StatusCode == 400
}

// --- Gmail API -------------------------------------------------------------------------

func gmail(ctx context.Context, do Do, accessToken, path string, query [][2]string, into any) error {
	u := GmailAPI + path
	if len(query) > 0 {
		u += "?" + FormEncode(query)
	}
	ctx, cancel := context.WithTimeout(ctx, googleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+accessToken)
	req.Header.Set("accept", "application/json")
	res, err := do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if ok(res) {
		b, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		// res.json() rejects with a SyntaxError (V8 words its message differently).
		if !json.Valid(b) {
			return &jserr.Error{Name: "SyntaxError", Message: "invalid JSON", Cause: json.Unmarshal(b, into)}
		}
		return json.Unmarshal(b, into)
	}
	code := "upstream"
	switch res.StatusCode {
	case 401:
		code = "reauth"
	case 404:
		code = "not_found"
	case 429:
		code = "rate_limited"
	}
	return &GoogleError{fmt.Sprintf("Gmail API returned %d", res.StatusCode), res.StatusCode, code}
}

// GmailHeader is a message header.
type GmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GmailBody is a part's body.
type GmailBody struct {
	Size         *float64 `json:"size,omitempty"`
	Data         *string  `json:"data,omitempty"`
	AttachmentID *string  `json:"attachmentId,omitempty"`
}

// GmailPart is a MIME part of a message.
type GmailPart struct {
	PartID   *string       `json:"partId,omitempty"`
	MimeType *string       `json:"mimeType,omitempty"`
	Filename *string       `json:"filename,omitempty"`
	Headers  []GmailHeader `json:"headers,omitempty"`
	Body     *GmailBody    `json:"body,omitempty"`
	Parts    []GmailPart   `json:"parts,omitempty"`
}

// GmailMessage is a message as the API returns it.
type GmailMessage struct {
	ID           string     `json:"id"`
	ThreadID     *string    `json:"threadId,omitempty"`
	LabelIDs     []string   `json:"labelIds,omitempty"`
	Snippet      *string    `json:"snippet,omitempty"`
	InternalDate *string    `json:"internalDate,omitempty"`
	SizeEstimate *float64   `json:"sizeEstimate,omitempty"`
	Payload      *GmailPart `json:"payload,omitempty"`
}

// GetProfile is getProfile.
func GetProfile(ctx context.Context, do Do, accessToken string) (string, error) {
	var p struct {
		EmailAddress *string `json:"emailAddress"`
	}
	if err := gmail(ctx, do, accessToken, "/profile", nil, &p); err != nil {
		return "", err
	}
	if p.EmailAddress == nil {
		return "", nil
	}
	return *p.EmailAddress, nil
}

// MessageList is listMessages' result.
type MessageList struct {
	Messages []struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	} `json:"messages,omitempty"`
	NextPageToken      *string  `json:"nextPageToken,omitempty"`
	ResultSizeEstimate *float64 `json:"resultSizeEstimate,omitempty"`
}

// ListMessages is listMessages.
func ListMessages(ctx context.Context, do Do, accessToken, q, pageToken string, maxResults int) (MessageList, error) {
	query := [][2]string{{"q", q}, {"maxResults", fmt.Sprint(maxResults)}}
	if pageToken != "" {
		query = append(query, [2]string{"pageToken", pageToken})
	}
	var out MessageList
	err := gmail(ctx, do, accessToken, "/messages", query, &out)
	return out, err
}

// GetMessageMetadata is getMessageMetadata.
func GetMessageMetadata(ctx context.Context, do Do, accessToken, id string) (GmailMessage, error) {
	var m GmailMessage
	err := gmail(ctx, do, accessToken, "/messages/"+EncodeURIComponent(id), [][2]string{{"format", "metadata"}, {"metadataHeaders", "From"}, {"metadataHeaders", "Subject"}, {"metadataHeaders", "Date"}}, &m)
	return m, err
}

// GetMessage is getMessage (format=full).
func GetMessage(ctx context.Context, do Do, accessToken, id string) (GmailMessage, error) {
	var m GmailMessage
	err := gmail(ctx, do, accessToken, "/messages/"+EncodeURIComponent(id), [][2]string{{"format", "full"}}, &m)
	return m, err
}

// GetAttachment is getAttachment: the attachment's bytes.
func GetAttachment(ctx context.Context, do Do, accessToken, messageID, attachmentID string) ([]byte, error) {
	var r struct {
		Data *string `json:"data"`
	}
	if err := gmail(ctx, do, accessToken, "/messages/"+EncodeURIComponent(messageID)+"/attachments/"+EncodeURIComponent(attachmentID), nil, &r); err != nil {
		return nil, err
	}
	data := ""
	if r.Data != nil {
		data = *r.Data
	}
	return DecodeBase64URL(data), nil
}

// DecodeBase64URL is Buffer.from(s, "base64url"): Node decodes either
// alphabet and skips what isn't base64.
func DecodeBase64URL(s string) []byte { return strx.DecodeBase64Node(s) }
