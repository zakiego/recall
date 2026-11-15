# Recall

Self-hosted memory engine. The server (Go: REST API, MCP, auth and the OAuth
server, SQLite with sqlite-vec and FTS5, e5 embeddings through ONNX Runtime in
a child process, one job loop, the admin CLI) is in `apps/server`. The
dashboard (Vite, React, TanStack Router, served by the Go binary under `/app`)
is in `apps/app`. The Claude Code hooks are in `integrations/claude-code`.
`PLAN.md` has the design and the ai-gateway contract, `README.md` has setup
and deploy, `docs/GO-PORT.md` has the port from TypeScript.

## Go: every command takes `-tags sqlite_fts5`

`github.com/mattn/go-sqlite3` compiles FTS5 in only under that tag, and the
database refuses to open without it.

```bash
cd apps/server
scripts/fetch-onnxruntime.sh                                   # ONNX Runtime, once per checkout (git-ignored lib/)
go run -tags sqlite_fts5 ./tools/fetch-model ../app/data/models  # the e5 model files (~120 MB), once per checkout
go build -tags sqlite_fts5 -o bin/recall ./cmd/recall
```

The binary looks for ONNX Runtime in `lib/onnxruntime/<os-arch>` next to or
one folder above itself, so run `bin/recall`, not `go run`, or set
`ONNXRUNTIME_LIB`. `scripts/setup-worktree.sh` links `apps/app/data/models` to
the main checkout's copy instead of downloading it. The server reads only its
process environment (no env file loading); see README, Develop.

## pnpm: use 10.17.0, never a bare `pnpm`

The repo pins `pnpm@10.17.0`. A global pnpm 12.x empties `node_modules` in this
repo, so don't run bare `pnpm <script>` unless `pnpm --version` says 10.17.0.

```bash
npx -y pnpm@10.17.0 install --frozen-lockfile   # from the repo root
```

Run tools directly from `apps/app` instead of through package scripts:
`npx tsc --noEmit`, `npx vitest run`, `npx vite build`.

## Worktrees

```bash
bash scripts/setup-worktree.sh   # copy .env* and the model files from the main checkout, install with pinned pnpm, fetch ONNX Runtime
bash scripts/clean-worktree.sh   # delete node_modules and build output (linked worktrees only)
```

The T3 actions in `t3.json` need importing in Settings > Projects > Actions.

## Verify

CI (`.github/workflows/ci.yml`) runs these on every push and pull request.
Run them before you push:

```bash
cd apps/server && test -z "$(gofmt -l .)"                 # format
cd apps/server && go vet -tags sqlite_fts5 ./...           # vet
cd apps/server && go test -tags sqlite_fts5 ./...          # server tests (need the model and ONNX Runtime)
cd apps/app && npx tsc --noEmit                           # dashboard typecheck
cd apps/app && npx vitest run                             # dashboard tests
cd apps/app && npx vite build                             # dashboard build
node --test integrations/claude-code/test/*.test.mjs      # hooks tests, from the root
```

One package: `go test -tags sqlite_fts5 ./internal/search/` from
`apps/server`. Use `-race` on packages with goroutines (jobs, embed, httpapi,
auth). `scripts/test-linux.sh arm64|amd64` runs the Go tests in a Linux
container, for changes that touch embedding or SQLite behavior.

Many tests compare against golden files in `apps/server/testdata`, exported
from the TypeScript build this server replaced. Their generators
(`testdata/gen`) import that build's backend, so they only run in a checkout
of the tree from before it was deleted (the Go port's first PR, before the
cleanup); see `testdata/gen/README.md`. A deliberate behavior change means
editing the golden by hand and saying why in the PR.

## Dashboard RPC

The dashboard calls the server through `GET|POST /app/api/rpc/<name>`
(`apps/app/src/lib/rpc-client.ts`, handlers in
`apps/server/internal/httpapi/rpc_*.go`). The input and output types are in
`apps/app/src/lib/rpc-types.ts`: change them together with the Go handler.

## Migrations

Migrations are hand-written SQL in
`apps/server/internal/db/migrations/NNNN_short_name.sql`, embedded into the
binary and applied in name order on open.

1. Add the file, numbered one past the highest. Separate statements with
   `--> statement-breakpoint`. Start with a comment that says why. Prefer
   additive changes (`ADD COLUMN`, `CREATE INDEX`): a rebuilt table can fire
   `ON DELETE CASCADE`.
2. Update the Go code that reads and writes the table (`internal/model` has
   the row types).
3. Test it the way `internal/db/migrate_test.go` does: open a database at the
   schema before your migration, insert old rows, migrate, check.
4. Update what pins the schema, on purpose: the file's name and SHA-256
   (`shasum -a 256`) in `shippedMigrations` (`migrate_test.go`); its name in
   `migrations` and its new or changed `sqlite_master` rows in `schema` in
   `testdata/m1.json` (`TestOpenAppliesEveryMigration` names what differs);
   and a new column in goldens that record whole rows (`testdata/rpc.json`,
   `rest.json`), which fail with the row that differs.

Never rename or edit a migration that has shipped. `migrate.go` records
applied migrations by file name, and production has both `0006_*` files
applied. `TestShippedMigrationsAreUnchanged` holds every file to its hash.

## Production

The app runs on the Mac mini (container `recall-app-1`). Don't deploy, write
through `docker exec`, or call the live gateway from a dev session.
