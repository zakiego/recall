import { definePlugin } from "nitro"
import { config } from "../config"
import { scheduleAppSync } from "../connectors/apps"
import { getDb } from "../db"
import { warmEmbedder } from "../embed"
import { JobRunner } from "../jobs/runner"
import { createHandlers } from "../pipeline"
import { scheduleReminders } from "../reminders"
import { bootstrapUsers, findWorkspace, LEGACY_WORKSPACE } from "../users"

// Opens the DB (and runs migrations) at boot, so a broken native module fails
// the process instead of the first request. Creates the first user from
// ADMIN_PASSWORD_HASH when there are none (docs/multi-user.md). Then starts the
// job loop and loads the embedding model in the background.
export default definePlugin((nitroApp) => {
  const db = getDb()
  const boot = bootstrapUsers(db)
  if (boot.created) console.log(`[recall] created user zaki as owner of workspace ${boot.workspaceId} from ADMIN_PASSWORD_HASH`)
  else if (boot.reason === "no_admin_hash") {
    // Migrated data lives in workspace "zaki"; a fresh database has no workspace yet.
    const cmd = findWorkspace(db, LEGACY_WORKSPACE.id) ? "create-user <username> --workspace zaki --role owner" : "create-user <username>"
    console.log(`[recall] no users yet: run \`pnpm cli ${cmd}\``)
  }
  warmEmbedder()
  if (!config().jobsEnabled) return
  const runner = new JobRunner(db, createHandlers(db))
  runner.start()
  // The hourly reminder check. It re-queues itself; this covers the first boot.
  scheduleReminders(db)
  // The hourly pull of the Oslo and Finansial feeds, the same way, but only
  // while a connector is on. Saving one arms it (connector-fns.ts).
  scheduleAppSync(db)
  nitroApp.hooks.hook("close", () => runner.stop())
})
