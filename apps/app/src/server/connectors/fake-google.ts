// A stand-in for Google's OAuth and Gmail endpoints, served from fixtures.
// Tests inject its fetch; `RECALL_GMAIL_FAKE=1` in dev uses it for the whole app.
import { ATTACHMENTS, b64, MESSAGES } from "./fixtures/gmail"
import { GMAIL_API, GMAIL_SCOPE, REVOKE_URL, TOKEN_URL, type GmailMessage } from "./google"
import { header } from "./gmail-message"

export type FakeGoogleOptions = {
  email?: string
  messages?: GmailMessage[]
  attachments?: Record<string, Record<string, Uint8Array>>
  // The refresh token was revoked at Google: refreshes fail with invalid_grant.
  revoked?: boolean
  // Access tokens come back already expired, so every call refreshes first.
  expiresIn?: number
}

export type FakeCall = { method: string; url: string; auth: string | null; body: string | null }

export function createFakeGoogle(opts: FakeGoogleOptions = {}) {
  const messages = opts.messages ?? MESSAGES
  const attachments = opts.attachments ?? ATTACHMENTS
  const calls: FakeCall[] = []
  const revokedTokens: string[] = []
  let issued = 0
  const state = { revoked: opts.revoked ?? false }

  const json = (body: unknown, status = 200) => Response.json(body, { status })

  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(input instanceof Request ? input.url : input.toString())
    const method = init?.method ?? "GET"
    const headers = new Headers(init?.headers)
    const body = typeof init?.body === "string" ? init.body : null
    calls.push({ method, url: url.toString(), auth: headers.get("authorization"), body })

    if (url.toString() === TOKEN_URL && method === "POST") {
      const form = new URLSearchParams(body ?? "")
      if (form.get("grant_type") === "refresh_token" && (state.revoked || revokedTokens.includes(form.get("refresh_token") ?? ""))) {
        return json({ error: "invalid_grant", error_description: "Token has been expired or revoked." }, 400)
      }
      issued += 1
      return json({
        access_token: `fake-access-${issued}`,
        expires_in: opts.expiresIn ?? 3599,
        scope: GMAIL_SCOPE,
        token_type: "Bearer",
        ...(form.get("grant_type") === "authorization_code" ? { refresh_token: `fake-refresh-${issued}` } : {}),
      })
    }
    if (url.toString().startsWith(REVOKE_URL) && method === "POST") {
      revokedTokens.push(new URLSearchParams(body ?? "").get("token") ?? "")
      return new Response(null, { status: 200 })
    }
    if (!url.toString().startsWith(GMAIL_API)) return json({ error: { code: 404, message: "Not Found" } }, 404)
    if (!headers.get("authorization")?.startsWith("Bearer fake-access-")) return json({ error: { code: 401, message: "Invalid Credentials" } }, 401)

    const path = url.pathname.replace(new URL(GMAIL_API).pathname, "")
    if (path === "/profile") return json({ emailAddress: opts.email ?? "zaki@example.com", messagesTotal: messages.length, threadsTotal: messages.length })
    if (path === "/messages") {
      const found = messages.filter((m) => matches(m, url.searchParams.get("q") ?? ""))
      const max = Number(url.searchParams.get("maxResults") ?? 100)
      const offset = Number(url.searchParams.get("pageToken") ?? 0)
      const page = found.slice(offset, offset + max)
      return json({
        ...(page.length ? { messages: page.map((m) => ({ id: m.id, threadId: m.threadId })) } : {}),
        ...(offset + max < found.length ? { nextPageToken: String(offset + max) } : {}),
        resultSizeEstimate: found.length,
      })
    }
    const att = /^\/messages\/([^/]+)\/attachments\/([^/]+)$/.exec(path)
    if (att) {
      const bytes = attachments[decodeURIComponent(att[1]!)]?.[decodeURIComponent(att[2]!)]
      return bytes ? json({ size: bytes.byteLength, data: b64(bytes) }) : json({ error: { code: 404, message: "Not Found" } }, 404)
    }
    const one = /^\/messages\/([^/]+)$/.exec(path)
    if (one) {
      const msg = messages.find((m) => m.id === decodeURIComponent(one[1]!))
      if (!msg) return json({ error: { code: 404, message: "Requested entity was not found." } }, 404)
      if (url.searchParams.get("format") === "metadata") {
        const wanted = url.searchParams.getAll("metadataHeaders").map((h) => h.toLowerCase())
        const { payload, ...rest } = msg
        return json({ ...rest, payload: { mimeType: payload?.mimeType, headers: payload?.headers?.filter((h) => wanted.includes(h.name.toLowerCase())) } })
      }
      return json(msg)
    }
    return json({ error: { code: 404, message: "Not Found" } }, 404)
  }

  return { fetch: fakeFetch as typeof fetch, calls, revokedTokens, state }
}

// Enough of Gmail's query syntax for the fixtures: from:, to:, subject:,
// label:, after:/before: (YYYY/MM/DD) and plain words, all ANDed.
function matches(m: GmailMessage, q: string) {
  const hay = (s: string | null | undefined) => (s ?? "").toLowerCase()
  for (const raw of q.match(/\S+:"[^"]*"|\S+/g) ?? []) {
    const [op, ...rest] = raw.includes(":") ? raw.split(":") : ["", raw]
    const v = rest.join(":").replace(/^"|"$/g, "").toLowerCase()
    const term = op ? v : raw.toLowerCase()
    switch (op?.toLowerCase()) {
      case "from":
      case "to":
      case "subject":
        if (!hay(header(m.payload, op)).includes(term)) return false
        break
      case "label":
        if (!(m.labelIds ?? []).some((l) => l.toLowerCase() === term || l.toLowerCase() === `label_${term}`)) return false
        break
      case "after":
      case "before": {
        const at = Date.parse(term.replace(/\//g, "-"))
        const t = Number(m.internalDate ?? 0)
        if (Number.isFinite(at) && (op === "after" ? t < at : t >= at)) return false
        break
      }
      default:
        if (![m.snippet, header(m.payload, "subject"), header(m.payload, "from")].some((s) => hay(s).includes(term))) return false
    }
  }
  return true
}
