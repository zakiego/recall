// Bundles src/cli.ts into .output/server/cli.mjs after the app build, so the
// production image (which ships only .output) can run admin commands:
//   node .output/server/cli.mjs create-key <name>
// Native modules stay external and resolve from .output/server/node_modules,
// which Nitro already fills for the app.
//
// It also builds .output/server/embed-child.mjs, the process the app forks to
// run the embedding model (src/server/embed.ts finds it next to index.mjs).
import { defineConfig } from "vite"

export default defineConfig({
  resolve: { tsconfigPaths: true },
  ssr: { noExternal: true, external: ["better-sqlite3", "sqlite-vec"] },
  build: {
    ssr: true,
    target: "node24",
    outDir: ".output/server",
    emptyOutDir: false,
    minify: false,
    copyPublicDir: false,
    rolldownOptions: {
      input: { cli: "src/cli.ts", "embed-child": "src/server/embed-child.ts" },
      external: ["better-sqlite3", "sqlite-vec", /^node:/],
      // Shared code goes in its own folder, away from Nitro's chunks/.
      output: { entryFileNames: "[name].mjs", chunkFileNames: "cli-chunks/[name]-[hash].mjs", format: "es" },
    },
  },
})
