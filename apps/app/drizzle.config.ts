import { defineConfig } from "drizzle-kit"

// Migrations in drizzle/ are hand-written (see AGENTS.md, "Migrations"). The
// snapshots drizzle-kit diffs against stopped at 0001 and two files share the
// number 0006, so a generate into drizzle/ would emit DDL that already exists.
// Generate writes to the git-ignored drizzle-draft/ instead: `pnpm db:draft`
// prints the whole schema as DDL to copy the new statements from.
export default defineConfig({
  dialect: "sqlite",
  schema: "./src/server/db/schema.ts",
  out: "./drizzle-draft",
})
