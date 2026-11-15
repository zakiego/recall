// Package users is users.ts: users, workspaces, memberships and invite links.
// Users are only ever created here: by the CLI, the first-boot bootstrap and
// invite acceptance.
package users

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/usernames"
)

// Error is UserError: a message for the person.
type Error struct{ Msg string }

func (e *Error) Error() string  { return e.Msg }
func (e *Error) JSName() string { return "Error" }

func userErr(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Roles.
const (
	Owner      = "owner"
	Admin      = "admin"
	MemberRole = "member"
)

const (
	LegacyWorkspaceID   = "ws_zaki"
	LegacyWorkspaceName = "Zaki"
	LegacyWorkspaceSlug = "zaki"
	LegacyUsername      = "zaki"
	InviteTTLMs         = 7 * 24 * 60 * 60 * 1000
	InvitePrefix        = "rci_"
)

// PasswordProblem is passwordProblem; "" when the password is fine.
func PasswordProblem(pw string) string {
	n := strx.Len16(pw)
	if n < auth.MinPasswordLength {
		return fmt.Sprintf("Use at least %d characters.", auth.MinPasswordLength)
	}
	if n > auth.MaxPasswordLength {
		return fmt.Sprintf("Use at most %d characters.", auth.MaxPasswordLength)
	}
	return ""
}

// User is a row of user (Better Auth's table; dates in ms).
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

func scanUser(s model.Scanner) (User, error) {
	var u User
	var verified int64
	var image, username, display sql.NullString
	err := s.Scan(&u.ID, &u.Name, &u.Email, &verified, &image, &username, &display, &u.CreatedAt, &u.UpdatedAt)
	u.EmailVerified = verified != 0
	u.Image, u.Username, u.DisplayUsername = model.Str(image), model.Str(username), model.Str(display)
	return u, err
}

// Count is userCount.
func Count(q db.Q) (int64, error) {
	var n int64
	err := q.QueryRow(`SELECT count(*) FROM user`).Scan(&n)
	return n, err
}

// FindByUsername is findUserByUsername.
func FindByUsername(q db.Q, username string) (User, bool, error) {
	u, err := scanUser(q.QueryRow(`SELECT `+userCols+` FROM user WHERE username = ?`, usernames.Normalize(username)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	return u, err == nil, err
}

var uniqueUser = regexp.MustCompile(`UNIQUE constraint failed: user\.(email|username)`)

// NewUser is insertUser's input.
type NewUser struct {
	Username     string
	PasswordHash string
	Name         string
}

// Insert is insertUser: the user and Better Auth's credential account. Hash
// the password first; run it in the transaction it belongs to.
func Insert(q db.Q, in NewUser) (User, error) {
	if p := usernames.Problem(in.Username); p != "" {
		return User{}, userErr("Username: %s", p)
	}
	username := usernames.Normalize(in.Username)
	taken := func() error { return userErr("That username is taken.") }
	// The email check catches a user renamed before usernames were immutable,
	// who still holds this username's placeholder email.
	if _, found, err := FindByUsername(q, username); err != nil || found {
		if err != nil {
			return User{}, err
		}
		return User{}, taken()
	}
	var existing string
	switch err := q.QueryRow(`SELECT id FROM user WHERE email = ?`, usernames.PlaceholderEmail(username)).Scan(&existing); {
	case err == nil:
		return User{}, taken()
	case !errors.Is(err, sql.ErrNoRows):
		return User{}, err
	}
	now := time.Now().UnixMilli()
	id := ids.New(ids.User)
	name := strx.Trim(in.Name)
	if name == "" {
		name = strx.Trim(in.Username)
	}
	u, err := scanUser(q.WQueryRow(`INSERT INTO user (id, name, email, email_verified, username, display_username, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?, ?, ?) RETURNING `+userCols,
		id, name, usernames.PlaceholderEmail(username), username, strx.Trim(in.Username), now, now))
	if err != nil {
		// Anything that slipped past the checks is still a taken name, not a 500.
		if uniqueUser.MatchString(err.Error()) {
			return User{}, taken()
		}
		return User{}, err
	}
	// Better Auth's credential account: account_id is the user id.
	if _, err := q.Exec(`INSERT INTO account (id, account_id, provider_id, user_id, password, created_at, updated_at) VALUES (?, ?, 'credential', ?, ?, ?, ?)`,
		ids.New(ids.Account), id, id, in.PasswordHash, now, now); err != nil {
		return User{}, err
	}
	return u, nil
}

// CreateWithPassword is createUserWithPassword.
func CreateWithPassword(d *db.DB, username, password, name string) (User, error) {
	if p := PasswordProblem(password); p != "" {
		return User{}, userErr("Password: %s", p)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = d.Tx(func(tx db.Q) error {
		var err error
		u, err = Insert(tx, NewUser{Username: username, PasswordHash: hash, Name: name})
		return err
	})
	return u, err
}

// --- workspaces ---------------------------------------------------------------------

var (
	notSlug   = regexp.MustCompile(`[^a-z0-9]+`)
	edgeDashs = regexp.MustCompile(`^-+|-+$`)
)

// Slugify is slugify.
func Slugify(name string) string {
	s := strx.NFKD(strx.Lower(name))
	s = notSlug.ReplaceAllString(s, "-")
	s = edgeDashs.ReplaceAllString(s, "")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "workspace"
	}
	return s
}

func uniqueSlug(q db.Q, name string) (string, error) {
	base := Slugify(name)
	for i := 0; i < 1000; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		var id string
		err := q.QueryRow(`SELECT id FROM organization WHERE slug = ?`, slug).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return slug, nil
		}
		if err != nil {
			return "", err
		}
	}
	return base + "-" + strx.Lower(auth.RandomToken(6)), nil
}

// Workspace is a row of organization.
type Workspace struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Logo      *string `json:"logo"`
	Metadata  *string `json:"metadata"`
	CreatedAt int64   `json:"createdAt"`
}

