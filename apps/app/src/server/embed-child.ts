// The embedding child process, forked by embed.ts. It holds the e5 model and
// answers embed requests over IPC until the parent stops it after an idle
// spell. Exiting is the point: onnxruntime and V8 keep most of what an
// inference run allocated, and only the end of the process hands it back.
//
// Built to .output/server/embed-child.mjs (vite.cli.config.ts). In dev and
// tests embed.ts runs this file from source, so local imports need ".ts".
import { createEmbedder, type EmbedKind } from "./embed-model.ts"
import { memoryLine } from "./mem.ts"

export type EmbedRequest = { id: number; texts: string[]; kind: EmbedKind }
export type EmbedResponse = { id: number; vectors: Float32Array[] } | { id: number; error: string }

const send = process.send?.bind(process)
if (!send) {
  console.error("embed-child must be started by embed.ts (fork with an IPC channel)")
  process.exit(1)
}

const embed = createEmbedder(process.env.MODELS_DIR ?? "data/models")
let loaded = false
// One inference at a time. Two at once would double the peak memory.
let queue: Promise<unknown> = Promise.resolve()

process.on("message", (msg: EmbedRequest) => {
  queue = queue.then(async () => {
    let reply: EmbedResponse
    try {
      reply = { id: msg.id, vectors: await embed(msg.texts, msg.kind) }
      if (!loaded) {
        loaded = true
        console.log(`[recall] embedder ready (pid ${process.pid}), memory: ${memoryLine()}`)
      }
    } catch (err) {
      reply = { id: msg.id, error: err instanceof Error ? (err.stack ?? err.message) : String(err) }
    }
    send(reply)
  })
})

// The parent went away (it exited or called disconnect()). Nothing to serve.
process.on("disconnect", () => process.exit(0))
