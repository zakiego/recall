// Server functions for the OAuth consent page: thin wrappers over the Go server's /app/api/rpc/<name>, with the
// same export names and call signatures the TanStack server functions had,
// so call sites don't change. The types are in rpc-types.ts.
import { type InputFn, rpcFn } from "@/lib/rpc-client"
import type * as T from "@/lib/rpc-types"

export type { ConsentRequest } from "@/lib/rpc-types"

export const getConsentRequest = rpcFn<InputFn<T.GetConsentRequestInput, T.GetConsentRequestOutput>>("getConsentRequest", "GET")
