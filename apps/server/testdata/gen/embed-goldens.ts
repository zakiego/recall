// Exports the embedding goldens from the real TS embedder (embed-model.ts):
// for every corpus text and both kinds, the exact string fed to the tokenizer,
// the token ids and the vector.
//
// Run from apps/app (the model must already be in data/models, nothing is
// downloaded):
//   MODELS_DIR=data/models npx tsx ../server/testdata/gen/embed-goldens.ts
import fs from "node:fs"
import path from "node:path"
import { env, pipeline } from "../../../app/node_modules/@huggingface/transformers/dist/transformers.node.mjs"
import { createEmbedder, EMBED_MODEL, type EmbedKind } from "../../../app/src/server/embed-model.ts"
import { maskSecrets } from "../../../app/src/server/sensitive.ts"
import { compactTokenizer } from "../../../app/src/server/tokenizer-compact.ts"
import { corpus } from "./corpus.ts"

const modelsDir = path.resolve(process.env.MODELS_DIR ?? "data/models")
const out = path.resolve(import.meta.dirname, "../embed.json")

// Never reach the Hub: the files are in the cache already, so any request is a bug.
globalThis.fetch = async (input: unknown) => {
  throw new Error(`network access refused: ${String(input)}`)
}

async function main() {
  const texts = await corpus(maskSecrets)

  // The same load as embed-model.ts, kept to read the tokenizer's ids.
  env.cacheDir = modelsDir
  env.allowLocalModels = false
  const fx = (await pipeline("feature-extraction", EMBED_MODEL, {
    dtype: "q8",
    session_options: { enableCpuMemArena: false, enableMemPattern: false },
  })) as any
  if (!compactTokenizer(fx.tokenizer)) throw new Error("compactTokenizer failed")

  const embed = createEmbedder(modelsDir)
  const cases: { kind: EmbedKind; text: string; fed: string; ids: number[]; vector: string }[] = []
  for (const kind of ["query", "passage"] as EmbedKind[]) {
    const vectors = await embed(texts, kind)
    texts.forEach((text, i) => {
      // The same string embed-model.ts builds.
      const fed = `${kind}: ${text.replace(/\s+/g, " ").trim()}`
      const enc = fx.tokenizer([fed], { padding: true, truncation: true })
      const ids = Array.from(enc.input_ids.data as BigInt64Array, (x) => Number(x))
      const v = vectors[i]!
      cases.push({ kind, text, fed, ids, vector: Buffer.from(v.buffer, v.byteOffset, v.byteLength).toString("base64") })
    })
  }
  fs.writeFileSync(out, JSON.stringify({ model: EMBED_MODEL, dims: 384, maxBatch: 4, texts, cases }, null, 1) + "\n")
  console.log(`wrote ${cases.length} cases (${texts.length} texts) to ${out}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
