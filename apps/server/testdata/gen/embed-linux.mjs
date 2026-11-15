// Runs the TS embedder (embed-model.ts, copied next to this file with
// tokenizer-compact.ts) inside a Linux container on the texts of
// testdata/embed.json and writes the vectors it produces there, so the Go
// build's vectors on Linux can be compared with the TS build's on the same
// platform (docs/GO-PORT.md, M0). See testdata/gen/embed-linux.sh.
//
//   node embed-linux.mjs <embed.json> <modelsDir> <out.json>
import fs from "node:fs"
import { createEmbedder } from "./embed-model.ts"

globalThis.fetch = async (input) => {
  throw new Error(`network access refused: ${String(input)}`)
}

const [goldenPath, modelsDir, outPath] = process.argv.slice(2)
const golden = JSON.parse(fs.readFileSync(goldenPath, "utf8"))
const embed = createEmbedder(modelsDir)
const out = { platform: `${process.platform}-${process.arch}`, cases: [] }
for (const kind of ["query", "passage"]) {
  const cases = golden.cases.filter((c) => c.kind === kind)
  const vectors = await embed(
    cases.map((c) => c.text),
    kind,
  )
  cases.forEach((c, i) => {
    const v = vectors[i]
    out.cases.push({ kind, text: c.text, vector: Buffer.from(v.buffer, v.byteOffset, v.byteLength).toString("base64") })
  })
}
fs.writeFileSync(outPath, JSON.stringify(out, null, 1) + "\n")
console.log(`wrote ${out.cases.length} vectors for ${out.platform}`)
