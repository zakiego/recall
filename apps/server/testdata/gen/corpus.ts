// The embedding golden corpus (docs/GO-PORT.md, Embeddings). Each text is fed
// to the real TS embedder as both kinds. Keep it deterministic: no clocks, no
// random values.

const base: string[] = [
  // English
  "Prefers Adidas running shoes",
  "Is allergic to peanuts",
  "Lives in Bandarjaya, South Kalimantan",
  "The quick brown fox jumps over the lazy dog.",
  "Meeting with the design team moved to Thursday at 3pm.",
  "Remember to renew the passport before March 2027.",
  "She doesn't like coffee, but she'll drink tea when it's cold.",
  "API rate limits: 100 requests per minute per key.",
  "What did I decide about the database migration last week?",
  "Don't forget: the car insurance renewal is due on the 14th!",
  "I've been working on Recall, a self-hosted memory engine.",
  "Their wedding anniversary is on 7 July.",
  "Favorite programming languages: Go, TypeScript, and Rust.",
  "U.S.A. vs. U.K. spelling: color/colour, organize/organise.",
  "e-mail, re-enter, co-operate, state-of-the-art",
  "Dr. Smith's office (Room 204) opens at 8:30 a.m.",
  "Price went from $1,299.99 to $999 — a 23% drop.",
  "#hashtag @mention https://example.com/path?q=1&r=2#frag",
  "user@example.com sent 3 files: a.pdf, b.docx, c.xlsx",
  "C:\\Users\\zane\\Documents\\notes.txt",
  "SELECT id, fact FROM memories WHERE container = 'default';",
  "func main() { fmt.Println(\"hello, world\") }",
  "const x = (a, b) => a ?? b;",
  "ALL CAPS SENTENCE WITH SHOUTING",
  "mIxEd CaSe TeXt",
  "1234567890",
  "3.14159265358979323846",
  "2026-11-14T10:30:00+08:00",
  "+62 812-3456-7890",
  "lowercase only words here",
  // Indonesian, formal
  "Rapat koordinasi akan dilaksanakan pada hari Senin pukul 10.30 WITA.",
  "Mahasiswa wajib mengumpulkan laporan akhir sebelum tanggal 20 Desember.",
  "Seminar hasil penelitian dijadwalkan di Ruang Baca Sosiologi.",
  "Pemerintah Provinsi Kalimantan Selatan meluncurkan program baru.",
  "Dengan hormat, kami sampaikan undangan untuk menghadiri acara tersebut.",
  "Zane suka sepatu Adidas",
  "alergi kacang food allergy",
  "tinggal kota domisili home city",
  // Indonesian, casual
  "gue lagi males banget ngoding hari ini wkwk",
  "eh besok jadi ketemuan gak? kabarin ya",
  "udah makan belum? jangan lupa minum obat",
  "anjir servernya down lagi, cek dong",
  "otw bentar lg nyampe, macet parah di jalan A. Yani",
  "mantap jiwa, akhirnya kelar juga skripsinya",
  "sabi lah ntar malem nongkrong",
  "btw makasih udah bantuin kemaren",
  // Mixed language
  "Besok ada deadline project, jadi I need to finish the API first.",
  "Tolong review PR-nya ya, the tests are already green.",
  "Meeting-nya dipindah ke Zoom karena hujan deras.",
  "gue prefer pakai Go buat backend, TypeScript buat frontend",
  "Deploy ke production pas weekend itu bad idea banget",
  // CJK
  "我喜欢在周末去公园散步。",
  "東京は日本の首都です。",
  "今日はとても暑いですね",
  "カタカナとひらがなと漢字",
  "서울은 한국의 수도입니다.",
  "안녕하세요, 만나서 반갑습니다",
  "中文、日本語、한국어 mixed with English",
  "𠀀𠀁𠀂 rare CJK extension B characters",
  // Arabic and RTL
  "مرحبا بك في التطبيق",
  "اللغة العربية جميلة جدا",
  "שלום עולם",
  "Arabic inside English: كتاب means book.",
  // Other scripts
  "Привет, как дела?",
  "Γειά σου Κόσμε",
  "नमस्ते दुनिया",
  "สวัสดีครับ ยินดีที่ได้รู้จัก",
  "Xin chào, tôi tên là Minh.",
  "Türkçe karakterler: ğüşiöç İ ı",
  "Ünïcödé àccénts éverywhere",
  "Straße, Mädchen, Größe",
  "ᐊᐃᓐᓇᐅᔭᖅ inuktitut syllabics",
  "𓀀𓀁𓀂 Egyptian hieroglyphs",
  "ꦲꦏ꧀ꦱꦫꦗꦮ Javanese script",
  // Emoji, ZWJ, flags, skin tones
  "I love pizza 🍕🍕🍕",
  "👨‍👩‍👧‍👦 family emoji ZWJ sequence",
  "👍🏽 thumbs up with skin tone",
  "🇮🇩 🇯🇵 🇺🇸 flags",
  "❤️ red heart with variation selector",
  "🏳️‍🌈 rainbow flag",
  "😀😃😄😁😆😅🤣😂🙂🙃",
  "keycap 1️⃣ 2️⃣ #️⃣",
  "🧑🏻‍💻 technologist",
  // Combining marks and normalization
  "e\u0301 cafe\u0301 combining acute",
  "n\u0303 an\u0303o",
  "\u0301 starts with a combining mark",
  "a\u0300\u0301\u0302\u0303\u0304 stacked marks",
  "ﬁ ﬂ ligatures ﬀ",
  "Ｆｕｌｌｗｉｄｔｈ ｔｅｘｔ １２３",
  "x² + y³ = z⁴",
  "½ ¼ ¾ fractions",
  "™ © ® symbols",
  "Ⅻ roman numeral twelve",
  "㎞ ㎏ ㏄ squared units",
  "ｶﾀｶﾅ halfwidth katakana",
  "～tilde～wave～",
  "a～b",
  "Hangul jamo 각",
  // Whitespace of every kind
  "tab\tseparated\tvalues",
  "line one\nline two\nline three",
  "windows\r\nline endings",
  "non\u00a0breaking\u00a0space",
  "em\u2003space and en\u2002space",
  "ideographic\u3000space",
  "zero\u200bwidth\u200bspace",
  "narrow\u202fno-break",
  "line\u2028separator and paragraph\u2029separator",
  "ogham\u1680space",
  "\ufeffbyte order mark at start",
  "vertical\u000btab and form\u000cfeed",
  "   leading and trailing spaces   ",
  "multiple     internal      spaces",
  "\t\n  mixed leading whitespace",
  "soft\u00adhyphen",
  "mongolian\u180evowel separator",
  "medium\u205fmath space",
  "hair\u200aspace thin\u2009space",
  "word\u2581with lower one eighth block",
  "replacement \ufffd character",
  "control\u0001chars\u0007here\u001f",
  "delete\u007fchar",
  "c1\u0085next line and \u008fss3",
  // Empty after trimming, tiny texts
  "",
  " ",
  "\t\n\r ",
  "\u00a0\u3000",
  "a",
  "Z",
  "7",
  ".",
  "我",
  "😀",
  "é",
  // Special-token look-alikes
  "<s>",
  "</s>",
  "<pad> <unk> <mask>",
  "text with <mask> in the middle",
  "<s>start and end</s>",
  "＜s＞fullwidth brackets＜/s＞",
  "<sss> not a special token",
  "<<s>>",
  "＜unk＞x",
  "<unk>",
  "a<unk>b",
  // Markdown and structured text
  "# Heading\n\n- item one\n- item two\n\n**bold** and _italic_",
  "| col a | col b |\n|---|---|\n| 1 | 2 |",
  "```go\nfmt.Println(1)\n```",
  "{\"key\": \"value\", \"n\": 1, \"ok\": true}",
  "<p>Some <b>HTML</b> markup &amp; entities</p>",
  "> quoted reply\n>> nested quote",
  "1. first\n2. second\n3. third",
]

