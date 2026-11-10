# recall

A self-hosted memory engine for AI agents. Send it text, chats, URLs or files. It extracts
facts per container (a user, project or team), keeps them current (a newer fact supersedes the
older one and the history is kept), and serves them through a REST API, an MCP server and a web
dashboard. Live at https://recall.zakiego.com, running on the Mac mini (see `PLAN.md`).

- Search is local: SQLite FTS5 + sqlite-vec, merged with reciprocal rank fusion. No LLM call,
  single-digit milliseconds once the embedding model is warm.
- Extraction, profiles and Ask go through the ai-gateway (`/v1/chat`), one job at a time; asks go first.
- Embeddings: `Xenova/multilingual-e5-small` (q8, 384 dims) in-process, cached in `/data/models`.

## API

Every `/v1` route takes `Authorization: Bearer rcl_...` (create keys in the dashboard under
Settings, API keys). A key belongs to one workspace and only ever sees that workspace's rows.
`container` is a namespace inside the workspace and defaults to `RECALL_DEFAULT_CONTAINER`.

```bash
# Add text, a chat, or a URL. Returns 202 with a document id; facts appear a few seconds later.
curl -X POST $RECALL/v1/memories -H "authorization: Bearer $KEY" -H 'content-type: application/json' \
  -d '{"container":"zaki","content":"I switched to Puma running shoes."}'
curl -X POST $RECALL/v1/memories ... -d '{"container":"zaki","messages":[{"role":"user","content":"..."}]}'
curl -X POST $RECALL/v1/memories ... -d '{"container":"zaki","url":"https://example.com/post"}'
# Files (PDF, text, markdown, html, up to 20 MB)
curl -X POST $RECALL/v1/memories -H "authorization: Bearer $KEY" -F file=@notes.pdf -F container=zaki

curl "$RECALL/v1/memories?container=zaki"              # current facts, ?history=1 for all versions, ?cursor= to page
curl "$RECALL/v1/memories?container=zaki&review=1"     # only current facts flagged as conflicting (see reviewNote)
curl "$RECALL/v1/memories/mem_..."                       # one fact and its version chain
curl -X PATCH "$RECALL/v1/memories/mem_..." -d '{"fact":"..."}'   # writes a new version (and clears any review note)
curl -X PATCH "$RECALL/v1/memories/mem_..." -d '{"resolve":true}'  # clears the review note on both sides, no new version
curl -X DELETE "$RECALL/v1/memories/mem_..."             # soft delete (sets valid_to)
curl -X POST $RECALL/v1/search -d '{"container":"zaki","q":"what shoes","include":["memories","chunks"]}'
curl "$RECALL/v1/profile?container=zaki&q=shoes"         # static + dynamic summary, top facts, optional search
curl -X POST $RECALL/v1/ask -d '{"container":"zaki","question":"what shoes do I wear?","include_chunks":false}'
curl "$RECALL/v1/documents/doc_..."                      # processing status
curl "$RECALL/v1/containers"
curl -X POST $RECALL/v1/connectors/gmail/import -d '{"container":"zaki","message_id":"18c2a1f0b9e4d701"}'  # with the key creator's Gmail connection
curl "$RECALL/v1/people?container=zaki&q=clara"       # people named in the facts, last mentioned first
curl "$RECALL/v1/people/per_..."                         # one person and the facts that mention them
curl "$RECALL/health"                                    # no auth
```

### Waiting for facts

