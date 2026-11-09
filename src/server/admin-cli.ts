// Admin commands shared by `pnpm cli` (tsx, dev checkout) and the bundled
// .output/server/cli.mjs (production image). Keep stdout clean: only the
// command's result goes there, so `--quiet` and `--json` output can be piped.
// See docs/multi-user.md for users and workspaces.
import { eq } from "drizzle-orm"
import { createApiKey, hashLegacyPassword, hashPassword, listApiKeys, revokeApiKey } from "./auth"
import { backupDb } from "./backup"
import { config } from "./config"
import type { DB } from "./db"
import { apiKeys, organization, ROLES, type Role } from "./db/schema"
import { addMember, bootstrapUsers, createWorkspace, findWorkspace, insertUser, listMembers, listMemberships, passwordProblem, UserError } from "./users"

export const USAGE = `usage: cli <command> [--json]
  create-user <username> [--workspace <slug> [--new-workspace]] [--role owner|admin|member]
                                password from stdin or a prompt. --workspace joins an
                                existing workspace; add --new-workspace to create it
                                instead. Without --workspace, a new workspace named
                                after the user. A new workspace needs --role owner
                                (the default)
  list-users [--workspace <slug>]
  create-key <name> [--workspace <slug>] [--quiet]
                                print a new API key (--quiet: the key only). --workspace
                                can be left out when there is only one workspace
  list-keys [--workspace <slug>]
  revoke-key <id>               revoke an API key
  hash-password                 pbkdf2 hash for ADMIN_PASSWORD_HASH (stdin or prompt)
  backup <dest-path>            gzipped online snapshot of the database, safe while the app runs`

export type CliIo = {
  db: () => DB
  out: (line: string) => void
  err: (line: string) => void
  readPassword: () => Promise<string>
}

const BOOL_FLAGS = ["--json", "--quiet", "-q", "--help", "--new-workspace"]
const VALUE_FLAGS = ["--workspace", "--role"]

function parse(argv: string[]) {
  const positionals: string[] = []
  const bools = new Set<string>()
  const values: Record<string, string> = {}
  const unknown: string[] = []
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!
    const [name, inline] = a.startsWith("--") && a.includes("=") ? [a.slice(0, a.indexOf("=")), a.slice(a.indexOf("=") + 1)] : [a, undefined]
    if (VALUE_FLAGS.includes(name)) {
      const v = inline ?? argv[++i]
      if (v === undefined) unknown.push(`${name} needs a value`)
      else values[name] = v
    } else if (BOOL_FLAGS.includes(a)) bools.add(a)
    else if (a.startsWith("--")) unknown.push(a)
    else positionals.push(a)
  }
  return { positionals, bools, values, unknown }
}

