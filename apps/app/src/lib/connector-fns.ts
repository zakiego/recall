// Server functions behind the Connectors page: thin wrappers over the Go server's /app/api/rpc/<name>, with the
// same export names and call signatures the TanStack server functions had,
// so call sites don't change. The types are in rpc-types.ts.
import { type InputFn, type NoInputFn, rpcFn } from "@/lib/rpc-client"
import type * as T from "@/lib/rpc-types"

export const getConnectors = rpcFn<InputFn<T.GetConnectorsInput, T.GetConnectorsOutput>>("getConnectors", "GET")
export const searchGmailMessages = rpcFn<InputFn<T.SearchGmailMessagesInput, T.SearchGmailMessagesOutput>>("searchGmailMessages", "POST")
export const importGmailMessages = rpcFn<InputFn<T.ImportGmailMessagesInput, T.ImportGmailMessagesOutput>>("importGmailMessages", "POST")
export const disconnectGmail = rpcFn<NoInputFn<T.DisconnectGmailOutput>>("disconnectGmail", "POST")
export const saveAppConnectorFn = rpcFn<InputFn<T.SaveAppConnectorFnInput, T.SaveAppConnectorFnOutput>>("saveAppConnectorFn", "POST")
export const removeAppConnectorFn = rpcFn<InputFn<T.RemoveAppConnectorFnInput, T.RemoveAppConnectorFnOutput>>("removeAppConnectorFn", "POST")
export const syncAppConnectorFn = rpcFn<InputFn<T.SyncAppConnectorFnInput, T.SyncAppConnectorFnOutput>>("syncAppConnectorFn", "POST")
