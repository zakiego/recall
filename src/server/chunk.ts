// Token counts are approximated by words. Close enough for English and
// Indonesian, and it keeps the chunker free of the tokenizer.
export type ChunkOptions = { size?: number; overlap?: number }

const SIZE = 800
const OVERLAP = 100

function words(s: string) {
  return s.split(/\s+/).filter(Boolean)
}

// Splits on paragraphs first, then sentences, then words, packing pieces into
// chunks of about `size` words. Each chunk starts with the last `overlap` words
// of the previous one.
export function chunkText(input: string, opts: ChunkOptions = {}): string[] {
  const size = opts.size ?? SIZE
  const overlap = Math.min(opts.overlap ?? OVERLAP, Math.floor(size / 2))
  const text = input.replace(/\r\n?/g, "\n").trim()
  if (!text) return []
  if (words(text).length <= size) return [text]

  const pieces: string[] = []
  for (const para of text.split(/\n{2,}/)) {
    const p = para.trim()
    if (!p) continue
    if (words(p).length <= size) {
      pieces.push(p)
      continue
    }
    for (const sentence of p.split(/(?<=[.!?])\s+/)) {
      const w = words(sentence)
      if (w.length <= size) pieces.push(sentence)
      else for (let i = 0; i < w.length; i += size) pieces.push(w.slice(i, i + size).join(" "))
    }
  }

  const chunks: string[] = []
  let current: string[] = []
  let count = 0
  const flush = () => {
    if (count === 0) return
    chunks.push(current.join("\n\n"))
    const tail = words(current.join(" ")).slice(-overlap)
    current = overlap > 0 ? [tail.join(" ")] : []
    count = overlap > 0 ? tail.length : 0
  }
  for (const piece of pieces) {
    const n = words(piece).length
    if (count + n > size && count > (overlap > 0 ? overlap : 0)) flush()
    current.push(piece)
    count += n
  }
  if (count > overlap || chunks.length === 0) chunks.push(current.join("\n\n"))
  return chunks
}
