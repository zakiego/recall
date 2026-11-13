# OAuth for MCP (claude.ai custom connectors)

claude.ai's custom connectors can't send an `Authorization` header. They only take a URL and
then run the MCP authorization flow (OAuth 2.1 with dynamic client registration). Recall acts as
both the authorization server and the protected resource.

## How a connection goes

1. Claude calls `POST /mcp` with no token. Recall answers 401 with
   `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp"`.
2. Claude reads that document (`src/server/oauth.ts`), finds the authorization server
   `https://recall.zakiego.com/api/auth`, and reads
   `/.well-known/oauth-authorization-server/api/auth`.
3. Claude registers itself at `/api/auth/oauth2/register` (unauthenticated dynamic
   registration) and sends the user to `/api/auth/oauth2/authorize` with PKCE and
   `resource=https://recall.zakiego.com/mcp`.
4. Better Auth redirects to `/app/oauth/sign-in` (no session) or `/app/oauth/approve`
   (consent). Those server routes keep the signed query in the `recall_oauth_query` cookie and
   open `/app/login?oauth=1` or `/app/oauth/consent`. The router's SSR would otherwise 307 the
   page URL to its own spelling of the query (repeated `ba_param` keys become a JSON array)
   and break Better Auth's signature. `src/lib/oauth-query.ts` puts the query back in the
   address bar right before each auth call, where `oauthProviderClient()` reads it.
5. On the consent page the user picks a workspace and presses Allow. The token is tied to the
   session's active workspace at that moment (`postLogin.consentReferenceId` in
   `src/server/better-auth.ts`), stored as `reference_id`.
6. Claude exchanges the code at `/api/auth/oauth2/token` and calls `/mcp` with
   `Bearer rco_...`. `verifyOAuthAccessToken` hashes it, checks expiry, revocation, the client,
   the resource and that the user is still a member of the workspace.

## Pieces

| What | Where |
|---|---|
| Plugin config | `src/server/better-auth.ts` (`oauthProvider`, opaque tokens, `disableJwtPlugin`) |
| Tables | `drizzle/0011_oauth_provider.sql`, `oauth*` in `src/server/db/schema.ts` |
| Discovery routes | `src/routes/[.]well-known/*.$.ts` |
| Token check for `/mcp` | `src/server/oauth.ts` (`mcpCaller`) |
| Login and consent hand-off | `src/routes/oauth.sign-in.ts`, `oauth.approve.ts`, `oauth.consent.tsx` |
| Edge routing | `apps/edge/src/routing.ts` sends `/.well-known/oauth-*` to the app |
| Tests | `src/server/oauth.test.ts` runs the whole flow against the real handler |

## Notes

- Access tokens (`rco_`) last an hour, refresh tokens (`rcr_`) 30 days. Both are stored as
  SHA-256 hashes.
- Scopes don't gate anything yet. `claudeai` is in the list because claude.ai asks for it when
  it registers, and registration fails on a scope the server doesn't list.
- To cut a connector off now, set `disabled = 1` on its row in `oauth_client`, or delete its
  rows in `oauth_access_token` and `oauth_refresh_token`.
