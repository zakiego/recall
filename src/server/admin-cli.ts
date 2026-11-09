// Admin commands shared by `pnpm cli` (tsx, dev checkout) and the bundled
// .output/server/cli.mjs (production image). Keep stdout clean: only the
// command's result goes there, so `--quiet` and `--json` output can be piped.
import { createApiKey, hashPassword, listApiKeys, revokeApiKey } from "./auth"
import type { DB } from "./db"

export const USAGE = `usage: cli <command> [--json]
  create-key <name> [--quiet]   print a new API key (--quiet: the key only)
  list-keys                     list API keys
  revoke-key <id>               revoke an API key
  hash-password                 hash a password for ADMIN_PASSWORD_HASH (stdin or prompt)`

export type CliIo = {
  db: () => DB
  out: (line: string) => void
  err: (line: string) => void
  readPassword: () => Promise<string>
}

export async function runCli(argv: string[], io: CliIo): Promise<number> {
  const flags = new Set(argv.filter((a) => a.startsWith("--") || a === "-q"))
  const [cmd, ...args] = argv.filter((a) => !flags.has(a))
  const json = flags.has("--json")
  const quiet = flags.has("--quiet") || flags.has("-q")
  const unknown = [...flags].filter((f) => !["--json", "--quiet", "-q", "--help"].includes(f))
  if (unknown.length) {
    io.err(`unknown flag: ${unknown.join(" ")}\n${USAGE}`)
    return 1
  }

  switch (cmd) {
    case "create-key": {
      const { key, row } = createApiKey(io.db(), args.join(" ") || "cli")
      if (json) io.out(JSON.stringify({ id: row.id, name: row.name, prefix: row.prefix, key }))
      else if (quiet) io.out(key)
      else io.out(`created ${row.id} (${row.name})\n${key}`)
      return 0
    }
    case "list-keys": {
      const rows = listApiKeys(io.db())
      if (json) {
        io.out(JSON.stringify(rows))
        return 0
      }
      if (!rows.length) {
        io.out("no keys")
        return 0
      }
      for (const r of rows) {
        const status = r.revokedAt ? `revoked ${iso(r.revokedAt)}` : "active"
        const used = r.lastUsedAt ? `last used ${iso(r.lastUsedAt)}` : "never used"
        io.out(`${r.id}  ${r.prefix}…  ${r.name}  created ${iso(r.createdAt)}  ${used}  ${status}`)
      }
      return 0
    }
    case "revoke-key": {
      const id = args[0] ?? ""
      const ok = id !== "" && revokeApiKey(io.db(), id)
      if (json) io.out(JSON.stringify({ id, revoked: ok }))
      else if (ok) io.out("revoked")
      else io.err("no such active key")
      return ok ? 0 : 1
    }
    case "hash-password": {
      const pw = await io.readPassword()
      if (pw.length < 12) {
        io.err("password must be at least 12 characters")
        return 1
      }
      const hash = await hashPassword(pw)
      io.out(json ? JSON.stringify({ hash }) : hash)
      return 0
    }
    default:
      if (cmd) {
        io.err(`unknown command: ${cmd}\n${USAGE}`)
        return 1
      }
      io.out(USAGE)
      return 0
  }
}

function iso(ms: number | null) {
  return ms == null ? "" : new Date(ms).toISOString()
}
