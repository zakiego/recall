// The e5 model itself, loaded on first use.
import type { FeatureExtractionPipeline } from "@huggingface/transformers"

export const EMBED_MODEL = "Xenova/multilingual-e5-small"
export const EMBED_DIMS = 384
// e5 truncates at 512 tokens. Chunks are ~800 "tokens" by our word-based
// count, so long chunks lose their tail in the vector. FTS still sees it all.
// A batch is padded to its longest text, and attention memory grows with
// batch x tokens squared: at 512 tokens one attention tensor is about 200 MB
// for 16 texts and 50 MB for 4.
const MAX_BATCH = 4

export type EmbedKind = "query" | "passage"

async function load(modelsDir: string): Promise<FeatureExtractionPipeline> {
  const { env, pipeline } = await import("@huggingface/transformers")
  env.cacheDir = modelsDir
  env.allowLocalModels = false
  // The pipeline() overloads produce a union too complex for tsc to narrow.
  const extractor = (await pipeline("feature-extraction", EMBED_MODEL, {
    dtype: "q8",
    // onnxruntime's arena keeps the largest run's buffers for the life of the
    // process. Without it they go back to the OS when a run ends.
    session_options: { enableCpuMemArena: false, enableMemPattern: false },
  })) as unknown as FeatureExtractionPipeline
  return extractor
}

// Loads the model on first use. Returns L2-normalized vectors, one per input.
export function createEmbedder(modelsDir: string) {
  let loading: Promise<FeatureExtractionPipeline> | undefined

  function extractor() {
    loading ??= load(modelsDir).catch((err) => {
      loading = undefined
      throw err
    })
    return loading
  }

  return async function embed(texts: string[], kind: EmbedKind): Promise<Float32Array[]> {
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
}
