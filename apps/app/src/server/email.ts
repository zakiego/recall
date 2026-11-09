// Outgoing email through Resend's HTTP API (https://resend.com/docs/api-reference/emails/send-email).
// Needs RESEND_API_KEY and RECALL_EMAIL_FROM. Without them, emailConfigured()
// is false and callers show "email not configured" instead of sending.
import { config } from "./config"

export type EmailMessage = {
  to: string
  subject: string
  text: string
  html?: string
  // Resend drops a repeat with the same key for 24 hours, so a retried send
  // after a lost response can't deliver twice.
  idempotencyKey?: string
}

export type SendEmail = (msg: EmailMessage) => Promise<{ id: string }>

export class EmailError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message)
    this.name = "EmailError"
  }
}

export function emailConfigured() {
  const c = config()
  return c.resendApiKey !== "" && c.emailFrom !== ""
}

const RESEND_URL = "https://api.resend.com/emails"
const TIMEOUT_MS = 15_000

export function createResendSender(opts: { apiKey: string; from: string; fetch?: typeof fetch }): SendEmail {
  const doFetch = opts.fetch ?? fetch
  return async (msg) => {
    const headers: Record<string, string> = { Authorization: `Bearer ${opts.apiKey}`, "Content-Type": "application/json" }
    if (msg.idempotencyKey) headers["Idempotency-Key"] = msg.idempotencyKey.slice(0, 256)
    const res = await doFetch(RESEND_URL, {
      method: "POST",
      headers,
      body: JSON.stringify({ from: opts.from, to: [msg.to], subject: msg.subject, text: msg.text, ...(msg.html ? { html: msg.html } : {}) }),
      signal: AbortSignal.timeout(TIMEOUT_MS),
    })
    const body = (await res.json().catch(() => ({}))) as { id?: string; message?: string; name?: string }
    if (!res.ok || !body.id) throw new EmailError(`Resend answered ${res.status}: ${body.message ?? body.name ?? "no message"}`, res.status)
    return { id: body.id }
  }
}

// The sender for the configured key, read at call time so tests can set env first.
export const sendEmail: SendEmail = (msg) => {
  const c = config()
  if (!emailConfigured()) return Promise.reject(new EmailError("email is not configured (set RESEND_API_KEY and RECALL_EMAIL_FROM)"))
  return createResendSender({ apiKey: c.resendApiKey, from: c.emailFrom })(msg)
}

export function escapeHtml(s: string) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;")
}
