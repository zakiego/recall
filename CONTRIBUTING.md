# Contributing

Thanks for looking. Recall is a personal project that I run in production, so I keep the scope tight. Bug reports and small fixes are very welcome. For a new feature, open an issue first so we can agree on it before you write code.

## Setup

See [Develop](README.md#develop) in the README. In short: Go (the version in `apps/server/go.mod`), Node 24, and pnpm 10.17.0 run through `npx -y pnpm@10.17.0` (a newer global pnpm breaks this repo's `node_modules`). Every Go command takes `-tags sqlite_fts5`.

## Before you open a pull request

Run what CI runs:

```bash
cd apps/server && test -z "$(gofmt -l .)"
cd apps/server && go vet -tags sqlite_fts5 ./...
cd apps/server && go test -tags sqlite_fts5 ./...
cd apps/app && npx tsc --noEmit && npx vitest run && npx vite build
node --test integrations/claude-code/test/*.test.mjs
```

The server tests need the embedding model and ONNX Runtime (README, Develop). Many tests compare against golden files in `apps/server/testdata`. If your change alters behavior on purpose, edit the golden by hand and say why in the PR.

## Conventions

- Commits follow [Conventional Commits](https://www.conventionalcommits.org/): `fix(server): handle empty containers`.
- One logical change per commit.
- Schema changes are new SQL files in `apps/server/internal/db/migrations`. Never edit a migration that has shipped. `AGENTS.md` has the full checklist.
- An MCP tool change also updates `apps/server/internal/mcp/playbook.md`.
- Test data must be made up. No real names, emails, phone numbers or memories in fixtures, even your own.

## Security issues

Don't file them as issues. See [SECURITY.md](SECURITY.md).
