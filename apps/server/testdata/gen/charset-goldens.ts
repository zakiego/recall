// What gmail-message.ts's decodeBody makes of message bodies: new
// TextDecoder(charset).decode(bytes), falling back to UTF-8 for a label
// TextDecoder refuses. Every WHATWG encoding, with valid and invalid bytes.
//
// Run from apps/app: npx tsx ../server/testdata/gen/charset-goldens.ts
import fs from "node:fs"
import path from "node:path"

const labels = [
  "utf-8", "unicode-1-1-utf-8", "utf-16le", "utf-16", "utf-16be", "iso-8859-1", "us-ascii", "latin1", "windows-1252",
  "iso-8859-2", "iso-8859-3", "iso-8859-4", "iso-8859-5", "iso-8859-6", "iso-8859-7", "iso-8859-8", "iso-8859-8-i",
  "iso-8859-10", "iso-8859-13", "iso-8859-14", "iso-8859-15", "iso-8859-16", "koi8-r", "koi8-u", "macintosh",
  "x-mac-cyrillic", "windows-874", "windows-1250", "windows-1251", "windows-1253", "windows-1254", "windows-1255",
  "windows-1256", "windows-1257", "windows-1258", "gbk", "gb2312", "gb18030", "big5", "euc-jp", "iso-2022-jp",
  "shift_jis", "sjis", "euc-kr", "ks_c_5601-1987", "x-user-defined", "iso-2022-kr", "hz-gb-2312", "replacement",
  "utf-7", "foo", "",
]

let seed = 12345
const rand = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) % 256
const inputs: Record<string, number[]> = {
  ascii: [...Buffer.from("Hello, World! 123\r\n")],
  allBytes: Array.from({ length: 256 }, (_, i) => i),
  random: Array.from({ length: 400 }, rand),
  utf8: [...Buffer.from("Héllo wörld, 日本語, 😀")],
  utf8Bom: [0xef, 0xbb, 0xbf, ...Buffer.from("bom")],
  utf16leBom: [0xff, 0xfe, 0x61, 0x00, 0x62, 0x00],
  utf16beBom: [0xfe, 0xff, 0x00, 0x61, 0x00, 0x62],
  utf16Odd: [0x61, 0x00, 0x62],
  loneSurrogates: [0x00, 0xd8, 0x61, 0x00, 0x00, 0xdc],
  truncatedLead: [0x41, 0x81],
  leadSpace: [0x81, 0x20, 0x8e, 0x20, 0x82, 0x20],
  sjis: [0x82, 0xa0, 0x88, 0x9f, 0x83, 0x41, 0xff],
  eucjp: [0xa4, 0xa2, 0x8e, 0xb1, 0x8f, 0xb0, 0xa1, 0xa1],
  big5: [0xa4, 0x40, 0x87, 0x40, 0x88, 0x62, 0xfe, 0xfe],
  gb18030: [0x81, 0x30, 0x81, 0x30, 0x84, 0x31, 0x95, 0x33, 0x80, 0xa2, 0xe3],
  iso2022jp: [...Buffer.from("a\x1b$B$\"\x1b(Bb\x1b(J~\x1b(I1\x1b(B\x0e")],
  euckr: [0xb0, 0xa1, 0x81, 0x41, 0xc8, 0xfe, 0xff],
}

const out: { label: string; input: string; text: string }[] = []
for (const label of labels)
  for (const [name, bytes] of Object.entries(inputs)) {
    const b = Buffer.from(bytes)
    let text: string
    try {
      text = new TextDecoder(label).decode(b)
    } catch {
      text = new TextDecoder().decode(b)
    }
    // Code units, so lone surrogates survive the JSON.
    out.push({ label, input: name, text: Array.from({ length: text.length }, (_, i) => text.charCodeAt(i).toString(16)).join(" ") })
  }

const file = path.resolve(import.meta.dirname, "../charsets.json")
fs.writeFileSync(file, JSON.stringify({ inputs, cases: out }, null, 1) + "\n")
console.log(`wrote ${file}: ${out.length} cases`)
