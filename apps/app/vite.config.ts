import tailwindcss from "@tailwindcss/vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { nitro } from "nitro/vite"
import { defineConfig } from "vite"

const APP_BASE = "/app"

export default defineConfig({
  resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
  plugins: [
    tailwindcss(),
    // The dashboard lives under /app. The public API (/v1, /mcp, /health) and
    // Better Auth (/api/auth) stay at the root; see src/lib/app-path.ts.
    tanstackStart({ router: { basepath: APP_BASE } }),
    nitro({
      preset: "node-server",
      // Built assets go to .output/public/app/assets, so they're served at
      // /app/assets with the dashboard. This instead of Vite's `base`: Nitro
      // takes `base` as its own baseURL and then only answers under /app,
      // redirecting the API there too.
      buildAssetsDir: `${APP_BASE.slice(1)}/assets`,
      plugins: ["./src/server/plugins/jobs.ts"],
      // sqlite-vec picks its platform package with import.meta.resolve at runtime,
      // which the tracer cannot see. Keep it external and copy the whole package.
      // transformers.js loads onnxruntime-node (native) with a runtime require,
      // so it stays external and gets traced too.
      traceDeps: [
        "sqlite-vec*",
        `sqlite-vec-${process.platform === "win32" ? "windows" : process.platform}-${process.arch}*`,
        "@huggingface/transformers",
        "onnxruntime-node",
      ],
    }),
    viteReact(),
  ],
})
