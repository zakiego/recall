// Admin CLI entry. Dev: `pnpm cli <command>`. Production image:
// `node .output/server/cli.mjs <command>` (bundled by vite.cli.config.ts).
// Both open $DATA_DIR/recall.db and run migrations, same as the app.
import { createInterface } from "node:readline/promises"
import { runCli } from "./server/admin-cli"
import { getDb } from "./server/db"

async function readPassword(): Promise<string> {
  if (!process.stdin.isTTY) {
    let data = ""
    for await (const chunk of process.stdin) data += chunk
    return data.trim()
  }
  // Prompt on stderr so stdout carries only the hash.
  const rl = createInterface({ input: process.stdin, output: process.stderr })
  const pw = await rl.question("Password (12+ chars): ")
  rl.close()
  return pw.trim()
}

process.exitCode = await runCli(process.argv.slice(2), {
  db: getDb,
  out: (line) => console.log(line),
  err: (line) => console.error(line),
  readPassword,
})
