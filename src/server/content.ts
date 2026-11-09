import { Readability } from "@mozilla/readability"
import { parseHTML } from "linkedom"

export const MAX_FETCH_BYTES = 15 * 1024 * 1024
export const MAX_TEXT_CHARS = 2_000_000

export type Resolved = { title: string | null; text: string; mime?: string }

export type ChatMessage = { role: string; content: string }

export function formatChat(messages: ChatMessage[]): string {
  return messages.map((m) => `${m.role}: ${m.content.trim()}`).join("\n\n")
}

export function htmlToText(html: string, url?: string): Resolved {
  const { document } = parseHTML(html)
  const title = document.querySelector("title")?.textContent?.trim() || null
  // Readability mutates the document, so it runs on its own parse.
  const article = new Readability(parseHTML(html).document as unknown as Document, { keepClasses: false }).parse()
  let text = article?.textContent ?? ""
  if (!text.trim()) {
    for (const el of document.querySelectorAll("script, style, noscript, nav, footer, header")) el.remove()
    text = document.body?.textContent ?? ""
  }
  text = text
    .replace(/[ \t ]+/g, " ")
    .replace(/\n\s*\n\s*/g, "\n\n")
    .trim()
  return { title: article?.title?.trim() || title || url || null, text }
}

export async function pdfToText(bytes: Uint8Array): Promise<Resolved> {
  const { extractText, getDocumentProxy, getMeta } = await import("unpdf")
  const pdf = await getDocumentProxy(new Uint8Array(bytes))
  let title: string | null = null
  try {
    const meta = await getMeta(pdf)
    const t = (meta.info as { Title?: unknown } | undefined)?.Title
    if (typeof t === "string" && t.trim()) title = t.trim()
  } catch {
    /* no metadata */
  }
  const { text } = await extractText(pdf, { mergePages: false })
  return { title, text: (text as string[]).join("\n\n").trim(), mime: "application/pdf" }
}

export function isPdf(mime: string | null | undefined, name?: string | null) {
  return mime?.includes("pdf") || name?.toLowerCase().endsWith(".pdf") === true
}

export function isTextLike(mime: string | null | undefined, name?: string | null) {
  if (mime && (mime.startsWith("text/") || /json|xml|yaml|markdown|csv/.test(mime))) return true
  return /\.(txt|md|markdown|csv|json|ya?ml|log|html?)$/i.test(name ?? "")
}

export async function bytesToText(bytes: Uint8Array, mime: string | null, name: string | null): Promise<Resolved> {
  if (isPdf(mime, name)) return pdfToText(bytes)
  const raw = new TextDecoder().decode(bytes)
  if (mime?.includes("html") || /\.html?$/i.test(name ?? "")) return htmlToText(raw)
  if (isTextLike(mime, name)) return { title: null, text: raw.trim(), mime: mime ?? undefined }
  throw new Error(`unsupported file type: ${mime ?? name ?? "unknown"}`)
}

async function readCapped(res: Response): Promise<Uint8Array> {
  const reader = res.body?.getReader()
  if (!reader) return new Uint8Array()
  const parts: Uint8Array[] = []
  let size = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    size += value.byteLength
    if (size > MAX_FETCH_BYTES) {
      await reader.cancel()
      throw new Error(`page is larger than ${MAX_FETCH_BYTES / 1024 / 1024} MB`)
    }
    parts.push(value)
  }
  return Buffer.concat(parts)
}

export async function fetchUrl(url: string, fetchImpl: typeof fetch = fetch): Promise<Resolved> {
  const u = new URL(url)
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error("only http and https URLs are supported")
  const res = await fetchImpl(u, {
    redirect: "follow",
    signal: AbortSignal.timeout(30_000),
    headers: {
      "user-agent": "Mozilla/5.0 (compatible; recall/1.0; +https://recall.zakiego.com)",
      accept: "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5",
    },
  })
  if (!res.ok) throw new Error(`fetch ${u.host} returned ${res.status}`)
  const mime = res.headers.get("content-type")?.split(";")[0]?.trim() ?? null
  const bytes = await readCapped(res)
  if (isPdf(mime, u.pathname)) {
    const r = await pdfToText(bytes)
    return { ...r, title: r.title ?? (decodeURIComponent(u.pathname.split("/").pop() || "") || u.host) }
  }
  if (!mime || mime.includes("html")) return htmlToText(new TextDecoder().decode(bytes), url)
  if (isTextLike(mime, u.pathname)) return { title: u.pathname.split("/").pop() || u.host, text: new TextDecoder().decode(bytes).trim(), mime }
  throw new Error(`unsupported content type: ${mime}`)
}
