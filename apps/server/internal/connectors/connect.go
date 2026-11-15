package connectors

import (
	"context"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/logx"
)

// CompleteConnect is the second half of handleCallback (oauth.ts), after
// the state cookie, the session and the membership were checked: the code
// is exchanged, the Gmail scope checked, the profile read and the
// connection saved, revoking a replaced grant. It returns the outcome the
// callback redirects with: "connected", "denied" or "failed". Failures log
// their message only (status codes), never the token response.
func CompleteConnect(ctx context.Context, d *db.DB, workspaceID, userID, code, verifier string, do Do, now int64) string {
	fail := func(err error) string {
		logx.Error("[recall:connectors] gmail connect failed: %s", err.Error())
		return "failed"
	}
	grant, err := ExchangeCode(ctx, do, code, verifier, now)
	if err != nil {
		return fail(err)
	}
	// The user can untick the Gmail box on Google's consent screen.
	scoped := false
	for _, s := range strings.Split(grant.Scope, " ") {
		scoped = scoped || s == GmailScope
	}
	if !scoped {
		RevokeToken(ctx, do, grant.RefreshToken)
		return "denied"
	}
	email, err := GetProfile(ctx, do, grant.AccessToken)
	if err != nil {
		return fail(err)
	}
	var addr *string
	if email != "" {
		addr = &email
	}
	_, replaced, err := SaveConnection(d, NewConnection{WorkspaceID: workspaceID, UserID: userID, Email: addr, Scope: grant.Scope, Tokens: grant.Tokens, Now: now})
	if err != nil {
		return fail(err)
	}
	if replaced != "" {
		RevokeToken(ctx, do, replaced)
	}
	return "connected"
}
