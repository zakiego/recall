// Package viewer is session.ts: who is calling a dashboard endpoint, which
// workspace they act in, and the role and same-origin guards.
package viewer

import (
	"errors"
	"net/http"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/users"
)

// ErrUnauthorized is UnauthorizedError: no session.
var ErrUnauthorized = errors.New("UNAUTHORIZED")

// Forbidden is ForbiddenError.
type Forbidden struct{ Msg string }

func (e *Forbidden) Error() string { return e.Msg }

var rank = map[string]int{users.MemberRole: 1, users.Admin: 2, users.Owner: 3}

// RoleAtLeast is roleAtLeast.
func RoleAtLeast(role, min string) bool { return rank[role] >= rank[min] }

// User is Viewer.user.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Viewer is session.ts Viewer.
type Viewer struct {
	SessionID  string             `json:"sessionId"`
	User       User               `json:"user"`
	Workspace  *users.Membership  `json:"workspace"`
	Workspaces []users.Membership `json:"workspaces"`
	Found      *auth.Found        `json:"-"`
}

// FromToken is viewerFromHeaders for a session cookie token: nil when
// signed out. A session pointing at a workspace the user has left falls back
// to their first one (and the session row is updated). now is ms.
func FromToken(d *db.DB, token string, now int64) (*Viewer, error) {
	f, err := auth.GetSession(d, token, true, now)
	if err != nil || f == nil {
		return nil, err
	}
	workspaces, err := users.ListMemberships(d, f.User.ID)
	if err != nil {
		return nil, err
	}
	if workspaces == nil {
		workspaces = []users.Membership{}
	}
	var active *string = f.Session.ActiveOrganizationID
	var ws *users.Membership
	for i := range workspaces {
		if active != nil && workspaces[i].ID == *active {
			ws = &workspaces[i]
		}
	}
	if ws == nil && len(workspaces) > 0 {
		ws = &workspaces[0]
	}
	var wsID *string
	if ws != nil {
		wsID = &ws.ID
	}
	if (wsID == nil) != (active == nil) || (wsID != nil && *wsID != *active) {
		if err := auth.SetActiveWorkspace(d, f.Session.ID, wsID); err != nil {
			return nil, err
		}
		f.Session.ActiveOrganizationID = wsID
	}
	// displayUsername || username || name
	name := f.User.Name
	if f.User.Username != nil && *f.User.Username != "" {
		name = *f.User.Username
	}
	if f.User.DisplayUsername != nil && *f.User.DisplayUsername != "" {
		name = *f.User.DisplayUsername
	}
	return &Viewer{SessionID: f.Session.ID, User: User{ID: f.User.ID, Username: name, Name: f.User.Name}, Workspace: ws, Workspaces: workspaces, Found: f}, nil
}

// FromRequest is viewerFromHeaders for a request.
func FromRequest(d *db.DB, r *http.Request, now int64) (*Viewer, error) {
	return FromToken(d, auth.TokenFrom(r), now)
}

// Member is requireMember's result: the workspace every query is scoped to.
type Member struct {
	WS     string
	Role   string
	Viewer *Viewer
}

// RequireMember is requireMember: signed in, in a workspace, with at least
// min. The role is read again so a change takes effect at once.
func RequireMember(d *db.DB, r *http.Request, min string, now int64) (*Member, error) {
	v, err := FromRequest(d, r, now)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, ErrUnauthorized
	}
	if v.Workspace == nil {
		return nil, &Forbidden{"You aren't in a workspace yet. Ask an owner for an invite link."}
	}
	role, err := users.GetRole(d, v.User.ID, v.Workspace.ID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return nil, &Forbidden{"You're no longer a member of this workspace."}
	}
	if !RoleAtLeast(role, min) {
		who := "owners and admins"
		if min == users.Owner {
			who = "owners"
		}
		return nil, &Forbidden{"Only workspace " + who + " can do that."}
	}
	return &Member{WS: v.Workspace.ID, Role: role, Viewer: v}, nil
}

// RequireUser is requireUser.
func RequireUser(d *db.DB, r *http.Request, now int64) (*Viewer, error) {
	v, err := FromRequest(d, r, now)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, ErrUnauthorized
	}
	return v, nil
}

// RequireSameOrigin is requireSameOrigin: dashboard writes come from the
// dashboard's own origin.
func RequireSameOrigin(r *http.Request, appURL string) error {
	if !auth.SameOrigin(r.Header, r.Host, appURL) {
		return &Forbidden{"cross-origin request refused"}
	}
	return nil
}
