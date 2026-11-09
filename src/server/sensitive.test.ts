import { describe, expect, it } from "vitest"
import { ibanValid, luhn, maskSecrets, nikShaped, sensitiveKinds } from "./sensitive"

// Fake values built to pass the checks (test card numbers, a made-up NIK).
// Keys are glued together at runtime so secret scanners don't flag this file.
const key = (...parts: string[]) => parts.join("")
const GH_TOKEN = key("gh", "p_", "aB3dE5fG7hJ9kL1mN2pQ4rS6tU8vW0xY2zA4")
const RCL_KEY = key("rc", "l_", "8fK2mQ9xLp3Vt7Rz1Yc5Hn0Wd4Gb6Js2")
const ZKG_KEY = key("zk", "g_", "live_9aF3kQ7mX2pL8vR1tY6w")
const SK_KEY = key("s", "k-", "proj-4hG8kL2mN6pQ0rS3tU7v")
const AWS_KEY = key("AK", "IA", "IOSFODNN7EXAMPLE")

describe("sensitiveKinds: true positives", () => {
  it.each([
    ["Credit card number is 4111 1111 1111 1111", "card"],
    ["Pays with Mastercard 5555-5555-5555-4444", "card"],
    ["Amex 378282246310005 expires 2029", "card"],
    ["NIK 3171011508900001", "nik"],
    ["Nomor KTP: 6371055508920003", "nik"],
    ["NIK 6371 0555 0892 0003", "nik"],
    ["KTP number 6371.05.5508.92.0003", "nik"],
    ["NIK: 3171-0115-0890-0001", "nik"],
    ["OTP: 2024", "otp"],
    ["Kode OTP adalah 1987", "otp"],
    ["Passport number is C1234567", "passport"],
    ["Nomor paspor X 1234567, berlaku sampai 2031", "passport"],
    ["IBAN GB82 WEST 1234 5698 7654 32", "iban"],
    ["Salary goes to DE89370400440532013000 BANK", "iban"],
    [`Recall API key ${RCL_KEY}`, "secret"],
    [`Gateway key ${ZKG_KEY}`, "secret"],
    [`OpenAI key ${SK_KEY}`, "secret"],
    [`GitHub token ${GH_TOKEN}`, "secret"],
    [`AWS key ${AWS_KEY}`, "secret"],
    ["Webhook secret: 9f8e7d6c5b4a39281706f5e4d3c2b1a0", "secret"],
    ["Session cookie value Zx8Qm3Lp9Rt2Vw7Yb4Nc6Kd1Hf5Gj0Ts3Ua8", "secret"],
    ["API key is [redacted:secret]", "secret"],
    ["Kode OTP 482913 jangan dibagikan", "otp"],
    ["Verification code: 7712", "otp"],
    ["493201 is your login code", "otp"],
    ["Login zaki@example.com with password: hunter2pass", "credentials"],
    ["Akun kantor budi@kantor.co.id, kata sandi adalah Rahasia123!", "credentials"],
    ["Netflix login: eva@gmail.com:Sup3rSecret", "credentials"],
  ])("flags %s", (text, kind) => {
    expect(sensitiveKinds(text)).toContain(kind)
  })
})

describe("sensitiveKinds: ordinary facts pass", () => {
  it.each([
    "Works at Speechify as a senior engineer",
    "Work email is zaki@speechify.com",
    "Recruiter Sarah can be reached at sarah@acme.io or +62 812-3456-7890",
    "Nomor HP Pak Budi 0812 3456 7890",
    "Contact Andi at 6281234567890 on WhatsApp",
    "Interview scheduled for 2026-11-20 at 10:00 WIB",
    "Offered salary is Rp 45.000.000 per month",
    "Expected compensation is $185,000 a year plus 0.25% equity",
    "Company registration number 12345678 (UK Companies House)",
    "PT Maju Jaya NIB 9120001234567",
    "NPWP badan 0012345678901000",
    "Holds an Indonesian passport valid until 2031",
    "Prefers password managers like 1Password",
    "Lives in Banjarbaru, South Kalimantan",
    "Tinggal di Banjarmasin, Kalimantan Selatan",
    "Uses OTP via authenticator app for every account",
    "Logs in with OTP via SMS since 2021",
    "Verification code by email since 2019",
    "Phone numbers 0812 3456 7890 1234 5678",
    "Fixed the bug in commit 3b5df7f8c2a1e4d6b9f0a7c3e5d1b8f2a4c6e9d0",
    "Order id 1234 5678 9012 3456 from the shop", // fails Luhn
    "Worked there from 2018 2019 2020 2021", // not card-shaped
    "Reads https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit",
    "Token-based auth is the team's default",
    "Bought tickets for the 2026 conference using the company card",
  ])("passes %s", (text) => {
    expect(sensitiveKinds(text)).toEqual([])
  })
})

describe("checksums", () => {
  it("validates Luhn, IBAN and NIK structure", () => {
    expect(luhn("4111111111111111")).toBe(true)
    expect(luhn("4111111111111112")).toBe(false)
    expect(ibanValid("GB82 WEST 1234 5698 7654 32")).toBe(true)
    expect(ibanValid("GB82 WEST 1234 5698 7654 33")).toBe(false)
    expect(nikShaped("3171011508900001")).toBe(true)
    expect(nikShaped("3171019908900001")).toBe(false) // day 99
    expect(nikShaped("0012345678901000")).toBe(false) // no province 00
  })
})

describe("maskSecrets", () => {
  it("masks keys, tokens and card numbers in place", () => {
    const text = `Hi team,\nuse ${RCL_KEY} for staging and ${GH_TOKEN} for CI.\nCard on file: 4111 1111 1111 1111.\nThanks, Zaki`
    const r = maskSecrets(text)
    expect(r.masked).toBe(3)
    expect(r.text).toBe(
      "Hi team,\nuse [redacted:secret] for staging and [redacted:secret] for CI.\nCard on file: [redacted:card].\nThanks, Zaki",
    )
  })

  it("masks only the value after a label", () => {
    expect(maskSecrets("token = a1B2c3D4e5F6g7H8i9J0k1L2").text).toBe("token = [redacted:secret]")
  })

  it("leaves ordinary text alone", () => {
    const text = [
      "Halo Pak Budi, jadwal interview hari Senin 2026-11-16 jam 10.00 WIB.",
      "Gaji yang ditawarkan Rp 25.000.000, hubungi 0812-3456-7890 atau hr@maju.co.id.",
      "NIK 3171011508900001 dan paspor C1234567 terlampir.",
      "See https://example.com/jobs/senior-engineer?ref=AbC123xYz789QwE456rTy0UiOp for details.",
      "Commit 3b5df7f8c2a1e4d6b9f0a7c3e5d1b8f2a4c6e9d0 fixed it.",
    ].join("\n")
    expect(maskSecrets(text)).toEqual({ text, masked: 0 })
  })

  it("is idempotent", () => {
    const once = maskSecrets(`key ${RCL_KEY}`).text
    expect(maskSecrets(once)).toEqual({ text: once, masked: 0 })
  })
})
