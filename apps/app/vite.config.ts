import tailwindcss from "@tailwindcss/vite"
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

// The dashboard is a static single-page app under /app that the Go server
// (apps/server) serves from its embedded build: `vite build` writes index.html,
// favicon.svg and hashed files under assets/ to dist/, which the Go build
// copies into apps/server/internal/web/dist. The API (/api, /v1, /mcp,
// /health, /.well-known) stays at the root and belongs to the Go server;
// `vite dev` proxies it there. (The shape is Palui's apps/app.)
const APP_BASE = "/app/"

// Where `vite dev` sends the API: a `recall serve` on its default port.
// Override with RECALL_API_URL (for example `RECALL_API_URL=http://localhost:4871 pnpm dev`).
const DEFAULT_API_URL = "http://localhost:3000"

export default defineConfig(({ mode }) => {
  const vars = loadEnv(mode, process.cwd(), "RECALL_")
  const api = vars.RECALL_API_URL || DEFAULT_API_URL
  // Keep the Host header (changeOrigin false): the Go server checks a write's
  // Origin against RECALL_URL, and its dev default is this server's address.
  const proxy = { target: api, changeOrigin: false }
  return {
    base: APP_BASE,
    // public/app/favicon.svg lands at dist/favicon.svg, served as /app/favicon.svg.
    publicDir: "public/app",
    resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
    server: {
      port: 4800,
      strictPort: true,
      proxy: {
        "/api": proxy,
        "/app/api": proxy,
        "/app/connectors/google": proxy,
        "/app/oauth/sign-in": proxy,
        "/app/oauth/approve": proxy,
        // The old root paths answer 302 to /app.
        "^/(login|search|ask|graph|add|keys|settings|invite)(/.*)?$": proxy,
        "/v1": proxy,
        "/mcp": proxy,
        "/health": proxy,
        "/.well-known": proxy,
      },
    },
    preview: { port: 4800 },
    build: {
      outDir: "dist",
      assetsDir: "assets",
      emptyOutDir: true,
    },
    plugins: [
      // File routes from src/routes.
      tanstackRouter({ target: "react", autoCodeSplitting: true }),
      viteReact(),
      tailwindcss(),
    ],
  }
})
