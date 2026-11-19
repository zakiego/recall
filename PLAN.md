# recall: plan

A self-hosted memory engine for AI agents, in the spirit of supermemory.ai. Send it chats,
text, URLs or files. It extracts facts per user or project, keeps them current (newer facts
supersede older ones), and serves them back through a REST API, an MCP server and a web UI.

Runs as one Docker container on the Mac mini M4 (zaki-selfhost), published at
`https://recall.zakiego.com`. All LLM work goes through Simpang Router
(`https://simpang.zakiego.com/api`). No other AI vendor. Until 2026-11-17 it went through the
ai-gateway (`/v1/chat`, AI SDK UI message stream).

## Decisions (already made, don't revisit)

The MVP was built in TypeScript (TanStack Start on Nitro, better-sqlite3, Drizzle, transformers.js,
Better Auth). The server was then ported to Go with the same behavior, database and API
(`docs/GO-PORT.md`). The table is the stack today; the Phases section below is how the MVP was built.

| Area | Choice |
|---|---|
| Server | Go (`apps/server`), one binary `recall`: REST API, MCP, auth, jobs, admin CLI, and the dashboard's files |
| UI | Vite + React + TanStack Router single-page app under `/app` (`apps/app`), ui-sdk + Tailwind 4 (same look as ai-gateway's dashboard), `@supermemory/memory-graph` for the graph page |
| DB | SQLite (`mattn/go-sqlite3`, FTS5 on), file at `/data/recall.db` (volume) |
| Search | SQLite FTS5 (keywords) + sqlite-vec (vectors), merged with reciprocal rank fusion |
| Schema | hand-written SQL migrations embedded in the binary (`apps/server/internal/db/migrations`) |
| Embeddings | `Xenova/multilingual-e5-small` quantized ONNX, 384 dims, through ONNX Runtime in a child process that stops when idle. Cache in `/data/models`. Prefix `query: ` / `passage: ` as e5 requires |
| LLM | Simpang `POST /v1/chat/completions` (streamed), model `claude-haiku-5-5` (env `EXTRACT_MODEL`; `FALLBACK_MODEL` defaults to the same model, so there is no fallback) |
| Jobs | `jobs` table + in-process loop, concurrency 1 |
| MCP | Streamable HTTP at `/mcp` |
| Deploy | zaki-selfhost, distroless image (`gcr.io/distroless/cc-debian12`), port **4800** |

## Gateway facts (Simpang Router)

- `POST {GATEWAY_URL}/v1/chat/completions` with `Authorization: Bearer sk-sp-...`, body
  `{ model, reasoning_effort: "low", stream: true, messages: [system, user] }`.
- Response is an OpenAI chat-completions stream (SSE). Join every `choices[].delta.content` until
  `data: [DONE]`, then parse JSON and validate. Retry once on invalid JSON. A chunk with `error`
  instead of `choices` is an upstream failure after the 200.
- Errors are OpenAI-shaped: `{ error: { message, type, code } }`. Simpang's reason is in `code`
  (`model_not_allowed`, `budget_exceeded`, `rate_limited`); recall reads `code` before `type`.
  `model_not_allowed` switches to `FALLBACK_MODEL` when that is a different model.
- Streaming matters: Simpang sits behind a Cloudflare tunnel, which drops a response that sends no
  bytes for 100 s.
- The key is named `recall` in Zaki's Simpang workspace. Recall uses Haiku 5.5 only, no Haiku 4.5.
  Simpang meters and budgets it like any other key.
- Still keep calls in the job loop, one at a time, never on a request path (search, profile read).

## Data model

All rows carry `container` (the namespace: a user id, project or team, free-form string).

- `api_keys`: id, name, key_hash (sha256), prefix, created_at, last_used_at, revoked_at
- `documents`: id, container, source_type (`text|chat|url|file`), title, url, content, status
  (`queued|processing|done|failed`), error, metadata json, created_at
- `chunks`: id, document_id, container, idx, text. Plus `chunks_fts` (FTS5) and `chunks_vec` (vec0, float[384])
- `memories`: id, container, fact, keywords (English + Indonesian synonyms, written by the LLM),
  kind (`static|dynamic`), source_document_id, supersedes_id, valid_from, valid_to (null = current), created_at.
  Plus `memories_fts` (over fact + keywords) and `memories_vec`
- `profiles`: container (pk), static_text, dynamic_text, updated_at
- `jobs`: id, type (`ingest|extract|profile`), payload json, status, attempts, run_after, last_error, created_at

## Pipeline

1. `POST /v1/memories` stores a document (`queued`) and an `ingest` job. Returns 202 with the id.
2. `ingest`: resolve content (URL via fetch + `@mozilla/readability` + `linkedom`; PDF via `unpdf`;
   text/markdown/chat as is), chunk (~800 tokens, overlap 100), embed, write chunks. Enqueue `extract`.
3. `extract`: embed the new content, pull the top 20 current memories in that container by vector,
   send both to the gateway. The model returns
   `{ add: [{fact, keywords, kind}], update: [{id, fact, keywords}], invalidate: [id] }`.
   `update` writes a new row with `supersedes_id` and sets the old row's `valid_to`. `invalidate`
   only sets `valid_to`. Embed and index new rows. Enqueue one `profile` job per container (dedupe).
4. `profile`: send current memories to the gateway, store a short static + dynamic summary.
5. Failed jobs retry with backoff (30 s, 2 min, 10 min), then mark the document `failed`.

## API (bearer API key)

- `POST /v1/memories` `{ container, content | url, type?, title?, metadata? }` and multipart for files
- `GET /v1/memories?container=&cursor=` list current memories; `?history=1` includes superseded
- `PATCH /v1/memories/:id`, `DELETE /v1/memories/:id` (soft: sets valid_to)
- `POST /v1/search` `{ container, q, limit?, include?: ["memories","chunks"] }`. Hybrid FTS5 + vector,
  RRF merge, current memories only. No gateway call. Target under 100 ms after the model is warm
- `GET /v1/profile?container=` returns the stored profile plus top memories
- `GET /v1/documents/:id` for status
- `GET /health` returns 200 without auth (healthcheck)

## MCP (`/mcp`, same bearer key)

Tools: `add_memory`, `search_memories`, `get_profile`, `list_memories`. Container comes from an
argument, defaulting to `RECALL_DEFAULT_CONTAINER`.

Later: `get_playbook`, listed first, returns the operating rules from
`apps/server/internal/mcp/playbook.md` with an FNV-1a version, and the server
instructions shrink to a pointer at it (the pattern vorneo, kebunsawit, lumbung
and simpang use).

## Web UI (password login)

Login copies ai-gateway's approach: `ADMIN_PASSWORD_HASH` (pbkdf2) + `SESSION_SECRET`, httpOnly cookie.
A container switcher sits in the header.

1. Memories: table of current facts, filter, edit, delete, expand to see the superseded chain
2. Search: query box, ranked hits with scores and source (memory or chunk)
3. Graph: `@supermemory/memory-graph` fed with documents and memories
4. Add: paste text, a URL, or upload a file. Shows job status
5. Keys: create (show the key once), revoke

Follow `zaki-design-ui` for UI work.

## Phases

How the TypeScript MVP was built (kept for the record; the commands are that build's). Each phase ended green: `pnpm typecheck`, `pnpm test` (vitest), and a manual check where noted.

1. **Scaffold**: TanStack Start + shadcn + Tailwind 4, Drizzle + better-sqlite3 + sqlite-vec loading,
   migrations, `/health`. Check: `pnpm build` and `node .output/server/index.mjs` boot and load sqlite-vec.
2. **Embeddings + search core**: transformers.js wrapper, chunker, FTS5 + vec tables, RRF search.
   Tests with a temp DB.
3. **Gateway client + jobs**: stream reader, zod schemas, job loop as a Nitro plugin, retries.
   Test the stream parser against a recorded fixture, not the live gateway.
4. **API + auth**: routes above, API keys, ingest (text, URL, PDF), extract, profile.
   Manual check: add 3 contradicting facts ("loves Adidas", then "switched to Puma"), confirm supersede.
5. **MCP**: `/mcp` tools. Check with `claude mcp add --transport http` against local.
6. **Web UI**: login + the five pages.
7. **Docker + deploy**: Dockerfile from `~/.agents/skills/zaki-selfhost/assets/Dockerfile.nitro` on
   bookworm-slim, healthcheck via `node -e "fetch('http://127.0.0.1:3000/health')..."` (no wget in slim),
   compose with `127.0.0.1:4800:3000`, volume `data:/data`, `GATEWAY_URL=http://host.docker.internal:8793`.
   Confirm native modules (better-sqlite3, sqlite-vec, onnxruntime-node) survive Nitro's `.output`;
   mark them external if not. Then follow the zaki-selfhost skill: private repo `zakiego/recall`,
   clone to `~/selfhost/recall`, `deploy.sh recall`, `add-host.sh recall.zakiego.com 4800 "recall memory engine"`.

## Things only Zaki can do

- Create `.env` files (a hook blocks agents from touching them). Keys: `GATEWAY_URL`, `GATEWAY_KEY`,
  `EXTRACT_MODEL`, `ADMIN_PASSWORD_HASH`, `SESSION_SECRET`, `RECALL_DEFAULT_CONTAINER`.
- Restart the ai-gateway (to enable Haiku 5.5) and the cloudflared tunnel after `add-host.sh`.

## Constraints

- Mac mini disk: about 7.7 GB free on 2026-11-09; `deploy.sh` refuses under 5 GB. Keep the image lean.
- OrbStack VM: 6 GB RAM (raised 2026-11-11), shared with Umami and Palui. The app container is capped at 768 MB (`compose.yaml`); the Go image measured about 15 MiB idle and 190 to 230 MiB with the model loaded. e5-small fits; nothing bigger without asking.
- Never start a background dev server without killing it via pidfile before the turn ends (see CLAUDE.md).

## Out of scope for the MVP

Connectors (Gmail, Notion, Drive), multi-user accounts, image OCR, a browser extension.
