import { config } from "./config"
import { createEmbedder, type EmbedKind } from "./embed-model"
import { memoryLine } from "./mem"

export { EMBED_DIMS, EMBED_MODEL, type EmbedKind } from "./embed-model"

let inline: ReturnType<typeof createEmbedder> | undefined

// Returns L2-normalized vectors, one Float32Array per input.
export async function embed(texts: string[], kind: EmbedKind): Promise<Float32Array[]> {
  if (texts.length === 0) return []
  inline ??= createEmbedder(config().modelsDir)
  return inline(texts, kind)
}

export async function embedOne(text: string, kind: EmbedKind): Promise<Float32Array> {
  const [v] = await embed([text], kind)
  return v!
}

// Loads the model in the background so the first search isn't slow.
export function warmEmbedder() {
  // One real inference too: onnxruntime compiles kernels on the first run.
  void embed(["warmup"], "query")
    .then(() => console.log(`[recall] embedder ready, memory: ${memoryLine()}`))
    .catch((err) => console.error("[recall] embedder warmup failed:", err))
}
