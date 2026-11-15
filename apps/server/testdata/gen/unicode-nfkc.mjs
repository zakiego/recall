// Exports the Unicode normalization data of the Node that runs the TS build
// (its ICU), so the Go tokenizer's NFKC equals String.prototype.normalize("NFKC")
// exactly: Go's golang.org/x/text tables are an older Unicode version and apply
// the stream-safe rule (a U+034F after 30 combining marks), which ICU does not.
//
// Writes internal/strx/nfkc.json:
//   decomp:  code point -> full compatibility decomposition (NFKD of that one
//            code point), for every code point that has one, Hangul syllables
//            excluded (they are algorithmic)
//   ccc:     code point -> canonical combining class rank. ICU doesn't expose
//            the class, so it is derived from canonical reordering: only the
//            order of classes matters to normalization, so ranks 1..n keep it.
//   compose: [first, second, composite] for every primary composite
//   checks:  code point -> NFKC of that code point, where it isn't itself,
//            plus NFKC of crafted sequences, for the Go test
//
// Run with plain node from the repo root:
//   node apps/server/testdata/gen/unicode-nfkc.mjs
import fs from "node:fs"
import path from "node:path"

const out = path.resolve(import.meta.dirname, "../../internal/strx/nfkc.json")
const MAX = 0x10ffff
const isHangulSyllable = (cp) => cp >= 0xac00 && cp <= 0xd7a3
const isSurrogate = (cp) => cp >= 0xd800 && cp <= 0xdfff
const str = (cp) => String.fromCodePoint(cp)
const cps = (s) => [...s].map((c) => c.codePointAt(0))

const decomp = {}
const composites = []
const nfkcSingles = {}
const fixedMarks = []
for (let cp = 0; cp <= MAX; cp++) {
  if (isSurrogate(cp)) continue
  const s = str(cp)
  const kd = s.normalize("NFKD")
  if (!isHangulSyllable(cp) && kd !== s) decomp[cp] = cps(kd)
  const kc = s.normalize("NFKC")
  if (kc !== s) nfkcSingles[cp] = cps(kc)
  if (/\p{M}/u.test(s) && kd === s) fixedMarks.push(cp)
  if (!isHangulSyllable(cp)) {
    const d = s.normalize("NFD")
    if (d !== s && s.normalize("NFC") === s) {
      const parts = cps(d)
      if (parts.length < 2) throw new Error(`composite U+${cp.toString(16)} has a singleton NFD`)
      const last = parts.pop()
      const first = cps(String.fromCodePoint(...parts).normalize("NFC"))
      if (first.length !== 1) throw new Error(`composite U+${cp.toString(16)}: head ${first} is not one code point`)
      composites.push([first[0], last, cp])
    }
  }
}

// Canonical combining classes, as ranks. " " composes with nothing, and NFD
// only decomposes and reorders, so NFD(" " + a + b) === " " + b + a exactly
// when ccc(a) > ccc(b) > 0.
const swaps = (a, b) => (" " + str(a) + str(b)).normalize("NFD") === " " + str(b) + str(a)
const nonzero = fixedMarks.filter((m) => [0x0301, 0x0323].some((r) => m !== r && (swaps(m, r) || swaps(r, m))) || m === 0x0301 || m === 0x0323)
nonzero.sort((a, b) => (swaps(a, b) ? 1 : swaps(b, a) ? -1 : 0))
const ccc = {}
let rank = 0
for (let i = 0; i < nonzero.length; i++) {
  if (i === 0 || swaps(nonzero[i], nonzero[i - 1])) rank++
  ccc[nonzero[i]] = rank
}
// The sort's comparator must have been a total preorder: check every
// neighbouring pair and a sample of others.
for (let i = 1; i < nonzero.length; i++) {
  if (swaps(nonzero[i - 1], nonzero[i])) throw new Error("ccc order is inconsistent")
}

const sequences = [
  "a" + "́".repeat(40),
  "a" + "̣́".repeat(20),
  "e" + "̖̗̀́".repeat(12) + "z",
  "́".repeat(35),
  "각 가 각",
  "Ạ̊",
  "ẛ̣",
  "̈́̈",
  "ﷺﷺ",
  "\u{1ccd6}\u{1ccf0}꟱",
  "क़ क़",
  "ÅÅÅ",
  "㌀㋿㏿",
  "\u{1d400}\u{1d7ce}",
  "Ｆｕｌｌ ｗｉｄｔｈ ～ ｶﾞ",
  "ཱཱཱིྀུ",
  "\u{16d67}\u{16d67}",
  "\u{11383}\u{113c2}",
]
const seqChecks = sequences.map((s) => [cps(s), cps(s.normalize("NFKC"))])
// NFKD of the same sequences (people.ts folds names with NFKD).
const seqChecksKD = sequences.map((s) => [cps(s), cps(s.normalize("NFKD"))])

