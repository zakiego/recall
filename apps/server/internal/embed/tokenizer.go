package embed

// The e5 tokenizer, ported from what the TS build runs: @huggingface/tokenizers
// 0.2.0 under @huggingface/transformers 4.3.2 (XLMRobertaTokenizer). The spec is
// that code, not the Rust tokenizers library: its Precompiled normalizer is an
// approximation (a few character rules plus NFKC) rather than the real
// sentencepiece charsmap, and the Unigram Viterbi breaks ties its own way.
// Token ids must equal the TS build's for every text (testdata/embed.json).

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// MaxTokens is the tokenizer's model_max_length: longer inputs are cut to it,
// dropping the closing </s> like transformers.js does.
const MaxTokens = 512

type addedToken struct {
	content    string
	id         int
	lstrip     bool
	rstrip     bool
	normalized bool
}

// Tokenizer turns text into input ids. It is safe for concurrent use.
type Tokenizer struct {
	vocab      []string
	scores     []float64
	tokenToID  map[string]int
	addedOnly  map[string]bool // added tokens that are not model vocab (not in the trie)
	unkID      int
	unkScore   float64
	maxChars   int
	added      map[string]addedToken
	unnormDict []string
	normDict   []string
	bosIDs     []int // the template's tokens before the sequence
	eosIDs     []int // and after it
	maxLength  int
}

type tokenizerJSON struct {
	AddedTokens []struct {
		ID         int    `json:"id"`
		Content    string `json:"content"`
		Lstrip     bool   `json:"lstrip"`
		Rstrip     bool   `json:"rstrip"`
		Normalized *bool  `json:"normalized"`
		Special    bool   `json:"special"`
	} `json:"added_tokens"`
	Normalizer struct {
		Type        string `json:"type"`
		Normalizers []struct {
			Type    string `json:"type"`
			Pattern struct {
				Regex *string `json:"Regex"`
			} `json:"pattern"`
			Content string `json:"content"`
		} `json:"normalizers"`
	} `json:"normalizer"`
	PreTokenizer struct {
		Type          string `json:"type"`
		Replacement   string `json:"replacement"`
		PrependScheme string `json:"prepend_scheme"`
	} `json:"pre_tokenizer"`
	PostProcessor struct {
		Type   string `json:"type"`
		Single []struct {
			SpecialToken *struct {
				ID string `json:"id"`
			} `json:"SpecialToken"`
			Sequence *struct {
				ID string `json:"id"`
			} `json:"Sequence"`
		} `json:"single"`
		SpecialTokens map[string]struct {
			IDs []int `json:"ids"`
		} `json:"special_tokens"`
	} `json:"post_processor"`
	Model struct {
		Type  string       `json:"type"`
		UnkID int          `json:"unk_id"`
		Vocab []vocabEntry `json:"vocab"`
	} `json:"model"`
}

type vocabEntry struct {
	piece string
	score float64
}

func (v *vocabEntry) UnmarshalJSON(b []byte) error {
	var pair [2]json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if err := json.Unmarshal(pair[0], &v.piece); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &v.score)
}

type tokenizerConfigJSON struct {
	ModelMaxLength *float64 `json:"model_max_length"`
}

// LoadTokenizer reads tokenizer.json and tokenizer_config.json from dir.
func LoadTokenizer(dir string) (*Tokenizer, error) {
	raw, err := os.ReadFile(dir + "/tokenizer.json")
	if err != nil {
		return nil, err
	}
	var tj tokenizerJSON
	if err := json.Unmarshal(raw, &tj); err != nil {
		return nil, fmt.Errorf("tokenizer.json: %w", err)
	}
	raw = nil
	cfgRaw, err := os.ReadFile(dir + "/tokenizer_config.json")
	if err != nil {
		return nil, err
	}
	var cfg tokenizerConfigJSON
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, fmt.Errorf("tokenizer_config.json: %w", err)
	}
	return newTokenizer(&tj, &cfg)
}

