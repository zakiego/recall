import { lookup } from "node:dns/promises"
import { BlockList, isIP } from "node:net"
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

// --- fetching a URL ---------------------------------------------------------------
// Anyone with an API key can ask for a URL, and the text lands in search. So
// the server must never fetch its own loopback (the ai-gateway is on
// 127.0.0.1), the LAN, or cloud metadata. Every hop is resolved and checked,
// and redirects are followed by hand so a public page can't bounce us inward.

export const MAX_REDIRECTS = 5
const FETCH_TIMEOUT_MS = 30_000

export type Resolve = (host: string) => Promise<string[]>

const systemResolve: Resolve = async (host) => (await lookup(host, { all: true, verbatim: true })).map((a) => a.address)

// Two lists: BlockList checks an IPv4 address against IPv6 rules as
// ::ffff:a.b.c.d, so one list with ::ffff:0:0/96 would block every IPv4 host.
const blockedV4 = new BlockList()
const blockedV6 = new BlockList()
for (const [net, bits] of [
  ["0.0.0.0", 8], // "this network"
  ["10.0.0.0", 8],
  ["100.64.0.0", 10], // CGNAT
  ["127.0.0.0", 8],
  ["169.254.0.0", 16], // link-local, cloud metadata
  ["172.16.0.0", 12],
  ["192.0.0.0", 24],
  ["192.0.2.0", 24],
  ["192.168.0.0", 16],
  ["198.18.0.0", 15],
  ["198.51.100.0", 24],
  ["203.0.113.0", 24],
  ["224.0.0.0", 4], // multicast
  ["240.0.0.0", 4], // reserved, broadcast
] as const) {
  blockedV4.addSubnet(net, bits, "ipv4")
}
for (const [net, bits] of [
  ["::", 96], // unspecified, loopback, IPv4-compatible
  ["::ffff:0:0", 96], // IPv4-mapped
  ["64:ff9b:1::", 48], // local-use NAT64
  ["100::", 64], // discard
  ["2001:db8::", 32],
  ["fc00::", 7], // ULA
  ["fe80::", 10], // link-local
  ["fec0::", 10], // old site-local
  ["ff00::", 8], // multicast
] as const) {
  blockedV6.addSubnet(net, bits, "ipv6")
}

export function isPrivateAddress(address: string): boolean {
  const family = isIP(address)
  if (family === 0) return true
  return family === 4 ? blockedV4.check(address, "ipv4") : blockedV6.check(address, "ipv6")
}

// Throws unless every address the host resolves to is public, since fetch may
// connect to any of them.
async function assertPublicUrl(u: URL, resolve: Resolve) {
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error("only http and https URLs are supported")
  const host = u.hostname.replace(/^\[|\]$/g, "")
  const addresses = isIP(host) ? [host] : await resolve(host)
  if (addresses.length === 0) throw new Error(`${u.host} has no address`)
  if (addresses.some(isPrivateAddress)) throw new Error(`refusing to fetch ${u.host}: it points to a private or local address`)
}

export async function fetchUrl(url: string, fetchImpl: typeof fetch = fetch, resolve: Resolve = systemResolve): Promise<Resolved> {
  let u = new URL(url)
  const signal = AbortSignal.timeout(FETCH_TIMEOUT_MS)
  let res: Response
  for (let hop = 0; ; hop++) {
    await assertPublicUrl(u, resolve)
    res = await fetchImpl(u, {
      redirect: "manual",
      signal,
      headers: {
        "user-agent": "Mozilla/5.0 (compatible; recall/1.0; +https://recall.zakiego.com)",
        accept: "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5",
      },
    })
    const location = res.status >= 300 && res.status < 400 ? res.headers.get("location") : null
    if (!location) break
    await res.body?.cancel()
    if (hop >= MAX_REDIRECTS) throw new Error(`fetch ${u.host} redirected more than ${MAX_REDIRECTS} times`)
    u = new URL(location, u)
  }
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
