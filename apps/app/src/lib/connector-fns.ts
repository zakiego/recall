// Server functions behind the Connectors page: thin wrappers over the Go server's /app/api/rpc/<name>, with the
// same export names and call signatures the TanStack server functions had,
// so call sites don't change. The types come from the old functions in
// src/server/fns/ (type-only, erased from the bundle); M10b moves them here
// before src/server goes.
import { rpcFn } from "@/lib/rpc-client"
import type * as F from "@/server/fns/connector-fns"

export const getConnectors = rpcFn<typeof F.getConnectors>("getConnectors", "GET")
export const searchGmailMessages = rpcFn<typeof F.searchGmailMessages>("searchGmailMessages", "POST")
export const importGmailMessages = rpcFn<typeof F.importGmailMessages>("importGmailMessages", "POST")
export const disconnectGmail = rpcFn<typeof F.disconnectGmail>("disconnectGmail", "POST")
export const saveAppConnectorFn = rpcFn<typeof F.saveAppConnectorFn>("saveAppConnectorFn", "POST")
export const syncAppConnectorFn = rpcFn<typeof F.syncAppConnectorFn>("syncAppConnectorFn", "POST")
