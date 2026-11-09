// Admin CLI entry. Dev: `pnpm cli <command>`. Production image:
// `node .output/server/cli.mjs <command>` (bundled by vite.cli.config.ts).
// Both open $DATA_DIR/recall.db and run migrations, same as the app.
import { runCli } from "./server/admin-cli"
import { getDb } from "./server/db"
import { readPassword } from "./server/password-prompt"

process.exitCode = await runCli(process.argv.slice(2), {
  db: getDb,
  out: (line) => console.log(line),
  err: (line) => console.error(line),
  readPassword: () => readPassword(process.stdin, process.stderr),
})
