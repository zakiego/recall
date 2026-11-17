package httpapi

// fns.ts: people, search, ask, documents, graph, keys, members and
// invites, invite acceptance, account.

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/graph"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/retry"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/usernames"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/viewer"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

var (
	personName = zod.String().Trim().Min(1).Max(120)
	shortList  = zod.Array(zod.String().Trim().Min(1).Max(120)).Max(20)
	httpsURL   = zod.String().URL(regexp.MustCompile(`^https?$`))
)

func strList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, len(arr))
	for i, x := range arr {
		out[i], _ = x.(string)
	}
	return out
}

func personSide(p people.Row) *jsonx.Object {
	return jsonx.ObjectOf("id", p.ID, "name", p.Name, "role", p.Role, "organization", p.Organization, "factCount", p.FactCount)
}

// userError is userErrors(): a users.Error's text reaches the page.
func userError(err error) error {
	var ue *users.Error
	if errors.As(err, &ue) {
		return rpcFail(ue.Error())
	}
	return err
}

var inviteAttempts = ratelimit.New(10, 60_000)

// limitInvites is limitInvites: 10 tries a minute per client IP.
func limitInvites(c *Call) error {
	if wait := inviteAttempts.Take(auth.ClientIP(c.R.Header), c.Now()); wait > 0 {
		return rpcFail("Too many tries. Wait " + strconv.Itoa(wait) + " seconds and try again.")
	}
	return nil
}

var inviteToken = zod.String().Min(8).Max(80)

