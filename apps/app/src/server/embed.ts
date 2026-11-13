import type { FeatureExtractionPipeline } from "@huggingface/transformers"
import { config } from "./config"
import { memoryLine } from "./mem"
import { compactTokenizer } from "./tokenizer-compact"

export const EMBED_MODEL = "Xenova/multilingual-e5-small"
export const EMBED_DIMS = 384
// e5 truncates at 512 tokens. Chunks are ~800 "tokens" by our word-based
// count, so long chunks lose their tail in the vector. FTS still sees it all.
// A batch is padded to its longest text, and attention memory grows with
// batch x tokens squared: at 512 tokens one attention tensor is about 200 MB
// for 16 texts and 50 MB for 4.
const MAX_BATCH = 4

let loading: Promise<FeatureExtractionPipeline> | undefined

async function load(): Promise<FeatureExtractionPipeline> {
  const { env, pipeline } = await import("@huggingface/transformers")
  env.cacheDir = config().modelsDir
  env.allowLocalModels = false
  // The pipeline() overloads produce a union too complex for tsc to narrow.
  const extractor = (await pipeline("feature-extraction", EMBED_MODEL, {
    dtype: "q8",
    // onnxruntime's arena keeps the largest run's buffers for the life of the
    // process. Without it they go back to the OS when a run ends.
    session_options: { enableCpuMemArena: false, enableMemPattern: false },
  })) as unknown as FeatureExtractionPipeline
  // About 120 MB of heap back (tokenizer-compact.ts).
  if (!compactTokenizer(extractor.tokenizer)) console.warn("[recall] tokenizer internals changed, kept the full-size tokenizer")
  return extractor
}

function extractor() {
  loading ??= load().catch((err) => {
    loading = undefined
    throw err
  })
  return loading
}

export type EmbedKind = "query" | "passage"

// Returns L2-normalized vectors, one Float32Array per input.
export async function embed(texts: string[], kind: EmbedKind): Promise<Float32Array[]> {
  if (texts.length === 0) return []
  const fx = await extractor()
  const out: Float32Array[] = []
  for (let i = 0; i < texts.length; i += MAX_BATCH) {
    const batch = texts.slice(i, i + MAX_BATCH).map((t) => `${kind}: ${t.replace(/\s+/g, " ").trim()}`)
    const tensor = await fx(batch, { pooling: "mean", normalize: true })
    const data = tensor.data as Float32Array
    for (let j = 0; j < batch.length; j++) {
      out.push(data.slice(j * EMBED_DIMS, (j + 1) * EMBED_DIMS))
    }
  }
  return out
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
