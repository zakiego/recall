// Better Auth in the browser: username sign-in, passkeys, password changes,
// and OAuth consent. Same origin as the dashboard, mounted at /api/auth.
import { oauthProviderClient } from "@better-auth/oauth-provider/client"
import { passkeyClient } from "@better-auth/passkey/client"
import { usernameClient } from "better-auth/client/plugins"
import { createAuthClient } from "better-auth/react"

export const authClient = createAuthClient({ basePath: "/api/auth", plugins: [usernameClient(), passkeyClient(), oauthProviderClient()] })

// Better Auth errors carry a status; 429 means the sign-in rate limit kicked in.
export function authErrorMessage(error: { status?: number; message?: string; code?: string } | null | undefined, fallback: string) {
  if (!error) return fallback
  if (error.status === 429) return "Too many tries. Wait a minute and try again."
  if (error.code === "INVALID_USERNAME_OR_PASSWORD" || error.status === 401) return "That username and password don't match."
  return error.message || fallback
}

// WebAuthn errors when the person closes the prompt or the device says no.
export function passkeyCancelled(error: { message?: string; code?: string } | null | undefined) {
  const s = `${error?.code ?? ""} ${error?.message ?? ""}`
  return /NotAllowed|AbortError|cancel|timed out|not allowed/i.test(s)
}