func init() {
	registerRPC(
		// --- people
		RPC{Name: "getPeople", Method: "GET", Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("q", zod.Optional(zod.String().Trim().Max(200))),
			zod.F("sort", zod.Optional(zod.Enum("recent", "name"))),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			limit := 200
			return people.List(c.DB(), people.ListOptions{WorkspaceID: m.WS, Container: c.Str("container"), Q: c.Str("q"), Sort: c.Str("sort"), Limit: &limit})
		}},
		RPC{Name: "getMergeSuggestions", Method: "GET", Input: zod.Object(zod.F("container", rpcContainer)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			found, err := people.SuggestMerges(c.DB(), search.Scope{WorkspaceID: m.WS, Container: c.Str("container")}, 20)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(found))
			for i, s := range found {
				out[i] = jsonx.ObjectOf("keep", personSide(s.Keep), "drop", personSide(s.Drop), "reasons", s.Reasons, "similarity", s.Similarity)
			}
			return out, nil
		}},
		RPC{Name: "getPersonPage", Method: "GET", Input: zod.Object(zod.F("id", rpcID), zod.F("history", zod.Optional(zod.Bool()))), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			h, _ := c.Obj().Get("history")
			found, ok, err := people.Get(c.DB(), m.WS, c.Str("id"), h == true)
			if err != nil || !ok {
				return nil, err
			}
			limit := 200
			list, err := people.List(c.DB(), people.ListOptions{WorkspaceID: m.WS, Container: found.Person.Container, Sort: "name", Limit: &limit})
			if err != nil {
				return nil, err
			}
			others := []any{}
			for _, p := range list.People {
				if p.ID != c.Str("id") {
					others = append(others, personSide(p))
				}
			}
			o := toObject(found)
			o.Set("others", others)
			return o, nil
		}},
		RPC{Name: "savePerson", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("id", rpcID),
			zod.F("name", personName),
			zod.F("aliases", shortList),
			zod.F("role", zod.Nullable(zod.String().Trim().Max(120))),
			zod.F("organization", zod.Nullable(zod.String().Trim().Max(120))),
			zod.F("summary", zod.String().Trim().Max(600)),
			zod.F("emails", zod.Array(zod.Email().Max(254)).Max(10)),
			zod.F("handles", zod.Array(zod.String().Trim().Min(1).Max(80)).Max(10)),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			o := c.Obj()
			name, summary := c.Str("name"), c.Str("summary")
			aliasesV, _ := o.Get("aliases")
			emailsV, _ := o.Get("emails")
			handlesV, _ := o.Get("handles")
			aliases, emails, handles := strList(aliasesV), strList(emailsV), strList(handlesV)
			opt := func(k string) people.OptString {
				v, _ := o.Get(k)
				if s, ok := v.(string); ok {
					return people.OptString{Set: true, Value: &s}
				}
				return people.OptString{Set: true}
			}
			row, ok, err := people.Update(c.DB(), m.WS, c.Str("id"), people.Patch{Name: &name, Aliases: &aliases, Role: opt("role"), Organization: opt("organization"), Summary: &summary, Emails: &emails, Handles: &handles})
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("No such person. Someone may have merged or deleted them.")
			}
			return row, nil
		}},
		RPC{Name: "mergePerson", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("keepId", rpcID), zod.F("dropId", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			row, err := people.Merge(c.DB(), m.WS, c.Str("keepId"), c.Str("dropId"))
			var pe *people.Error
			if errors.As(err, &pe) {
				return nil, rpcFail(pe.Msg)
			}
			return row, err
		}},
		RPC{Name: "removePerson", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			ok, err := people.Delete(c.DB(), m.WS, c.Str("id"))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("No such person.")
			}
			return jsonx.ObjectOf("ok", true), nil
		}},

		// --- search and ask
		RPC{Name: "searchMemories", Method: "POST", Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("q", zod.String().Trim().Min(1).Max(2000)),
			zod.F("chunks", zod.Optional(zod.Bool())),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(50))),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			limit := 20
			if v, ok := c.Opt("limit"); ok {
				limit = int(v.(float64))
			}
			include := []string{"memories"}
			if v, _ := c.Obj().Get("chunks"); v == true {
				include = []string{"memories", "chunks"}
			}
			return api.Search(c.DB(), m.WS, api.SearchInput{Container: c.Str("container"), Q: c.Str("q"), Limit: &limit, Include: include})
		}},
		RPC{Name: "askMemory", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("question", zod.String().Trim().Min(1).Max(2000)),
			zod.F("chunks", zod.Optional(zod.Bool())),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			chunks := false
			if v, _ := c.Obj().Get("chunks"); v == true {
				chunks = true
			}
			// A cancelled fetch cancels the request context, which stops the ask.
			return ask.Ask(c.R.Context(), c.DB(), m.WS, ask.Input{Container: c.Str("container"), Question: c.Str("question"), IncludeChunks: &chunks}, 0)
		}},

		// --- documents
		RPC{Name: "getDocuments", Method: "GET", Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("cursor", zod.Optional(zod.String().Max(200))),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(200))),
		), Fn: rpcGetDocuments},
		RPC{Name: "addText", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("container", rpcContainer),
			zod.F("kind", zod.Enum("text", "url")),
			zod.F("value", zod.String().Trim().Min(1).Max(1_000_000)),
			zod.F("title", zod.Optional(zod.String().Trim().Max(300))),
		), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			in := api.AddInput{Container: c.Str("container")}
			if t := c.Str("title"); t != "" {
				in.Title = &t
			}
			if c.Str("kind") == "url" {
				u, err := zod.Parse(httpsURL, c.Str("value"))
				if err != nil {
					return nil, rpcFail("Enter a full http or https URL.")
				}
				s := u.(string)
				in.URL = &s
			} else {
				v := c.Str("value")
				in.Content = &v
			}
			return api.AddContent(c.DB(), m.WS, in, c.Now())
		}},
		RPC{Name: "uploadFile", Method: "POST", SameOrigin: true, Parse: parseUpload, Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			up := c.Input.(*upload)
			name := up.file.name
			if name == "" {
				name = "upload"
			}
			mime := up.file.mime
			if mime == "" {
				mime = "application/octet-stream"
			}
			title := up.file.name
			return api.AddFile(c.DB(), m.WS, api.FileInput{Container: up.container, Title: &title, Name: name, Mime: mime, Bytes: up.file.bytes}, c.Now())
		}},
		RPC{Name: "retryDocument", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			switch err := retry.Document(c.DB(), m.WS, c.Str("id"), retry.Options{UserID: m.Viewer.User.ID}, c.Now()); {
			case errors.Is(err, retry.ErrNoDocument):
				return nil, rpcFail(err.Error())
			case errors.Is(err, retry.ErrNotImporter):
				return nil, &viewer.Forbidden{Msg: err.Error()}
			case err != nil:
				return nil, err
			}
			return jsonx.ObjectOf("ok", true), nil
		}},
		RPC{Name: "removeDocument", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			ok, err := memories.DeleteDocument(c.DB(), m.WS, c.Str("id"))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("No such document.")
			}
			return jsonx.ObjectOf("ok", true), nil
		}},
		RPC{Name: "getGraph", Method: "GET", Input: zod.Object(zod.F("container", rpcContainer)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			return graph.Build(c.DB(), m.WS, c.Str("container"))
		}},

		// --- keys
		RPC{Name: "getKeys", Method: "GET", Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			keys, err := auth.ListAPIKeys(c.DB(), m.WS)
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("keys", keys, "canManage", viewer.RoleAtLeast(m.Role, users.Admin)), nil
		}},
		RPC{Name: "getConnectInfo", Method: "GET", Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("origin", c.URL(), "canManage", viewer.RoleAtLeast(m.Role, users.Admin)), nil
		}},
		RPC{Name: "createKey", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("name", zod.String().Trim().Min(1).Max(80))), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			uid := m.Viewer.User.ID
			key, row, err := auth.CreateAPIKey(c.DB(), m.WS, c.Str("name"), &uid)
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("key", key, "id", row.ID, "name", row.Name, "prefix", row.Prefix), nil
		}},
		RPC{Name: "revokeKey", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			ok, err := auth.RevokeAPIKey(c.DB(), m.WS, c.Str("id"), c.Now())
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("ok", ok), nil
		}},

		// --- members and invites
		RPC{Name: "getMembers", Method: "GET", Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			members, err := users.ListMembers(c.DB(), m.WS)
			if err != nil {
				return nil, err
			}
			canManage := viewer.RoleAtLeast(m.Role, users.Admin)
			invites := []users.InviteView{}
			if canManage {
				if invites, err = users.ListInvites(c.DB(), m.WS, c.Now()); err != nil {
					return nil, err
				}
			}
			return jsonx.ObjectOf("members", members, "invites", invites, "myRole", m.Role, "myUserId", m.Viewer.User.ID, "canManage", canManage), nil
		}},
		RPC{Name: "createInviteLink", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("role", rpcRole)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			role := c.Str("role")
			if !users.CanInvite(m.Role, role) {
				return nil, &viewer.Forbidden{Msg: "Only owners can invite owners."}
			}
			token, inv, err := users.CreateInvite(c.DB(), m.WS, role, m.Viewer.User.ID, c.Now())
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("url", c.URL()+"/app/invite/"+token, "id", inv.ID, "role", inv.Role, "expiresAt", inv.ExpiresAt), nil
		}},
		RPC{Name: "revokeInviteLink", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			ok, err := users.RevokeInvite(c.DB(), m.WS, c.Str("id"))
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("ok", ok), nil
		}},
		RPC{Name: "setMemberRole", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("memberId", rpcID), zod.F("role", rpcRole)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.Admin)
			if err != nil {
				return nil, err
			}
			if _, err := users.ChangeMemberRole(c.DB(), m.WS, users.Actor{UserID: m.Viewer.User.ID, Role: m.Role}, c.Str("memberId"), c.Str("role")); err != nil {
				return nil, userError(err)
			}
			return jsonx.ObjectOf("ok", true), nil
		}},
		RPC{Name: "removeWorkspaceMember", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("memberId", rpcID)), Fn: func(c *Call) (any, error) {
			m, err := c.RequireMember(users.MemberRole)
			if err != nil {
				return nil, err
			}
			removed, err := users.RemoveMember(c.DB(), m.WS, users.Actor{UserID: m.Viewer.User.ID, Role: m.Role}, c.Str("memberId"))
			if err != nil {
				return nil, userError(err)
			}
			return jsonx.ObjectOf("ok", true, "left", removed.UserID == m.Viewer.User.ID), nil
		}},

		// --- invite acceptance (public page)
		RPC{Name: "getInvite", Method: "GET", Input: zod.Object(zod.F("token", inviteToken)), Fn: func(c *Call) (any, error) {
			if err := limitInvites(c); err != nil {
				return nil, err
			}
			found, err := users.LookupInvite(c.DB(), c.Str("token"), c.Now())
			if err != nil {
				return nil, err
			}
			v, err := c.Viewer()
			if err != nil {
				return nil, err
			}
			var signedInAs any
			if v != nil {
				signedInAs = v.User.Username
			}
			if found.Reason != "" {
				return jsonx.ObjectOf("ok", false, "problem", users.InviteProblem(found.Reason), "signedInAs", signedInAs), nil
			}
			already := false
			if v != nil {
				role, err := users.GetRole(c.DB(), v.User.ID, found.WorkspaceID)
				if err != nil {
					return nil, err
				}
				already = role != ""
			}
			return jsonx.ObjectOf("ok", true, "workspace", found.WorkspaceName, "role", found.Invite.Role, "expiresAt", found.Invite.ExpiresAt, "signedInAs", signedInAs, "alreadyMember", already), nil
		}},
		RPC{Name: "acceptInvite", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("token", inviteToken)), Fn: func(c *Call) (any, error) {
			if err := limitInvites(c); err != nil {
				return nil, err
			}
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			ws, err := users.AcceptInviteAsUser(c.DB(), c.Str("token"), v.User.ID, c.Now())
			if err != nil {
				return nil, userError(err)
			}
			if err := auth.SetActiveWorkspace(c.DB(), v.SessionID, &ws.ID); err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("ok", true, "workspace", ws.Name), nil
		}},
		RPC{Name: "acceptInviteNewAccount", Method: "POST", SameOrigin: true, Input: zod.Object(
			zod.F("token", inviteToken),
			zod.F("username", zod.String().Trim().Min(1).Max(usernames.Max)),
			zod.F("password", zod.String().Min(1).Max(auth.MaxPasswordLength)),
		), Fn: func(c *Call) (any, error) {
			if err := limitInvites(c); err != nil {
				return nil, err
			}
			u, ws, err := users.AcceptInviteWithNewUser(c.DB(), c.Str("token"), c.Str("username"), c.Str("password"), c.Now())
			if err != nil {
				return nil, userError(err)
			}
			// Sign the new user in; the session lands in the workspace they joined.
			token, _, err := auth.CreateSession(c.DB(), u.ID, auth.ClientIP(c.R.Header), c.R.UserAgent(), c.Now())
			if err != nil {
				return nil, err
			}
			http.SetCookie(c.W, auth.SessionCookie(c.URL(), token))
			return jsonx.ObjectOf("ok", true, "workspace", ws.Name), nil
		}},

		// --- account
		RPC{Name: "getAccount", Method: "GET", Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			keys, err := users.ListPasskeys(c.DB(), v.User.ID)
			if err != nil {
				return nil, err
			}
			return jsonx.ObjectOf("username", v.User.Username, "passkeys", keys), nil
		}},
		RPC{Name: "removePasskey", Method: "POST", SameOrigin: true, Input: zod.Object(zod.F("id", rpcID)), Fn: func(c *Call) (any, error) {
			v, err := c.RequireUser()
			if err != nil {
				return nil, err
			}
			ok, err := users.DeletePasskey(c.DB(), v.User.ID, c.Str("id"))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, rpcFail("No such passkey.")
			}
			return jsonx.ObjectOf("ok", true), nil
		}},
	)
}

