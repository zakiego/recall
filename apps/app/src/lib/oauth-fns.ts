// Server functions for the OAuth consent page (/app/oauth/consent).
import { verifyOAuthQueryParams } from "@better-auth/oauth-provider"
import { createServerFn } from "@tanstack/react-start"
import { eq } from "drizzle-orm"
import { z } from "zod"
import { getAuth } from "@/server/better-auth"
import { getDb } from "@/server/db"
import { oauthClient } from "@/server/db/schema"
import { getViewer } from "@/server/session"

export type ConsentRequest =
  | { ok: false; problem: string; signedIn: boolean }
  | {
      ok: true
      client: { name: string; uri: string | null; redirectHost: string }
      scopes: string[]
      username: string
      workspaces: { id: string; name: string }[]
      activeWorkspaceId: string | null
    }

function hostOf(url: string | null | undefined) {
  try {
    return url ? new URL(url).host : ""
  } catch {
    return ""
  }
}

// Reads the signed authorization request Better Auth put in the page's query
// string, so the page can say which app is asking and where it will send the user.
export const getConsentRequest = createServerFn({ method: "GET" })
  .validator(z.object({ query: z.string().max(8000) }))
  .handler(async ({ data }): Promise<ConsentRequest> => {
    const viewer = await getViewer()
    if (!viewer) return { ok: false, problem: "Log in first, then start connecting again from the app.", signedIn: false }
    const query = data.query.replace(/^\?/, "")
    const ctx = await getAuth().$context
    if (!query || !(await verifyOAuthQueryParams(query, ctx.secret))) {
      return { ok: false, problem: "This approval link is invalid or has expired. Start connecting again from the app.", signedIn: true }
    }
    const params = new URLSearchParams(query)
    const clientId = params.get("client_id") ?? ""
    const client = getDb()
      .select({ name: oauthClient.name, uri: oauthClient.uri, disabled: oauthClient.disabled })
      .from(oauthClient)
      .where(eq(oauthClient.clientId, clientId))
      .get()
    if (!client || client.disabled) return { ok: false, problem: "The app asking for access isn't registered here.", signedIn: true }
    if (!viewer.workspaces.length) return { ok: false, problem: "You aren't in a workspace yet. Ask an owner for an invite link.", signedIn: true }
    const redirectHost = hostOf(params.get("redirect_uri"))
    return {
      ok: true,
      client: { name: client.name || redirectHost || "An app", uri: client.uri ?? null, redirectHost },
      scopes: (params.get("scope") ?? "").split(" ").filter(Boolean),
      username: viewer.user.username,
      workspaces: viewer.workspaces.map((w) => ({ id: w.id, name: w.name })),
      activeWorkspaceId: viewer.workspace?.id ?? null,
    }
  })
