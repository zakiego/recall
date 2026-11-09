// Detects sensitive data so it never becomes a memory fact, and masks secrets
// in raw text before it is chunked and indexed. Every rule is conservative on
// purpose: ordinary emails, contact phone numbers, dates, money amounts and
// company registration numbers must pass untouched.

export type SensitiveKind = "card" | "nik" | "passport" | "iban" | "secret" | "otp" | "credentials"
export type SensitiveHit = { kind: SensitiveKind; start: number; end: number }

// What maskSecrets puts in place of a secret. Facts the model writes around a
// placeholder ("API key is [redacted:secret]") are dropped too.
const PLACEHOLDER_RE = /\[redacted:[a-z]+\]/
const placeholder = (kind: SensitiveKind) => `[redacted:${kind}]`

// --- cards ------------------------------------------------------------------

// 14 to 19 digits, optionally grouped by single spaces or dashes. A leading +
// or digit means it's part of a longer number (a phone number, an id).
const CARD_RE = /(?<![\d+])\d(?:[ -]?\d){13,18}(?![\d])/g

export function luhn(digits: string) {
  let sum = 0
  for (let i = 0; i < digits.length; i++) {
    let d = digits.charCodeAt(digits.length - 1 - i) - 48
    if (i % 2 === 1) {
      d *= 2
      if (d > 9) d -= 9
    }
    sum += d
  }
  return sum % 10 === 0
}

// Issuer prefix and length, so a random Luhn-valid number (1 in 10) isn't enough.
function cardShaped(d: string) {
  const n = d.length
  if (/^4/.test(d)) return n === 16 || n === 19
  if (/^(5[1-5]|222[1-9]|22[3-9]\d|2[3-6]\d\d|27[01]\d|2720)/.test(d)) return n === 16
  if (/^3[47]/.test(d)) return n === 15
  if (/^3(0[0-5]|[689])/.test(d)) return n >= 14 && n <= 19
  if (/^(6011|64[4-9]|65|62|35(2[89]|[3-8]\d))/.test(d)) return n >= 16 && n <= 19
  return false
}

// Grouped numbers must be grouped like a card (4-4-4-4, 4-6-5, ...), not a list of years.
function cardGrouping(raw: string) {
  const groups = raw.split(/[ -]/)
  if (groups.length === 1) return true
  return groups.slice(0, -1).every((g, i) => g.length === 4 || (i === 1 && g.length === 6))
}

function cards(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  for (const m of text.matchAll(CARD_RE)) {
    const digits = m[0].replace(/\D/g, "")
    if (cardShaped(digits) && cardGrouping(m[0]) && luhn(digits)) hits.push({ kind: "card", start: m.index, end: m.index + m[0].length })
  }
  return hits
}

// --- NIK (Indonesian national id) ---------------------------------------------

// 16 digits: province, regency, district, birth date DDMMYY (women add 40 to DD), serial.
// Forms often group it ("6371 0555 0892 0003", "6371.05.5508.92.0003"), so a
// single space, dot or dash may sit between digits. Not part of a longer number.
const NIK_RE = /(?<![\d+]|\d[ .-])\d(?:[ .-]?\d){15}(?![ .-]?\d)/g
const PROVINCES = new Set([
  11, 12, 13, 14, 15, 16, 17, 18, 19, 21, 31, 32, 33, 34, 35, 36, 51, 52, 53, 61, 62, 63, 64, 65, 71, 72, 73, 74, 75, 76,
  81, 82, 91, 92, 93, 94, 95, 96,
])

export function nikShaped(d: string) {
  if (!PROVINCES.has(Number(d.slice(0, 2)))) return false
  const day = Number(d.slice(6, 8))
  const month = Number(d.slice(8, 10))
  const dayOk = (day >= 1 && day <= 31) || (day >= 41 && day <= 71)
  return dayOk && month >= 1 && month <= 12 && d.slice(12) !== "0000"
}

function niks(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  for (const m of text.matchAll(NIK_RE)) {
    if (nikShaped(m[0].replace(/\D/g, ""))) hits.push({ kind: "nik", start: m.index, end: m.index + m[0].length })
  }
  return hits
}

// --- passport ---------------------------------------------------------------

// Only a passport-like number shortly after the word itself ("Passport No. C1234567",
// "nomor paspor X 1234567"). "Passport expires in 2030" passes.
const PASSPORT_RE = /\b(?:[Pp]assport|PASSPORT|[Pp]aspor|PASPOR)\b[^\n]{0,40}?(?<![A-Za-z0-9])([A-Z]{1,2} ?\d{6,8}|\d{8,9})(?![A-Za-z0-9])/g

