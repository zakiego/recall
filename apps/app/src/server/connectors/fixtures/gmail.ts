// Gmail API responses in the shape users.messages.get?format=full returns,
// modeled on recorded responses with the content replaced. Bodies are built
// with b64() so the text stays readable here; the wire format is the same.
import type { GmailMessage, GmailPart } from "../google"

export const b64 = (s: string | Uint8Array) => Buffer.from(s).toString("base64url")

// A one-page PDF with real text, small enough to live in a fixture.
export function minimalPdf(text: string, title = "Fixture"): Uint8Array {
  const esc = (s: string) => s.replace(/[\\()]/g, (c) => `\\${c}`)
  const stream = `BT /F1 14 Tf 72 720 Td (${esc(text)}) Tj ET`
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
    `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    `<< /Title (${esc(title)}) >>`,
  ]
  let out = "%PDF-1.4\n"
  const offsets: number[] = []
  objects.forEach((body, i) => {
    offsets.push(out.length)
    out += `${i + 1} 0 obj\n${body}\nendobj\n`
  })
  const xref = out.length
  out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (const o of offsets) out += `${String(o).padStart(10, "0")} 00000 n \n`
  out += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R /Info 6 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return new TextEncoder().encode(out)
}

const headers = (h: Record<string, string>) => Object.entries(h).map(([name, value]) => ({ name, value }))

function text(partId: string, mimeType: string, body: string, charset = "UTF-8"): GmailPart {
  const bytes = Buffer.from(body)
  return {
    partId,
    mimeType,
    filename: "",
    headers: headers({ "Content-Type": `${mimeType}; charset="${charset}"`, "Content-Transfer-Encoding": "quoted-printable" }),
    body: { size: bytes.byteLength, data: b64(bytes) },
  }
}

export const TICKET_PDF = minimalPdf("Booking code KX7Q2M. Jakarta CGK to Banjarbaru BDJ, 14 November 2026, seat 21A.", "E-ticket GA 532")

export const ATTACHMENTS: Record<string, Record<string, Uint8Array>> = {
  "18c2a1f0b9e4d701": { "ANGjdJ8w-ticket-part-2": TICKET_PDF },
}

export const MESSAGES: GmailMessage[] = [
  {
    id: "18c2a1f0b9e4d701",
    threadId: "18c2a1f0b9e4d701",
    labelIds: ["INBOX", "CATEGORY_UPDATES", "Label_travel"],
    snippet: "Your trip to Banjarbaru is confirmed. Booking code KX7Q2M, departing 14 Nov 2026 at 09:40 from Jakarta (CGK).",
    internalDate: "1794286800000",
    sizeEstimate: 48211,
    payload: {
      partId: "",
      mimeType: "multipart/mixed",
      filename: "",
      headers: headers({
        From: "Garuda Indonesia <noreply@garuda-indonesia.example>",
        To: "Zaki <zaki@example.com>",
        Subject: "E-ticket: Jakarta to Banjarbaru, 14 Nov",
        Date: "Mon, 10 Nov 2026 05:00:00 +0000",
        "Content-Type": 'multipart/mixed; boundary="000000000000a1"',
      }),
      body: { size: 0 },
      parts: [
        {
          partId: "0",
          mimeType: "multipart/alternative",
          filename: "",
          headers: headers({ "Content-Type": 'multipart/alternative; boundary="000000000000a2"' }),
          body: { size: 0 },
          parts: [
            text("0.0", "text/plain", "Hi Zaki,\r\n\r\nYour trip to Banjarbaru is confirmed.\r\nBooking code: KX7Q2M\r\nFlight GA 532, departing 14 Nov 2026 at 09:40 from Jakarta (CGK), seat 21A.\r\n\r\nThe e-ticket is attached.\r\n"),
            text("0.1", "text/html", "<html><body><p>Hi Zaki,</p><p>Your trip to Banjarbaru is confirmed.</p></body></html>"),
          ],
        },
        {
          partId: "1",
          mimeType: "image/png",
          filename: "logo.png",
          headers: headers({ "Content-Type": 'image/png; name="logo.png"', "Content-Disposition": 'inline; filename="logo.png"' }),
          body: { size: 2048, attachmentId: "ANGjdJ8w-logo-part-1" },
        },
        {
          partId: "2",
          mimeType: "application/pdf",
          filename: "e-ticket-GA532.pdf",
          headers: headers({ "Content-Type": 'application/pdf; name="e-ticket-GA532.pdf"', "Content-Disposition": 'attachment; filename="e-ticket-GA532.pdf"' }),
          body: { size: TICKET_PDF.byteLength, attachmentId: "ANGjdJ8w-ticket-part-2" },
        },
      ],
    },
  },
  {
    id: "18c29e7d3a5b6c02",
    threadId: "18c29e1a00000002",
    labelIds: ["INBOX", "IMPORTANT"],
    snippet: "Thanks for today. Summary: we ship the Gmail import first, Drive later. Rina owns the docs, Zaki owns the OAuth setup.",
    internalDate: "1794196800000",
    sizeEstimate: 6120,
    payload: {
      partId: "",
      mimeType: "text/plain",
      filename: "",
      headers: headers({
        From: "Rina Pratama <rina@example.com>",
        To: "zaki@example.com",
        Subject: "Re: Project kickoff notes",
        Date: "Sun, 9 Nov 2026 04:00:00 +0000",
        "Content-Type": 'text/plain; charset="UTF-8"',
      }),
      body: {
        size: 180,
        data: b64("Thanks for today. Summary: we ship the Gmail import first, Drive later.\r\nRina owns the docs, Zaki owns the OAuth setup.\r\nNext check-in is Thursday.\r\n"),
      },
    },
  },
  {
    id: "18c29b11e0f4a903",
    threadId: "18c29b11e0f4a903",
    labelIds: ["INBOX", "Label_bills"],
    snippet: "Tagihan listrik Oktober 2026 untuk ID pelanggan 5321 sebesar Rp 412.500. Jatuh tempo 20 November 2026.",
    internalDate: "1794110400000",
    sizeEstimate: 15400,
    payload: {
      partId: "",
      mimeType: "text/html",
      filename: "",
      headers: headers({
        From: "PLN Mobile <tagihan@pln.example>",
        To: "zaki@example.com",
        Subject: "Tagihan listrik Oktober 2026",
        Date: "Sat, 8 Nov 2026 04:00:00 +0000",
        "Content-Type": 'text/html; charset="UTF-8"',
      }),
      body: {
        size: 400,
        data: b64(
          "<html><head><title>Tagihan</title><style>p{color:red}</style></head><body><nav>Menu</nav><h1>Tagihan listrik Oktober 2026</h1><p>Tagihan listrik Oktober 2026 untuk ID pelanggan 5321 sebesar Rp 412.500.</p><p>Jatuh tempo 20 November 2026.</p></body></html>",
        ),
      },
    },
  },
  {
    id: "18c2981c5d2e7b04",
    threadId: "18c2981c5d2e7b04",
    labelIds: ["CATEGORY_UPDATES", "Label_github"],
    snippet: "zakiego merged 1 commit into main: fix(app): scan only src/ for Tailwind classes &amp; drop the old glob (#11)",
    internalDate: "1794024000000",
    sizeEstimate: 9030,
    payload: {
      partId: "",
      mimeType: "text/plain",
      filename: "",
      headers: headers({
        From: "GitHub <notifications@github.example>",
        To: "zaki@example.com",
        Subject: "[zakiego/recall] fix(app): scan only src/ for Tailwind classes (#11)",
        Date: "Fri, 7 Nov 2026 04:00:00 +0000",
        "Content-Type": 'text/plain; charset="UTF-8"',
      }),
      body: { size: 120, data: b64("Merged #11 into main.\r\n\r\nfix(app): scan only src/ for Tailwind classes & drop the old glob\r\n") },
    },
  },
  {
    id: "18c2950a7f1c3d05",
    threadId: "18c2950a7f1c3d05",
    labelIds: ["INBOX", "Label_bills"],
    snippet: "Invoice INV-2026-1107 for Cloudflare Workers Paid, November 2026. Amount due: USD 5.00.",
    internalDate: "1793937600000",
    sizeEstimate: 30500,
    payload: {
      partId: "",
      mimeType: "multipart/mixed",
      filename: "",
      headers: headers({
        From: "Cloudflare Billing <billing@cloudflare.example>",
        To: "zaki@example.com",
        Subject: "Your Cloudflare invoice INV-2026-1107",
        Date: "Thu, 6 Nov 2026 04:00:00 +0000",
        "Content-Type": 'multipart/mixed; boundary="000000000000b1"',
      }),
      body: { size: 0 },
      parts: [
        text("0", "text/plain", "Invoice INV-2026-1107 for Cloudflare Workers Paid, November 2026.\r\nAmount due: USD 5.00.\r\n"),
        {
          partId: "1",
          mimeType: "application/octet-stream",
          filename: "INV-2026-1107.pdf",
          headers: headers({ "Content-Disposition": 'attachment; filename="INV-2026-1107.pdf"' }),
          body: { size: 0, data: b64(minimalPdf("Invoice INV-2026-1107. Workers Paid. USD 5.00.", "INV-2026-1107")) },
        },
      ],
    },
  },
]

// Worst realistic mail for the Connectors page (break-ui): a very long
// Indonesian subject from a long hyphenated name and an unbreakable address,
// a message with no subject, no sender name and no snippet, a Japanese subject
// from an earlier year, and Vietnamese diacritics. Shown in dev with
// RECALL_GMAIL_FAKE=1 RECALL_GMAIL_FAKE_DATA=worst.
const plain = (id: string, h: Record<string, string>, body: string, rest: Partial<GmailMessage>): GmailMessage => ({
  id,
  threadId: id,
  labelIds: ["INBOX"],
  sizeEstimate: 4000,
  ...rest,
  payload: {
    partId: "",
    mimeType: "text/plain",
    filename: "",
    headers: headers({ ...h, "Content-Type": 'text/plain; charset="UTF-8"' }),
    body: { size: body.length, data: b64(body) },
  },
})

export const WORST_MESSAGES: GmailMessage[] = [
  plain(
    "18c28f1a2b3c4d06",
    {
      From: "Aleksandra Wiśniewska-Kowalczyk <aleksandra.wisniewska-kowalczyk@northwind-industries-holdings.example.com>",
      To: "zaki@example.com",
      Subject:
        "Re: Fwd: [Action required] Perpanjangan kontrak sewa ruko Jl. Ahmad Yani Km. 4,5 Banjarmasin, periode Januari 2027 sampai Desember 2029 (revisi ketiga, mohon ditandatangani sebelum 15 November)",
      Date: "Mon, 10 Nov 2026 08:00:00 +0000",
    },
    "Mohon dicek lampiran kontraknya.\r\n",
    {
      internalDate: "1794297600000",
      snippet:
        "Halo Pak Zaki, berikut revisi ketiga kontrak sewa ruko. Perubahan utama ada di pasal 4 (kenaikan sewa 7% per tahun), pasal 9 (biaya perawatan AC ditanggung penyewa) dan lampiran B (denah lantai 2). Mohon ditandatangani &amp; dikirim balik sebelum 15 November.",
    },
  ),
  plain("18c28e0a1b2c3d07", { From: "bartholomew.fitzgerald@northwind-industries-holdings.example.com", To: "zaki@example.com" }, "", {
    internalDate: "1794200000000",
    snippet: "",
  }),
  plain(
    "1874f00a1b2c3d08",
    {
      From: "楽天カード <info@mail.rakuten-card.example.co.jp>",
      To: "zaki@example.com",
      Subject: "【重要】2024年11月分のご請求金額のお知らせ（楽天カード株式会社）",
      Date: "Fri, 8 Nov 2024 01:00:00 +0000",
    },
    "ご請求金額のお知らせです。\r\n",
    { internalDate: "1731027600000", snippet: "いつも楽天カードをご利用いただきありがとうございます。2024年11月分のご請求金額が確定しました。" },
  ),
  plain(
    "18c28d0a1b2c3d09",
    { From: "Nguyễn Thị Hương <huong@example.vn>", To: "zaki@example.com", Subject: "Thông báo thay đổi lịch họp: Thứ Năm, ngày 13 tháng 11", Date: "Sun, 9 Nov 2026 02:00:00 +0000" },
    "Cuộc họp dời sang 14:00.\r\n",
    { internalDate: "1794189600000", snippet: "Cuộc họp dời sang 14:00 thứ Năm. Vui lòng xác nhận." },
  ),
]
