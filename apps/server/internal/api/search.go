package api

import (
	"math"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/search"
)

// SearchInput is searchInput after validation.
type SearchInput struct {
	Container string
	Q         string
	Limit     *int
	Include   []string
}

// SearchHit is a hybridSearch hit; a memory hit also carries the people it
// mentions, as a last "people" key.
type SearchHit struct {
	search.Hit
	People []people.Ref
}

// MarshalJSON writes the hit's own keys, then "people" for a memory.
func (h SearchHit) MarshalJSON() ([]byte, error) {
	b, err := h.Hit.MarshalJSON()
	if err != nil || h.Type != "memory" {
		return b, err
	}
	o, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	refs := h.People
	if refs == nil {
		refs = []people.Ref{}
	}
	o.Set("people", refs)
	return jsonx.Marshal(o)
}

// SearchResult is search's result.
type SearchResult struct {
	Container string      `json:"container"`
	Q         string      `json:"q"`
	Results   []SearchHit `json:"results"`
	TimingMs  int64       `json:"timingMs"`
}

// RoundMs is Math.round of a duration in milliseconds.
func RoundMs(d time.Duration) int64 {
	return int64(math.Floor(float64(d.Microseconds())/1000 + 0.5))
}

// Search is search: the query embedded, hybridSearch, and each fact's people.
func Search(d *db.DB, workspaceID string, in SearchInput) (SearchResult, error) {
	started := time.Now()
	vector, err := embed.EmbedOne(in.Q, embed.Query)
	if err != nil {
		return SearchResult{}, err
	}
	hits, err := search.HybridSearch(d, search.Input{WorkspaceID: workspaceID, Container: in.Container, Q: in.Q, Vector: vector, Limit: in.Limit, Include: in.Include})
	if err != nil {
		return SearchResult{}, err
	}
	var memIDs []string
	for _, h := range hits {
		if h.Type == "memory" {
			memIDs = append(memIDs, h.ID)
		}
	}
	linked, err := people.ForMemories(d, workspaceID, memIDs)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Container: in.Container, Q: in.Q, Results: make([]SearchHit, len(hits))}
	for i, h := range hits {
		out.Results[i] = SearchHit{Hit: h}
		if h.Type == "memory" {
			refs, _ := linked.Get(h.ID)
			if refs == nil {
				refs = []people.Ref{}
			}
			out.Results[i].People = refs
		}
	}
	out.TimingMs = RoundMs(time.Since(started))
	return out, nil
}
