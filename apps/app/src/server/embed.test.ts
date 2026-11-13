import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest"
import { EMBED_DIMS, embed, embedderPid, stopEmbedder } from "./embed"

const TEXTS = ["Zaki suka sepatu Adidas", "Notes from the planning call about the Q4 roadmap.", "a", "b", "c"]

function alive(pid: number) {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

async function waitFor(check: () => boolean, ms = 10_000) {
  const end = Date.now() + ms
  while (!check()) {
    if (Date.now() > end) throw new Error("timed out")
    await new Promise((r) => setTimeout(r, 50))
  }
}

describe("embed in a child process", () => {
  // A batch is padded to its longest text, which shifts the q8 numbers a little,
  // so single-text calls are compared with single-text vectors.
  let inline: Float32Array[]
  let single: Float32Array[]

  beforeAll(async () => {
    process.env.RECALL_EMBED = "inline"
    inline = await embed(TEXTS, "passage")
    single = [(await embed([TEXTS[0]!], "passage"))[0]!, (await embed([TEXTS[1]!], "passage"))[0]!]
    process.env.RECALL_EMBED = "child"
  })
  afterEach(() => {
    delete process.env.RECALL_EMBED_IDLE_MS
    stopEmbedder()
  })
  afterAll(() => {
    delete process.env.RECALL_EMBED
  })

  it("returns the same vectors as the in-process model", async () => {
    const vectors = await embed(TEXTS, "passage")
    expect(embedderPid()).toBeTypeOf("number")
    expect(vectors).toHaveLength(TEXTS.length)
    for (let i = 0; i < TEXTS.length; i++) {
      expect(vectors[i]).toBeInstanceOf(Float32Array)
      expect(vectors[i]!.length).toBe(EMBED_DIMS)
      for (let j = 0; j < EMBED_DIMS; j++) expect(vectors[i]![j]).toBeCloseTo(inline[i]![j]!, 5)
    }
  })

  it("answers concurrent calls with their own vectors", async () => {
    const [a, b] = await Promise.all([embed([TEXTS[0]!], "passage"), embed([TEXTS[1]!], "passage")])
    expect(a[0]![0]).toBeCloseTo(single[0]![0]!, 5)
    expect(b[0]![0]).toBeCloseTo(single[1]![0]!, 5)
  })

  it("stops the child after the idle spell and starts a new one on the next call", async () => {
    process.env.RECALL_EMBED_IDLE_MS = "200"
    await embed(["warmup"], "query")
    const first = embedderPid()!
    await waitFor(() => embedderPid() === undefined && !alive(first))

    const [v] = await embed([TEXTS[0]!], "passage")
    expect(v![0]).toBeCloseTo(single[0]![0]!, 5)
    expect(embedderPid()).not.toBe(first)
  })

  it("fails pending calls when the child dies, then recovers", async () => {
    const pending = embed(TEXTS, "passage")
    await waitFor(() => embedderPid() !== undefined)
    process.kill(embedderPid()!, "SIGKILL")
    await expect(pending).rejects.toThrow(/embedder exited/)

    const [v] = await embed([TEXTS[0]!], "passage")
    expect(v![0]).toBeCloseTo(single[0]![0]!, 5)
  })
})
