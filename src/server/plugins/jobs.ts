import { definePlugin } from "nitro"
import { getDb } from "../db"

// Opens the DB (and runs migrations) at boot, so a broken native module fails
// the process instead of the first request. The job loop starts here in phase 3.
export default definePlugin(() => {
  getDb()
})