function passports(text: string): SensitiveHit[] {
  return [...text.matchAll(PASSPORT_RE)].map((m) => {
    const start = m.index + m[0].length - m[1]!.length
    return { kind: "passport" as const, start, end: m.index + m[0].length }
  })
}

// --- IBAN -------------------------------------------------------------------

const IBAN_RE = /\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?\b/g

export function ibanValid(raw: string) {
  const s = raw.replace(/ /g, "")
  if (s.length < 15 || s.length > 34) return false
  const moved = s.slice(4) + s.slice(0, 4)
  let rem = 0
  for (const ch of moved) {
    const v = ch >= "A" ? String(ch.charCodeAt(0) - 55) : ch
    for (const c of v) rem = (rem * 10 + (c.charCodeAt(0) - 48)) % 97
  }
  return rem === 1
}

function ibans(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  for (const m of text.matchAll(IBAN_RE)) {
    // A trailing uppercase word ("... 00 BANK") can be swallowed; drop groups until it checks.
    let s = m[0]
    while (s.replace(/ /g, "").length >= 15) {
      if (ibanValid(s)) {
        hits.push({ kind: "iban", start: m.index, end: m.index + s.length })
        break
      }
      s = s.replace(/ ?[A-Z0-9]+$/, "")
    }
  }
  return hits
}

// --- API keys, tokens and other secrets -------------------------------------

const KEY_RES: RegExp[] = [
  /\brcl_[A-Za-z0-9]{16,}/g, // recall API keys
  /\bzkg_[A-Za-z0-9_-]{16,}/g, // ai-gateway keys
  /\bsk-(?=[A-Za-z0-9_-]*\d)[A-Za-z0-9_-]{20,}/g, // OpenAI / Anthropic style
  /\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}/g, // Stripe
  /\bgh[pousr]_[A-Za-z0-9]{30,}/g, // GitHub
  /\bgithub_pat_[A-Za-z0-9_]{40,}/g,
  /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g, // AWS access key id
  /\bAIza[0-9A-Za-z_-]{35}/g, // Google API key
  /\bxox[abprs]-[A-Za-z0-9-]{10,}/g, // Slack
  /\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/g, // JWT
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)/g,
]

