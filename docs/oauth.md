# OAuth for MCP (claude.ai custom connectors)

claude.ai's custom connectors can't send an `Authorization` header. They only take a URL and
then run the MCP authorization flow (OAuth 2.1 with dynamic client registration). Recall acts as
both the authorization server and the protected resource. The authorization server is a Go
port of `@better-auth/oauth-provider` 1.7.18 (what the TypeScript build ran), with the same
endpoints, tables and token formats, so clients connected before the port keep working.

## How a connection goes

1. Claude calls `POST /mcp` with no token. Recall answers 401 with
   `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp"`.
2. Claude reads that document (`internal/oauth/resource.go`), finds the authorization server
   `https://recall.zakiego.com/api/auth`, and reads
   `/.well-known/oauth-authorization-server/api/auth`.
3. Claude registers itself at `/api/auth/oauth2/register` (unauthenticated dynamic
   registration) and sends the user to `/api/auth/oauth2/authorize` with PKCE and
   `resource=https://recall.zakiego.com/mcp`.
4. The server redirects to `/app/oauth/sign-in` (no session) or `/app/oauth/approve`
   (consent). Those routes keep the signed query in the `recall_oauth_query` cookie and open
   `/app/login?oauth=1` or `/app/oauth/consent`, so the query keeps the exact spelling its
   signature covers. `apps/app/src/lib/oauth-query.ts` puts the query back in the address bar
   right before each auth call, where `auth-client.ts` sends it along as `oauth_query`.
5. On the consent page the user picks a workspace and presses Allow. The token is tied to the
   session's active workspace at that moment (`internal/oauth/consent.go`), stored as
   `reference_id`.
6. Claude exchanges the code at `/api/auth/oauth2/token` and calls `/mcp` with
   `Bearer rco_...`. `VerifyAccessToken` hashes it, checks expiry, revocation, the client,
   the resource and that the user is still a member of the workspace.

## Pieces

Paths under `apps/server` unless they say otherwise.

| What | Where |
|---|---|
| Authorization server | `internal/oauth` (register, authorize, consent, token, revoke, introspect, userinfo, metadata), mounted by `internal/httpapi/oauth_server.go` |
| Tables | `internal/db/migrations/0011_oauth_provider.sql` |
| Discovery routes | `internal/httpapi/server.go`, `internal/oauth/resource.go` |
| Token check for `/mcp` | `internal/oauth/resource.go` (`MCPCaller`) |
| Login and consent hand-off | `internal/oauth/handoff.go`; the pages are `apps/app/src/routes/login.tsx` and `oauth.consent.tsx` |
| Edge routing | `apps/edge/src/routing.ts` sends `/.well-known/oauth-*` to the app |
| Tests | `internal/httpapi/auth_golden_test.go` replays the whole flow recorded from Better Auth; `auth_handover_test.go` opens clients, consents and tokens Better Auth wrote |

## Notes

- Access tokens (`rco_`) last an hour, refresh tokens (`rcr_`) 30 days. Both are stored as
  SHA-256 hashes.
- Scopes don't gate anything yet. `claudeai` is in the list because claude.ai asks for it when
  it registers, and registration fails on a scope the server doesn't list.
- To cut a connector off now, set `disabled = 1` on its row in `oauth_client`, or delete its
  rows in `oauth_access_token` and `oauth_refresh_token`.
