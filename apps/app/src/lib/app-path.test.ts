import { describe, expect, it } from "vitest"
import { legacyDashboardPath } from "./app-path"

const at = (path: string) => legacyDashboardPath(new URL(path, "https://recall.zakiego.com"))

describe("legacyDashboardPath", () => {
  it("sends old dashboard pages to /app, keeping the query", () => {
    expect(at("/")).toBe("/app/")
    expect(at("/login?next=%2Fsearch")).toBe("/app/login?next=%2Fsearch")
    expect(at("/search?c=zaki")).toBe("/app/search?c=zaki")
    expect(at("/settings")).toBe("/app/settings")
    expect(at("/settings/keys")).toBe("/app/settings/keys")
    expect(at("/keys")).toBe("/app/keys")
    expect(at("/invite/rci_abc")).toBe("/app/invite/rci_abc")
  })

  it("leaves the API, auth, MCP, health and /app itself alone", () => {
    for (const p of ["/v1/memories", "/mcp", "/health", "/api/auth/get-session", "/app", "/app/login", "/app/assets/x.js"]) {
      expect(at(p)).toBeNull()
    }
  })

  it("doesn't match on a prefix that isn't a path segment", () => {
    expect(at("/searching")).toBeNull()
    expect(at("/addons")).toBeNull()
  })
})
