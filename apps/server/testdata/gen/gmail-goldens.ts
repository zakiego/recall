// Exports the Gmail connector's fixtures and what the TS code makes of them.
//
// Writes internal/connectors/fixtures/gmail.json (the fixture mailbox the
// dev fake and the tests serve: MESSAGES, WORST_MESSAGES, ATTACHMENTS as
// base64url) and testdata/gmail.json (parseMessage of every fixture message,
// decodeEntities and header cases, Google's authorization URL for a fixed
// state and challenge, and token exchange and refresh requests).
//
// Run from apps/app: npx tsx ../server/testdata/gen/gmail-goldens.ts
import fs from "node:fs"
import path from "node:path"

process.env.RECALL_URL = "http://localhost:4800"
process.env.GOOGLE_CLIENT_ID = "test-client.apps.googleusercontent.com"
process.env.GOOGLE_CLIENT_SECRET = "test-client-secret"

const { MESSAGES, WORST_MESSAGES, ATTACHMENTS, b64 } = await import("../../../app/src/server/connectors/fixtures/gmail.ts")
const { parseMessage, decodeEntities, header } = await import("../../../app/src/server/connectors/gmail-message.ts")
const { authorizationUrl, exchangeCode, refreshAccessToken, revokeToken, getMessageMetadata, listMessages, getAttachment } = await import("../../../app/src/server/connectors/google.ts")

const attachments: Record<string, Record<string, string>> = {}
for (const [msg, parts] of Object.entries(ATTACHMENTS)) {
  attachments[msg] = {}
  for (const [id, bytes] of Object.entries(parts)) attachments[msg][id] = b64(bytes)
}
const fixturesFile = path.resolve(import.meta.dirname, "../../internal/connectors/fixtures/gmail.json")
fs.mkdirSync(path.dirname(fixturesFile), { recursive: true })
fs.writeFileSync(fixturesFile, JSON.stringify({ messages: MESSAGES, worst: WORST_MESSAGES, attachments }, null, 1) + "\n")

const parsed = [...MESSAGES, ...WORST_MESSAGES].map((m) => {
  try {
    return { id: m.id, ok: parseMessage(m) }
  } catch (err) {
    return { id: m.id, error: `${(err as Error).name}: ${(err as Error).message}` }
  }
})

// Extra parts the fixtures don't cover: charsets, entities, odd headers.
const enc = (s: string | Uint8Array) => Buffer.from(s).toString("base64url")
const extra = [
  { id: "x_latin1", payload: { mimeType: "text/plain", headers: [{ name: "Content-Type", value: "text/plain; charset=ISO-8859-1" }, { name: "Subject", value: "  Café  " }], body: { data: enc(Buffer.from([0x43, 0x61, 0x66, 0xe9, 0x20, 0x80])) } } },
  { id: "x_win1252", payload: { mimeType: "text/plain", headers: [{ name: "content-type", value: 'text/plain; CHARSET="windows-1252"' }], body: { data: enc(Buffer.from([0x93, 0x68, 0x69, 0x94])) } } },
  { id: "x_unknown_charset", payload: { mimeType: "text/plain", headers: [{ name: "Content-Type", value: "text/plain; charset=x-made-up" }], body: { data: enc("plain é") } } },
  { id: "x_utf8_bom", payload: { mimeType: "text/plain", headers: [], body: { data: enc("﻿with bom\r\nline two  \r\n\r\n\r\n\r\nend") } } },
  { id: "x_shift_jis", payload: { mimeType: "text/plain", headers: [{ name: "Content-Type", value: "text/plain; charset=Shift_JIS" }], body: { data: enc(Buffer.from([0x82, 0xa0, 0x82, 0xa2])) } } },
  { id: "x_attachment_disposition", payload: { mimeType: "multipart/mixed", parts: [{ partId: "0", mimeType: "text/plain", body: { data: enc("body") } }, { partId: "1", mimeType: "application/pdf", headers: [{ name: "Content-Disposition", value: "ATTACHMENT; x" }], body: { attachmentId: "att1", size: 10 } }, { partId: "", mimeType: "application/octet-stream", filename: "scan.PDF", body: { data: enc("%PDF-1.4"), size: 8 } }] } },
  { id: "x_no_body", payload: { headers: [{ name: "Date", value: "Tue, 3 May 2016 23:30:00 -0700" }, { name: "From", value: "   " }] } },
  { id: "x_html_only", payload: { mimeType: "multipart/alternative", parts: [{ mimeType: "TEXT/HTML", body: { data: enc("<html><body><p>Hi &amp; bye</p></body></html>") } }, { mimeType: "text/html", body: { data: enc("<html><body><p>Second</p></body></html>") } }] } },
  { id: "x_html_fragment", payload: { mimeType: "text/html", body: { data: enc("no tags at all") } } },
  { id: "x_no_payload" },
]
const extraParsed = extra.map((m) => {
  try {
    return { id: m.id, message: m, ok: parseMessage(m as never) }
  } catch (err) {
    return { id: m.id, message: m, error: `${(err as Error).name}: ${(err as Error).message}` }
  }
})

