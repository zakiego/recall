// Package cli is admin-cli.ts: the admin commands. Only the command's
// result goes to Out, so --quiet and --json output can be piped.
package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/backfill"
	"github.com/zakiego/recall/apps/server/internal/backup"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
)

// Usage is USAGE.
const Usage = `usage: cli <command> [--json]
  create-user <username> [--workspace <slug> [--new-workspace]] [--role owner|admin|member]
                                password from stdin or a prompt. --workspace joins an
                                existing workspace; add --new-workspace to create it
                                instead. Without --workspace, a new workspace named
                                after the user. A new workspace needs --role owner
                                (the default)
  list-users [--workspace <slug>]
  create-key <name> [--workspace <slug>] [--quiet]
                                print a new API key (--quiet: the key only). --workspace
                                can be left out when there is only one workspace
  list-keys [--workspace <slug>]
  revoke-key <id>               revoke an API key
  hash-password                 pbkdf2 hash for ADMIN_PASSWORD_HASH (stdin or prompt)
  backup <dest-path>            gzipped online snapshot of the database, safe while the app runs
                                ("-" writes the gzip to stdout)
  prune-backups <dir> <name-prefix> <keep>
                                keep the newest <keep> files named <name-prefix>*, delete the rest
  backfill-dates [--workspace <slug>] [--container <name>]
                                one-off: queue jobs that ask the gateway to date current
                                facts that have no dates yet, in batches. Every workspace
                                unless --workspace. The running server's job loop does the
                                work; progress shows in its log as [recall:backfill]
  backfill-people [--workspace <slug>] [--container <name>] [--now]
                                link current facts to people. Queues one job per
                                container for the app's job loop (one gateway call
                                at a time); --now runs it in this process instead.
                                Facts already checked are skipped, so reruns are cheap
  suggest-people-merges [--workspace <slug>] [--container <name>]
                                read-only: list people who are likely the same person
                                (a shared email or handle, or look-alike names the
                                embedding agrees on). Merges nothing: merge them on
                                the People page`

// IO is CliIo. Backfill, when set, replaces the people backfill of
// backfill-people --now (tests).
type IO struct {
	DB           func() *db.DB
	Out          func(string)
	Err          func(string)
	ReadPassword func() (string, error)
	Backfill     func(d *db.DB, scope search.Scope) (people.BackfillStats, error)
	// Stdout takes raw bytes (`backup -`).
	Stdout interface{ Write([]byte) (int, error) }
}

var (
	boolFlags  = map[string]bool{"--json": true, "--quiet": true, "-q": true, "--help": true, "--new-workspace": true, "--now": true}
	valueFlags = map[string]bool{"--workspace": true, "--role": true, "--container": true}
)

type parsed struct {
	positionals []string
	bools       map[string]bool
	values      map[string]string
	unknown     []string
}

func parse(argv []string) parsed {
	p := parsed{bools: map[string]bool{}, values: map[string]string{}}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		name, inline, hasInline := a, "", false
		if strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
			k := strings.Index(a, "=")
			name, inline, hasInline = a[:k], a[k+1:], true
		}
		switch {
		case valueFlags[name]:
			switch {
			case hasInline:
				p.values[name] = inline
			case i+1 < len(argv):
				i++
				p.values[name] = argv[i]
			default:
				p.unknown = append(p.unknown, name+" needs a value")
			}
		case boolFlags[a]:
			p.bools[a] = true
		case strings.HasPrefix(a, "--"):
			p.unknown = append(p.unknown, a)
		default:
			p.positionals = append(p.positionals, a)
		}
	}
	return p
}

type userErr struct{ msg string }

func (e *userErr) Error() string { return e.msg }