func newTokenizer(tj *tokenizerJSON, cfg *tokenizerConfigJSON) (*Tokenizer, error) {
	if tj.Model.Type != "Unigram" {
		return nil, fmt.Errorf("tokenizer model %q is not Unigram", tj.Model.Type)
	}
	if tj.PreTokenizer.Type != "Metaspace" {
		return nil, fmt.Errorf("pre_tokenizer %q is not Metaspace", tj.PreTokenizer.Type)
	}
	if tj.PreTokenizer.PrependScheme != "" && tj.PreTokenizer.PrependScheme != "always" {
		return nil, fmt.Errorf("prepend_scheme %q is not supported", tj.PreTokenizer.PrependScheme)
	}
	if tj.PreTokenizer.Replacement != "" && tj.PreTokenizer.Replacement != metaspace {
		return nil, fmt.Errorf("metaspace replacement %q is not supported", tj.PreTokenizer.Replacement)
	}
	ns := tj.Normalizer.Normalizers
	if tj.Normalizer.Type != "Sequence" || len(ns) != 2 || ns[0].Type != "Precompiled" ||
		ns[1].Type != "Replace" || ns[1].Pattern.Regex == nil || *ns[1].Pattern.Regex != " {2,}" || ns[1].Content != " " {
		return nil, fmt.Errorf(`normalizer is not Sequence[Precompiled, Replace(/ {2,}/, " ")]`)
	}
	if tj.PostProcessor.Type != "TemplateProcessing" {
		return nil, fmt.Errorf("post_processor %q is not TemplateProcessing", tj.PostProcessor.Type)
	}

	n := len(tj.Model.Vocab)
	t := &Tokenizer{
		vocab:     make([]string, n),
		scores:    make([]float64, n),
		tokenToID: make(map[string]int, n),
		unkID:     tj.Model.UnkID,
		added:     map[string]addedToken{},
		maxLength: math.MaxInt,
	}
	minScore := math.Inf(1)
	for i, e := range tj.Model.Vocab {
		t.vocab[i] = e.piece
		t.scores[i] = e.score
		// new Map(vocab.map((x, i) => [x, i])): a repeated piece keeps its last id.
		t.tokenToID[e.piece] = i
		if i == 0 || e.score < minScore {
			minScore = e.score
		}
		if c := utf8.RuneCountInString(e.piece); c > t.maxChars {
			t.maxChars = c
		}
	}
	t.unkScore = minScore - 10
	t.scores[t.unkID] = t.unkScore
	// The trie holds the model's own vocab. The Tokenizer constructor then sets
	// added tokens on the same token-to-id map the lattice reads ids from.
	t.addedOnly = map[string]bool{}

	for _, a := range tj.AddedTokens {
		normalized := !a.Special
		if a.Normalized != nil {
			normalized = *a.Normalized
		}
		tok := addedToken{content: a.Content, id: a.ID, lstrip: a.Lstrip, rstrip: a.Rstrip, normalized: normalized}
		if _, inVocab := t.tokenToID[a.Content]; !inVocab {
			t.addedOnly[a.Content] = true
		}
		t.tokenToID[a.Content] = a.ID
		if a.ID < len(t.vocab) {
			t.vocab[a.ID] = a.Content
		}
		t.added[a.Content] = tok
		if normalized {
			nc := normalize(a.Content)
			t.normDict = append(t.normDict, nc)
			t.added[nc] = tok
		} else {
			t.unnormDict = append(t.unnormDict, a.Content)
		}
	}

	seenSeq := false
	for _, item := range tj.PostProcessor.Single {
		switch {
		case item.SpecialToken != nil:
			st, ok := tj.PostProcessor.SpecialTokens[item.SpecialToken.ID]
			if !ok {
				return nil, fmt.Errorf("post_processor: unknown special token %q", item.SpecialToken.ID)
			}
			if seenSeq {
				t.eosIDs = append(t.eosIDs, st.IDs...)
			} else {
				t.bosIDs = append(t.bosIDs, st.IDs...)
			}
		case item.Sequence != nil:
			seenSeq = true
		}
	}
	if cfg.ModelMaxLength != nil && *cfg.ModelMaxLength < float64(math.MaxInt32) {
		t.maxLength = int(*cfg.ModelMaxLength)
	}
	return t, nil
}

// lookup is a trie hit: the piece is model vocab, and its id (and score) come
// from the shared token-to-id map.
func (t *Tokenizer) lookup(piece string) (int, bool) {
	id, ok := t.tokenToID[piece]
	if !ok || t.addedOnly[piece] {
		return 0, false
	}
	return id, true
}

const metaspace = "▁"

