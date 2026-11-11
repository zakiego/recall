# Recall

Self-hosted memory engine. The app (TanStack Start, SQLite with sqlite-vec,
in-process e5 embeddings, one job loop) is in `apps/app`. The Claude Code hooks
are in `integrations/claude-code`. `PLAN.md` has the design and the ai-gateway
contract, `README.md` has setup and deploy.

## pnpm: use 10.17.0, never a bare `pnpm`

The repo pins `pnpm@10.17.0`. A global pnpm 12.x empties `node_modules` in this
repo, so don't run bare `pnpm <script>` unless `pnpm --version` says 10.17.0.

```bash
npx -y pnpm@10.17.0 install --frozen-lockfile   # from the repo root
```

Run tools directly from `apps/app` instead of through package scripts:
`npx tsc --noEmit`, `npx vitest run`, `npx tsx src/cli.ts`.

## Verify

CI (`.github/workflows/ci.yml`) runs these on every push and pull request.
Run them before you push:

```bash
cd apps/app && npx tsc --noEmit                    # typecheck
cd apps/app && npx vitest run                      # app tests
node --test integrations/claude-code/test/*.test.mjs   # hooks tests, from the root
```

One test file: `npx vitest run src/server/<file>.test.ts` from `apps/app`.
The first test run downloads the e5 model (~120 MB) into
`apps/app/data/models`. Copy it from another checkout if you have one.

## Migrations

Migrations are hand-written SQL. Don't use `drizzle-kit generate` to write
them: its snapshots were never kept up to date, so it can't diff correctly.
`db:generate` refuses on purpose.

1. Change `apps/app/src/server/db/schema.ts`.
2. Add `apps/app/drizzle/NNNN_short_name.sql`, numbered one past the highest
   file. Separate statements with `--> statement-breakpoint`. Start with a
   comment that says why. Prefer additive changes (`ADD COLUMN`,
   `CREATE INDEX`): a rebuilt table can fire `ON DELETE CASCADE`.
3. Run `npx tsx scripts/bundle-migrations.ts` from `apps/app`. It regenerates
   `src/server/db/migrations.gen.ts`, which the app, the CLI and the tests
   apply. `db.test.ts` fails when you forget.
4. Test it the way `src/server/db/migration.test.ts` does: open a database
   at the schema before your migration, insert old rows, migrate, check.

For the DDL of a new table or index, `npm run db:draft` from `apps/app`
prints the whole current schema to the git-ignored `drizzle-draft/`. Copy the
statements you need from there.

Never rename or edit a migration that has shipped. `migrate.ts` records
applied migrations by file name, and production has both `0006_*` files
applied.

## Production

The app runs on the Mac mini (container `recall-app-1`). Don't deploy, write
through `docker exec`, or call the live gateway from a dev session.
