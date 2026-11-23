# Multi-user and workspaces

Recall goes from one admin password to real users who belong to one or more workspaces.
Every row of data belongs to exactly one workspace, and nothing ever reads across that line.

## Auth: Better Auth

This section is the design as it was built in TypeScript. The Go server that replaced it
(`docs/GO-PORT.md`) keeps the same tables, password hashes, passkeys, roles and checks, with its
own session cookie `recall_session` (the token's hash is stored in `session.token`) and its own
`/api/auth` endpoints for the dashboard (`apps/server/internal/httpapi/auth.go`).

Better Auth fit as is. It runs in the Nitro node server, and its Drizzle adapter sits on the
same better-sqlite3 connection the app already opens (`getDb()`), so there is one database file
and one migration runner.

- Plugins: `username` (sign in with username + password), `@better-auth/passkey` (WebAuthn) and
  `organization` (shown as "workspaces" in the UI, roles `owner`, `admin`, `member`).
- Mounted at `/api/auth/*` through a TanStack Start server route, like the house setup in
  social-media-agency. `tanstackStartCookies()` is the last plugin so server functions can set
  the session cookie.
- Better Auth requires an email on every user. Recall has no email flows, so each user gets a
  placeholder `<username>@users.recall.invalid` (`.invalid` is reserved and can never deliver).
  The UI never shows it.
- Open signup is off (`emailAndPassword.disableSignUp`). Users are only created by the CLI, the
  first-boot bootstrap, or by accepting an invite, all through the same server-side
  `createUserWithPassword()`.
- Passwords: new ones are hashed with Better Auth's default scrypt. The verifier also accepts the
  old `pbkdf2$sha256$...` format, which is how the existing `ADMIN_PASSWORD_HASH` keeps working.
- Secret: `SESSION_SECRET` (already set in prod) becomes the Better Auth secret. No new secret.
- Base URL and passkeys: `RECALL_URL` (new, `https://recall.zakiego.com` in prod, default
  `http://localhost:4800`). The passkey `rpID` is its hostname unless `PASSKEY_RP_ID` overrides it,
  and the WebAuthn origin is its origin.

### Security

- Session cookie `recall.session_token`: httpOnly, `SameSite=Lax`, `Secure` whenever `RECALL_URL`
  is https. Lax (not Strict) so an invite link opened from a chat app still sees an existing
  session. Lax cookies are never sent on cross-site POSTs.
- Origin checks: Better Auth only checks `Origin` on requests that already carry a cookie, so a
  sign-in would slip through (login CSRF). `/api/auth` therefore refuses every write whose
  `Origin` isn't `RECALL_URL` or whose `Sec-Fetch-Site` is `cross-site`, before Better Auth runs.
  Better Auth's own origin and CSRF checks are forced on (it turns them off under a test runner).
  Dashboard server functions keep `requireSameOrigin()` and also refuse `Sec-Fetch-Site: cross-site`.
- Rate limits, in memory, keyed by client IP (`cf-connecting-ip`, then `x-forwarded-for`, copied
  into a header the client can't set): 5 per minute per IP for username sign-in, passkey sign-in
  and password changes, 100 per minute for everything else under `/api/auth` (Better Auth), and 10
  per minute for invite lookups and acceptance (Recall's own limiter).
- Passkey challenges live server-side in the `verification` table, keyed by a signed, short-lived
  cookie, so a challenge can't be replayed or forged by the client.
- API keys stay sha256-hashed and shown once.

## Tenancy

`organization` is the workspace table. `container` stays a free-form namespace inside a
workspace, so one workspace can still hold several people or projects.

| Table | Change |
|---|---|
| `documents`, `chunks`, `memories`, `jobs`, `api_keys` | new `workspace_id text NOT NULL`, plus triggers that refuse an empty value |
| `profiles` | rebuilt with primary key `(workspace_id, container)` |
| `chunks_fts`, `memories_fts` | rebuilt with a `workspace_id UNINDEXED` column |
| `chunks_vec`, `memories_vec` | rebuilt with `workspace_id` and `container` as vec0 partition keys |
| `api_keys` | also `created_by` (user id, nullable for legacy keys) |

Every read takes a workspace id and filters on it: list, get by id, version chains, containers,
profile, graph, documents, FTS, vector KNN and the final row fetch after fusion. A memory id from
another workspace returns 404, the same as one that doesn't exist.

Where the workspace comes from:

- REST and MCP: from the API key. Request and response shapes don't change.
- Dashboard: the session's active workspace (`session.activeOrganizationId`), re-checked against
  `member` on every server function call.
- Jobs: from `jobs.workspace_id`, set when the job is queued.

`workspace_id` is added with `ALTER TABLE ... ADD COLUMN ... DEFAULT ''` rather than a table
rebuild. A rebuild of `documents` would need foreign keys off (dropping it cascades into `chunks`),
and it would silently drop columns that the other open branches add (`memories.review_note` in
0002, `documents.source_id` in 0003). The triggers make the empty default unusable, so a missed
`workspace_id` fails loudly instead of landing somewhere.

## Roles

| Action | owner | admin | member |
|---|---|---|---|
| Read, search, add, edit and forget memories | yes | yes | yes |
| See API keys | yes | yes | yes |
| Create and revoke API keys | yes | yes | no |
| Invite, change roles, remove members | yes | yes (not owners) | no |
| Leave the workspace | yes (not the last owner) | yes | yes |
| Create a new workspace (becomes its owner) | yes | yes | yes |

Role changes and removals are in `apps/server/internal/users` (it was `src/server/users.ts`), with
tests: only owners make or touch owners, and a workspace always keeps one owner. Workspace
deletion is off.

## Invites

The organization plugin's invitations are tied to an email address and need the invitee to be
signed in already, which doesn't match "no email, invitee picks a username". So invites are a
small table of their own:

`workspace_invites`: id, workspace_id, role, token_hash (sha256), created_by, created_at,
expires_at (7 days), accepted_at, accepted_by, revoked_at.

1. An owner or admin creates an invite in Settings, Members and copies the link
   `RECALL_URL/invite/<token>` (shown once, like a key).
2. A visitor who isn't signed in picks a username and password. The account, the membership and a
   session are created in one step.
3. A visitor who is already signed in just joins. Either way the invite is single-use.
4. A link only works while its creator could still create it: removing the creator revokes their
   pending links (and the API keys they made in that workspace), demoting them revokes links at
   roles they can no longer grant, and acceptance re-checks the creator's current role.
5. After signing in, Settings, Account offers "Add a passkey".

## Bootstrap and the migration of existing data

Migration `0004_workspaces` (SQL, runs at boot like the others):

1. Creates the Better Auth tables (`user`, `session`, `account`, `verification`, `passkey`,
   `organization`, `member`, `invitation`) and `workspace_invites`.
2. If the database already holds any documents, memories, profiles, jobs or API keys, it creates
   workspace `ws_zaki` named "Zaki" (slug `zaki`) and sets `workspace_id = 'ws_zaki'` on every
   existing row, including the search indexes. A fresh database gets no workspace.

First boot after the migration (`bootstrapUsers()`, also run by the CLI):

- If `ADMIN_PASSWORD_HASH` is set and the `user` table is empty, it creates user `admin` with that
  exact hash as the password and makes it the owner of the migrated workspace (or of a new
  workspace "Home" on a fresh database). The old admin password then logs in as `admin`. Existing
  API keys keep working, scoped to the migrated workspace.
- If any user exists it does nothing, so removing or changing `ADMIN_PASSWORD_HASH` later has no
  effect. After the first login it can be dropped from the environment.
- If `ADMIN_PASSWORD_HASH` is empty and there are no users, nobody can log in yet. The login page
  says to run `recall create-user <username> --workspace <migrated slug> --role owner` (or plain
  `create-user <username>` on a fresh database, which has no workspace yet). Data and keys keep
  working in the meantime.

The old `recall_session` cookie stops meaning anything, so everyone signs in once more.

## CLI

```bash
recall create-user <username> [--workspace <slug> [--new-workspace]] [--role owner|admin|member]   # password from stdin or prompt
recall list-users [--workspace <slug>]
recall create-key <name> [--workspace <slug>] [--quiet]   # --workspace optional with one workspace
recall list-keys [--workspace <slug>]
recall revoke-key <id>
recall hash-password                                      # pbkdf2, for ADMIN_PASSWORD_HASH
# all take --json; in the container: docker compose exec app recall <command>
```

Without `--workspace`, `create-user` makes a new workspace named after the user. With
`--workspace <slug>` it joins that workspace and fails when no such slug exists, so a typo can't
create a stray workspace; pass `--new-workspace` to create it on purpose. A new workspace always
needs `--role owner` (the default).

## Dashboard

- Login: username + password, and "Sign in with a passkey".
- Header: workspace switcher next to the container switcher. Switching workspaces resets the
  container to the default.
- Settings: Account (change password, add and remove passkeys), Members (invite link, change role,
  remove), API keys (moved from `/keys`, which now redirects).

## Coordination with the other branches

- Migrations 0004 and 0005 run after main's 0002 (`memories.review_note`) and 0003
  (`documents.source_id`). 0004 didn't need changes for those columns. `0005_source_id_per_workspace`
  replaces 0003's unique index on `(container, source_id)` with one on
  `(workspace_id, container, source_id)`, so the same source can be ingested once per workspace.
- Main's later features are workspace-scoped too: `source_id` dedupe, `?wait=true` (the wait only
  reads its own workspace's document and facts), review flags (`setReviewNote`, `reviewCount`,
  `resolveMemory`), Ask (`/v1/ask`, `ask_memory` and the Ask page queue an `ask` job carrying the
  workspace; the job searches and reads the profile only there), and the sensitive-data guard (runs
  per fact, nothing to scope). The `backup` command snapshots the whole file, every workspace in it.
- Migrations are hand-written (see AGENTS.md; they now live in `apps/server/internal/db/migrations`). The stale
  drizzle-kit snapshots in `drizzle/meta` were removed later.
- Extraction prompts aren't touched.
