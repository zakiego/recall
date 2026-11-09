// Admin session checks for server functions. Server-only.
import { getCookie, getRequest } from "@tanstack/react-start/server"
import { SESSION_COOKIE, verifySession } from "./auth"
import { config } from "./config"

export class UnauthorizedError extends Error {
  constructor() {
    super("UNAUTHORIZED")
  }
}

export function isAuthed(): boolean {
  const token = getCookie(SESSION_COOKIE)
  return !!token && verifySession(token, config().sessionSecret)
}

export function requireAdmin() {
  if (!isAuthed()) throw new UnauthorizedError()
}

// Writes from the dashboard must come from the dashboard's own origin.
export function requireSameOrigin() {
  const req = getRequest()
  const origin = req.headers.get("origin")
  if (!origin) return
  const host = req.headers.get("x-forwarded-host") ?? req.headers.get("host")
  if (!host || new URL(origin).host !== host) throw new Error("cross-origin request refused")
}

export function cookieSecure(): boolean {
  const req = getRequest()
  const host = (req.headers.get("x-forwarded-host") ?? req.headers.get("host") ?? "").split(":")[0]
  return host !== "localhost" && host !== "127.0.0.1"
}
