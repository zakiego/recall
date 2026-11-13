// The e5 tokenizer (XLM-R, a 250k-token Unigram vocab) holds about 160 MB of
// JS heap in @huggingface/transformers 4.x, measured with forced GC:
//   - a character trie with one Map per node (505k nodes): 118 MB
//   - the parsed tokenizer.json vocab, only read by the constructor: 21 MB
// The trie only answers "which vocab tokens start at this position", which a
// Set of the same tokens answers too, for about a tenth of the memory.
// compactTokenizer swaps the trie for that Set and drops the parsed vocab.
// It touches library internals, so it checks their shape first and leaves the
// tokenizer alone (returning false) when they don't match.

type TrieNode = { is_leaf: boolean; children: Map<string, TrieNode> }

type UnigramLike = {
  trie: { root?: TrieNode; common_prefix_search: (chars: string[], start?: number) => Iterable<string> } | null
}

type TokenizerLike = {
  _tokenizer?: {
    model?: UnigramLike & { constructor: { name: string } }
    tokenizer?: { model?: { vocab?: unknown } }
  }
}

// The vocab strings stored in the trie, read back out of it, so the Set holds
// exactly what the trie held.
function trieWords(root: TrieNode) {
  const words = new Set<string>()
  let maxChars = 0
  const stack: [TrieNode, string, number][] = [[root, "", 0]]
  while (stack.length) {
    const [node, prefix, depth] = stack.pop()!
    if (node.is_leaf) {
      words.add(prefix)
      if (depth > maxChars) maxChars = depth
    }
    for (const [ch, child] of node.children) stack.push([child, prefix + ch, depth + 1])
  }
  return { words, maxChars }
}

export function compactTokenizer(tokenizer: unknown): boolean {
  const t = (tokenizer as TokenizerLike | undefined)?._tokenizer
  if (!t?.model) return false
  const model = t.model
  const root = model.trie?.root
  if (model.constructor.name !== "Unigram" || !root || !(root.children instanceof Map)) return false
  const { words, maxChars } = trieWords(root)
  model.trie = {
    // Same contract as CharTrie.common_prefix_search: every stored token that is
    // a prefix of chars[start..], shortest first. `chars` holds one code point
    // per entry, and so does each trie level, so maxChars bounds the search.
    *common_prefix_search(chars: string[], start = 0) {
      let prefix = ""
      const end = Math.min(chars.length, start + maxChars)
      for (let i = start; i < end; i++) {
        prefix += chars[i]
        if (words.has(prefix)) yield prefix
      }
    },
  }
  // tokenizer.json's model.vocab: 250k [token, score] pairs, copied into the
  // model's own arrays by the Unigram constructor and never read again.
  const json = t.tokenizer?.model
  if (json && Array.isArray(json.vocab)) json.vocab = []
  return true
}
