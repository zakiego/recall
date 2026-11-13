import path from "node:path"
import { defineConfig } from "vitest/config"

export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: {
    // Tests that point DATA_DIR at a tmp dir would otherwise put the models
    // there too and download e5 (~120 MB) again on every run.
    env: { MODELS_DIR: process.env.MODELS_DIR ?? path.resolve(import.meta.dirname, "data/models") },
    testTimeout: 60_000,
    hookTimeout: 120_000,
    projects: [
      {
        extends: true,
        test: { name: "node", include: ["src/**/*.test.ts"], environment: "node" },
      },
      {
        extends: true,
        test: {
          name: "dom",
          include: ["src/**/*.test.tsx"],
          environment: "jsdom",
          setupFiles: ["./src/test/setup.ts"],
        },
      },
    ],
  },
})
