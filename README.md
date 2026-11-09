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
curl "$RECALL/v1/memories?container=zaki&review=1"     # only current facts flagged as conflicting (see reviewNote)
curl "$RECALL/v1/memories/mem_..."                       # one fact and its version chain
curl -X PATCH "$RECALL/v1/memories/mem_..." -d '{"fact":"..."}'   # writes a new version (and clears any review note)
curl -X PATCH "$RECALL/v1/memories/mem_..." -d '{"resolve":true}'  # clears the review note, no new version
curl -X DELETE "$RECALL/v1/memories/mem_..."             # soft delete (sets valid_to)
curl -X POST $RECALL/v1/search -d '{"container":"zaki","q":"what shoes","include":["memories","chunks"]}'
curl "$RECALL/v1/profile?container=zaki&q=shoes"         # static + dynamic summary, top facts, optional search
curl "$RECALL/v1/documents/doc_..."                      # processing status
curl "$RECALL/v1/containers"
curl "$RECALL/health"                                    # no auth
```

## MCP

Streamable HTTP at `/mcp`, same bearer key. Tools: `add_memory`, `search_memories`,
`get_profile`, `list_memories` (pass `needs_review: true` for flagged facts only).

When new content disagrees with a fact and it isn't clear which one is right, the extractor
keeps both as current and sets `reviewNote` on each. The dashboard shows them with a
"Needs review" badge and a filter; "Mark resolved" or an edit clears the note.

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
pnpm cli create-key <name> [--quiet] | list-keys | revoke-key <id> | hash-password  # --json on any
pnpm db:generate    # after editing src/server/db/schema.ts
```

## Deploy

zaki-selfhost: push to `main`, then on the Mac mini
`~/.agents/skills/zaki-selfhost/scripts/deploy.sh recall`. Port 4800, volume `recall_data`.

The image ships the admin CLI at `.output/server/cli.mjs` (bundled by `pnpm build`). It opens
`$DATA_DIR/recall.db` (`/data` in the container) and runs migrations like the app does. Run it from
the compose project directory:

```bash
docker compose exec app node .output/server/cli.mjs create-key <name>           # id, name and key
docker compose exec -T app node .output/server/cli.mjs create-key <name> --quiet > key.txt   # key only
docker compose exec app node .output/server/cli.mjs create-key <name> --json    # {"id","name","prefix","key"}
docker compose exec app node .output/server/cli.mjs list-keys                   # add --json for JSON
docker compose exec app node .output/server/cli.mjs revoke-key <id>
docker compose exec app node .output/server/cli.mjs hash-password             # prompts for the password
```

`-T` turns off the TTY so the redirected output holds only the key. Outside Docker, after `pnpm build`:
`DATA_DIR=./data node .output/server/cli.mjs list-keys`.
