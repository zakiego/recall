package viewer_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
)

const T0 int64 = 1_800_000_000_000

func TestViewerFallsBackToTheFirstWorkspace(t *testing.T) {
	d := testkit.OpenDB(t)
	u, err := users.CreateWithPassword(d, "ida", "a long enough password", "")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := users.CreateWorkspace(d, "Home", "ws_home", "")
	work, _ := users.CreateWorkspace(d, "Work", "ws_work", "")
	users.AddMember(d, home.ID, u.ID, users.Owner)
	users.AddMember(d, work.ID, u.ID, users.MemberRole)
	token, _, err := auth.CreateSession(d, u.ID, "", "", T0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := viewer.FromToken(d, token, T0)
	if err != nil || v.Workspace.ID != "ws_home" || len(v.Workspaces) != 2 || v.User.Username != "ida" {
		t.Fatalf("%+v %v", v, err)
	}
	// Leaving the active workspace moves the session to the next one.
	d.Exec(`UPDATE session SET active_organization_id = 'ws_work'`)
	d.Exec(`DELETE FROM member WHERE organization_id = 'ws_work'`)
	v, _ = viewer.FromToken(d, token, T0)
	if v.Workspace.ID != "ws_home" {
		t.Fatal(v.Workspace)
	}
	var active string
	d.QueryRow(`SELECT active_organization_id FROM session`).Scan(&active)
	if active != "ws_home" {
		t.Fatal(active)
	}
	// Roles: a member can't do what needs an admin.
	r, _ := http.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	d.Exec(`UPDATE member SET role = 'member'`)
	var f *viewer.Forbidden
	if _, err := viewer.RequireMember(d, r, users.Admin, T0); !errors.As(err, &f) || f.Msg != "Only workspace owners and admins can do that." {
		t.Fatal(err)
	}
	if m, err := viewer.RequireMember(d, r, users.MemberRole, T0); err != nil || m.WS != "ws_home" || m.Role != "member" {
		t.Fatal(m, err)
	}
	// No workspace at all, and no session.
	d.Exec(`DELETE FROM member`)
	if _, err := viewer.RequireMember(d, r, users.MemberRole, T0); !errors.As(err, &f) || f.Msg != "You aren't in a workspace yet. Ask an owner for an invite link." {
		t.Fatal(err)
	}
	anon, _ := http.NewRequest("GET", "/", nil)
	if _, err := viewer.RequireUser(d, anon, T0); err != viewer.ErrUnauthorized {
		t.Fatal(err)
	}
}

func TestRequireSameOrigin(t *testing.T) {
	cases := []struct {
		h    map[string]string
		host string
		ok   bool
	}{
		{map[string]string{"origin": "http://localhost:4800"}, "localhost:4800", true},
		{map[string]string{"origin": "https://evil.example"}, "localhost:4800", false},
		{map[string]string{"sec-fetch-site": "cross-site"}, "localhost:4800", false},
		{map[string]string{"origin": "not a url"}, "localhost:4800", false},
		{map[string]string{}, "localhost:4800", true},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("POST", "/", nil)
		r.Host = c.host
		for k, v := range c.h {
			r.Header.Set(k, v)
		}
		if err := viewer.RequireSameOrigin(r, "http://localhost:4800"); (err == nil) != c.ok {
			t.Errorf("%v: %v", c.h, err)
		}
	}
}
