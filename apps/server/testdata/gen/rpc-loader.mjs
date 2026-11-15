// Module resolve hook for rpc-goldens.ts: the server function files import
// createServerFn from @tanstack/react-start and getRequest from
// @tanstack/react-start/server, which need a running web server. Both point
// at rpc-shim.ts, so the real validators and handler bodies run in a script.
let shim = ""

export function initialize(data) {
  shim = data.shim
}

export async function resolve(specifier, context, next) {
  if (specifier === "@tanstack/react-start" || specifier === "@tanstack/react-start/server") return { url: shim, shortCircuit: true }
  return next(specifier, context)
}
