# recall

A self-hosted memory engine for AI agents. Send it text, chats, URLs or files. It extracts
facts per container (a user, project or team), keeps them current (a newer fact supersedes the
older one and the history is kept), and serves them through a REST API, an MCP server and a web
dashboard. Live at https://recall.zakiego.com, running on the Mac mini (see `PLAN.md`).

- Search is local: SQLite FTS5 + sqlite-vec, merged with reciprocal rank fusion. No LLM call,
  single-digit milliseconds once the embedding model is warm.
- Extraction and profiles go through the ai-gateway (`/v1/chat`), one job at a time.
- Embeddings: `Xenova/multilingual-e5-small` (q8, 384 dims) in-process, cached in `/data/models`.

## API

Every `/v1` route takes `Authorization: Bearer rcl_...` (create keys in the dashboard).
`container` defaults to `RECALL_DEFAULT_CONTAINER`.

```bash
# Add text, a chat, or a URL. Returns 202 with a document id; facts appear a few seconds later.
curl -X POST $RECALL/v1/memories -H "authorization: Bearer $KEY" -H 'content-type: application/json' \
  -d '{"container":"zaki","content":"I switched to Puma running shoes."}'
curl -X POST $RECALL/v1/memories ... -d '{"container":"zaki","messages":[{"role":"user","content":"..."}]}'
curl -X POST $RECALL/v1/memories ... -d '{"container":"zaki","url":"https://example.com/post"}'
# Files (PDF, text, markdown, html, up to 20 MB)
curl -X POST $RECALL/v1/memories -H "authorization: Bearer $KEY" -F file=@notes.pdf -F container=zaki

curl "$RECALL/v1/memories?container=zaki"              # current facts, ?history=1 for all versions, ?cursor= to page
curl "$RECALL/v1/memories/mem_..."                       # one fact and its version chain
curl -X PATCH "$RECALL/v1/memories/mem_..." -d '{"fact":"..."}'   # writes a new version
curl -X DELETE "$RECALL/v1/memories/mem_..."             # soft delete (sets valid_to)
curl -X POST $RECALL/v1/search -d '{"container":"zaki","q":"what shoes","include":["memories","chunks"]}'
curl "$RECALL/v1/profile?container=zaki&q=shoes"         # static + dynamic summary, top facts, optional search
curl "$RECALL/v1/documents/doc_..."                      # processing status
curl "$RECALL/v1/containers"
curl "$RECALL/health"                                    # no auth
```

## MCP

Streamable HTTP at `/mcp`, same bearer key. Tools: `add_memory`, `search_memories`,
`get_profile`, `list_memories`.

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
| `ADMIN_PASSWORD_HASH` | from `pnpm cli hash-password` |
| `SESSION_SECRET` | 32+ random characters (`openssl rand -base64 48`) |
| `RECALL_DEFAULT_CONTAINER` | container used when a request names none (default `default`) |
| `DATA_DIR` | where `recall.db` and `models/` live (`/data` in the container, `./data` locally) |

## Develop

```bash
pnpm install
pnpm dev            # http://localhost:4800
pnpm test           # vitest, downloads the e5 model to ./data/models on first run
pnpm typecheck
pnpm cli create-key <name> | list-keys | revoke-key <id> | hash-password
pnpm db:generate    # after editing src/server/db/schema.ts
```

## Deploy

zaki-selfhost: push to `main`, then on the Mac mini
`~/.agents/skills/zaki-selfhost/scripts/deploy.sh recall`. Port 4800, volume `recall_data`.

## Backups

Every night at 03:15 a launchd agent on the Mac mini (`com.zakiego.recall-backup`) runs
`scripts/backup.sh`. It takes a consistent snapshot of `/data/recall.db` inside the running
container with SQLite's online backup API, gzips it, copies it out with `docker compose cp`,
uploads it to the private R2 bucket `recall-backups` on Zakiego Lab as
`recall/YYYY-MM-DD/recall-<timestamp>.db.gz`, and deletes both local copies. A lifecycle rule
deletes objects after 30 days. `/data/models` isn't backed up, since the app downloads it again.

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
