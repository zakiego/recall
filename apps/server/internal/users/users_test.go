package users_test

// users.test.ts up to the Better Auth endpoints (those are M6).

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/users"
)

const PW = "a long enough password"

func openDB(t *testing.T) *db.DB {
	d, err := db.Open(filepath.Join(t.TempDir(), "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func now() int64 { return time.Now().UnixMilli() }

func mustUser(t *testing.T, d *db.DB, name string) users.User {
	t.Helper()
	u, err := users.CreateWithPassword(d, name, PW, "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustWS(t *testing.T, d *db.DB, name string) string {
	t.Helper()
	w, err := users.CreateWorkspace(d, name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func mustMember(t *testing.T, d *db.DB, ws, user, role string) users.Member {
	t.Helper()
	m, err := users.AddMember(d, ws, user, role)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func isUserErr(err error, sub string) bool {
	var ue *users.Error
	return errors.As(err, &ue) && strings.Contains(ue.Msg, sub)
}

func role(t *testing.T, d *db.DB, user, ws string) string {
	r, err := users.GetRole(d, user, ws)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lookup(t *testing.T, d *db.DB, token string, at int64) users.Lookup {
	l, err := users.LookupInvite(d, token, at)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBootstrapWithoutHash(t *testing.T) {
	d := openDB(t)
	empty := ""
	r, err := users.BootstrapUsers(d, &empty)
	if err != nil || r != (users.Bootstrap{Reason: "no_admin_hash"}) {
		t.Fatal(r, err)
	}
}

func TestBootstrapOnce(t *testing.T) {
	d := openDB(t)
	hash, _ := auth.HashLegacyPassword(PW)
	r, err := users.BootstrapUsers(d, &hash)
	if err != nil || !r.Created || r.WorkspaceID == "" {
		t.Fatal(r, err)
	}
	ws, found, _ := users.FindWorkspace(d, r.WorkspaceID)
	if !found || ws.Name != "Home" || ws.Slug != "home" {
		t.Fatalf("%+v", ws)
	}
	members, _ := users.ListMembers(d, r.WorkspaceID)
	if len(members) != 1 || *members[0].Username != "admin" || members[0].Role != "owner" {
		t.Fatalf("%+v", members)
	}
	if r, _ := users.BootstrapUsers(d, &hash); r != (users.Bootstrap{Reason: "users_exist"}) {
		t.Fatal(r)
	}
}

func TestUsernamesNormalizedAndChecked(t *testing.T) {
	d := openDB(t)
	u := mustUser(t, d, "Eva.G")
	if *u.Username != "eva.g" || *u.DisplayUsername != "Eva.G" || u.Email != "eva.g@users.recall.invalid" {
		t.Fatalf("%+v", u)
	}
	if _, err := users.CreateWithPassword(d, "eva.g", PW, ""); !isUserErr(err, "") {
		t.Fatal(err)
	}
	if _, err := users.CreateWithPassword(d, "no spaces", PW, ""); !isUserErr(err, "letters, numbers") {
		t.Fatal(err)
	}
	if _, err := users.CreateWithPassword(d, "ok_name", "short", ""); !isUserErr(err, "at least 12") {
		t.Fatal(err)
	}
}

func TestPlaceholderEmailClash(t *testing.T) {
	d := openDB(t)
	renamed := mustUser(t, d, "mallory")
	// A rename from before usernames were immutable keeps the old placeholder email.
	d.Exec(`UPDATE user SET username = 'eva' WHERE id = ?`, renamed.ID)
	ws := mustWS(t, d, "Team")
	owner := mustUser(t, d, "owner")
	mustMember(t, d, ws, owner.ID, "owner")
	token, _, _ := users.CreateInvite(d, ws, "member", owner.ID, now())
	if _, _, err := users.AcceptInviteWithNewUser(d, token, "mallory", PW, now()); !isUserErr(err, "") {
		t.Fatal(err)
	}
	if _, err := users.CreateWithPassword(d, "Mallory", PW, ""); !isUserErr(err, "taken") {
		t.Fatal(err)
	}
	if l := lookup(t, d, token, now()); l.Reason != "" {
		t.Fatal(l.Reason)
	}
}

func invitesSetup(t *testing.T) (*db.DB, string, string) {
	d := openDB(t)
	owner := mustUser(t, d, "owner").ID
	ws := mustWS(t, d, "Team")
	mustMember(t, d, ws, owner, "owner")
	return d, owner, ws
}

func TestInviteCreatesUserAndMembershipOnce(t *testing.T) {
	d, owner, ws := invitesSetup(t)
	token, _, _ := users.CreateInvite(d, ws, "member", owner, now())
	if l, _ := users.ListInvites(d, ws, now()); len(l) != 1 {
		t.Fatal(l)
	}
	u, _, err := users.AcceptInviteWithNewUser(d, token, "eva", PW, now())
	if err != nil {
		t.Fatal(err)
	}
	if role(t, d, u.ID, ws) != "member" {
		t.Fatal("role")
	}
	if l, _ := users.ListInvites(d, ws, now()); len(l) != 0 {
		t.Fatal(l)
	}
	if _, _, err := users.AcceptInviteWithNewUser(d, token, "eva2", PW, now()); !isUserErr(err, "already used") {
		t.Fatal(err)
	}
	if l := lookup(t, d, token, now()); l.Reason != "used" {
		t.Fatal(l)
	}
}

func TestInviteRollsBackNewUser(t *testing.T) {
	d, owner, ws := invitesSetup(t)
	token, row, _ := users.CreateInvite(d, ws, "member", owner, now())
	users.RevokeInvite(d, ws, row.ID)
	if _, _, err := users.AcceptInviteWithNewUser(d, token, "ghost", PW, now()); !isUserErr(err, "revoked") {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM user WHERE username = 'ghost'`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestInviteExpiresAndUnknown(t *testing.T) {
	d, owner, ws := invitesSetup(t)
	at := now()
	token, _, _ := users.CreateInvite(d, ws, "admin", owner, at)
	if l := lookup(t, d, token, at+users.InviteTTLMs+1); l.Reason != "expired" {
		t.Fatal(l)
	}
	if l := lookup(t, d, "rci_nope", now()); l.Reason != "not_found" {
		t.Fatal(l)
	}
}

func TestExistingUserJoinsSecondWorkspace(t *testing.T) {
	d, owner, ws := invitesSetup(t)
	other := mustUser(t, d, "joiner")
	token, _, _ := users.CreateInvite(d, ws, "admin", owner, now())
	got, err := users.AcceptInviteAsUser(d, token, other.ID, now())
	if err != nil || got.ID != ws || role(t, d, other.ID, ws) != "admin" {
		t.Fatal(got, err)
	}
}

func TestOnlyOwnersInviteOwners(t *testing.T) {
	if !users.CanInvite("owner", "owner") || users.CanInvite("admin", "owner") || !users.CanInvite("admin", "admin") || users.CanInvite("member", "member") {
		t.Fatal("canInvite")
	}
}

func TestInviteNotRevokedFromAnotherWorkspace(t *testing.T) {
	d, owner, ws := invitesSetup(t)
	other := mustWS(t, d, "Other")
	_, row, _ := users.CreateInvite(d, ws, "member", owner, now())
	if ok, _ := users.RevokeInvite(d, other, row.ID); ok {
		t.Fatal("revoked from another workspace")
	}
	if ok, _ := users.RevokeInvite(d, ws, row.ID); !ok {
		t.Fatal("not revoked")
	}
}

func TestRolesProtectOwners(t *testing.T) {
	d := openDB(t)
	ws := mustWS(t, d, "Team")
	type ids struct{ user, member string }
	m := map[string]ids{}
	for _, x := range [][2]string{{"own", "owner"}, {"adm", "admin"}, {"mem", "member"}} {
		u := mustUser(t, d, x[0])
		m[x[0]] = ids{u.ID, mustMember(t, d, ws, u.ID, x[1]).ID}
	}
	admin := users.Actor{UserID: m["adm"].user, Role: "admin"}
	owner := users.Actor{UserID: m["own"].user, Role: "owner"}
	memberActor := users.Actor{UserID: m["mem"].user, Role: "member"}
	check := func(err error, sub string) {
		t.Helper()
		if !isUserErr(err, sub) {
			t.Fatalf("%v, want %q", err, sub)
		}
	}
	_, err := users.ChangeMemberRole(d, ws, admin, m["own"].member, "member")
	check(err, "Only owners")
	_, err = users.ChangeMemberRole(d, ws, admin, m["mem"].member, "owner")
	check(err, "Only owners")
	_, err = users.ChangeMemberRole(d, ws, memberActor, m["adm"].member, "member")
	check(err, "")
	_, err = users.ChangeMemberRole(d, ws, owner, m["own"].member, "admin")
	check(err, "at least one owner")
	_, err = users.RemoveMember(d, ws, admin, m["own"].member)
	check(err, "Only owners")
	_, err = users.RemoveMember(d, ws, memberActor, m["adm"].member)
	check(err, "")
	if _, err := users.ChangeMemberRole(d, ws, admin, m["mem"].member, "admin"); err != nil {
		t.Fatal(err)
	}
	if role(t, d, m["mem"].user, ws) != "admin" {
		t.Fatal("not promoted")
	}
	// Anyone can leave, except the last owner.
	if _, err := users.RemoveMember(d, ws, users.Actor{UserID: m["mem"].user, Role: "admin"}, m["mem"].member); err != nil {
		t.Fatal(err)
	}
	if role(t, d, m["mem"].user, ws) != "" {
		t.Fatal("still a member")
	}
	_, err = users.RemoveMember(d, ws, owner, m["own"].member)
	check(err, "at least one owner")
	// A member id from another workspace is "no such member".
	other := mustWS(t, d, "Other")
	_, err = users.ChangeMemberRole(d, other, owner, m["adm"].member, "member")
	check(err, "No such member")
}

func removedSetup(t *testing.T) (*db.DB, string, users.Actor) {
	d := openDB(t)
	ws := mustWS(t, d, "Team")
	u := mustUser(t, d, "owner")
	mustMember(t, d, ws, u.ID, "owner")
	return d, ws, users.Actor{UserID: u.ID, Role: "owner"}
}

func join(t *testing.T, d *db.DB, ws, name, r string) (string, string) {
	u := mustUser(t, d, name)
	return u.ID, mustMember(t, d, ws, u.ID, r).ID
}

func key(t *testing.T, d *db.DB, ws, name, by string) string {
	k, _, err := auth.CreateAPIKey(d, ws, name, &by)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func valid(t *testing.T, d *db.DB, k string) bool {
	a, err := auth.VerifyAPIKey(d, k, now())
	if err != nil {
		t.Fatal(err)
	}
	return a != nil
}

func TestRemovedMemberCantRejoin(t *testing.T) {
	d, ws, owner := removedSetup(t)
	adminUser, adminMember := join(t, d, ws, "adm", "admin")
	token, _, _ := users.CreateInvite(d, ws, "admin", adminUser, now())
	k := key(t, d, ws, "adm laptop", adminUser)
	ownersKey := key(t, d, ws, "owner", owner.UserID)
	removed, err := users.RemoveMember(d, ws, owner, adminMember)
	if err != nil || removed.RevokedKeys != 1 {
		t.Fatal(removed, err)
	}
	if l := lookup(t, d, token, now()); l.Reason != "revoked" {
		t.Fatal(l)
	}
	if _, err := users.AcceptInviteAsUser(d, token, adminUser, now()); !isUserErr(err, "revoked") {
		t.Fatal(err)
	}
	if role(t, d, adminUser, ws) != "" || valid(t, d, k) || !valid(t, d, ownersKey) {
		t.Fatal("removal incomplete")
	}
}

func TestDemotionRevokesOnlyUngrantableInvites(t *testing.T) {
	d, ws, owner := removedSetup(t)
	coUser, coMember := join(t, d, ws, "coowner", "owner")
	ownerLink, _, _ := users.CreateInvite(d, ws, "owner", coUser, now())
	adminLink, _, _ := users.CreateInvite(d, ws, "admin", coUser, now())
	users.ChangeMemberRole(d, ws, owner, coMember, "admin")
	if lookup(t, d, ownerLink, now()).Reason != "revoked" || lookup(t, d, adminLink, now()).Reason != "" {
		t.Fatal("after demotion to admin")
	}
	users.ChangeMemberRole(d, ws, owner, coMember, "member")
	if lookup(t, d, adminLink, now()).Reason != "revoked" {
		t.Fatal("after demotion to member")
	}
	if l, _ := users.ListInvites(d, ws, now()); len(l) != 0 {
		t.Fatal(l)
	}
}

func TestDemotedAdminLosesKeys(t *testing.T) {
	d, ws, owner := removedSetup(t)
	adminUser, adminMember := join(t, d, ws, "adm", "admin")
	coUser, coMember := join(t, d, ws, "coowner", "owner")
	adminKey := key(t, d, ws, "adm laptop", adminUser)
	coKey := key(t, d, ws, "co laptop", coUser)
	ownersKey := key(t, d, ws, "owner", owner.UserID)
	if c, _ := users.ChangeMemberRole(d, ws, owner, coMember, "admin"); c.RevokedKeys != 0 || !valid(t, d, coKey) {
		t.Fatal(c)
	}
	if c, _ := users.ChangeMemberRole(d, ws, owner, adminMember, "member"); c.RevokedKeys != 1 || valid(t, d, adminKey) || !valid(t, d, ownersKey) {
		t.Fatal(c)
	}
	// Promoting them back doesn't bring the old key back.
	users.ChangeMemberRole(d, ws, owner, adminMember, "admin")
	if valid(t, d, adminKey) {
		t.Fatal("key came back")
	}
}

func TestCreatorRoleRecheckedOnUse(t *testing.T) {
	d, ws, _ := removedSetup(t)
	adminUser, adminMember := join(t, d, ws, "adm", "admin")
	token, _, _ := users.CreateInvite(d, ws, "member", adminUser, now())
	// A role change that didn't go through ChangeMemberRole still counts.
	d.Exec(`UPDATE member SET role = 'member' WHERE id = ?`, adminMember)
	if _, _, err := users.AcceptInviteWithNewUser(d, token, "newbie", PW, now()); !isUserErr(err, "revoked") {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM user WHERE username = 'newbie'`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Team": "team", "  Ünïcode Team! ": "u-ni-code-team", "!!!": "workspace", strings.Repeat("ab", 30): strings.Repeat("ab", 20)} {
		if got := users.Slugify(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
