// Feeds keystrokes to the real TS readPassword on a fake terminal (readline
// in terminal mode) and records the password or the error for each input.
//
// Run from apps/app: npx tsx ../server/testdata/gen/password-goldens.ts
import fs from "node:fs"
import path from "node:path"
import { PassThrough } from "node:stream"

const { readPassword } = await import("../../../app/src/server/password-prompt.ts")

const ESC = "\x1b"
const inputs = [
  "correct horse battery\r",
  "plain\n",
  "  padded  \r",
  "abc\x7fd\r",
  "abc\bd\r",
  "wrong\x15right\r",
  "keep\x01X\r",
  "ab\x02\x02X\r",
  "abc\x02\x02\x06Y\r",
  "abcd\x01\x0b\r",
  "abc\x01\x05Z\r",
  "one two\x17\r",
  "one two  \x17\r",
  "one two.,\x17\r",
  "a-b\x17\r",
  "\x7f\x7fok\r",
  "abc\x02\x04\r",
  "abc\x04\r",
  `abc${ESC}[D${ESC}[DX\r`,
  `abc${ESC}[D${ESC}[CX\r`,
  `abc${ESC}[HX\r`,
  `abc${ESC}[H${ESC}[FX\r`,
  `abc${ESC}[1~X\r`,
  `abc${ESC}[4~${ESC}[7~${ESC}[8~X\r`,
  `abc${ESC}OD${ESC}OHX\r`,
  `abcd${ESC}[D${ESC}[D${ESC}[3~\r`,
  `abc${ESC}[A${ESC}[BX\r`,
  `one two${ESC}[1;5DX\r`,
  `one two${ESC}[1;5D${ESC}[1;5CX\r`,
  `one two${ESC}b${ESC}bX\r`,
  `one two${ESC}b${ESC}d\r`,
  `one two${ESC}\x7f\r`,
  `one two${ESC}b${ESC}fX\r`,
  `ab${ESC}[15~${ESC}OPcd\r`,
  `ab${ESC}xcd\r`,
  "a\tb\r",
  "a\x07b\x0cc\x0ed\x10e\r",
  "é😀x\x7f\x7f\r",
  "😀ab\x02\x02\x02\x7fZ\r",
  "line\r\nnext\r",
  "first\rsecond\r",
  "abc\x15\x19\r",
  "abc\x0b\x01\x0b\x19\r",
  "pass\x03",
  "\x03",
  "abc\x02\x14\r",
  "ctrl d at end\x04\r",
  "one two\x17\x19\x19\r",
  "one two\x17\x15\x19\r",
  "one two\x17\x15\x19\x1by\r",
  "one two\x17\x15\x19\x1by\x1by\r",
  `one two${ESC}b${ESC}d${ESC}[H\x19\r`,
  `one two${ESC}\x7f\x01\x19\r`,
  `one two${ESC}[1;5D${ESC}[3;5~\x01\x19\r`,
  `a.b c${ESC}[1;5D${ESC}[1;5DX\r`,
  `a.b c${ESC}b${ESC}bX\r`,
  `a.b c\x01${ESC}d\r`,
  `a.b c\x01${ESC}f${ESC}fX\r`,
  `..ab  cd${ESC}[H${ESC}d\r`,
  `ab${ESC}`,
  `ab${ESC}${ESC}cd\r`,
  "\x04",
  "ab\x01\x04\x04\r",
  "x\x1d\x1c\x1e\x1fy\r",
  "\x00a\r",
  `ab${ESC}[200~pasted${ESC}[201~cd\r`,
  "abc\x01\x0b\x0b\x19\r",
  "ab\x15cd\x15\x19\x1by\r",
  "ab\x15cd\x15\x19\x1by\x1by\r",
  "ab\x15cd\x15\x19X\x1by\r",
  // Every control character between two letters, except Ctrl+Z (readline
  // suspends the process with SIGTSTP) and the line enders.
  ...Array.from({ length: 32 }, (_, c) => c)
    .filter((c) => ![0x0a, 0x0d, 0x1a, 0x03, 0x04].includes(c))
    .map((c) => `a${String.fromCharCode(c)}b\r`),
  "a\x7fb\r",
  "a\x80\x9bb\r",
]

const results: { input: string; password?: string; error?: string }[] = []
for (const input of inputs) {
  const stdin = Object.assign(new PassThrough(), { isTTY: true, setRawMode: () => stdin })
  const out = Object.assign(new PassThrough(), { isTTY: true, columns: 80 })
  out.resume()
  const pw = readPassword(stdin, out)
  // One write per keystroke group, the way a terminal delivers them.
  stdin.write(input)
  const settle = (ms: number) =>
    Promise.race([
      pw.then((password) => ({ password }), (err: Error) => ({ error: err.message })),
      new Promise<null>((r) => setTimeout(() => r(null), ms)),
    ])
  let r = await settle(300)
  // Still waiting for Enter: what happens when the input then ends.
  if (!r) {
    stdin.end()
    r = { ...((await settle(300)) ?? { error: "<still pending>" }), pendingUntilEof: true } as never
  }
  results.push({ input, ...r })
}

const file = path.resolve(import.meta.dirname, "../password.json")
fs.writeFileSync(file, JSON.stringify(results, null, 1) + "\n")
console.log(`wrote ${file}: ${results.length} inputs`)
