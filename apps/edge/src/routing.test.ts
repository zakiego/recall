import { describe, expect, it } from "vitest"
import { isAppPath } from "./routing.ts"

describe("isAppPath", () => {
  it("sends the app's paths to the origin", () => {
    for (const p of [
      "/app",
      "/app/",
      "/app/login",
      "/app/assets/app-abc.css",
      "/app/_serverFn/abc",
      // The Gmail connector's OAuth redirect URI and start link, and its REST import.
      "/app/connectors/google/callback",
      "/app/connectors/google/start",
      "/v1/connectors/gmail/import",
      "/v1/memories",
      "/v1/memories/mem_1",
      "/v1/ask",
      "/mcp",
      "/health",
      "/api/auth/sign-in/username",
      "/api/auth/passkey/generate-authenticate-options",
      // OAuth discovery for claude.ai's custom connector.
      "/.well-known/oauth-protected-resource",
      "/.well-known/oauth-protected-resource/mcp",
      "/.well-known/oauth-authorization-server",
      "/.well-known/oauth-authorization-server/api/auth",
      "/.well-known/openid-configuration",
      "/login",
      "/settings/keys",
      "/invite/rci_abc",
    ]) {
      expect(isAppPath(p), p).toBe(true)
    }
  })

  it("serves everything else from the site", () => {
    for (const p of ["/", "/docs", "/docs/api", "/_next/static/x.js", "/og.png", "/robots.txt", "/sitemap.xml", "/apple", "/v10", "/mcpx", "/api/search", "/healthz", "/.well-known/security.txt"]) {
      expect(isAppPath(p), p).toBe(false)
    }
  })
})