const workspaceCols = "id, name, slug, logo, metadata, created_at"

func scanWorkspace(s model.Scanner) (Workspace, error) {
	var w Workspace
	var logo, meta sql.NullString
	err := s.Scan(&w.ID, &w.Name, &w.Slug, &logo, &meta, &w.CreatedAt)
	w.Logo, w.Metadata = model.Str(logo), model.Str(meta)
	return w, err
}

// CreateWorkspace is createWorkspace; id and slug "" are generated.
func CreateWorkspace(q db.Q, name, id, slug string) (Workspace, error) {
	name = strx.Trim(name)
	if name == "" || strx.Len16(name) > 80 {
		return Workspace{}, userErr("Name the workspace (up to 80 characters).")
	}
	if id == "" {
		id = ids.New(ids.Workspace)
	}
	if slug == "" {
		var err error
		if slug, err = uniqueSlug(q, name); err != nil {
			return Workspace{}, err
		}
	}
	return scanWorkspace(q.WQueryRow(`INSERT INTO organization (id, name, slug, created_at) VALUES (?, ?, ?, ?) RETURNING `+workspaceCols, id, name, slug, time.Now().UnixMilli()))
}

// FindWorkspace is findWorkspace: by id, else by slug.
func FindWorkspace(q db.Q, idOrSlug string) (Workspace, bool, error) {
	w, err := scanWorkspace(q.QueryRow(`SELECT `+workspaceCols+` FROM organization WHERE id = ?`, idOrSlug))
	if errors.Is(err, sql.ErrNoRows) {
		w, err = scanWorkspace(q.QueryRow(`SELECT `+workspaceCols+` FROM organization WHERE slug = ?`, strx.Lower(idOrSlug)))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, false, nil
	}
	return w, err == nil, err
}

// Member is a row of member.
type Member struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	UserID         string `json:"userId"`
	Role           string `json:"role"`
	CreatedAt      int64  `json:"createdAt"`
}

const memberCols = "id, organization_id, user_id, role, created_at"

func scanMember(s model.Scanner) (Member, error) {
	var m Member
	err := s.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt)
	return m, err
}

