package search

import (
	"bytes"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// MarshalJSON writes the hit as JSON.stringify writes the TS object: the
// fields of its kind in the TS order, and ftsRank, vecRank and similarity only
// when set.
func (h Hit) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	first := true
	field := func(k string, v any) error {
		if !first {
			b.WriteByte(',')
		}
		first = false
		kb, _ := jsonx.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := jsonx.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(vb)
		return nil
	}
	b.WriteByte('{')
	var fields []struct {
		k string
		v any
	}
	add := func(k string, v any) {
		fields = append(fields, struct {
			k string
			v any
		}{k, v})
	}
	add("type", h.Type)
	add("id", h.ID)
	add("text", h.Text)
	if h.Type == "memory" {
		add("kind", h.Kind)
		add("keywords", h.Keywords)
		add("sourceDocumentId", h.SourceDocumentID)
		add("createdAt", h.CreatedAt)
		add("eventDate", h.EventDate)
		add("eventPrecision", h.EventPrecision)
		add("dueDate", h.DueDate)
		add("duePrecision", h.DuePrecision)
	} else {
		add("idx", h.Idx)
		add("documentId", h.DocumentID)
		add("documentTitle", h.DocumentTitle)
	}
	add("score", h.Score)
	if h.FtsRank != nil {
		add("ftsRank", *h.FtsRank)
	}
	if h.VecRank != nil {
		add("vecRank", *h.VecRank)
	}
	if h.Similarity != nil {
		add("similarity", *h.Similarity)
	}
	for _, f := range fields {
		if err := field(f.k, f.v); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
