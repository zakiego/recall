import tailwindcss from "@tailwindcss/vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { nitro } from "nitro/vite"
import { defineConfig } from "vite"

export default defineConfig({
  resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
  plugins: [
    tailwindcss(),
    tanstackStart(),
    nitro({
      preset: "node-server",
      plugins: ["./src/server/plugins/jobs.ts"],
      // sqlite-vec picks its platform package with import.meta.resolve at runtime,
      // which the tracer cannot see. Keep it external and copy the whole package.
      traceDeps: ["sqlite-vec*", `sqlite-vec-${process.platform === "win32" ? "windows" : process.platform}-${process.arch}*`],
    }),
    viteReact(),
  ],
})