func rpcGetDocuments(c *Call) (any, error) {
	m, err := c.RequireMember(users.MemberRole)
	if err != nil {
		return nil, err
	}
	limit := 50
	if v, ok := c.Opt("limit"); ok {
		limit = int(v.(float64))
	}
	cursor := c.Str("cursor")
	docs, next, err := memories.ListDocuments(c.DB(), m.WS, c.Str("container"), "", cursor, &limit)
	if err != nil {
		return nil, err
	}
	var total any
	if cursor == "" {
		n, err := memories.CountDocuments(c.DB(), m.WS, c.Str("container"))
		if err != nil {
			return nil, err
		}
		total = n
	}
	out := make([]any, len(docs))
	for i, d := range docs {
		var extract any
		if d.Metadata != nil {
			if ex, ok := d.Metadata.Get("extract"); ok {
				if eo, ok := ex.(*jsonx.Object); ok {
					num := func(k string) any { v, _ := eo.Get(k); return v }
					conflicts := num("conflicts")
					if conflicts == nil {
						conflicts = 0.0
					}
					extract = jsonx.ObjectOf("added", num("added"), "updated", num("updated"), "invalidated", num("invalidated"), "conflicts", conflicts)
				}
			}
		}
		o := toObject(d)
		o.Delete("metadata")
		o.Set("extract", extract)
		out[i] = o
	}
	return jsonx.ObjectOf("nextCursor", next, "total", total, "documents", out), nil
}

type upload struct {
	container string
	file      *formFile
}

// parseUpload is uploadFile's validator: form data with a non-empty file
// within the size limit, and a container.
func parseUpload(c *Call) (any, error) {
	limit := int64(api.MaxFileBytes) + 1<<20
	body, err := io.ReadAll(io.LimitReader(c.R.Body, limit+1))
	if err != nil {
		return nil, rpcFail("Expected form data.")
	}
	if int64(len(body)) > limit {
		return nil, rpcFail("Files can be up to " + strconv.Itoa(api.MaxFileBytes/1024/1024) + " MB.")
	}
	form, err := parseForm(c.R.Header.Get("content-type"), body)
	if err != nil {
		return nil, rpcFail("Expected form data.")
	}
	file := form.file("file")
	if file == nil || len(file.bytes) == 0 {
		return nil, rpcFail("Choose a file to upload.")
	}
	if len(file.bytes) > api.MaxFileBytes {
		return nil, rpcFail("Files can be up to " + strconv.Itoa(api.MaxFileBytes/1024/1024) + " MB.")
	}
	ct, err := zod.Parse(rpcContainer, form.value("container"))
	if err != nil {
		return nil, err
	}
	return &upload{container: ct.(string), file: file}, nil
}