export async function runCli(argv: string[], io: CliIo): Promise<number> {
  const { positionals, bools, values, unknown } = parse(argv)
  const [cmd, ...args] = positionals
  const json = bools.has("--json")
  const quiet = bools.has("--quiet") || bools.has("-q")
  if (unknown.length) {
    io.err(`unknown flag: ${unknown.join(" ")}\n${USAGE}`)
    return 1
  }

  // The named workspace, or the only one when there's exactly one.
  const workspace = (required: boolean) => {
    const db = io.db()
    const slug = values["--workspace"]
    if (slug) {
      const ws = findWorkspace(db, slug)
      if (!ws) throw new UserError(`no workspace "${slug}"`)
      return ws
    }
    const all = db.select().from(organization).all()
    if (all.length === 1) return all[0]!
    if (!required) return null
    throw new UserError(all.length === 0 ? "no workspaces yet: run create-user first" : `pass --workspace (one of: ${all.map((w) => w.slug).join(", ")})`)
  }

  try {
    switch (cmd) {
      case "create-user": {
        const username = args[0]
        if (!username) {
          io.err(USAGE)
          return 1
        }
        const role = (values["--role"] ?? "owner") as Role
        if (!ROLES.includes(role)) {
          io.err(`role must be one of ${ROLES.join(", ")}`)
          return 1
        }
        const slug = values["--workspace"]
        const newWorkspace = bools.has("--new-workspace")
        if (newWorkspace && !slug) {
          io.err("--new-workspace needs --workspace <slug>")
          return 1
        }
        const db = io.db()
        // A pending ADMIN_PASSWORD_HASH bootstrap goes first, so `zaki` keeps the old password.
        bootstrapUsers(db)
        // Check the workspace before asking for a password: a typo must not
        // quietly make a new workspace, or an owner-less one.
        const existing = slug ? findWorkspace(db, slug) : undefined
        if (slug && !existing && !newWorkspace) throw new UserError(`no workspace "${slug}" (add --new-workspace to create it)`)
        if (slug && existing && newWorkspace) throw new UserError(`workspace "${slug}" already exists (drop --new-workspace to join it)`)
        if (!existing && role !== "owner") throw new UserError(`a new workspace needs an owner: use --role owner, or join an existing one with --workspace`)
        const password = await io.readPassword()
        const problem = passwordProblem(password)
        if (problem) throw new UserError(`Password: ${problem}`)
        const passwordHash = await hashPassword(password)
        // User, workspace and membership land together or not at all.
        const { u, ws } = db.transaction(() => {
          const u = insertUser(db, { username, passwordHash })
          const ws = existing ?? createWorkspace(db, { name: slug ?? username })
          addMember(db, { workspaceId: ws.id, userId: u.id, role })
          return { u, ws }
        })
        if (json) io.out(JSON.stringify({ id: u.id, username: u.username, workspace: ws.slug, role }))
        else io.out(`created ${u.username} (${u.id}), ${role} of ${ws.name} (${ws.slug})`)
        return 0
      }
      case "list-users": {
        const db = io.db()
        if (values["--workspace"]) {
          const rows = listMembers(db, workspace(true)!.id)
          if (json) io.out(JSON.stringify(rows))
          else for (const m of rows) io.out(`${m.username}  ${m.role}  joined ${iso(m.joinedAt)}`)
          return 0
        }
        const users = db.$client.prepare("SELECT id, username FROM user ORDER BY created_at").all() as { id: string; username: string }[]
        const rows = users.map((u) => ({ username: u.username, workspaces: listMemberships(db, u.id).map((w) => ({ slug: w.slug, role: w.role })) }))
        if (json) io.out(JSON.stringify(rows))
        else if (!rows.length) io.out("no users")
        else for (const r of rows) io.out(`${r.username}  ${r.workspaces.map((w) => `${w.slug}:${w.role}`).join(", ") || "(no workspace)"}`)
        return 0
      }
      case "create-key": {
        const ws = workspace(true)!
        const { key, row } = createApiKey(io.db(), { workspaceId: ws.id, name: args.join(" ") || "cli" })
        if (json) io.out(JSON.stringify({ id: row.id, name: row.name, prefix: row.prefix, workspace: ws.slug, key }))
        else if (quiet) io.out(key)
        else io.out(`created ${row.id} (${row.name}) in ${ws.slug}\n${key}`)
        return 0
      }
      case "list-keys": {
        const rows = listApiKeys(io.db(), workspace(true)!.id)
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
        const db = io.db()
        // An operator on the box may revoke any key; look up its workspace first.
        const row = id ? db.select({ workspaceId: apiKeys.workspaceId }).from(apiKeys).where(eq(apiKeys.id, id)).get() : undefined
        const ok = !!row && revokeApiKey(db, row.workspaceId, id)
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
        const hash = await hashLegacyPassword(pw)
        io.out(json ? JSON.stringify({ hash }) : hash)
        return 0
      }
      case "backup": {
        // Reads the file directly: no migrations, so it never changes the live DB.
        const dest = args[0]
        if (!dest) {
          io.err(`usage: backup <dest-path>   (e.g. /data/backups/recall.db.gz)`)
          return 1
        }
        const res = await backupDb(config().dbPath, dest)
        io.out(json ? JSON.stringify(res) : `backup ${res.dest} ${res.bytes} bytes (db ${res.dbBytes} bytes)`)
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
  } catch (err) {
    if (err instanceof UserError) {
      io.err(err.message)
      return 1
    }
    throw err
  }
}

function iso(ms: number | null) {
  return ms == null ? "" : new Date(ms).toISOString()
}