Add `?wait=1` (or `"wait": true` in the JSON body, or a `wait` form field for uploads) and the
request returns when the document is `done` or `failed`, with the facts it wrote. The default
timeout is 60 s; set `wait_timeout` (seconds, up to 120) in the same place. On timeout you get
the usual 202 with the id, plus `"settled": false`, and can check `GET /v1/documents/:id` later.
Extraction still runs in the single job loop, so a wait queues behind other work. Behind a proxy
with a shorter read timeout (Cloudflare's is 100 s), keep `wait_timeout` under it.

```bash
curl -X POST "$RECALL/v1/memories?wait=1" ... -d '{"container":"zaki","content":"I switched to Puma running shoes."}'
# 200
# {"id":"doc_...","container":"zaki","status":"done","sourceId":null,"duplicate":false,"settled":true,
#  "error":null,"added":[],"updated":[{"id":"mem_...","fact":"Switched to Puma running shoes",
#  "keywords":"...","kind":"dynamic","supersedesId":"mem_..."}],"invalidated":0}
```

`added` holds new facts, `updated` holds new versions of older facts (`supersedesId` is the one
replaced), and `invalidated` counts facts the document closed. A fact with `reviewNote`
conflicts with an older one and is flagged for review (see MCP below). A `failed` document returns 200
with `status: "failed"` and `error`.

### Deduping with source_id

Pass `source_id` (up to 200 chars, e.g. `gmail:1a1ab7843db48820`) in the JSON body or as a form
field. It's unique per container: a second add with the same `source_id` in the same container
creates nothing and returns 200 with the existing document and `"duplicate": true` (with `wait`,
it waits on that document instead). The same `source_id` in another container is a separate
document. To ingest a source again, delete its document first.

```bash
curl -X POST $RECALL/v1/memories ... -d '{"container":"zaki","content":"...","source_id":"gmail:1a1ab7843db48820"}'
# first time: 202 {"id":"doc_a","status":"queued","sourceId":"gmail:1a1ab7843db48820","duplicate":false,...}
# again:      200 {"id":"doc_a","status":"done","sourceId":"gmail:1a1ab7843db48820","duplicate":true,...}
```

### Asking

`/v1/ask` returns `{ answer, covered, citations: [{ ref, type, id, text }], model, timingMs }`. The
answer is written only from the container's top 12 facts (plus 6 document excerpts with
`include_chunks`), in the question's language, and `[n]` in it is `citations[n-1]`, mapped to real
memory or chunk ids. `covered: false` means memory doesn't answer it. It's one gateway call, run
as an `ask` job that goes ahead of ingest/extract/profile; the request waits up to 90 s, then
returns 504.

## People

Extraction also returns the people each fact names (colleagues, clients, family). Recall matches
them to people in the same workspace and container by email, handle, name or alias (ignoring case
and accents), and accepts a looser match ("Clara" for "Clara Simmons") only when the local
embedding agrees. It never creates a person for the container's own subject. The dashboard has a
People list and a page per person (edit, merge, delete); Ask uses that page when a question names
someone; search hits carry `people`. Facts saved before this feature have no links until
`backfill-people` runs once. See `apps/site/content/docs/people.mdx`.

## MCP

Streamable HTTP at `/mcp`, same bearer key. Tools: `add_memory`, `search_memories`,
`ask_memory`, `get_profile`, `list_memories` (pass `needs_review: true` for flagged facts only),
`upcoming` (facts due in the next N days), `list_people`, `get_person` (by id or name).
`add_memory` takes optional `source_id` (same dedupe
as the API), `document_date` (relative dates resolve against it) and `wait` (waits up to 60 s
and returns the facts it added or updated).

## Dates and reminders

Facts carry an optional event date and due date (ISO date plus precision: day, month or year).
The extractor fills them only when the content states or implies a date. `GET /v1/upcoming` and
the dashboard's Upcoming page list what's due. Each user can turn on email reminders (Resend) in
Settings, Reminders; an hourly `reminders` job sends one digest a day after 08:00 in their zone,
each reminder once. Date older facts once with `pnpm cli backfill-dates`. Details in the docs at
`/docs/dates-and-reminders`.

When new content disagrees with a fact and it isn't clear which one is right, the extractor
keeps both as current, sets `reviewNote` on each and links them with `conflictsWithId`. The
dashboard shows them with a "Needs review" badge and a filter, with "Keep this one" and "Both
are right" on each row. Resolving, editing or deleting either side clears the note on both.

```bash
claude mcp add --transport http recall https://recall.zakiego.com/mcp --header "Authorization: Bearer rcl_..."
```

## Environment

| Key | What |
|---|---|
| `GATEWAY_URL` | ai-gateway base URL (`http://host.docker.internal:8793` in the container) |
| `GATEWAY_KEY` | `zkg_...` key for the gateway's `recall` project |
| `EXTRACT_MODEL` | defaults to `claude-haiku-5-5` |
| `FALLBACK_MODEL` | used when the gateway rejects `EXTRACT_MODEL`, defaults to `claude-haiku-4-5` |
| `SESSION_SECRET` | 32+ random characters (`openssl rand -base64 48`). Signs Better Auth sessions |
| `RECALL_URL` | public base URL, `https://recall.zakiego.com` in prod (default `http://localhost:4800`). Trusted origin and WebAuthn origin |
| `PASSKEY_RP_ID` | optional, passkey relying party id. Defaults to the hostname of `RECALL_URL` |
| `ADMIN_PASSWORD_HASH` | optional. Only read when there are no users: creates user `zaki` with this hash (see `docs/multi-user.md`) |
| `RECALL_DEFAULT_CONTAINER` | container used when a request names none (default `default`) |
| `DATA_DIR` | where `recall.db` and `models/` live (`/data` in the container, `./data` locally) |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | optional, the Google OAuth web client for the Gmail connector (redirect URI `RECALL_URL/app/connectors/google/callback`) |
| `CONNECTOR_SECRET` | optional, 32+ random characters (`openssl rand -base64 48`). Encrypts stored connector tokens. Gmail stays "not configured" until all three keys are set |
| `RESEND_API_KEY` | optional. Resend key for reminder emails. Without it (or `RECALL_EMAIL_FROM`) reminders say "email not configured" |
| `RECALL_EMAIL_FROM` | optional. Reminder sender on a Resend-verified domain, e.g. `Recall <reminders@zakiego.com>` |

