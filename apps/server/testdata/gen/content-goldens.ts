// Goldens for content.ts from the real TS code: htmlToText on saved pages and
// small cases, bytesToText for each file kind, pdfToText (pdf.js through
// unpdf) on the PDFs in testdata/content/pdf plus ones written here, and
// isPrivateAddress. Writes testdata/content.json.
//
// Run from apps/app: npx tsx ../server/testdata/gen/content-goldens.ts
import fs from "node:fs"
import path from "node:path"
import { bytesToText, formatChat, htmlToText, isPdf, isPrivateAddress, isTextLike, pdfToText } from "../../../app/src/server/content.ts"

const dir = path.resolve(import.meta.dirname, "../content")
const out = path.resolve(import.meta.dirname, "../content.json")

// A tiny PDF writer: one Helvetica font, each page a list of lines; a line is
// a list of [text, kerning] pairs for a TJ array.
function makePdf(pages: { lines: (string | [string, number][])[] }[], title?: string) {
  const objs: string[] = []
  const add = (s: string) => {
    objs.push(s)
    return objs.length
  }
  const font = add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
  const pagesId = objs.length + 1 + pages.length * 2
  const kids: number[] = []
  const esc = (s: string) => s.replace(/\\/g, "\\\\").replace(/\(/g, "\\(").replace(/\)/g, "\\)")
  for (const p of pages) {
    let content = "BT /F1 12 Tf 72 720 Td 14 TL\n"
    for (const line of p.lines) {
      if (typeof line === "string") content += `(${esc(line)}) Tj T*\n`
      else content += `[${line.map(([t, k]) => `(${esc(t)}) ${k}`).join(" ")}] TJ T*\n`
    }
    content += "ET"
    const c = add(`<< /Length ${Buffer.byteLength(content, "latin1")} >>\nstream\n${content}\nendstream`)
    kids.push(add(`<< /Type /Page /Parent ${pagesId} 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 ${font} 0 R >> >> /Contents ${c} 0 R >>`))
  }
  add(`<< /Type /Pages /Kids [${kids.map((k) => `${k} 0 R`).join(" ")}] /Count ${kids.length} >>`)
  const catalog = add(`<< /Type /Catalog /Pages ${pagesId} 0 R >>`)
  const info = title ? add(`<< /Title (${esc(title)}) >>`) : 0
  let body = "%PDF-1.4\n"
  const offsets: number[] = []
  objs.forEach((o, i) => {
    offsets.push(Buffer.byteLength(body, "latin1"))
    body += `${i + 1} 0 obj\n${o}\nendobj\n`
  })
  const xref = Buffer.byteLength(body, "latin1")
  body += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n${offsets.map((o) => `${String(o).padStart(10, "0")} 00000 n \n`).join("")}`
  body += `trailer\n<< /Size ${objs.length + 1} /Root ${catalog} 0 R${info ? ` /Info ${info} 0 R` : ""} >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(body, "latin1")
}