// A long value right after a word like "token" or "api key". Only the value is masked.
const LABELED_SECRET_RE =
  /\b(?:api[_ -]?key|secret|token|bearer|access[_ -]?key|client[_ -]?secret|auth[_ -]?key)\b["']?\s*(?:[:=]|is|adalah)?\s*["']?([A-Za-z0-9+/_\-.=]{20,})/gi

// A long, high-entropy base64-ish string standing on its own (not inside a URL,
// path or query string). Hex alone is not enough: commit SHAs are ordinary text.
const LOOSE_SECRET_RE = /(?<![A-Za-z0-9+/_=.:@%&?#~-])[A-Za-z0-9+_-]{32,}={0,2}(?![A-Za-z0-9+/_=.-])/g

export function entropy(s: string) {
  const counts = new Map<string, number>()
  for (const ch of s) counts.set(ch, (counts.get(ch) ?? 0) + 1)
  let h = 0
  for (const n of counts.values()) h -= (n / s.length) * Math.log2(n / s.length)
  return h
}

const mixed = (s: string) => /[a-z]/.test(s) && /[A-Z]/.test(s) && (s.match(/\d/g)?.length ?? 0) >= 2

function secrets(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  for (const re of KEY_RES) for (const m of text.matchAll(re)) hits.push({ kind: "secret", start: m.index, end: m.index + m[0].length })
  for (const m of text.matchAll(LABELED_SECRET_RE)) {
    const v = m[1]!
    if (/\d/.test(v) && /[A-Za-z]/.test(v)) {
      const start = m.index + m[0].length - v.length
      hits.push({ kind: "secret", start, end: start + v.length })
    }
  }
  for (const m of text.matchAll(LOOSE_SECRET_RE)) {
    if (mixed(m[0]) && entropy(m[0]) >= 4.2) hits.push({ kind: "secret", start: m.index, end: m.index + m[0].length })
  }
  return hits
}

// --- OTP codes --------------------------------------------------------------

const OTP_WORDS =
  "OTP|one[- ]time (?:password|passcode|code|pin)|verification code|kode verifikasi|security code|kode keamanan|login code|kode login|(?:2FA|MFA) code|auth(?:entication)? code|kode OTP"
// "OTP ... 482913": the word, up to 20 non-digits, then the code.
const OTP_AFTER_WORD = new RegExp(`\\b(?:${OTP_WORDS})\\b([^\\d\\n]{0,20})(\\d{4,8})\\b`, "gi")
// "482913 is your login code"
const OTP_BEFORE_WORD = /\b(\d{4,8})\s+(?:is your|adalah)\b[^\n]{0,30}\b(?:code|kode|OTP|password)\b/gi
const YEAR = /^(?:19|20)\d\d$/
// What sits right before a code that is being handed over ("code: 2024", "OTP is 2024").
const CODE_CONTEXT = /(?::|\bis|\badalah|\bkode)\s*$/i

function otps(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  for (const m of text.matchAll(OTP_AFTER_WORD)) {
    // "Logs in with OTP via SMS since 2021" is a year, not a code.
    if (YEAR.test(m[2]!) && !CODE_CONTEXT.test(m[1]!)) continue
    hits.push({ kind: "otp", start: m.index, end: m.index + m[0].length })
  }
  for (const m of text.matchAll(OTP_BEFORE_WORD)) hits.push({ kind: "otp", start: m.index, end: m.index + m[0].length })
  return hits
}

// --- email + password -------------------------------------------------------

const EMAIL_RE = /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/
const PASSWORD_RE = /\b(?:password|passwd|pwd|passcode|kata sandi|sandi)\b\s*(?:([:=])|is|adalah)\s*["']?(\S{4,})/gi
// "me@x.com:hunter2", "me@x.com | Hunter2!", "me@x.com / Hunter2!"
const PAIR_RE = /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::(?=\S)|\s*\|\s*|\s+\/\s+)(\S{4,})/g

const hasDigitOrSymbol = (s: string) => /[\d!#$%^&*()_+=\[\]{};'"\\|<>?~`]/.test(s)
const phoneLike = (s: string) => /^[\d\s()+.-]+$/.test(s)

function credentials(text: string): SensitiveHit[] {
  const hits: SensitiveHit[] = []
  if (EMAIL_RE.test(text)) {
    for (const m of text.matchAll(PASSWORD_RE)) {
      // "password: x" counts as is; "password is x" needs x to look like a password.
      if (m[1] || hasDigitOrSymbol(m[2]!)) hits.push({ kind: "credentials", start: m.index, end: m.index + m[0].length })
    }
  }
  for (const m of text.matchAll(PAIR_RE)) {
    const v = m[1]!
    if (!v.includes("@") && !/^https?:/i.test(v) && !phoneLike(v) && hasDigitOrSymbol(v)) {
      hits.push({ kind: "credentials", start: m.index, end: m.index + m[0].length })
    }
  }
  return hits
}

// --- public API -------------------------------------------------------------

const DETECTORS: Record<SensitiveKind, (text: string) => SensitiveHit[]> = {
  card: cards,
  nik: niks,
  passport: passports,
  iban: ibans,
  secret: secrets,
  otp: otps,
  credentials,
}

export function findSensitive(text: string, kinds: SensitiveKind[] = Object.keys(DETECTORS) as SensitiveKind[]): SensitiveHit[] {
  return kinds.flatMap((k) => DETECTORS[k](text)).sort((a, b) => a.start - b.start || b.end - a.end)
}

// The kinds found in a fact, or [] when it's safe to store. A fact that only
// exists to point at a masked secret is not worth keeping either.
export function sensitiveKinds(text: string): SensitiveKind[] {
  const kinds = new Set(findSensitive(text).map((h) => h.kind))
  if (PLACEHOLDER_RE.test(text)) kinds.add("secret")
  return [...kinds]
}

// Kinds masked in raw text (chunks, stored document content). Ids like NIK or a
// passport number stay in the source text; only facts are guarded against them.
const MASKED_KINDS: SensitiveKind[] = ["secret", "card"]

export function maskSecrets(text: string): { text: string; masked: number } {
  const hits = findSensitive(text, MASKED_KINDS)
  if (hits.length === 0) return { text, masked: 0 }
  let out = ""
  let pos = 0
  let masked = 0
  for (const h of hits) {
    if (h.start < pos) {
      // Overlaps a hit already masked: swallow whatever sticks out past it.
      pos = Math.max(pos, h.end)
      continue
    }
    out += text.slice(pos, h.start) + placeholder(h.kind)
    pos = h.end
    masked++
  }
  return { text: out + text.slice(pos), masked }
}
