package oauth

import (
	"fmt"
	"strings"
)

// Where the authorization server sends the browser for login and consent
// (OAUTH_LOGIN_HANDOFF and friends), and the pages those open.
const (
	LoginHandoff   = "/app/oauth/sign-in"
	ConsentHandoff = "/app/oauth/approve"
	LoginPage      = "/app/login?oauth=1"
	ConsentPage    = "/app/oauth/consent"
	// QueryCookie is OAUTH_QUERY_COOKIE: the signed query, kept while the
	// page opens.
	QueryCookie = "recall_oauth_query"
)

// encodeURIComponent is JS encodeURIComponent for valid strings.
func encodeURIComponent(s string) string {
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

// HandOff is handOffSignedQuery: a 302 to page, with the request's query
// (rawQuery, without "?") kept in a 10-minute cookie scoped to /app.
// It returns the headers of the 302.
func HandOff(rawQuery, page, appURL string) map[string]string {
	if rawQuery == "" {
		return map[string]string{"location": page}
	}
	secure := ""
	if strings.HasPrefix(appURL, "https://") {
		secure = "; Secure"
	}
	return map[string]string{
		"location":      page,
		"set-cookie":    QueryCookie + "=" + encodeURIComponent(rawQuery) + "; Path=/app; Max-Age=600; SameSite=Lax" + secure,
		"cache-control": "no-store",
	}
}