// Encode returns the input ids for one text with the special tokens added and
// cut to the model's maximum length, like tokenizer(text, {padding: true,
// truncation: true}) for a batch of one.
func (t *Tokenizer) Encode(text string) []int64 {
	tokens := t.encodeText(text)
	ids := make([]int64, 0, len(tokens)+len(t.bosIDs)+len(t.eosIDs))
	for _, id := range t.bosIDs {
		ids = append(ids, int64(id))
	}
	for _, tok := range tokens {
		ids = append(ids, int64(t.tokenID(tok)))
	}
	for _, id := range t.eosIDs {
		ids = append(ids, int64(id))
	}
	if len(ids) > t.maxLength {
		ids = ids[:t.maxLength]
	}
	return ids
}

// tokenID: added_tokens_map.get(t)?.id ?? model.tokens_to_ids.get(t) ?? unk.
func (t *Tokenizer) tokenID(tok string) int {
	if a, ok := t.added[tok]; ok {
		return a.id
	}
	if id, ok := t.tokenToID[tok]; ok {
		return id
	}
	return t.unkID
}

func (t *Tokenizer) encodeText(text string) []string {
	sections := dictionarySplit(t.unnormDict, text)
	stripAround(sections, t.added)
	var out []string
	for sectionIndex, section := range sections {
		_ = sectionIndex // Metaspace's "always" scheme ignores it.
		if len(section) == 0 {
			continue
		}
		if _, ok := t.added[section]; ok {
			out = append(out, section)
			continue
		}
		section = normalize(section)
		if len(section) == 0 {
			continue
		}
		subs := dictionarySplit(t.normDict, section)
		stripAround(subs, t.added)
		for _, sub := range subs {
			if len(sub) == 0 {
				continue
			}
			if _, ok := t.added[sub]; ok {
				out = append(out, sub)
				continue
			}
			out = append(out, t.unigram(metaspacePreTokenize(sub))...)
		}
	}
	return out
}

// stripAround applies an added token's lstrip and rstrip to its neighbours.
func stripAround(sections []string, added map[string]addedToken) {
	for i, s := range sections {
		a, ok := added[s]
		if !ok {
			continue
		}
		if a.lstrip && i > 0 {
			sections[i-1] = trimEndJS(sections[i-1])
		}
		if a.rstrip && i < len(sections)-1 {
			sections[i+1] = trimStartJS(sections[i+1])
		}
	}
}

// dictionarySplit is DictionarySplitter.split: at each position the longest
// dictionary word that starts there is cut out, scanning left to right.
// The JS walks UTF-16 code units; the words here are what the tokenizer
// config lists, and matching on bytes gives the same cuts for any word
// because UTF-8 and UTF-16 agree on code point boundaries.
func dictionarySplit(dict []string, text string) []string {
	if len(dict) == 0 {
		return []string{text}
	}
	var result []string
	start, i := 0, 0
	for i < len(text) {
		match := ""
		for _, w := range dict {
			if len(w) > len(match) && strings.HasPrefix(text[i:], w) {
				match = w
			}
		}
		if match != "" {
			if i > start {
				result = append(result, text[start:i])
			}
			result = append(result, match)
			i += len(match)
			start = i
		} else {
			_, size := utf8.DecodeRuneInString(text[i:])
			i += size
		}
	}
	if start < len(text) {
		result = append(result, text[start:])
	}
	return result
}

// normalize is the tokenizer's normalizer sequence: Precompiled, then runs of
// two or more spaces become one (Replace / {2,}/gu).
func normalize(text string) string {
	text = precompiledNormalize(text)
	if !strings.Contains(text, "  ") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' && i > 0 && text[i-1] == ' ' {
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// precompiledNormalize is the Precompiled normalizer of @huggingface/tokenizers.
func precompiledNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r >= 0x01 && r <= 0x08, r == 0x0B, r >= 0x0E && r <= 0x1F, r == 0x7F, r == 0x8F, r == 0x9F:
			// dropped
		case r == 0x09, r == 0x0A, r == 0x0C, r == 0x0D, r == 0xA0, r == 0x1680,
			r >= 0x2000 && r <= 0x200F, r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F,
			r == 0x2581, r == 0x3000, r == 0xFEFF, r == 0xFFFD:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	text = b.String()
	// U+FF5E FULLWIDTH TILDE is kept: the parts around it are normalized alone.
	if strings.ContainsRune(text, '～') {
		return strx.NFKCJoin(strings.Split(text, "～"), "～")
	}
	return strx.NFKC(text)
}

