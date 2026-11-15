// Server functions for the OAuth consent page: thin wrappers over the Go server's /app/api/rpc/<name>, with the
// same export names and call signatures the TanStack server functions had,
// so call sites don't change. The types come from the old functions in
// src/server/fns/ (type-only, erased from the bundle); M10b moves them here
// before src/server goes.
import { rpcFn } from "@/lib/rpc-client"
import type * as F from "@/server/fns/oauth-fns"

export type ConsentRequest = F.ConsentRequest

export const getConsentRequest = rpcFn<typeof F.getConsentRequest>("getConsentRequest", "GET")
