import fs from "node:fs"
import path from "node:path"
import { beforeAll, describe, expect, it } from "vitest"
import { config } from "./config"
import { EMBED_MODEL } from "./embed"
import { compactTokenizer } from "./tokenizer-compact"

type Tok = ((text: string | string[], opts?: Record<string, unknown>) => { input_ids: { tolist: () => unknown } }) & {
  _tokenizer: { model: { trie: unknown }; tokenizer: { model: { vocab: unknown[] } } }
}

async function loadTokenizer(): Promise<Tok> {
  const { AutoTokenizer, env } = await import("@huggingface/transformers")
  env.cacheDir = config().modelsDir
  env.allowLocalModels = false
  return (await AutoTokenizer.from_pretrained(EMBED_MODEL)) as unknown as Tok
}

const ids = (tok: Tok, text: string | string[]) =>
  JSON.stringify(tok(text, { padding: true, truncation: true }).input_ids.tolist(), (_k, v) => (typeof v === "bigint" ? Number(v) : v))

// Mixed scripts, numbers, emoji, long words and odd spacing, plus real source
// files, so most of the vocab's prefix paths get exercised.
const samples = [
  "passage: Rumah dijual di Banjarbaru, 3 kamar, harga Rp 850 juta. Hubungi 0812-3456-7890.",
  "query: who signed the contract on 2026-11-02?",
  "東京で会議 ☕️ émigré naïve Ünïcödé, ไทย, العربية, русский, 한국어, हिन्दी",
  "Supercalifragilisticexpialidocious pneumonoultramicroscopicsilicovolcanoconiosis",
  "x".repeat(3000),
  "   leading and   repeated   spaces\tand\ttabs\n\nnewlines   ",
  "👨‍👩‍👧‍👦🏳️‍🌈 🇮🇩 á ​ zero-width",
  "",
  ...["pipeline.ts", "embed.ts", "search.ts", "people.ts"].map((f) => fs.readFileSync(path.join(__dirname, f), "utf8")),
]

describe("compactTokenizer", () => {
  let full: Tok
  let compact: Tok
  beforeAll(async () => {
    full = await loadTokenizer()
    compact = await loadTokenizer()
    expect(compactTokenizer(compact)).toBe(true)
  })

  it("tokenizes exactly like the full tokenizer", () => {
    for (const s of samples) expect(ids(compact, s)).toBe(ids(full, s))
    // Every line of the source files on its own too, and a padded batch.
    const lines = samples.slice(-4).flatMap((s) => s.split("\n"))
    for (const l of lines) expect(ids(compact, l)).toBe(ids(full, l))
    expect(ids(compact, samples.slice(0, 4))).toBe(ids(full, samples.slice(0, 4)))
  })

  it("drops the trie's per-node maps and the parsed vocab", () => {
    expect(compact._tokenizer.model.trie).not.toHaveProperty("root")
    expect(compact._tokenizer.tokenizer.model.vocab).toEqual([])
  })

  it("leaves anything that isn't a Unigram tokenizer alone", () => {
    expect(compactTokenizer(undefined)).toBe(false)
    expect(compactTokenizer({ _tokenizer: { model: { trie: null } } })).toBe(false)
  })
})