async function main() {
  const pdfDir = path.join(dir, "pdf")
  fs.writeFileSync(
    path.join(pdfDir, "made-simple.pdf"),
    makePdf([{ lines: ["Meeting notes for the Q4 roadmap.", "Second line with numbers 123 and 4.5%.", "", "After a blank line."] }], "Made Simple"),
  )
  fs.writeFileSync(
    path.join(pdfDir, "made-kerning.pdf"),
    makePdf([
      {
        lines: [
          [
            ["Spaced", 0],
            ["words", -400],
            ["by", -300],
            ["kerning", -1000],
          ],
          [
            ["Tight", -20],
            ["kern", -50],
            ["ing", 0],
          ],
          "Plain line (with parentheses) and back\\slash",
        ],
      },
      { lines: ["Page two starts here.", "Caf\xe9 na\xefve r\xe9sum\xe9"] },
    ]),
  )
  fs.writeFileSync(path.join(pdfDir, "made-empty.pdf"), makePdf([{ lines: [] }]))

  const html: Record<string, unknown> = {}
  for (const f of fs.readdirSync(path.join(dir, "html")).sort()) {
    const page = fs.readFileSync(path.join(dir, "html", f), "utf8")
    html[f] = { noUrl: htmlToText(page), withUrl: htmlToText(page, "https://example.com/page") }
  }
  const htmlCases = [
    "",
    "plain text, no tags",
    "<html><head><title>  Only Title </title></head><body></body></html>",
    "<html><head><title>T</title></head><body><nav>menu</nav><p>Short body.</p><footer>foot</footer><script>x=1</script></body></html>",
    "<p>a&nbsp;&nbsp;b\t\tc</p>\n\n\n<p>d</p>",
    "<body><header>Top</header><main><h1>Head</h1><p>Para one.</p><p>Para two.</p></main></body>",
    "<p>only a paragraph</p>",
    "<!doctype html><title>t</title>",
    "text <b>bold</b>",
    " ",
    // Where linkedom finds document.body: right after a leading <head>, or
    // as the first child (a <head> is put before it).
    "<html><meta charset=utf-8><title>T</title><body><p>Body after meta.</p></body></html>",
    "<html><head><title>T</title></head><div>between</div><body><p>Body after a div.</p></body></html>",
    "<div><body><p>hi from a nested body</p></body></div>",
    "<html><body><p>Body without head.</p></body></html>",
    "<!doctype html><html><head><title>T</title><body><p>Head left open.</p></body></html>",
    "<html><head><title>T</title><link rel=x><script>1</script><body><p>Body closes link and script.</p></body></html>",
    "<html>\n <head><title>T</title></head>\n <body><p>Whitespace between.</p></body>\n</html>",
    "<p>intro</p><html><body><p>Second root.</p></body></html>",
    "</p><html><body><p>Stray close.</p></body></html>",
    "<html></head><body><p>Stray head close.</p></body></html>",
    "<html><head><noscript><body>x</body></noscript></head><body><p>Noscript in head.</p></body></html>",
    "<html><head><title>a</title></head><!-- c --><body><p>Comment between.</p></body></html>",
    "<html><svg/><body><p>Self-closing svg.</p></body></html>",
    "<html><head/><body><p>Self-closing head.</p></body></html>",
    "<html><HEAD><TITLE>T</TITLE></HEAD><BODY><P>Upper case.</P></BODY></html>",
    "<html><head></head><body/><p>after</p></html>",
    "<meta charset=utf-8><html><body><p>Meta first.</p></body></html>",
    "<html><br></br><body><p>br first.</p></body></html>",
  ].map((h) => {
    const run = (u?: string) => {
      try {
        return { ok: htmlToText(h, u) }
      } catch (err) {
        return { error: (err as Error).message }
      }
    }
    return { html: h, out: run(), outUrl: run("https://x.test/") }
  })

  const enc = new TextEncoder()
  const files: { mime: string | null; name: string | null; text: string }[] = [
    { mime: "text/plain", name: "a.txt", text: "  hello\n" },
    { mime: null, name: "notes.md", text: "# Title\n\nbody" },
    { mime: null, name: "NOTES.MD", text: "upper" },
    { mime: "application/json", name: null, text: '{"a":1}' },
    { mime: "text/html", name: null, text: "<title>X</title><p>Body text here.</p>" },
    { mime: null, name: "page.HTM", text: "<p>htm</p>" },
    { mime: "application/x-yaml", name: null, text: "a: 1" },
    { mime: null, name: "data.yml", text: "a: 2" },
    { mime: "application/octet-stream", name: "x.bin", text: "zz" },
    { mime: null, name: null, text: "zz" },
    { mime: null, name: "x.docx", text: "zz" },
    { mime: "text/csv", name: null, text: "﻿a,b\n1,2" },
    { mime: "", name: "x.bin", text: "zz" },
    { mime: "", name: null, text: "zz" },
    { mime: null, name: "", text: "zz" },
    { mime: "", name: "", text: "zz" },
  ]
  const bytes = []
  for (const f of files) {
    try {
      bytes.push({ ...f, ok: await bytesToText(enc.encode(f.text), f.mime, f.name) })
    } catch (err) {
      bytes.push({ ...f, error: (err as Error).message })
    }
  }
  const pdfs: Record<string, unknown> = {}
  for (const f of fs.readdirSync(pdfDir).sort()) {
    try {
      pdfs[f] = await pdfToText(fs.readFileSync(path.join(pdfDir, f)))
    } catch (err) {
      pdfs[f] = { error: (err as Error).message }
    }
  }
  const predicates = [
    ["application/pdf", null],
    [null, "X.PDF"],
    ["text/plain", "a.pdf"],
    ["text/markdown", null],
    ["application/xml", null],
    [null, "a.log"],
    [null, "a.LOG"],
    [null, "a.yaml"],
    [null, "a.ya"],
    ["image/png", "a.png"],
    [null, null],
  ].map(([mime, name]) => ({ mime, name, pdf: isPdf(mime, name), text: isTextLike(mime, name) }))
  const addresses = [
    "127.0.0.1", "127.8.8.8", "0.0.0.0", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.255",
    "224.0.0.1", "255.255.255.255", "::", "::1", "::ffff:127.0.0.1", "::ffff:7f00:1", "::ffff:10.0.0.1", "::ffff:8.8.8.8", "fc00::1", "fd12:3456::1",
    "fe80::1", "ff02::1", "not-an-ip", "8.8.8.8", "93.184.215.14", "172.32.0.1", "100.128.0.1", "2606:4700::6810:85e5", "2001:4860:4860::8888",
    "64:ff9b:1::1", "64:ff9b::808:808", "100::1", "2001:db8::1", "fec0::1", "198.18.0.1", "198.20.0.1", "192.0.0.8", "192.0.2.1", "203.0.113.9",
    "240.0.0.1", "1.1.1.1", "::2", "::ffff:0:0", "fe80::1%en0", "1.2.3", "01.2.3.4", "1.2.3.4/8", "",
  ].map((a) => ({ a, private: isPrivateAddress(a) }))
  const chat = formatChat([
    { role: "user", content: "  hi \n" },
    { role: "assistant", content: "hello" },
  ])
  // formatChat on whatever a caller put in metadata.messages.
  const chats = [
    [{ role: "user", content: "  hi \n" }],
    [{ content: "no role" }],
    [{ role: 1.5, content: "a" }, { role: null, content: "b" }, { role: true, content: "c" }],
    [{ role: { a: 1 }, content: "d" }, { role: [1, [2, null], "x"], content: "e" }, { role: [], content: "f" }],
    [{ role: 1e21, content: "g" }, { role: -0, content: "h" }],
    [{ role: "user", content: 5 }],
    [{ role: "user" }],
    [{ role: "user", content: null }],
    [{ role: "user", content: { text: "x" } }],
    [{ role: "user", content: ["x"] }],
    [null],
    ["hi"],
    [5],
    [[]],
    [],
  ].map((messages) => {
    try {
      return { messages, ok: formatChat(messages as never) }
    } catch (err) {
      return { messages, error: `${(err as Error).name}: ${(err as Error).message}` }
    }
  })
  fs.writeFileSync(out, JSON.stringify({ html, htmlCases, bytes, pdfs, predicates, addresses, chat, chats }, null, 1) + "\n")
  console.log(`wrote ${out}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
