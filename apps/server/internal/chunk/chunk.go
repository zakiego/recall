// Package chunk is chunk.ts. Token counts are approximated by words: close
// enough for English and Indonesian, and it keeps the chunker free of the
// tokenizer.
package chunk

import (
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	Size    = 800
	Overlap = 100
)

// Options are ChunkOptions; nil fields take the defaults.
type Options struct {
	Size    *int
	Overlap *int
}

// words is s.split(/\s+/).filter(Boolean).
func words(s string) []string {
	return strings.FieldsFunc(s, strx.IsSpace)
}

var (
	crlf          = regexp.MustCompile(`\r\n?`)
	paraBreak     = regexp.MustCompile(`\n{2,}`)
	sentenceBreak = jsre.MustCompile(`(?<=[.!?])\s+`, "")
)

// Text splits on paragraphs first, then sentences, then words, packing pieces
// into chunks of about `size` words. Each chunk starts with the last
// `overlap` words of the previous one.
func Text(input string, opts Options) []string {
	size := Size
	if opts.Size != nil {
		size = *opts.Size
	}
	overlap := Overlap
	if opts.Overlap != nil {
		overlap = *opts.Overlap
	}
	overlap = min(overlap, floorDiv(size, 2))
	text := strx.Trim(crlf.ReplaceAllString(input, "\n"))
	if text == "" {
		return []string{}
	}
	if len(words(text)) <= size {
		return []string{text}
	}

	var pieces []string
	for _, para := range paraBreak.Split(text, -1) {
		p := strx.Trim(para)
		if p == "" {
			continue
		}
		if len(words(p)) <= size {
			pieces = append(pieces, p)
			continue
		}
		for _, sentence := range sentenceBreak.Split(p) {
			w := words(sentence)
			if len(w) <= size {
				pieces = append(pieces, sentence)
			} else {
				for i := 0; i < len(w); i += size {
					pieces = append(pieces, strings.Join(w[i:min(i+size, len(w))], " "))
				}
			}
		}
	}

	chunks := []string{}
	var current []string
	count := 0
	minOverlap := 0
	if overlap > 0 {
		minOverlap = overlap
	}
	flush := func() {
		if count == 0 {
			return
		}
		chunks = append(chunks, strings.Join(current, "\n\n"))
		all := words(strings.Join(current, " "))
		tail := all[max(len(all)-overlap, 0):]
		if overlap > 0 {
			current = []string{strings.Join(tail, " ")}
			count = len(tail)
		} else {
			current = nil
			count = 0
		}
	}
	for _, piece := range pieces {
		n := len(words(piece))
		if count+n > size && count > minOverlap {
			flush()
		}
		current = append(current, piece)
		count += n
	}
	if count > overlap || len(chunks) == 0 {
		chunks = append(chunks, strings.Join(current, "\n\n"))
	}
	return chunks
}

// floorDiv is Math.floor(a / b) for b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
