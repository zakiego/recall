// Bundles src/cli.ts into .output/server/cli.mjs after the app build, so the
// production image (which ships only .output) can run admin commands:
//   node .output/server/cli.mjs create-key <name>
// Native modules stay external and resolve from .output/server/node_modules,
// which Nitro already fills for the app.
import { defineConfig } from "vite"

export default defineConfig({
  resolve: { tsconfigPaths: true },
  ssr: { noExternal: true, external: ["better-sqlite3", "sqlite-vec"] },
  build: {
    ssr: "src/cli.ts",
    target: "node24",
    outDir: ".output/server",
    emptyOutDir: false,
    minify: false,
    copyPublicDir: false,
    rolldownOptions: {
      external: ["better-sqlite3", "sqlite-vec", /^node:/],
      output: { entryFileNames: "cli.mjs", format: "es" },
    },
  },
})
