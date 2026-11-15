// Package embed runs the e5 embedding model (port of embed-model.ts, embed.ts
// and embed-child.ts).
package embed

// ModelID is the Hub id of the model, also its folder under the models dir.
const ModelID = "Xenova/multilingual-e5-small"

// Dims is the vector length.
const Dims = 384

// MaxBatch is how many texts go through the model at once. A batch is padded
// to its longest text, and attention memory grows with batch x tokens squared.
const MaxBatch = 4

// Kind is the e5 prefix: "query" for search input, "passage" for stored text.
type Kind string

const (
	Query   Kind = "query"
	Passage Kind = "passage"
)

// FedText is the string the tokenizer sees: `${kind}: ${collapsed text}`.
func FedText(text string, kind Kind) string {
	return string(kind) + ": " + CollapseSpace(text)
}
