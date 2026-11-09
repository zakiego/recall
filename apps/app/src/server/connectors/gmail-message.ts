// Turns a Gmail API message (format=full) into the text Recall ingests, and
// finds its PDF attachments. Pure functions, no I/O.
import { htmlToText, isPdf } from "../content"
import type { GmailMessage, GmailPart } from "./google"

export type PdfAttachment = { partId: string; attachmentId: string | null; inlineData: string | null; filename: string; size: number }

export type ParsedMessage = {
  id: string
  subject: string | null
  from: string | null
  to: string | null
  date: string | null
  text: string
  pdfs: PdfAttachment[]
}

export function header(part: GmailPart | undefined, name: string): string | null {
  const h = part?.headers?.find((x) => x.name.toLowerCase() === name.toLowerCase())
  return h?.value?.trim() || null
}

function charsetOf(part: GmailPart) {
  return /charset="?([^";\s]+)"?/i.exec(header(part, "Content-Type") ?? "")?.[1]?.toLowerCase() ?? "utf-8"
}

function decodeBody(part: GmailPart): string {
  const bytes = Buffer.from(part.body?.data ?? "", "base64url")
  try {
    return new TextDecoder(charsetOf(part)).decode(bytes)
  } catch {
    return new TextDecoder().decode(bytes)
  }
}

function walk(part: GmailPart, visit: (p: GmailPart) => void) {
  visit(part)
  for (const child of part.parts ?? []) walk(child, visit)
}

const isAttachment = (p: GmailPart) => !!p.filename || /^attachment/i.test(header(p, "Content-Disposition") ?? "")

export function parseMessage(msg: GmailMessage): ParsedMessage {
  const root = msg.payload ?? {}
  const plain: string[] = []
  const html: string[] = []
  const pdfs: PdfAttachment[] = []
  walk(root, (p) => {
    const mime = p.mimeType?.toLowerCase() ?? ""
    if (isAttachment(p)) {
      if (isPdf(mime, p.filename) && (p.body?.attachmentId || p.body?.data)) {
        pdfs.push({
          partId: p.partId || String(pdfs.length),
          attachmentId: p.body.attachmentId ?? null,
          inlineData: p.body.attachmentId ? null : (p.body.data ?? null),
          filename: p.filename || "attachment.pdf",
          size: p.body.size ?? 0,
        })
      }
      return
    }
    if (!p.body?.data) return
    if (mime === "text/plain") plain.push(decodeBody(p))
    else if (mime === "text/html") html.push(decodeBody(p))
  })
  // Plain text when the sender wrote one; otherwise the HTML, read like a page.
  const body = plain.length ? plain.join("\n\n") : html.map((h) => htmlToText(h).text).join("\n\n")
  const subject = header(root, "Subject")
  const from = header(root, "From")
  const to = header(root, "To")
  const date = header(root, "Date")
  const head = [subject && `Subject: ${subject}`, from && `From: ${from}`, to && `To: ${to}`, date && `Date: ${date}`].filter(Boolean).join("\n")
  const text = [head, normalize(body)].filter(Boolean).join("\n\n")
  return { id: msg.id, subject, from, to, date, text, pdfs }
}

function normalize(text: string) {
  return text
    .replace(/\r\n?/g, "\n")
    .replace(/[ \t ]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " " }

// Gmail's snippet comes HTML-escaped.
export function decodeEntities(s: string) {
  return s.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e: string) => {
    if (e[0] === "#") {
      const code = e[1]?.toLowerCase() === "x" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10)
      return Number.isFinite(code) && code > 0 && code < 0x110000 ? String.fromCodePoint(code) : m
    }
    return ENTITIES[e.toLowerCase()] ?? m
  })
}
