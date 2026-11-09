import { definePlugin } from "nitro"
import { config } from "../config"
import { getDb } from "../db"
import { warmEmbedder } from "../embed"
import { JobRunner } from "../jobs/runner"
import { createHandlers } from "../pipeline"

// Opens the DB (and runs migrations) at boot, so a broken native module fails
// the process instead of the first request. Then starts the job loop and
// loads the embedding model in the background.
export default definePlugin((nitroApp) => {
  const db = getDb()
  warmEmbedder()
  if (!config().jobsEnabled) return
  const runner = new JobRunner(db, createHandlers(db))
  runner.start()
  nitroApp.hooks.hook("close", () => runner.stop())
})
