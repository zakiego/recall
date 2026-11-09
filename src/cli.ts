// Admin CLI: pnpm cli <command>. In the container: node .output/server/... is
// not available, so create keys in the web UI there.
import { createInterface } from "node:readline/promises"
import { createApiKey, hashPassword, listApiKeys, revokeApiKey } from "./server/auth"
import { getDb } from "./server/db"

const [cmd, ...args] = process.argv.slice(2)

async function readPassword(): Promise<string> {
  if (!process.stdin.isTTY) {
    let data = ""
    for await (const chunk of process.stdin) data += chunk
    return data.trim()
  }
  const rl = createInterface({ input: process.stdin, output: process.stdout })
  const pw = await rl.question("Password (12+ chars): ")
  rl.close()
  return pw.trim()
}

switch (cmd) {
  case "create-key": {
    const { key, row } = createApiKey(getDb(), args.join(" ") || "cli")
    console.log(`created ${row.id} (${row.name})\n${key}`)
    break
  }
  case "list-keys":
    console.table(listApiKeys(getDb()))
    break
  case "revoke-key":
    console.log(revokeApiKey(getDb(), args[0] ?? "") ? "revoked" : "no such active key")
    break
  case "hash-password": {
    const pw = await readPassword()
    if (pw.length < 12) {
      console.error("password must be at least 12 characters")
      process.exit(1)
    }
    console.log(await hashPassword(pw))
    break
  }
  default:
    console.log("usage: pnpm cli create-key <name> | list-keys | revoke-key <id> | hash-password")
    process.exit(cmd ? 1 : 0)
}