// metaspacePreTokenize: spaces become U+2581 and one is put in front unless
// the text already starts with it (prepend_scheme "always").
func metaspacePreTokenize(text string) string {
	s := strings.ReplaceAll(text, " ", metaspace)
	if !strings.HasPrefix(s, metaspace) {
		s = metaspace + s
	}
	return s
}

type latticeNode struct {
	tokenID   int
	pos       int
	length    int
	score     float64
	prev      int // index into nodes, -1 for none
	backtrace float64
}

// unigram is Unigram.tokenize plus fuse_unk: the Viterbi path's pieces, with
// runs of unknown pieces joined into one.
func (t *Tokenizer) unigram(text string) []string {
	// chars: one entry per code point, as byte offsets into text.
	offsets := make([]int, 0, len(text)+1)
	for i := range text {
		offsets = append(offsets, i)
	}
	n := len(offsets)
	offsets = append(offsets, len(text))

	nodes := make([]latticeNode, 0, n*4+2)
	begin := make([][]int, n+1)
	end := make([][]int, n+1)
	// bos and eos ids don't matter for the path.
	nodes = append(nodes, latticeNode{pos: 0, prev: -1})
	nodes = append(nodes, latticeNode{pos: n, prev: -1})
	begin[n] = append(begin[n], 1)
	end[0] = append(end[0], 0)
	insert := func(pos, length int, score float64, id int) {
		idx := len(nodes)
		nodes = append(nodes, latticeNode{tokenID: id, pos: pos, length: length, score: score, prev: -1})
		begin[pos] = append(begin[pos], idx)
		end[pos+length] = append(end[pos+length], idx)
	}

	for beginPos := 0; beginPos < n; beginPos++ {
		hasSingle := false
		limit := beginPos + t.maxChars
		if limit > n {
			limit = n
		}
		for e := beginPos + 1; e <= limit; e++ {
			piece := text[offsets[beginPos]:offsets[e]]
			id, ok := t.lookup(piece)
			if !ok {
				continue
			}
			length := e - beginPos
			insert(beginPos, length, t.scores[id], id)
			if length == 1 {
				hasSingle = true
			}
		}
		if !hasSingle {
			insert(beginPos, 1, t.unkScore, t.unkID)
		}
	}

	// viterbi
	for pos := 0; pos <= n; pos++ {
		if len(begin[pos]) == 0 {
			return nil
		}
		for _, r := range begin[pos] {
			nodes[r].prev = -1
			best := -1
			bestScore := 0.0
			for _, l := range end[pos] {
				score := nodes[l].backtrace + nodes[r].score
				if best == -1 || score > bestScore {
					best = l
					bestScore = score
				}
			}
			if best == -1 {
				return nil
			}
			nodes[r].prev = best
			nodes[r].backtrace = bestScore
		}
	}
	root := begin[n][0]
	if nodes[root].prev == -1 {
		return nil
	}
	var path []int
	for node := nodes[root].prev; nodes[node].prev != -1; node = nodes[node].prev {
		path = append(path, node)
	}
	pieces := make([]string, len(path))
	for i := range path {
		nd := nodes[path[len(path)-1-i]]
		pieces[i] = text[offsets[nd.pos]:offsets[nd.pos+nd.length]]
	}
	return t.fuseUnk(pieces)
}

// fuseUnk is fuse_unk from @huggingface/tokenizers.
func (t *Tokenizer) fuseUnk(arr []string) []string {
	idOf := func(s string) int {
		if id, ok := t.tokenToID[s]; ok {
			return id
		}
		return t.unkID
	}
	var fused []string
	i := 0
	for i < len(arr) {
		fused = append(fused, arr[i])
		if idOf(arr[i]) != t.unkID {
			i++
			continue
		}
		for i++; i < len(arr) && idOf(arr[i]) == t.unkID; i++ {
			last := fused[len(fused)-1]
			if id, ok := t.tokenToID[last]; !ok || id != t.unkID {
				fused[len(fused)-1] = last + arr[i]
			}
		}
	}
	return fused
}