## Develop

A pnpm workspace (pnpm 10.17.0, pinned in `packageManager`):

| Path | What |
|---|---|
| `apps/app` | the engine: API, MCP, jobs and the dashboard (TanStack Start), built into the Docker image |
| `apps/site` | the public landing page and docs (Fumadocs, static export to Cloudflare Pages) |
| `apps/edge` | the Cloudflare Worker on `recall.zakiego.com/*` that splits traffic between the site and the app |
| `integrations/claude-code` | Claude Code hooks: inject the profile at session start, send the session back at the end (`pnpm hooks:install`, `pnpm hooks:test`) |
| `compose.yaml`, `Dockerfile`, `scripts/`, `ops/` | deploy and backup, run from the repo root |

```bash
pnpm install
pnpm dev            # the app, http://localhost:4800/app
pnpm test           # vitest, downloads the e5 model to apps/app/data/models on first run
pnpm typecheck
pnpm cli create-user <username> [--workspace <slug> [--new-workspace]] [--role owner|admin|member]
pnpm cli list-users [--workspace <slug>]
pnpm cli create-key <name> [--workspace <slug>] [--quiet] | list-keys [--workspace <slug>] | revoke-key <id> | hash-password | backup <dest>  # --json on any
pnpm cli backfill-dates [--workspace <slug>] [--container <name>]  # one-off, queues gateway jobs
pnpm cli backfill-people [--workspace <slug>] [--container <name>] [--now]   # link facts saved before people existed
pnpm --filter @recall/app db:bundle      # after adding a hand-written apps/app/drizzle/NNNN_name.sql (AGENTS.md)
```

## Deploy

zaki-selfhost: push to `main`, then on the Mac mini
`~/.agents/skills/zaki-selfhost/scripts/deploy.sh recall`. Port 4800, volume `recall_data`.

Before building, the deploy script runs `scripts/pre-deploy.sh` from the checkout. With the app
running, it snapshots the database with the bundled CLI to
`/data/backups/pre-deploy-<UTC timestamp>.db.gz` inside the volume and keeps the newest 3. A failed
backup stops the deploy; on a first deploy (no running app) there's nothing to back up. It also
reruns `ops/install-backup.sh` when `scripts/backup.sh` differs from the installed copy.

`/health` returns 503 with `"error": "db_newer_than_app"` when the database has migrations the
running build doesn't ship. That happens when a new image migrated the database and the deploy
script then rolled back to `recall:prev`. Restore the newest pre-deploy backup (see Restore) or
deploy the newer build again.

The image ships the admin CLI at `.output/server/cli.mjs` (bundled by `pnpm build`). It opens
`$DATA_DIR/recall.db` (`/data` in the container) and runs migrations like the app does. Run it from
the compose project directory:

```bash
docker compose exec app node .output/server/cli.mjs create-user eva --workspace zaki --role member   # prompts for the password
docker compose exec app node .output/server/cli.mjs create-user rina --workspace rina --new-workspace   # new workspace, rina owns it
docker compose exec app node .output/server/cli.mjs list-users                  # every user and their workspaces
docker compose exec app node .output/server/cli.mjs create-key <name> --workspace zaki   # id, name and key
docker compose exec -T app node .output/server/cli.mjs create-key <name> --workspace zaki --quiet > key.txt   # key only
docker compose exec app node .output/server/cli.mjs create-key <name> --workspace zaki --json   # {"id","name","prefix","workspace","key"}
docker compose exec app node .output/server/cli.mjs list-keys --workspace zaki  # add --json for JSON
docker compose exec app node .output/server/cli.mjs revoke-key <id>
docker compose exec app node .output/server/cli.mjs hash-password             # prompts for the password
docker compose exec -T app node .output/server/cli.mjs backup /data/backups/x.db.gz   # gzipped online snapshot
```

