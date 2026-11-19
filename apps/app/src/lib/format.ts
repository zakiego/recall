import { RpcError } from "@/lib/rpc-client"

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" })
const dateFmt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" })
const dayFmt = new Intl.DateTimeFormat("en", { dateStyle: "medium" })

// Relative under 30 days, the date alone after ("Oct 9, 2026"). Pair it with
// fullDate in a title.
export function ago(ms: number, now = Date.now()): string {
  const s = Math.round((ms - now) / 1000)
  const abs = Math.abs(s)
  if (abs < 45) return "just now"
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute")
  if (abs < 86_400) return rtf.format(Math.round(s / 3600), "hour")
  if (abs < 30 * 86_400) return rtf.format(Math.round(s / 86_400), "day")
  return dayFmt.format(ms)
}

export function fullDate(ms: number): string {
  return dateFmt.format(ms)
}

export function plural(n: number, one: string, many = `${one}s`) {
  return `${n.toLocaleString("en")} ${n === 1 ? one : many}`
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

// A failure as people read it: a plain sentence, the server's own text for
// Details (empty when it adds nothing), and whether trying again can help.
export type ExplainedError = { message: string; detail: string; retry: boolean }

const MODEL_MISSING = /onnxruntime|model_quantized|tokenizer(_config)?\.json|load model/i

// The RPC layer sends every server error as code "ERROR", so this reads the status and the text.
export function explainError(err: unknown, feature: string, fallback: string): ExplainedError {
  const raw = errorMessage(err)
  const status = err instanceof RpcError ? err.status : 0
  const pick = (message: string, retry: boolean): ExplainedError => ({ message, detail: raw === message ? "" : raw, retry })
  if (MODEL_MISSING.test(raw)) return pick(`${feature} needs the embedding model, which isn't installed on this server.`, false)
  if (/RECALL_JOBS=off/.test(raw)) return pick(`${feature} is turned off on this server.`, false)
  if (status === 504) return pick("No answer in time. Try again in a minute.", true)
  if (status === 502 && /gateway/i.test(raw)) return pick("The model gateway didn't answer.", true)
  if (status === 502) return pick("The model's reply couldn't be read.", true)
  if (err instanceof TypeError && /fetch|network/i.test(raw)) return pick("Couldn't reach the server. Check your connection.", true)
  return pick(fallback, true)
}