fs.writeFileSync(
  out,
  JSON.stringify({
    node: process.versions.node,
    icu: process.versions.icu,
    unicode: process.versions.unicode,
    decomp,
    ccc,
    compose: composites,
    singles: nfkcSingles,
    sequences: seqChecks,
    sequencesKD: seqChecksKD,
  }) + "\n",
)
console.log(
  `unicode ${process.versions.unicode}: ${Object.keys(decomp).length} decompositions, ${nonzero.length} marks in ${rank} classes, ${composites.length} composites, ${Object.keys(nfkcSingles).length} changed singles -> ${out}`,
)

// Case mappings: toLowerCase and toUpperCase of every code point where the
// result isn't the code point itself (written to internal/strx/case.json).
// U+03A3 (capital sigma) lowercases by context; strx handles that rule.
const lower = {}
const upper = {}
for (let cp = 0; cp <= MAX; cp++) {
  if (isSurrogate(cp)) continue
  const s = str(cp)
  const l = s.toLowerCase()
  if (l !== s) lower[cp] = cps(l)
  const u = s.toUpperCase()
  if (u !== s) upper[cp] = cps(u)
}
// Final sigma (the only context rule in toLowerCase): ICU skips case-ignorable
// characters around the sigma and then asks whether the next one is cased.
// Neither property is a JS regex property, so both come from probing:
//   ignorable: "ΑXΣ" ends in ς (skipped back to the cased Α)
//              and "1XΣ" ends in σ (skipped back to the uncased 1)
//   cased:     "1XΣ" ends in ς (X itself is the cased letter before it)
const ignorable = []
const cased = []
const finalSigma = (s) => s.toLowerCase().endsWith("ς")
for (let cp = 0; cp <= MAX; cp++) {
  if (isSurrogate(cp) || cp === 0x3a3) continue
  const x = str(cp)
  const afterCased = finalSigma("Α" + x + "Σ")
  const afterDigit = finalSigma("1" + x + "Σ")
  if (afterCased && !afterDigit) ignorable.push(cp)
  else if (afterDigit) cased.push(cp)
}
const caseSequences = [
  "ΟΔΟΣ ΟΔΟΣ.",
  "Σ",
  "ΑΣ",
  "ΑΣΑ",
  "Α.Σ",
  "ΑΣ́",
  "ΆΣ",
  "İstanbul",
  "ǅungla",
  "ẞtraße",
  "ﬁ",
  "ΑΣ\u0301",
  "Α\u0301Σ",
  "Α.Σ",
  "ΑΣ.Α",
  "1Σ",
  "ΑΣ1",
  "Σ\u0301",
  "1ᵃΣ",
  "ᵃΣ",
  "ΑΣᵃ",
  "ΑΣ'Α",
  "Α'Σ",
  "ΑΣΣ",
  "ΣΣΣ ΣΑ ΑΣΑΣ",
  "ΑΣ\u{1F600}",
  "\u{10400}Σ",
].map((s) => [cps(s), cps(s.toLowerCase()), cps(s.toUpperCase())])
// \p{L}, \p{N}, \p{M} and \p{Lu} as Node's regex engine sees them, as [first, last] ranges
// (internal/strx/props.json), for the FTS query builder's split.
function ranges(re) {
  const out = []
  let start = -1
  for (let cp = 0; cp <= MAX + 1; cp++) {
    const hit = cp <= MAX && !isSurrogate(cp) && re.test(str(cp))
    if (hit && start < 0) start = cp
    if (!hit && start >= 0) {
      out.push([start, cp - 1])
      start = -1
    }
  }
  return out
}
const propsOut = path.resolve(import.meta.dirname, "../../internal/strx/props.json")
const letters = ranges(/^\p{L}$/u)
const numbers = ranges(/^\p{N}$/u)
const marks = ranges(/^\p{M}$/u)
const upperLetters = ranges(/^\p{Lu}$/u)
fs.writeFileSync(propsOut, JSON.stringify({ node: process.versions.node, unicode: process.versions.unicode, L: letters, N: numbers, M: marks, Lu: upperLetters }) + "\n")
console.log(`props: ${letters.length} letter, ${numbers.length} number, ${marks.length} mark, ${upperLetters.length} uppercase ranges -> ${propsOut}`)
const caseOut = path.resolve(import.meta.dirname, "../../internal/strx/case.json")
fs.writeFileSync(caseOut, JSON.stringify({ node: process.versions.node, unicode: process.versions.unicode, lower, upper, ignorable, cased, sequences: caseSequences }) + "\n")
console.log(`case: ${Object.keys(lower).length} lower, ${Object.keys(upper).length} upper, ${ignorable.length} case-ignorable, ${cased.length} cased -> ${caseOut}`)