`create-user --workspace <slug>` fails when the slug doesn't exist, unless `--new-workspace` is
passed; a new workspace needs `--role owner` (the default).
`--workspace` can be left out of `create-key` and `list-keys` while there is only one workspace.
`-T` turns off the TTY so the redirected output holds only the key. Outside Docker, after `pnpm build`:
`cd apps/app && DATA_DIR=./data node .output/server/cli.mjs list-keys`.

## Backups

Every night at 03:15 a launchd agent on the Mac mini (`com.zakiego.recall-backup`) runs
`scripts/backup.sh`. It takes a consistent snapshot of `/data/recall.db` inside the running
container with SQLite's online backup API, gzips it, copies it out with `docker compose cp`,
uploads it to the private R2 bucket `recall-backups` on Zakiego Lab as
`recall/YYYY-MM-DD/recall-<timestamp>.db.gz`, and deletes both local copies. A lifecycle rule
deletes objects after 30 days. `/data/models` isn't backed up, since the app downloads it again. If the
container is restarting at 03:15, the snapshot gets 3 tries, 60 s apart, before the run fails.

- Log: `~/selfhost/recall-backup.log` (each run ends with `done` or `FAILED`)
- Credentials: the Keychain item `cloudflare-personal-full`, through the cf-zakiego-lab wrapper.
  Nothing is written to `.env` or any other file.
- Snapshot command: `node .output/server/cli.mjs backup <dest-path>` in the container
  (`pnpm cli backup ./data/backups/x.db.gz` locally). Images built before the CLI was bundled
  fall back to the same steps inline through `node -e`.

```bash
./ops/install-backup.sh                                          # install or update the agent
launchctl kickstart gui/$(id -u)/com.zakiego.recall-backup       # run a backup now
launchctl print gui/$(id -u)/com.zakiego.recall-backup | grep -E 'runs|last exit'
```

`install-backup.sh` copies the script to `~/selfhost/bin/recall-backup.sh`, so rerun it after
changing `scripts/backup.sh` or the plist.

### Restore

On the Mac mini. Pick an object, download it, and check it before touching the live data:

```bash
CF=~/.agents/skills/cf-zakiego-lab/scripts/cf-zakiego-lab
"$CF" r2 objects list --bucket-name recall-backups --prefix recall/ --per-page 100
mkdir -p /tmp/recall-restore
"$CF" r2 objects get recall/2026-11-09/recall-20261109-142633.db.gz \
  --bucket-name recall-backups > /tmp/recall-restore/recall.db.gz
gunzip /tmp/recall-restore/recall.db.gz
sqlite3 /tmp/recall-restore/recall.db 'pragma integrity_check; select count(*) from memories'
```

Then swap it into the volume. The old files (including `-wal` and `-shm`, which must not sit
next to the restored file) move to `/data/pre-restore/`, and the copy runs as the `node` user
so the app can write to it:

```bash
cd ~/selfhost/recall
docker compose stop app
docker run --rm --user 1000:1000 -v recall_data:/data -v /tmp/recall-restore:/restore:ro recall:latest \
  sh -c 'rm -rf /data/pre-restore && mkdir /data/pre-restore && mv /data/recall.db* /data/pre-restore/ && cp /restore/recall.db /data/recall.db'
docker compose start app
curl -s http://127.0.0.1:4800/health
rm -r /tmp/recall-restore
```

Once the app looks right, `docker run --rm -v recall_data:/data recall:latest rm -rf /data/pre-restore`.

To restore a pre-deploy backup instead of an R2 object, copy it out of the volume first, then
check and swap it the same way:

```bash
cd ~/selfhost/recall
docker compose exec -T app ls -1 /data/backups/      # pre-deploy-<UTC timestamp>.db.gz, newest 3
mkdir -p /tmp/recall-restore
docker compose cp app:/data/backups/pre-deploy-20261109T081500Z.db.gz /tmp/recall-restore/recall.db.gz
gunzip /tmp/recall-restore/recall.db.gz
```

What a restore brings back with it:

- Logins are whatever the snapshot holds. Anyone who changed their password after it signs in with
  the old one, and passkeys added after it are gone (add them again in Settings, Account).
- A snapshot from before multi-user (migration 0004) has no users. On boot the app recreates `zaki`
  from `ADMIN_PASSWORD_HASH`; if that's no longer set, nobody can sign in. Recreate the owner with
  `docker compose exec app node .output/server/cli.mjs create-user zaki --workspace zaki --role owner`
  (it prompts for a new password). Workspace `zaki` exists after the migration, so this joins it.
