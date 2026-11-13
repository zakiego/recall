import { type ChildProcess, fork } from "node:child_process"
import fs from "node:fs"
import path from "node:path"
import { config } from "./config"
import type { EmbedRequest, EmbedResponse } from "./embed-child"
import { createEmbedder, type EmbedKind } from "./embed-model"
import { memoryLine } from "./mem"

export { EMBED_DIMS, EMBED_MODEL, type EmbedKind } from "./embed-model"

// Two ways to run the model (config().embedMode):
//
// - "inline": in this process, loaded on first use and kept. Dev, tests, the CLI.
// - "child": in embed-child.ts, forked on first use and killed after
//   config().embedIdleMs without a request. Production. The model plus what
//   onnxruntime keeps after inference was most of the app's RSS, and none of
//   it went back to the OS until the process ended. The cost is ~1.5 s of
//   model loading on the first request after an idle spell.

let inline: ReturnType<typeof createEmbedder> | undefined

function inlineEmbed(texts: string[], kind: EmbedKind) {
  inline ??= createEmbedder(config().modelsDir)
  return inline(texts, kind)
}

type Pending = { resolve: (v: Float32Array[]) => void; reject: (err: Error) => void }
type Child = { proc: ChildProcess; pending: Map<number, Pending>; idle?: NodeJS.Timeout }

let child: Child | undefined
let nextId = 1
let entry: string | null | undefined

// The built entry next to index.mjs/cli.mjs in production, the source file in
// dev and tests (Node strips the types). null when neither exists.
function childEntry(): string | null {
  if (entry !== undefined) return entry
  const script = process.argv[1]
  const candidates = [
    process.env.RECALL_EMBED_CHILD_PATH,
    script ? path.join(path.dirname(script), "embed-child.mjs") : undefined,
    path.resolve("src/server/embed-child.ts"),
  ]
  entry = candidates.find((c): c is string => !!c && fs.existsSync(c)) ?? null
  if (!entry) console.error("[recall] embed-child entry not found, embedding in-process instead")
  return entry
}

function startChild(file: string): Child {
  const proc = fork(file, [], {
    // Structured clone, so Float32Arrays cross as they are instead of as JSON.
    serialization: "advanced",
    env: { ...process.env, MODELS_DIR: config().modelsDir },
    // Don't pass on this process's flags (vite/vitest loaders). NODE_OPTIONS
    // still applies through env, so the child gets the same heap cap.
    execArgv: [],
  })
  const c: Child = { proc, pending: new Map() }

  const fail = (err: Error) => {
    if (child === c) child = undefined
    clearTimeout(c.idle)
    for (const p of c.pending.values()) p.reject(err)
    c.pending.clear()
  }

  proc.on("message", (msg: EmbedResponse) => {
    const p = c.pending.get(msg.id)
    if (!p) return
    c.pending.delete(msg.id)
    if ("error" in msg) p.reject(new Error(`embedder: ${msg.error}`))
    else p.resolve(msg.vectors)
    if (c.pending.size === 0) idle(c)
  })
  proc.on("error", (err) => fail(err))
  proc.on("exit", (code, signal) => fail(new Error(`embedder exited (${signal ?? `code ${code}`})`)))
  return c
}

// Nothing in flight: let the parent exit without waiting on the child (the CLI,
// tests) and stop the child once the idle spell is over.
function idle(c: Child) {
  c.proc.unref()
  c.proc.channel?.unref()
  clearTimeout(c.idle)
  const ms = config().embedIdleMs
  c.idle = setTimeout(() => {
    if (child !== c || c.pending.size > 0) return
    child = undefined
    c.proc.kill()
    console.log(`[recall] embedder stopped after ${Math.round(ms / 1000)}s idle`)
  }, ms)
  c.idle.unref()
}

function childEmbed(file: string, texts: string[], kind: EmbedKind): Promise<Float32Array[]> {
  child ??= startChild(file)
  const c = child
  clearTimeout(c.idle)
  c.proc.ref()
  c.proc.channel?.ref()
  const id = nextId++
  return new Promise((resolve, reject) => {
    c.pending.set(id, { resolve, reject })
    const req: EmbedRequest = { id, texts, kind }
    c.proc.send(req, (err) => {
      if (!err || !c.pending.delete(id)) return
      reject(err)
      if (c.pending.size === 0) idle(c)
    })
  })
}

function useChild(): string | null {
  return config().embedMode === "child" ? childEntry() : null
}

// Returns L2-normalized vectors, one Float32Array per input.
export async function embed(texts: string[], kind: EmbedKind): Promise<Float32Array[]> {
  if (texts.length === 0) return []
  const file = useChild()
  return file ? childEmbed(file, texts, kind) : inlineEmbed(texts, kind)
}

export async function embedOne(text: string, kind: EmbedKind): Promise<Float32Array> {
  const [v] = await embed([text], kind)
  return v!
}

// The running child's pid, for tests.
export function embedderPid(): number | undefined {
  return child?.proc.pid
}

// Kills the child if one is running. Pending requests fail.
export function stopEmbedder() {
  const c = child
  if (!c) return
  child = undefined
  clearTimeout(c.idle)
  c.proc.kill()
}

// Loads the model in the background so the first search isn't slow. In child
// mode that lasts until the first idle stop. The child logs its own memory.
export function warmEmbedder() {
  // One real inference too: onnxruntime compiles kernels on the first run.
  const inChild = !!useChild()
  void embed(["warmup"], "query")
    .then(() => {
      if (!inChild) console.log(`[recall] embedder ready, memory: ${memoryLine()}`)
    })
    .catch((err) => console.error("[recall] embedder warmup failed:", err))
}
