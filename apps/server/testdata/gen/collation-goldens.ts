// String.prototype.localeCompare (ICU root collation, Node's default locale)
// on ASCII: how each character compares with every other, and random
// strings of ids and timestamps. strx.LocaleCompare is checked against it.
//
// Run from apps/app: npx tsx ../server/testdata/gen/collation-goldens.ts
import fs from "node:fs"
import path from "node:path"

const chars = Array.from({ length: 128 }, (_, i) => String.fromCharCode(i))
const charTable = chars.map((a) => chars.map((b) => Math.sign(a.localeCompare(b))).join(","))

let seed = 7
const rand = (n: number) => (seed = (seed * 1103515245 + 12345) % 2 ** 31) % n
const alphabets = [
  "aAbBzZ09",
  "0123456789-:.TZ+",
  "abcABC_-",
  " !\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~aA0",
  Array.from({ length: 128 }, (_, i) => String.fromCharCode(i)).join(""),
]
const word = (alpha: string) => Array.from({ length: 1 + rand(8) }, () => alpha[rand(alpha.length)]).join("")
const pairs: [string, string, number][] = []
for (let i = 0; i < 4000; i++) {
  const alpha = alphabets[i % alphabets.length]!
  const a = word(alpha)
  // Half the pairs share a prefix, so later positions decide.
  const b = rand(2) ? a.slice(0, rand(a.length + 1)) + word(alpha) : word(alpha)
  pairs.push([a, b, Math.sign(a.localeCompare(b))])
}
const extra = ["2026-11-09T02:15:00Z", "2026-11-09T02:15:00.000Z", "2026-11-09T02:15:00+08:00", "2026-11-09T02:15:00-01:30", "t1", "T1", "t_a1", "t_B2", "tsk_AbC", "tsk_abc", "a b", "ab", "a-b", "a_b", "", "A", "a"]
for (const a of extra) for (const b of extra) pairs.push([a, b, Math.sign(a.localeCompare(b))])

const file = path.resolve(import.meta.dirname, "../collation.json")
fs.writeFileSync(file, JSON.stringify({ charTable, pairs }) + "\n")
console.log(`wrote ${file}: ${pairs.length} pairs`)
