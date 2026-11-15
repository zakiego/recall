// Stand-ins for @tanstack/react-start and @tanstack/react-start/server (see
// rpc-loader.mjs). createServerFn keeps the method, the validator and the
// handler; `call(input)` runs them the way TanStack Start does: the
// validator (a zod schema's parse, or the function), then handler({ data }).
// getRequest returns the request the generator set for the current step.
export type ServerFn = { method: "GET" | "POST"; call: (input: unknown) => Promise<unknown> }

type Validator = { parse: (v: unknown) => unknown } | ((v: unknown) => unknown)

let current: Request | undefined
export function setRequest(r: Request) {
  current = r
}
export function getRequest(): Request {
  if (!current) throw new Error("no request")
  return current
}

export function createServerFn(opts: { method: "GET" | "POST" }) {
  let validator: Validator | undefined
  const builder = {
    validator(v: Validator) {
      validator = v
      return builder
    },
    handler(fn: (ctx: { data: unknown }) => unknown): ServerFn {
      return {
        method: opts.method,
        call: async (input: unknown) => {
          const data = validator === undefined ? undefined : typeof validator === "function" ? validator(input) : validator.parse(input)
          return fn({ data })
        },
      }
    },
  }
  return builder
}
