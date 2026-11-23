package auth

// Sessions, replacing Better Auth's (better-auth.ts: expiresIn 7 days,
// updateAge 1 day, httpOnly SameSite=Lax cookies, Secure when RECALL_URL is
// https; session.ts for the viewer). `session.token`
// holds the token's SHA-256 (base64url), not the token, and the cookie has a
// new name: sessions Better Auth made stop working and people sign in again
// (allowed at cutover). A new session lands in the user's first workspace,
// as the databaseHooks.session.create hook did.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
)

const (
	// SessionExpiresInMs is session.expiresIn (7 days).
	SessionExpiresInMs int64 = 7 * 24 * 60 * 60 * 1000
	// SessionUpdateAgeMs: a session's expiry moves at most once a day.
	SessionUpdateAgeMs int64 = 24 * 60 * 60 * 1000
	// SessionFreshAgeMs: adding a passkey needs a session younger than this
	// (Better Auth's freshSessionMiddleware default).
	SessionFreshAgeMs int64 = 24 * 60 * 60 * 1000
	// CookieName is the session cookie (Better Auth's was
	// recall.session_token, or __Secure-recall.session_token).
	CookieName = "recall_session"
)

// User is a `user` row.
type User struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	EmailVerified   bool    `json:"emailVerified"`
	Image           *string `json:"image"`
	Username        *string `json:"username"`
	DisplayUsername *string `json:"displayUsername"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
}

const userCols = "id, name, email, email_verified, image, username, display_username, created_at, updated_at"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.EmailVerified, &u.Image, &u.Username, &u.DisplayUsername, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// Session is a `session` row without the token hash.
type Session struct {
	ID                   string  `json:"id"`
	UserID               string  `json:"userId"`
	ExpiresAt            int64   `json:"expiresAt"`
	CreatedAt            int64   `json:"createdAt"`
	UpdatedAt            int64   `json:"updatedAt"`
	IPAddress            *string `json:"ipAddress"`
	UserAgent            *string `json:"userAgent"`
	ActiveOrganizationID *string `json:"activeOrganizationId"`
}

const sessionCols = "id, user_id, expires_at, created_at, updated_at, ip_address, user_agent, active_organization_id"

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt, &s.IPAddress, &s.UserAgent, &s.ActiveOrganizationID)
	return s, err
}

// HashToken is what the `session.token` column stores for a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString is Better Auth's generateRandomString(n) over alphabet
// (default a-z, A-Z, 0-9), without modulo bias.
func RandomString(n int, alphabet string) string {
	if alphabet == "" {
		alphabet = alnum
	}
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	limit := 256 - 256%len(alphabet)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if int(b) < limit {
				out = append(out, alphabet[int(b)%len(alphabet)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// firstWorkspace is the user's oldest membership, nil without one.
func firstWorkspace(q db.Q, userID string) (any, error) {
	var id string
	err := q.QueryRow(`SELECT organization_id FROM member WHERE user_id = ? ORDER BY created_at ASC LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return id, nil
}

// CreateSession starts a session for userID in their first workspace and
// returns the cookie token. now is ms.
func CreateSession(q db.Q, userID, ip, userAgent string, now int64) (string, Session, error) {
	ws, err := firstWorkspace(q, userID)
	if err != nil {
		return "", Session{}, err
	}
	token := RandomString(32, "")
	s, err := scanSession(q.WQueryRow("INSERT INTO session (id, expires_at, token, created_at, updated_at, ip_address, user_agent, user_id, active_organization_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+sessionCols,
		RandomString(32, ""), now+SessionExpiresInMs, HashToken(token), now, now, nullable(ip), nullable(userAgent), userID, ws))
	return token, s, err
}

// Found is a live session and its user.
type Found struct {
	Session Session
	User    User
	// Refreshed: the expiry moved forward, so the cookie is set again.
	Refreshed bool
	// Token is the cookie token it was found by.
	Token string
}

// Viewer is the passkey ceremonies' view of this session.
func (f *Found) Viewer() *Viewer {
	v := &Viewer{SessionID: f.Session.ID, SessionCreatedAt: f.Session.CreatedAt}
	v.User.ID = f.User.ID
	return v
}

// GetSession is getSession: the session for a cookie token, or nil when
// there is none or it expired (an expired row is deleted). With refresh, a
// session whose expiry last moved more than a day ago gets a new 7 days.
func GetSession(q db.Q, token string, refresh bool, now int64) (*Found, error) {
	if token == "" {
		return nil, nil
	}
	s, err := scanSession(q.QueryRow("SELECT "+sessionCols+" FROM session WHERE token = ?", HashToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.ExpiresAt < now {
		_, err := q.Exec("DELETE FROM session WHERE id = ?", s.ID)
		return nil, err
	}
	u, err := scanUser(q.QueryRow("SELECT "+userCols+" FROM user WHERE id = ?", s.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &Found{Session: s, User: u, Token: token}
	if refresh && s.ExpiresAt-SessionExpiresInMs+SessionUpdateAgeMs <= now {
		updated, err := scanSession(q.WQueryRow("UPDATE session SET expires_at = ?, updated_at = ? WHERE id = ? RETURNING "+sessionCols, now+SessionExpiresInMs, now, s.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		f.Session, f.Refreshed = updated, true
	}
	return f, nil
}

// SetActiveWorkspace points a session at a workspace (nil for none).
func SetActiveWorkspace(q db.Q, sessionID string, ws *string) error {
	_, err := q.Exec(`UPDATE session SET active_organization_id = ? WHERE id = ?`, ws, sessionID)
	return err
}

// DeleteSession signs a token out. Missing is fine.
func DeleteSession(q db.Q, token string) error {
	if token == "" {
		return nil
	}
	_, err := q.Exec("DELETE FROM session WHERE token = ?", HashToken(token))
	return err
}

// DeleteOtherSessions ends every session of userID except keepID.
func DeleteOtherSessions(q db.Q, userID, keepID string) error {
	_, err := q.Exec("DELETE FROM session WHERE user_id = ? AND id != ?", userID, keepID)
	return err
}

// --- cookies --------------------------------------------------------------------------

// Secure is useSecureCookies: RECALL_URL is https.
func Secure(appURL string) bool { return strings.HasPrefix(appURL, "https://") }

// SessionCookie is the Set-Cookie for a session token.
func SessionCookie(appURL, token string) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: token, Path: "/", MaxAge: int(SessionExpiresInMs / 1000), HttpOnly: true, Secure: Secure(appURL), SameSite: http.SameSiteLaxMode}
}

// ClearCookie expires a cookie.
func ClearCookie(appURL, name string) *http.Cookie {
	return &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: Secure(appURL), SameSite: http.SameSiteLaxMode}
}

// TokenFrom reads the session token cookie.
func TokenFrom(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ClientIP is withClientIp / clientIp: cf-connecting-ip, the first
// x-forwarded-for hop, x-real-ip, else "local".
func ClientIP(h http.Header) string {
	if v := h.Get("cf-connecting-ip"); v != "" {
		return v
	}
	if v := h.Get("x-forwarded-for"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	if v := h.Get("x-real-ip"); v != "" {
		return v
	}
	return "local"
}