func iso(ms *int64) string {
	if ms == nil {
		return ""
	}
	return time.UnixMilli(*ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func allWorkspaces(q db.Q) ([]users.Workspace, error) {
	rows, err := q.Query(`SELECT id, slug FROM organization`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []users.Workspace
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, err
		}
		w, _, err := users.FindWorkspace(q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Run is runCli: the exit code.
func Run(argv []string, io IO) (code int) {
	p := parse(argv)
	cmd, args := "", []string{}
	if len(p.positionals) > 0 {
		cmd, args = p.positionals[0], p.positionals[1:]
	}
	json := p.bools["--json"]
	quiet := p.bools["--quiet"] || p.bools["-q"]
	if len(p.unknown) > 0 {
		io.Err("unknown flag: " + strings.Join(p.unknown, " ") + "\n" + Usage)
		return 1
	}
	// The named workspace, or the only one when there's exactly one.
	workspace := func(required bool) (*users.Workspace, error) {
		d := io.DB()
		if slug, ok := p.values["--workspace"]; ok && slug != "" {
			ws, found, err := users.FindWorkspace(d, slug)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, &userErr{fmt.Sprintf(`no workspace "%s"`, slug)}
			}
			return &ws, nil
		}
		all, err := allWorkspaces(d)
		if err != nil {
			return nil, err
		}
		if len(all) == 1 {
			return &all[0], nil
		}
		if !required {
			return nil, nil
		}
		if len(all) == 0 {
			return nil, &userErr{"no workspaces yet: run create-user first"}
		}
		slugs := make([]string, len(all))
		for i, w := range all {
			slugs[i] = w.Slug
		}
		return nil, &userErr{"pass --workspace (one of: " + strings.Join(slugs, ", ") + ")"}
	}
	targets := func() ([]users.Workspace, error) {
		if p.values["--workspace"] != "" {
			ws, err := workspace(true)
			if err != nil {
				return nil, err
			}
			return []users.Workspace{*ws}, nil
		}
		return allWorkspaces(io.DB())
	}
	err := func() error {
		switch cmd {
		case "create-user":
			return createUser(io, p, args, json, &code)
		case "list-users":
			d := io.DB()
			if p.values["--workspace"] != "" {
				ws, err := workspace(true)
				if err != nil {
					return err
				}
				rows, err := users.ListMembers(d, ws.ID)
				if err != nil {
					return err
				}
				if json {
					io.Out(jsonx.Stringify(rows))
				} else {
					for _, m := range rows {
						io.Out(fmt.Sprintf("%s  %s  joined %s", str(m.Username), m.Role, iso(&m.JoinedAt)))
					}
				}
				return nil
			}
			rs, err := d.Query(`SELECT id, username FROM user ORDER BY created_at`)
			if err != nil {
				return err
			}
			type ref struct {
				Slug string `json:"slug"`
				Role string `json:"role"`
			}
			type row struct {
				Username   *string `json:"username"`
				Workspaces []ref   `json:"workspaces"`
			}
			var list []row
			var userIDs []string
			for rs.Next() {
				var id string
				var username sql.NullString
				rs.Scan(&id, &username)
				var u *string
				if username.Valid {
					u = &username.String
				}
				list = append(list, row{Username: u})
				userIDs = append(userIDs, id)
			}
			rs.Close()
			for i, id := range userIDs {
				ms, err := users.ListMemberships(d, id)
				if err != nil {
					return err
				}
				list[i].Workspaces = []ref{}
				for _, m := range ms {
					list[i].Workspaces = append(list[i].Workspaces, ref{m.Slug, m.Role})
				}
			}
			switch {
			case json:
				if list == nil {
					list = []row{}
				}
				io.Out(jsonx.Stringify(list))
			case len(list) == 0:
				io.Out("no users")
			default:
				for _, r := range list {
					var parts []string
					for _, w := range r.Workspaces {
						parts = append(parts, w.Slug+":"+w.Role)
					}
					ws := strings.Join(parts, ", ")
					if ws == "" {
						ws = "(no workspace)"
					}
					io.Out(str(r.Username) + "  " + ws)
				}
			}
			return nil
		case "create-key":
			ws, err := workspace(true)
			if err != nil {
				return err
			}
			name := strings.Join(args, " ")
			if name == "" {
				name = "cli"
			}
			key, row, err := auth.CreateAPIKey(io.DB(), ws.ID, name, nil)
			if err != nil {
				return err
			}
			switch {
			case json:
				io.Out(jsonx.Stringify(jsonx.ObjectOf("id", row.ID, "name", row.Name, "prefix", row.Prefix, "workspace", ws.Slug, "key", key)))
			case quiet:
				io.Out(key)
			default:
				io.Out(fmt.Sprintf("created %s (%s) in %s\n%s", row.ID, row.Name, ws.Slug, key))
			}
			return nil
		case "list-keys":
			ws, err := workspace(true)
			if err != nil {
				return err
			}
			rows, err := auth.ListAPIKeys(io.DB(), ws.ID)
			if err != nil {
				return err
			}
			switch {
			case json:
				io.Out(jsonx.Stringify(rows))
			case len(rows) == 0:
				io.Out("no keys")
			default:
				for _, r := range rows {
					status := "active"
					if r.RevokedAt != nil && *r.RevokedAt != 0 {
						status = "revoked " + iso(r.RevokedAt)
					}
					used := "never used"
					if r.LastUsedAt != nil && *r.LastUsedAt != 0 {
						used = "last used " + iso(r.LastUsedAt)
					}
					io.Out(fmt.Sprintf("%s  %s…  %s  created %s  %s  %s", r.ID, r.Prefix, r.Name, iso(&r.CreatedAt), used, status))
				}
			}
			return nil
		case "revoke-key":
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			d := io.DB()
			ok := false
			if id != "" {
				// An operator on the box may revoke any key; look up its workspace first.
				var ws string
				err := d.QueryRow(`SELECT workspace_id FROM api_keys WHERE id = ?`, id).Scan(&ws)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil {
					if ok, err = auth.RevokeAPIKey(d, ws, id, time.Now().UnixMilli()); err != nil {
						return err
					}
				}
			}
			switch {
			case json:
				io.Out(jsonx.Stringify(jsonx.ObjectOf("id", id, "revoked", ok)))
			case ok:
				io.Out("revoked")
			default:
				io.Err("no such active key")
			}
			if !ok {
				code = 1
			}
			return nil
		case "hash-password":
			pw, err := io.ReadPassword()
			if err != nil {
				return err
			}
			if strx.Len16(pw) < 12 {
				io.Err("password must be at least 12 characters")
				code = 1
				return nil
			}
			hash, err := auth.HashLegacyPassword(pw)
			if err != nil {
				return err
			}
			if json {
				io.Out(jsonx.Stringify(jsonx.ObjectOf("hash", hash)))
			} else {
				io.Out(hash)
			}
			return nil
		case "backup":
			// Reads the file directly: no migrations, so it never changes the live DB.
			if len(args) == 0 || args[0] == "" {
				io.Err("usage: backup <dest-path>   (e.g. /data/backups/recall.db.gz)")
				code = 1
				return nil
			}
			if args[0] == "-" {
				// The gzip on stdout (the Go image has no shell to copy it out with).
				res, err := backup.ToWriter(config.Load().DBPath, io.Stdout)
				if err != nil {
					return err
				}
				io.Err(fmt.Sprintf("backup - %d bytes (db %d bytes)", res.Bytes, res.DBBytes))
				return nil
			}
			res, err := backup.DB(config.Load().DBPath, args[0])
			if err != nil {
				return err
			}
			if json {
				io.Out(jsonx.Stringify(res))
			} else {
				io.Out(fmt.Sprintf("backup %s %d bytes (db %d bytes)", res.Dest, res.Bytes, res.DBBytes))
			}
			return nil
		case "prune-backups":
			// Keeps the newest <keep> files named <prefix>* in <dir> (pre-deploy.sh).
			keep, convErr := strconv.Atoi(argAt(args, 2))
			if len(args) < 3 || convErr != nil || keep < 0 {
				io.Err("usage: prune-backups <dir> <name-prefix> <keep>")
				code = 1
				return nil
			}
			removed, err := backup.Prune(args[0], args[1], keep)
			for _, n := range removed {
				io.Out("removed " + n)
			}
			return err
		case "backfill-people":
			return backfillPeople(io, p, targets, json)
		case "suggest-people-merges":
			return suggestMerges(io, p, targets, json)
		case "backfill-dates":
			d := io.DB()
			list, err := targets()
			if err != nil {
				return err
			}
			container := p.values["--container"]
			type row struct {
				Workspace string `json:"workspace"`
				Undated   int64  `json:"undated"`
				Job       *int64 `json:"job"`
			}
			queued := []row{}
			for _, ws := range list {
				n, err := backfill.CountUndated(d, ws.ID, container)
				if err != nil {
					return err
				}
				r := row{Workspace: ws.Slug, Undated: n}
				if n > 0 {
					payload := jsonx.NewMap[any]()
					if container != "" {
						payload.Set("container", container)
					}
					id, ok, err := jobs.Enqueue(d, ws.ID, jobs.BackfillDates, payload, time.Now().UnixMilli())
					if err != nil {
						return err
					}
					if ok {
						r.Job = &id
					}
				}
				queued = append(queued, r)
			}
			if json {
				io.Out(jsonx.Stringify(queued))
				return nil
			}
			queuedAny := false
			for _, q := range queued {
				if q.Job != nil {
					queuedAny = true
					io.Out(fmt.Sprintf("%s: queued job #%d for %d undated fact(s)", q.Workspace, *q.Job, q.Undated))
				} else {
					io.Out(q.Workspace + ": nothing to date")
				}
			}
			if queuedAny {
				io.Out("The running server's job loop picks these up (RECALL_JOBS must not be off). Watch its log for [recall:backfill].")
			}
			return nil
		case "":
			io.Out(Usage)
			return nil
		}
		io.Err("unknown command: " + cmd + "\n" + Usage)
		code = 1
		return nil
	}()
	if err != nil {
		var ue *userErr
		var ue2 *users.Error
		switch {
		case errors.As(err, &ue):
			io.Err(ue.msg)
		case errors.As(err, &ue2):
			io.Err(ue2.Msg)
		default:
			io.Err(err.Error())
		}
		return 1
	}
	return code
}

func str(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

func createUser(io IO, p parsed, args []string, json bool, code *int) error {
	if len(args) == 0 || args[0] == "" {
		io.Err(Usage)
		*code = 1
		return nil
	}
	username := args[0]
	// values["--role"] ?? "owner": an empty --role= is refused below.
	role, hasRole := p.values["--role"]
	if !hasRole {
		role = users.Owner
	}
	if role != users.Owner && role != users.Admin && role != users.MemberRole {
		io.Err("role must be one of owner, admin, member")
		*code = 1
		return nil
	}
	slug, hasSlug := p.values["--workspace"]
	newWorkspace := p.bools["--new-workspace"]
	if newWorkspace && slug == "" {
		io.Err("--new-workspace needs --workspace <slug>")
		*code = 1
		return nil
	}
	d := io.DB()
	// A pending ADMIN_PASSWORD_HASH bootstrap goes first, so `zaki` keeps the old password.
	if _, err := users.BootstrapUsers(d, nil); err != nil {
		return err
	}
	// Check the workspace before asking for a password.
	var existing *users.Workspace
	if slug != "" {
		w, found, err := users.FindWorkspace(d, slug)
		if err != nil {
			return err
		}
		if found {
			existing = &w
		}
	}
	if slug != "" && existing == nil && !newWorkspace {
		return &userErr{fmt.Sprintf(`no workspace "%s" (add --new-workspace to create it)`, slug)}
	}
	if slug != "" && existing != nil && newWorkspace {
		return &userErr{fmt.Sprintf(`workspace "%s" already exists (drop --new-workspace to join it)`, slug)}
	}
	if existing == nil && role != users.Owner {
		return &userErr{"a new workspace needs an owner: use --role owner, or join an existing one with --workspace"}
	}
	password, err := io.ReadPassword()
	if err != nil {
		return err
	}
	if prob := users.PasswordProblem(password); prob != "" {
		return &userErr{"Password: " + prob}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	// User, workspace and membership land together or not at all.
	var u users.User
	var ws users.Workspace
	err = d.Tx(func(tx db.Q) error {
		var err error
		if u, err = users.Insert(tx, users.NewUser{Username: username, PasswordHash: hash}); err != nil {
			return err
		}
		if existing != nil {
			ws = *existing
		} else {
			// slug ?? username: an empty --workspace= still names it "".
			name := slug
			if !hasSlug {
				name = username
			}
			if ws, err = users.CreateWorkspace(tx, name, "", ""); err != nil {
				return err
			}
		}
		_, err = users.AddMember(tx, ws.ID, u.ID, role)
		return err
	})
	if err != nil {
		return err
	}
	if json {
		io.Out(jsonx.Stringify(jsonx.ObjectOf("id", u.ID, "username", u.Username, "workspace", ws.Slug, "role", role)))
	} else {
		io.Out(fmt.Sprintf("created %s (%s), %s of %s (%s)", str(u.Username), u.ID, role, ws.Name, ws.Slug))
	}
	return nil
}

func backfillPeople(io IO, p parsed, targets func() ([]users.Workspace, error), json bool) error {
	d := io.DB()
	list, err := targets()
	if err != nil {
		return err
	}
	type row struct {
		Workspace string                `json:"workspace"`
		Container string                `json:"container"`
		Pending   int64                 `json:"pending"`
		Job       *int64                `json:"job,omitempty"`
		Queued    bool                  `json:"-"`
		Stats     *people.BackfillStats `json:"stats,omitempty"`
	}
	rows := []jsonx.Object{}
	var plain []row
	run := io.Backfill
	if run == nil {
		run = func(d *db.DB, scope search.Scope) (people.BackfillStats, error) {
			return people.Backfill(context.Background(), d, scope, nil, 0)
		}
	}
	for _, ws := range list {
		pending, err := people.ContainersToBackfill(d, ws.ID)
		if err != nil {
			return err
		}
		for _, c := range pending {
			if want := p.values["--container"]; want != "" && c.Container != want {
				continue
			}
			o := jsonx.ObjectOf("workspace", ws.Slug, "container", c.Container, "pending", c.Pending)
			r := row{Workspace: ws.Slug, Container: c.Container, Pending: c.Pending}
			if p.bools["--now"] {
				st, err := run(d, search.Scope{WorkspaceID: ws.ID, Container: c.Container})
				if err != nil {
					return err
				}
				o.Set("stats", st)
				r.Stats = &st
			} else {
				id, ok, err := jobs.Enqueue(d, ws.ID, jobs.PeopleBackfill, jsonx.ObjectOf("container", c.Container), time.Now().UnixMilli())
				if err != nil {
					return err
				}
				if ok {
					o.Set("job", id)
					r.Job = &id
				} else {
					o.Set("job", nil)
				}
			}
			rows = append(rows, *o)
			plain = append(plain, r)
		}
	}
	switch {
	case json:
		io.Out(jsonx.Stringify(rows))
	case len(plain) == 0:
		io.Out("nothing to backfill: every current fact was already checked for people")
	default:
		for _, r := range plain {
			what := "already queued"
			switch {
			case r.Stats != nil:
				what = fmt.Sprintf("checked %d, %d link(s), %d new people", r.Stats.Scanned, r.Stats.Linked, r.Stats.Created)
			case r.Job != nil && *r.Job != 0:
				what = fmt.Sprintf("queued job %d", *r.Job)
			}
			io.Out(fmt.Sprintf("%s/%s  %d fact(s) to check  %s", r.Workspace, r.Container, r.Pending, what))
		}
	}
	return nil
}

func suggestMerges(io IO, p parsed, targets func() ([]users.Workspace, error), json bool) error {
	d := io.DB()
	list, err := targets()
	if err != nil {
		return err
	}
	type side struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		FactCount int64  `json:"factCount"`
	}
	type row struct {
		Workspace  string   `json:"workspace"`
		Container  string   `json:"container"`
		Keep       side     `json:"keep"`
		Drop       side     `json:"drop"`
		Reasons    []string `json:"reasons"`
		Similarity *float64 `json:"similarity"`
	}
	rows := []row{}
	for _, ws := range list {
		rs, err := d.Query(`SELECT DISTINCT container FROM people WHERE workspace_id = ? ORDER BY container`, ws.ID)
		if err != nil {
			return err
		}
		var containers []string
		for rs.Next() {
			var c string
			rs.Scan(&c)
			if want := p.values["--container"]; want == "" || c == want {
				containers = append(containers, c)
			}
		}
		rs.Close()
		for _, c := range containers {
			found, err := people.SuggestMerges(d, search.Scope{WorkspaceID: ws.ID, Container: c}, 0)
			if err != nil {
				return err
			}
			for _, s := range found {
				rows = append(rows, row{ws.Slug, c, side{s.Keep.ID, s.Keep.Name, s.Keep.FactCount}, side{s.Drop.ID, s.Drop.Name, s.Drop.FactCount}, s.Reasons, s.Similarity})
			}
		}
	}
	switch {
	case json:
		io.Out(jsonx.Stringify(rows))
	case len(rows) == 0:
		io.Out("no likely duplicates")
	default:
		for _, r := range rows {
			why := strings.Join(r.Reasons, ", ")
			if r.Similarity != nil {
				why += " (" + strx.ToFixed(*r.Similarity, 2) + ")"
			}
			who := func(s side) string { return fmt.Sprintf(`"%s" (%s, %d fact(s))`, s.Name, s.ID, s.FactCount) }
			io.Out(fmt.Sprintf("%s/%s  keep %s  merge in %s  %s", r.Workspace, r.Container, who(r.Keep), who(r.Drop), why))
		}
	}
	return nil
}

func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
