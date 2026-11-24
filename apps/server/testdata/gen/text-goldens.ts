// Goldens for chunk.ts and sensitive.ts from the real TS code, on a fixed
// corpus plus a deterministic fuzz corpus built from fragments that trip each
// detector (and near misses), mixed with Unicode noise. Writes
// testdata/text.json.
//
// Run from apps/app: npx tsx ../server/testdata/gen/text-goldens.ts
import fs from "node:fs"
import path from "node:path"
import { chunkText } from "../../../app/src/server/chunk.ts"
import { entropy, findSensitive, ibanValid, luhn, maskSecrets, nikShaped, sensitiveKinds } from "../../../app/src/server/sensitive.ts"

const out = path.resolve(import.meta.dirname, "../text.json")

// mulberry32: a small deterministic PRNG.
function rng(seed: number) {
  return () => {
    seed |= 0
    seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const rand = rng(20261114)
const pick = <T>(xs: readonly T[]) => xs[Math.floor(rand() * xs.length)]!

// Secret-shaped values are glued at runtime so scanners don't flag this file.
const k = (...p: string[]) => p.join("")
const fragments = [
  // cards
  "4111 1111 1111 1111", "4111-1111-1111-1111", "4111111111111111", "5555-5555-5555-4444", "378282246310005", "3782 822463 10005",
  "6011 1111 1111 1117", "30569309025904", "4111 1111 1111 1112", "1999 2000 2001 2002", "+4111111111111111", "94111111111111111",
  "4111 11111 111 1111", "2221 0000 0000 0009", "3530111333300000",
  // NIK
  "3171011508900001", "6371055508920003", "6371 0555 0892 0003", "6371.05.5508.92.0003", "3171-0115-0890-0001", "9971011508900001",
  "3171013208900001", "3171011513900001", "3171011508900000", "63710555089200031", "1 6371055508920003",
  // passports
  "Passport number is C1234567", "Nomor paspor X 1234567, berlaku sampai 2031", "PASSPORT: AB 12345678", "passport expires in 2030",
  "Paspor 123456789", "my passport is in the drawer with 12345678", "Passports C1234567",
  // IBAN
  "IBAN GB82 WEST 1234 5698 7654 32", "DE89370400440532013000 BANK", "DE89 3704 0044 0532 0130 00", "GB82WEST12345698765433", "NL91ABNA0417164300",
  // keys
  k("rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2"), k("zk", "g_", "live_9aF3kQ7mX2pL8vR1tY6w"), k("s", "k-", "proj-4hG8kL2mN6pQ0rS3tU7v"),
  k("s", "k-", "abcdefghijklmnopqrstuvwxyz"), k("sk", "_live_", "4eC39HqLyjWDarjtT1zdp7dc"), k("gh", "p_", "aB3dE5fG7hJ9kL1mN2pQ4rS6tU8vW0xY2zA4"),
  k("github", "_pat_", "11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyzABCDEFG"), k("AK", "IA", "IOSFODNN7EXAMPLE"), k("AI", "za", "SyD-1234567890abcdefghijklmnopqrstu"),
  k("xo", "xb-", "1234567890-abcdefghij"), k("ey", "J", "hbGciOiJIUzI1NiJ9.ey", "J", "zdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"),
  k("-----BEGIN RSA PRIVATE", " KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\n-----END RSA PRIVATE", " KEY-----"),
  k("-----BEGIN PRIVATE", " KEY-----\nunterminated"),
  "Webhook secret: 9f8e7d6c5b4a39281706f5e4d3c2b1a0", "Session cookie value Zx8Qm3Lp9Rt2Vw7Yb4Nc6Kd1Hf5Gj0Ts3Ua8", "api_key = \"Ab12Cd34Ef56Gh78Ij90Kl\"",
  "token: abcdefghijklmnopqrstuvwxyz", "Bearer abcDEF123ghiJKL456mnoPQR789", "client-secret adalah Qw3rTy5UiOp7AsDf9GhJk", "TOKEN Is ZZZZZZZZZZZZZZZZZZZZ1a",
  "commit 3f5a1c9e2b7d4a6f8c0e1b3d5f7a9c2e4b6d8f0a", "https://example.com/path/Zx8Qm3Lp9Rt2Vw7Yb4Nc6Kd1Hf5Gj0Ts3Ua8", "Zx8Qm3Lp9Rt2Vw7Yb4Nc6Kd1Hf5Gj0Ts3Ua8==",
  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa11AA", "API key is [redacted:secret]",
  // OTP
  "OTP: 2024", "Kode OTP adalah 1987", "Kode OTP 482913 jangan dibagikan", "Verification code: 7712", "493201 is your login code", "Logs in with OTP via SMS since 2021",
  "one-time passcode 553201", "2FA code is 2019", "your auth code 12345678", "123456 adalah kode verifikasi", "kode login: 9981",
  // credentials
  "Login zane@example.com with password: hunter2pass", "zane@example.com:Hunter2!", "zane@example.com | Hunter2!", "zane@example.com / Hunter2!",
  "password is hunter2", "password is hunter", "kata sandi adalah rahasia123", "pwd= x9!z", "email zane@example.com, call +62 812-3456-7890",
  "zane@example.com:https://x.y", "zane@example.com / 0812 3456 7890", "zane@example.com: other@example.com",
  // ordinary text that must pass
  "Meeting on 2026-11-16 at 10:30", "Rp 1.250.000", "NPWP 01.234.567.8-901.000", "Order #4111", "+62 812 3456 7890", "years 2019 2020 2021 2022 2023",
  "Zane", "Bandarjaya", "the quick brown fox", "",
]
const noise = [
  " ", "\n", "\n\n", ", ", ": ", " ", "　", "\t", " 😀 ", " 👨‍👩‍👧 ", "K", "İ", "ſ", "１２３４", "é", "—", "\"", "'", "(", ")", ".", "!", "?", "中文", "ك",
]
const seen = new Set<string>()
const texts: string[] = []
const add = (t: string) => {
  if (!seen.has(t)) {
    seen.add(t)
    texts.push(t)
  }
}
for (const f of fragments) add(f)
for (let i = 0; i < 4000; i++) {
  const parts: string[] = []
  const n = 1 + Math.floor(rand() * 4)
  for (let j = 0; j < n; j++) {
    if (rand() < 0.35) parts.push(pick(noise))
    let f = pick(fragments)
    const r = rand()
    if (r < 0.1) f = f.toLowerCase()
    else if (r < 0.2) f = f.toUpperCase()
    else if (r < 0.25) f = f.replace(/ /g, " ")
    else if (r < 0.3) f = f.slice(0, Math.floor(rand() * f.length))
    parts.push(f)
  }
  add(parts.join(pick(["", " ", "\n", " | ", ";"])))
}

const sensitive = texts.map((text) => {
  const m = maskSecrets(text)
  return { text, hits: findSensitive(text), kinds: sensitiveKinds(text), masked: m.text, count: m.masked }
})

const luhnCases = ["4111111111111111", "4111111111111112", "0", "79927398713", "1234567812345670", "", "00000000000000"].map((d) => ({ d, ok: luhn(d) }))
const nikCases = ["3171011508900001", "9971011508900001", "3171013208900001", "3171011513900001", "3171011508900000", "3171014108900001", "3171017208900001"].map((d) => ({
  d,
  ok: nikShaped(d),
}))
const ibanCases = ["GB82 WEST 1234 5698 7654 32", "DE89370400440532013000", "GB82WEST12345698765433", "NL91ABNA0417164300", "XX00", "GB82 WEST 1234 5698 7654 32 BANK"].map((s) => ({
  s,
  ok: ibanValid(s),
}))
const entropyCases = ["", "a", "aaaa", "abcd", "Zx8Qm3Lp9Rt2Vw7Yb4Nc6Kd1Hf5Gj0Ts3Ua8", "😀😀ab"].map((s) => ({ s, h: entropy(s) }))

// --- chunks ---------------------------------------------------------------------
const words = (n: number, w = "word") => Array.from({ length: n }, (_, i) => `${w}${i}`).join(" ")
const sentence = () => {
  const n = 3 + Math.floor(rand() * 25)
  return Array.from({ length: n }, () => pick(["alpha", "beta", "gamma", "rumah", "kopi", "😀", "naïve", "x", "Zane", "data"])).join(pick([" ", "  ", "  "])) + pick([".", "!", "?", "", ",", "..."])
}
const chunkInputs: { text: string; opts?: { size?: number; overlap?: number } }[] = [
  { text: "  \n " },
  { text: "hello there\n\nsecond paragraph" },
  { text: [words(500, "a"), words(500, "b"), words(500, "c")].join("\n\n"), opts: { size: 800, overlap: 100 } },
  { text: words(2000), opts: { size: 800, overlap: 100 } },
  { text: words(2000), opts: { size: 10, overlap: 0 } },
  { text: words(30), opts: { size: 10, overlap: 7 } },
  { text: "a\r\nb\r\n\r\nc\rd\r\r" + words(50), opts: { size: 20 } },
  { text: "One. Two! Three? Four.  Five\n\nSix.\tSeven", opts: { size: 3, overlap: 1 } },
]
for (let i = 0; i < 120; i++) {
  const paras = Array.from({ length: 1 + Math.floor(rand() * 8) }, () => Array.from({ length: 1 + Math.floor(rand() * 12) }, sentence).join(pick([" ", "  ", "\n", " 　"])))
  const text = pick(["", "  ", "\r\n"]) + paras.join(pick(["\n\n", "\n\n\n", "\r\n\r\n", "\n \n"])) + pick(["", "\n", " "])
  const size = pick([5, 10, 20, 50, 100, 800, undefined])
  const overlap = pick([0, 1, 3, 10, 100, undefined])
  chunkInputs.push({ text, opts: size === undefined && overlap === undefined ? undefined : { size, overlap } })
}
const chunks = chunkInputs.map((c) => ({ ...c, chunks: chunkText(c.text, c.opts) }))

fs.writeFileSync(out, JSON.stringify({ sensitive, luhn: luhnCases, nik: nikCases, iban: ibanCases, entropy: entropyCases, chunks }) + "\n")
console.log(`wrote ${sensitive.length} sensitive cases, ${chunks.length} chunk cases to ${out}`)
