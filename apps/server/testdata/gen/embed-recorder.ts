// An embedding child for golden generators: answers embed.ts's requests the
// way embed-child.ts does and appends every request with its vectors (base64
// float32 LE) to the JSONL file EMBED_LOG names. Started by embed.ts when
// RECALL_EMBED=child and RECALL_EMBED_CHILD_PATH point here.
import fs from "node:fs"
import { createEmbedder, type EmbedKind } from "../../../app/src/server/embed-model.ts"

type Req = { id: number; texts: string[]; kind: EmbedKind }

const send = process.send!.bind(process)
const log = process.env.EMBED_LOG!
const embed = createEmbedder(process.env.MODELS_DIR ?? "data/models")
let queue: Promise<unknown> = Promise.resolve()

process.on("message", (msg: Req) => {
  queue = queue.then(async () => {
    try {
      const vectors = await embed(msg.texts, msg.kind)
      const b64 = vectors.map((v) => Buffer.from(v.buffer, v.byteOffset, v.byteLength).toString("base64"))
      fs.appendFileSync(log, JSON.stringify({ kind: msg.kind, texts: msg.texts, vectors: b64 }) + "\n")
      send({ id: msg.id, vectors })
    } catch (err) {
      send({ id: msg.id, error: err instanceof Error ? (err.stack ?? err.message) : String(err) })
    }
  })
})
process.on("disconnect", () => process.exit(0))
