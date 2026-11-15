// The dashboard's side of the Go server's RPC endpoints (one per old TanStack
// server function): `GET /app/api/rpc/<name>?input=<JSON>` or `POST` with a
// JSON body, JSON back. A failure is a non-2xx answer with
// `{ error: "<message>", code: "<CODE>", issues? }`; its message is thrown as
// an Error, the same text the server function's error carried, so toasts and
// forms show what they did before. (Adapted from Palui's apps/app.)

export type RpcMethod = "GET" | "POST"

export class RpcError extends Error {
  readonly status: number
  readonly code: string
  readonly fields?: Record<string, string>
  readonly issues?: unknown[]

  constructor(message: string, opts: { status: number; code: string; fields?: Record<string, string>; issues?: unknown[] }) {
    super(message)
    this.name = "RpcError"
    this.status = opts.status
    this.code = opts.code
    this.fields = opts.fields
    this.issues = opts.issues
  }
}

// What a 401 means to the dashboard: the session is gone. The message stays
// "UNAUTHORIZED", which the route error boundary (AppError) matches on.
export const UNAUTHORIZED = "UNAUTHORIZED"

const unauthorizedListeners = new Set<() => void>()

// Called on every 401 from an RPC and on the streams' `unauthorized` event.
// The app wires this to "forget the viewer, go to /login". Returns the unsubscribe.
export function onUnauthorized(fn: () => void): () => void {
  unauthorizedListeners.add(fn)
  return () => {
    unauthorizedListeners.delete(fn)
  }
}

export function notifyUnauthorized() {
  for (const fn of unauthorizedListeners) {
    try {
      fn()
    } catch {
      // one listener can't stop the others
    }
  }
}

export const RPC_BASE = "/app/api/rpc"

export function rpcUrl(name: string, method: RpcMethod, input?: unknown): string {
  const url = `${RPC_BASE}/${name}`
  if (method === "GET" && input !== undefined) return `${url}?input=${encodeURIComponent(JSON.stringify(input))}`
  return url
}

type ErrorBody = { error?: unknown; message?: unknown; code?: unknown; fields?: unknown; issues?: unknown }

function messageOf(body: ErrorBody | null, status: number, statusText: string): string {
  const e = body?.error
  if (typeof e === "string" && e) return e
  // Tolerate `{ error: { message } }` too.
  if (e && typeof e === "object" && typeof (e as { message?: unknown }).message === "string") return (e as { message: string }).message
  if (typeof body?.message === "string" && body.message) return body.message
  if (status === 401) return UNAUTHORIZED
  return statusText ? `${status} ${statusText}` : `Request failed (${status})`
}

async function readJson(res: Response): Promise<unknown> {
  const text = await res.text()
  if (!text) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

// One call. `input` undefined sends no input (a GET without ?input=, a POST
// without a body), which the server reads as undefined, like a server
// function called without data.
export async function rpc<O>(name: string, method: RpcMethod, input?: unknown, init?: { signal?: AbortSignal }): Promise<O> {
  const headers: Record<string, string> = { accept: "application/json" }
  let body: string | undefined
  if (method === "POST" && input !== undefined) {
    headers["content-type"] = "application/json"
    body = JSON.stringify(input)
  }
  const res = await fetch(rpcUrl(name, method, input), { method, headers, body, credentials: "same-origin", signal: init?.signal })
  const data = await readJson(res)
  if (!res.ok) {
    const err = (data && typeof data === "object" ? data : null) as ErrorBody | null
    const message = messageOf(err, res.status, res.statusText)
    const code = typeof err?.code === "string" ? err.code : res.status === 401 ? UNAUTHORIZED : "ERROR"
    if (res.status === 401) notifyUnauthorized()
    throw new RpcError(message, {
      status: res.status,
      code,
      fields: err?.fields && typeof err.fields === "object" ? (err.fields as Record<string, string>) : undefined,
      issues: Array.isArray(err?.issues) ? err.issues : undefined,
    })
  }
  return (data === undefined ? null : data) as O
}

// The shapes a wrapper module builds its exports from: `fn()` for a function
// that takes no input, `fn({ data })` for one that does, as server functions
// were called.
export type NoInputFn<O> = (opts?: { signal?: AbortSignal }) => Promise<O>
export type InputFn<I, O> = (opts: { data: I; signal?: AbortSignal }) => Promise<O>

export function noInput<O>(name: string, method: RpcMethod): NoInputFn<O> {
  return (opts) => rpc<O>(name, method, undefined, opts)
}

export function withInput<I, O>(name: string, method: RpcMethod): InputFn<I, O> {
  return (opts) => rpc<O>(name, method, opts.data, opts)
}

// A server function wrapper with the old function's exact call signature
// (`fn()`, `fn({ data })`, `fn({ data, signal })`). FormData input goes as
// multipart (uploadFile).
export function rpcFn<T>(name: string, method: RpcMethod): T {
  const call = (opts?: { data?: unknown; signal?: AbortSignal }) => {
    if (typeof FormData !== "undefined" && opts?.data instanceof FormData) return rpcForm(name, opts.data, opts)
    return rpc(name, method, opts?.data, opts)
  }
  return call as unknown as T
}

async function rpcForm<O>(name: string, form: FormData, init?: { signal?: AbortSignal }): Promise<O> {
  const res = await fetch(rpcUrl(name, "POST"), { method: "POST", body: form, headers: { accept: "application/json" }, credentials: "same-origin", signal: init?.signal })
  const text = await res.text()
  let data: unknown
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    data = undefined
  }
  if (!res.ok) {
    const err = (data && typeof data === "object" ? data : {}) as { error?: string; code?: string }
    if (res.status === 401) notifyUnauthorized()
    throw new RpcError(err.error || (res.status === 401 ? UNAUTHORIZED : `Request failed (${res.status})`), { status: res.status, code: err.code ?? "ERROR" })
  }
  return (data === undefined ? null : data) as O
}