// AddMember is addMember: the existing membership, or a new one.
func AddMember(q db.Q, workspaceID, userID, role string) (Member, error) {
	m, err := scanMember(q.WQueryRow(`SELECT `+memberCols+` FROM member WHERE organization_id = ? AND user_id = ?`, workspaceID, userID))
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	return scanMember(q.WQueryRow(`INSERT INTO member (id, organization_id, user_id, role, created_at) VALUES (?, ?, ?, ?, ?) RETURNING `+memberCols,
		ids.New(ids.Member), workspaceID, userID, role, time.Now().UnixMilli()))
}

// Membership is one workspace a user belongs to.
type Membership struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

// ListMemberships is listMemberships: oldest membership first.
func ListMemberships(q db.Q, userID string) ([]Membership, error) {
	rows, err := q.Query(`SELECT o.id, o.name, o.slug, m.role FROM member m JOIN organization o ON o.id = m.organization_id WHERE m.user_id = ? ORDER BY m.created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.Name, &m.Slug, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetRole is getRole; "" when the user isn't a member.
func GetRole(q db.Q, userID, workspaceID string) (string, error) {
	var role string
	err := q.QueryRow(`SELECT role FROM member WHERE user_id = ? AND organization_id = ?`, userID, workspaceID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// MemberView is one row of listMembers.
type MemberView struct {
	MemberID        string  `json:"memberId"`
	UserID          string  `json:"userId"`
	Username        *string `json:"username"`
	DisplayUsername *string `json:"displayUsername"`
	Name            string  `json:"name"`
	Role            string  `json:"role"`
	JoinedAt        int64   `json:"joinedAt"`
}

// ListMembers is listMembers: oldest first.
func ListMembers(q db.Q, workspaceID string) ([]MemberView, error) {
	rows, err := q.Query(`SELECT m.id, u.id, u.username, u.display_username, u.name, m.role, m.created_at FROM member m JOIN user u ON u.id = m.user_id WHERE m.organization_id = ? ORDER BY m.created_at ASC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberView{}
	for rows.Next() {
		var v MemberView
		var username, display sql.NullString
		if err := rows.Scan(&v.MemberID, &v.UserID, &username, &display, &v.Name, &v.Role, &v.JoinedAt); err != nil {
			return nil, err
		}
		v.Username, v.DisplayUsername = model.Str(username), model.Str(display)
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- invites ---------------------------------------------------------------------------

// Invite is a row of workspace_invites.
type Invite struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspaceId"`
	Role        string  `json:"role"`
	TokenHash   string  `json:"tokenHash"`
	CreatedBy   *string `json:"createdBy"`
	CreatedAt   int64   `json:"createdAt"`
	ExpiresAt   int64   `json:"expiresAt"`
	AcceptedAt  *int64  `json:"acceptedAt"`
	AcceptedBy  *string `json:"acceptedBy"`
	RevokedAt   *int64  `json:"revokedAt"`
}

const inviteCols = "id, workspace_id, role, token_hash, created_by, created_at, expires_at, accepted_at, accepted_by, revoked_at"

func scanInvite(s model.Scanner) (Invite, error) {
	var i Invite
	var by, acceptedBy sql.NullString
	var accepted, revoked sql.NullInt64
	err := s.Scan(&i.ID, &i.WorkspaceID, &i.Role, &i.TokenHash, &by, &i.CreatedAt, &i.ExpiresAt, &accepted, &acceptedBy, &revoked)
	i.CreatedBy, i.AcceptedBy = model.Str(by), model.Str(acceptedBy)
	i.AcceptedAt, i.RevokedAt = model.Int(accepted), model.Int(revoked)
	return i, err
}

// CreateInvite is createInvite: the token is shown once, only its hash is
// stored.
func CreateInvite(q db.Q, workspaceID, role, createdBy string, now int64) (string, Invite, error) {
	token := InvitePrefix + auth.RandomToken(32)
	row, err := scanInvite(q.WQueryRow(`INSERT INTO workspace_invites (id, workspace_id, role, token_hash, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING `+inviteCols,
		ids.New(ids.Invite), workspaceID, role, auth.Sha256(token), createdBy, now, now+InviteTTLMs))
	return token, row, err
}

// InviteView is one row of listInvites.
type InviteView struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	CreatedAt int64   `json:"createdAt"`
	ExpiresAt int64   `json:"expiresAt"`
	CreatedBy *string `json:"createdBy"`
}

// ListInvites is listInvites: open invites, newest first.
func ListInvites(q db.Q, workspaceID string, now int64) ([]InviteView, error) {
	rows, err := q.Query(`SELECT i.id, i.role, i.created_at, i.expires_at, u.display_username FROM workspace_invites i LEFT JOIN user u ON u.id = i.created_by
		WHERE i.workspace_id = ? AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > ? ORDER BY i.created_at DESC`, workspaceID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InviteView{}
	for rows.Next() {
		var v InviteView
		var by sql.NullString
		if err := rows.Scan(&v.ID, &v.Role, &v.CreatedAt, &v.ExpiresAt, &by); err != nil {
			return nil, err
		}
		v.CreatedBy = model.Str(by)
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeInvite is revokeInvite.
func RevokeInvite(q db.Q, workspaceID, id string) (bool, error) {
	n, err := db.Changes(q.Exec(`UPDATE workspace_invites SET revoked_at = ? WHERE id = ? AND workspace_id = ? AND accepted_at IS NULL AND revoked_at IS NULL`, time.Now().UnixMilli(), id, workspaceID))
	return n > 0, err
}

// Lookup is lookupInvite's result: Reason "" when the invite is good.
type Lookup struct {
	Reason        string
	Invite        Invite
	WorkspaceID   string
	WorkspaceName string
}

// LookupInvite is lookupInvite.
func LookupInvite(q db.Q, token string, now int64) (Lookup, error) {
	if !strings.HasPrefix(token, InvitePrefix) {
		return Lookup{Reason: "not_found"}, nil
	}
	inv, err := scanInvite(q.QueryRow(`SELECT `+inviteCols+` FROM workspace_invites WHERE token_hash = ?`, auth.Sha256(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return Lookup{Reason: "not_found"}, nil
	}
	if err != nil {
		return Lookup{}, err
	}
	switch {
	case inv.RevokedAt != nil && *inv.RevokedAt != 0:
		return Lookup{Reason: "revoked"}, nil
	case inv.AcceptedAt != nil && *inv.AcceptedAt != 0:
		return Lookup{Reason: "used"}, nil
	case inv.ExpiresAt <= now:
		return Lookup{Reason: "expired"}, nil
	}
	var wsID, wsName string
	err = q.QueryRow(`SELECT id, name FROM organization WHERE id = ?`, inv.WorkspaceID).Scan(&wsID, &wsName)
	if errors.Is(err, sql.ErrNoRows) {
		return Lookup{Reason: "not_found"}, nil
	}
	if err != nil {
		return Lookup{}, err
	}
	// A link is only as good as its creator's current role.
	creatorRole := ""
	if inv.CreatedBy != nil && *inv.CreatedBy != "" {
		if creatorRole, err = GetRole(q, *inv.CreatedBy, inv.WorkspaceID); err != nil {
			return Lookup{}, err
		}
	}
	if creatorRole == "" || !CanInvite(creatorRole, inv.Role) {
		return Lookup{Reason: "revoked"}, nil
	}
	return Lookup{Invite: inv, WorkspaceID: wsID, WorkspaceName: wsName}, nil
}

var inviteProblems = map[string]string{
	"not_found": "This invite link isn't valid.",
	"used":      "This invite link was already used.",
	"revoked":   "This invite link was revoked.",
	"expired":   "This invite link expired. Ask for a new one.",
}

// InviteProblem is inviteProblem.
func InviteProblem(reason string) string { return inviteProblems[reason] }

// claimInvite: single use, claimed and the membership written in one
// transaction (the caller's).
func claimInvite(q db.Q, token, userID string, now int64) (string, string, error) {
	found, err := LookupInvite(q, token, now)
	if err != nil {
		return "", "", err
	}
	if found.Reason != "" {
		return "", "", &Error{InviteProblem(found.Reason)}
	}
	n, err := db.Changes(q.Exec(`UPDATE workspace_invites SET accepted_at = ?, accepted_by = ? WHERE id = ? AND accepted_at IS NULL AND revoked_at IS NULL`, now, userID, found.Invite.ID))
	if err != nil {
		return "", "", err
	}
	if n != 1 {
		return "", "", &Error{InviteProblem("used")}
	}
	if _, err := AddMember(q, found.Invite.WorkspaceID, userID, found.Invite.Role); err != nil {
		return "", "", err
	}
	return found.WorkspaceID, found.WorkspaceName, nil
}

// WorkspaceRef is {id, name}.
type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AcceptInviteAsUser is acceptInviteAsUser.
func AcceptInviteAsUser(d *db.DB, token, userID string, now int64) (WorkspaceRef, error) {
	var ws WorkspaceRef
	err := d.Tx(func(tx db.Q) error {
		id, name, err := claimInvite(tx, token, userID, now)
		ws = WorkspaceRef{id, name}
		return err
	})
	return ws, err
}

// AcceptInviteWithNewUser is acceptInviteWithNewUser.
func AcceptInviteWithNewUser(d *db.DB, token, username, password string, now int64) (User, WorkspaceRef, error) {
	pre, err := LookupInvite(d, token, now)
	if err != nil {
		return User{}, WorkspaceRef{}, err
	}
	if pre.Reason != "" {
		return User{}, WorkspaceRef{}, &Error{InviteProblem(pre.Reason)}
	}
	if p := PasswordProblem(password); p != "" {
		return User{}, WorkspaceRef{}, userErr("Password: %s", p)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return User{}, WorkspaceRef{}, err
	}
	var u User
	var ws WorkspaceRef
	err = d.Tx(func(tx db.Q) error {
		var err error
		if u, err = Insert(tx, NewUser{Username: username, PasswordHash: hash}); err != nil {
			return err
		}
		id, name, err := claimInvite(tx, token, u.ID, now)
		ws = WorkspaceRef{id, name}
		return err
	})
	return u, ws, err
}

// --- first boot ------------------------------------------------------------------------

// Bootstrap is bootstrapUsers' result: Reason is "users_exist" or
// "no_admin_hash" when nothing was created.
type Bootstrap struct {
	Created     bool
	Reason      string
	UserID      string
	WorkspaceID string
}

// BootstrapUsers is bootstrapUsers: on the first boot after migration 0004
// the old single admin becomes user `zaki`, owner of workspace "Zaki". Does
// nothing once any user exists. adminHash nil reads the config.
func BootstrapUsers(d *db.DB, adminHash *string) (Bootstrap, error) {
	hash := config.Load().AdminPasswordHash
	if adminHash != nil {
		hash = *adminHash
	}
	var out Bootstrap
	err := d.Tx(func(tx db.Q) error {
		n, err := Count(tx)
		if err != nil {
			return err
		}
		if n > 0 {
			out = Bootstrap{Reason: "users_exist"}
			return nil
		}
		if strx.Trim(hash) == "" {
			out = Bootstrap{Reason: "no_admin_hash"}
			return nil
		}
		u, err := Insert(tx, NewUser{Username: LegacyUsername, Name: "Zaki", PasswordHash: strx.Trim(hash)})
		if err != nil {
			return err
		}
		ws, found, err := FindWorkspace(tx, LegacyWorkspaceID)
		if err != nil {
			return err
		}
		if !found {
			if ws, err = CreateWorkspace(tx, LegacyWorkspaceName, LegacyWorkspaceID, LegacyWorkspaceSlug); err != nil {
				return err
			}
		}
		if _, err := AddMember(tx, ws.ID, u.ID, Owner); err != nil {
			return err
		}
		out = Bootstrap{Created: true, UserID: u.ID, WorkspaceID: ws.ID}
		return nil
	})
	return out, err
}

// --- member management -------------------------------------------------------------------

// Actor is who is acting.
type Actor struct {
	UserID string
	Role   string
}

func memberIn(q db.Q, workspaceID, memberID string) (Member, bool, error) {
	m, err := scanMember(q.WQueryRow(`SELECT `+memberCols+` FROM member WHERE id = ? AND organization_id = ?`, memberID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, false, nil
	}
	return m, err == nil, err
}

func ownerCount(q db.Q, workspaceID string) (int64, error) {
	var n int64
	err := q.WQueryRow(`SELECT count(*) FROM member WHERE organization_id = ? AND role = 'owner'`, workspaceID).Scan(&n)
	return n, err
}

// revokeInvitesBy revokes the open invites userID made in the workspace,
// those whose role which accepts (nil is all).
func revokeInvitesBy(q db.Q, workspaceID, userID string, which func(role string) bool) (int, error) {
	rows, err := q.WQuery(`SELECT id, role FROM workspace_invites WHERE workspace_id = ? AND created_by = ? AND accepted_at IS NULL AND revoked_at IS NULL`, workspaceID, userID)
	if err != nil {
		return 0, err
	}
	var pending []any
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			rows.Close()
			return 0, err
		}
		if which == nil || which(role) {
			pending = append(pending, id)
		}
	}
	rows.Close()
	if len(pending) == 0 {
		return 0, nil
	}
	args := append([]any{time.Now().UnixMilli()}, pending...)
	_, err = q.Exec(`UPDATE workspace_invites SET revoked_at = ? WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(pending)), ", ")+`)`, args...)
	return len(pending), err
}

func revokeKeysBy(q db.Q, workspaceID, userID string) (int64, error) {
	return db.Changes(q.Exec(`UPDATE api_keys SET revoked_at = ? WHERE workspace_id = ? AND created_by = ? AND revoked_at IS NULL`, time.Now().UnixMilli(), workspaceID, userID))
}

// MemberChange is the changed or removed member with the keys revoked.
type MemberChange struct {
	Member
	RevokedKeys int64 `json:"revokedKeys"`
}

// ChangeMemberRole is changeMemberRole.
func ChangeMemberRole(d *db.DB, workspaceID string, actor Actor, memberID, role string) (MemberChange, error) {
	var out MemberChange
	err := d.Tx(func(tx db.Q) error {
		target, ok, err := memberIn(tx, workspaceID, memberID)
		if err != nil {
			return err
		}
		if !ok {
			return userErr("No such member.")
		}
		if actor.Role == MemberRole {
			return userErr("Only owners and admins can change roles.")
		}
		if actor.Role != Owner && (target.Role == Owner || role == Owner) {
			return userErr("Only owners can make or change owners.")
		}
		if target.Role == Owner && role != Owner {
			n, err := ownerCount(tx, workspaceID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return userErr("A workspace needs at least one owner. Make someone else an owner first.")
			}
		}
		if _, err := tx.Exec(`UPDATE member SET role = ? WHERE id = ?`, role, target.ID); err != nil {
			return err
		}
		// Links they made at a role they can no longer hand out stop working,
		// and so do keys they made once their new role can't create keys.
		if _, err := revokeInvitesBy(tx, workspaceID, target.UserID, func(invited string) bool { return !CanInvite(role, invited) }); err != nil {
			return err
		}
		var revoked int64
		if !CanManageKeys(role) {
			if revoked, err = revokeKeysBy(tx, workspaceID, target.UserID); err != nil {
				return err
			}
		}
		target.Role = role
		out = MemberChange{Member: target, RevokedKeys: revoked}
		return nil
	})
	return out, err
}

// RemoveMember is removeMember.
func RemoveMember(d *db.DB, workspaceID string, actor Actor, memberID string) (MemberChange, error) {
	var out MemberChange
	err := d.Tx(func(tx db.Q) error {
		target, ok, err := memberIn(tx, workspaceID, memberID)
		if err != nil {
			return err
		}
		if !ok {
			return userErr("No such member.")
		}
		self := target.UserID == actor.UserID
		if !self && actor.Role == MemberRole {
			return userErr("Only owners and admins can remove members.")
		}
		if !self && actor.Role != Owner && target.Role == Owner {
			return userErr("Only owners can remove an owner.")
		}
		if target.Role == Owner {
			n, err := ownerCount(tx, workspaceID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return userErr("A workspace needs at least one owner.")
			}
		}
		if _, err := tx.Exec(`DELETE FROM member WHERE id = ?`, target.ID); err != nil {
			return err
		}
		// Without this, a saved invite link of their own would let them back
		// in, and keys they made would keep reading the workspace.
		if _, err := revokeInvitesBy(tx, workspaceID, target.UserID, nil); err != nil {
			return err
		}
		revoked, err := revokeKeysBy(tx, workspaceID, target.UserID)
		if err != nil {
			return err
		}
		out = MemberChange{Member: target, RevokedKeys: revoked}
		return nil
	})
	return out, err
}

// CanManageKeys is canManageKeys: admins and owners.
func CanManageKeys(role string) bool { return role != MemberRole }

// CanInvite is canInvite.
func CanInvite(actor, role string) bool {
	if actor == MemberRole {
		return false
	}
	return role != Owner || actor == Owner
}

// --- passkeys ------------------------------------------------------------------------

// Passkey is one row of listPasskeys.
type Passkey struct {
	ID         string  `json:"id"`
	Name       *string `json:"name"`
	DeviceType string  `json:"deviceType"`
	BackedUp   bool    `json:"backedUp"`
	CreatedAt  *int64  `json:"createdAt"`
}

// ListPasskeys is listPasskeys: newest first.
func ListPasskeys(q db.Q, userID string) ([]Passkey, error) {
	rows, err := q.Query(`SELECT id, name, device_type, backed_up, created_at FROM passkey WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		var p Passkey
		var name sql.NullString
		var backed int64
		var created sql.NullInt64
		if err := rows.Scan(&p.ID, &name, &p.DeviceType, &backed, &created); err != nil {
			return nil, err
		}
		p.Name, p.BackedUp, p.CreatedAt = model.Str(name), backed != 0, model.Int(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePasskey is deletePasskey.
func DeletePasskey(q db.Q, userID, id string) (bool, error) {
	n, err := db.Changes(q.Exec(`DELETE FROM passkey WHERE id = ? AND user_id = ?`, id, userID))
	return n > 0, err
}

// --- credentials ------------------------------------------------------------------------

// credentialHash is the password hash on the user's credential account.
func credentialHash(q db.Q, userID string) (string, bool, error) {
	var hash sql.NullString
	err := q.QueryRow(`SELECT password FROM account WHERE user_id = ? AND provider_id = 'credential'`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !hash.Valid) {
		return "", false, nil
	}
	return hash.String, err == nil, err
}

// VerifyCredentials is the username sign-in check (Better Auth's username
// plugin): the user by normalized username, and their credential
// account's password.
func VerifyCredentials(q db.Q, username, password string) (User, bool, error) {
	u, found, err := FindByUsername(q, username)
	if err != nil {
		return User{}, false, err
	}
	if !found {
		// Hash anyway, so the answer takes as long as for a real user.
		auth.HashPassword(password)
		return User{}, false, nil
	}
	hash, ok, err := credentialHash(q, u.ID)
	if err != nil || !ok {
		return User{}, false, err
	}
	return u, auth.VerifyPassword(hash, password), nil
}

// CheckPassword reports whether password is the user's current one.
func CheckPassword(q db.Q, userID, password string) (bool, error) {
	hash, ok, err := credentialHash(q, userID)
	if err != nil || !ok {
		return false, err
	}
	return auth.VerifyPassword(hash, password), nil
}

// SetPassword stores a new password hash on the credential account. now is ms.
func SetPassword(q db.Q, userID, hash string, now int64) error {
	_, err := q.Exec(`UPDATE account SET password = ?, updated_at = ? WHERE user_id = ? AND provider_id = 'credential'`, hash, now, userID)
	return err
}
