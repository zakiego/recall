import { describe, expect, it } from "vitest"
import { chunkText } from "./chunk"

const words = (n: number, w = "word") => Array.from({ length: n }, (_, i) => `${w}${i}`).join(" ")

describe("chunkText", () => {
  it("returns nothing for blank input", () => {
    expect(chunkText("  \n ")).toEqual([])
  })

  it("keeps short text as one chunk", () => {
    expect(chunkText("hello there\n\nsecond paragraph")).toEqual(["hello there\n\nsecond paragraph"])
  })

  it("splits long text with overlap", () => {
    const text = [words(500, "a"), words(500, "b"), words(500, "c")].join("\n\n")
    const chunks = chunkText(text, { size: 800, overlap: 100 })
    expect(chunks.length).toBeGreaterThan(1)
    for (const c of chunks) expect(c.split(/\s+/).length).toBeLessThanOrEqual(900)
    // The second chunk starts with the tail of the first.
    const firstTail = chunks[0]!.split(/\s+/).slice(-100).join(" ")
    expect(chunks[1]!.startsWith(firstTail)).toBe(true)
    // Nothing is lost.
    const all = new Set(chunks.join(" ").split(/\s+/))
    for (const w of text.split(/\s+/)) expect(all.has(w)).toBe(true)
  })

  it("splits one giant paragraph without sentence breaks", () => {
    const chunks = chunkText(words(2000), { size: 800, overlap: 100 })
    expect(chunks.length).toBe(3)
  })
})