const entities = ["a &amp; b", "&lt;p&gt;", "&#39;quoted&#39;", "&#x1F600; &#X41;", "&#0; &#1114112;", "&nbsp;&NBSP;&Amp;", "&unknown; &;", "&#65&#66;", "no entities"].map((s) => ({ in: s, out: decodeEntities(s) }))
const headerPart = { headers: [{ name: "Subject", value: "  hello  " }, { name: "subject", value: "second" }, { name: "Empty", value: "   " }] }
const headers = ["Subject", "SUBJECT", "Empty", "Missing"].map((n) => ({ name: n, value: header(headerPart as never, n) }))

// Requests the client makes, captured from a recording fetch.
const requests: unknown[] = []
const recorder = (reply: unknown, status = 200) =>
  (async (input: unknown, init?: RequestInit) => {
    requests.push({ url: String(input), method: init?.method ?? "GET", headers: init?.headers, body: init?.body ?? null })
    return new Response(reply === null ? null : JSON.stringify(reply), { status })
  }) as typeof fetch
const outcomes: Record<string, unknown> = {}
const run = async (name: string, fn: () => Promise<unknown>) => {
  try {
    outcomes[name] = { ok: await fn() }
  } catch (err) {
    const e = err as { name: string; message: string; status?: number; code?: string }
    outcomes[name] = { error: { name: e.name, message: e.message, status: e.status, code: e.code } }
  }
}
await run("exchange", () => exchangeCode(recorder({ access_token: "a1", refresh_token: "r1", expires_in: 3599.5, scope: "x y" }), { code: "4/0A b+c", verifier: "v~*.", now: 1_000 }))
await run("exchange-no-refresh", () => exchangeCode(recorder({ access_token: "a1" }), { code: "c", verifier: "v", now: 1_000 }))
await run("exchange-error", () => exchangeCode(recorder({ error: "invalid_grant" }, 400), { code: "c", verifier: "v", now: 1_000 }))
await run("refresh", () => refreshAccessToken(recorder({ access_token: "a2" }), "r1", 2_000))
await run("refresh-revoked", () => refreshAccessToken(recorder({ error: "invalid_grant" }, 400), "r1", 2_000))
await run("refresh-401", () => refreshAccessToken(recorder({}, 401), "r1", 2_000))
await run("refresh-500", () => refreshAccessToken(recorder({}, 500), "r1", 2_000))
await run("revoke-ok", () => revokeToken(recorder(null, 200), "tok"))
await run("revoke-dead", () => revokeToken(recorder(null, 400), "tok"))
await run("revoke-500", () => revokeToken(recorder(null, 500), "tok"))
await run("metadata", () => getMessageMetadata(recorder({ id: "m1" }), "acc", "id/with space"))
await run("list", () => listMessages(recorder({}), "acc", { q: "from:a b~*", maxResults: 20, pageToken: "p 1" }))
await run("list-404", () => listMessages(recorder({}, 404), "acc", { q: "x", maxResults: 20 }))
await run("list-429", () => listMessages(recorder({}, 429), "acc", { q: "x", maxResults: 20 }))
await run("list-401", () => listMessages(recorder({}, 401), "acc", { q: "x", maxResults: 20 }))
await run("attachment", async () => b64(await getAttachment(recorder({ data: b64("hello") }), "acc", "m1", "a/1")))

const out = path.resolve(import.meta.dirname, "../gmail.json")
fs.writeFileSync(
  out,
  JSON.stringify(
    {
      parsed,
      extraParsed,
      entities,
      headers,
      authUrl: authorizationUrl({ clientId: "test-client.apps.googleusercontent.com", state: "st~a*te", challenge: "ch_al-lenge" }),
      requests,
      outcomes,
    },
    null,
    1,
  ) + "\n",
)
console.log(`wrote ${fixturesFile} and ${out}: ${parsed.length + extraParsed.length} messages, ${requests.length} requests`)