// Texts with secrets, masked the way sensitive.ts masks them before embedding.
export const secretTexts: string[] = [
  "my card is 4111 1111 1111 1111 exp 12/29",
  `api key ${"sk" + "-proj-"}${"AbCdEf0123456789".repeat(2)}AbCdEf01 for the gateway`,
  "password: hunter2Super!Secret#2026",
  "NIK 6371010101900001 atas nama Zane",
  `github token ${"gh" + "p_"}AbCdEfGhIjKlMnOpQrStUvWxYz0123456789`,
  "IBAN DE89 3704 0044 0532 0130 00 for the transfer",
]

function long(seed: string, words: number): string {
  const parts: string[] = []
  const vocab = seed.split(" ")
  for (let i = 0; i < words; i++) parts.push(vocab[i % vocab.length]!)
  return parts.join(" ")
}

const longTexts: string[] = [
  long("The memory engine stores facts about people places and plans so later questions find them quickly", 700),
  long("Pemerintah daerah mengumumkan jadwal baru untuk pelayanan publik di kantor kecamatan setiap hari kerja", 900),
  long("我们 今天 讨论 了 项目 的 进度 和 下 一步 计划", 1200),
  long("emoji 😀 test 🍕 with 👨‍👩‍👧 many 🇮🇩 symbols", 600),
  "x".repeat(3000),
  "abc ".repeat(800),
  long("a b c d e f g h i j", 1100),
  long("Zane is a software engineer based in Banjarmasin who likes Go TypeScript and self hosting", 520),
]

// Programmatic variants: case, punctuation and spacing around the base texts.
function variants(): string[] {
  const out: string[] = []
  const picks = base.filter((t) => t.length > 8 && t.length < 80).slice(0, 40)
  for (const t of picks) out.push(t.toUpperCase())
  for (const t of picks.slice(0, 20)) out.push(`  ${t.replace(/ /g, "  ")}\n`)
  return out
}

export async function corpus(maskSecrets: (t: string) => { text: string }): Promise<string[]> {
  const masked = secretTexts.map((t) => maskSecrets(t).text)
  return [...base, ...secretTexts, ...masked, ...longTexts, ...variants()]
}
