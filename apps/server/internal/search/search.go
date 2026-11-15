// Package search is search.ts: raw SQL over the FTS5 and vec0 indexes, and
// hybrid search with reciprocal rank fusion.
package search

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// RRFK is the reciprocal rank fusion constant.
const RRFK = 60

// VecParam is the vector as vec0 takes it: 1536 bytes of little-endian float32.
func VecParam(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

// --- writes ------------------------------------------------------------------

// ChunkRef is what IndexChunk needs.
type ChunkRef struct {
	ID          string
	WorkspaceID string
	Container   string
	Text        string
}

// IndexChunk adds a chunk to both indexes.
func IndexChunk(q db.Q, c ChunkRef, vector []float32) error {
	if _, err := q.Exec("INSERT INTO chunks_fts (text, chunk_id, workspace_id, container) VALUES (?, ?, ?, ?)", c.Text, c.ID, c.WorkspaceID, c.Container); err != nil {
		return err
	}
	_, err := q.Exec("INSERT INTO chunks_vec (chunk_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)", c.ID, c.WorkspaceID, c.Container, VecParam(vector))
	return err
}

// UnindexChunks removes chunks from both indexes.
func UnindexChunks(q db.Q, ids []string) error {
	for _, id := range ids {
		if _, err := q.Exec("DELETE FROM chunks_fts WHERE chunk_id = ?", id); err != nil {
			return err
		}
		if _, err := q.Exec("DELETE FROM chunks_vec WHERE chunk_id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

// MemoryRef is what IndexMemory needs.
type MemoryRef struct {
	ID          string
	WorkspaceID string
	Container   string
	Fact        string
	Keywords    string
}

// IndexMemory (re)indexes a memory in both indexes.
func IndexMemory(q db.Q, m MemoryRef, vector []float32) error {
	if err := UnindexMemory(q, m.ID); err != nil {
		return err
	}
	if _, err := q.Exec("INSERT INTO memories_fts (fact, keywords, memory_id, workspace_id, container) VALUES (?, ?, ?, ?, ?)", m.Fact, m.Keywords, m.ID, m.WorkspaceID, m.Container); err != nil {
		return err
	}
	_, err := q.Exec("INSERT INTO memories_vec (memory_id, workspace_id, container, embedding) VALUES (?, ?, ?, ?)", m.ID, m.WorkspaceID, m.Container, VecParam(vector))
	return err
}

// UnindexMemory removes a memory from both indexes.
func UnindexMemory(q db.Q, id string) error {
	if _, err := q.Exec("DELETE FROM memories_fts WHERE memory_id = ?", id); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM memories_vec WHERE memory_id = ?", id)
	return err
}

// --- reads -------------------------------------------------------------------

// FtsQuery turns free text into an FTS5 query: every word quoted (so
// punctuation and operators in user input can't break the syntax), OR-ed,
// prefix-matched. ok is false when nothing is searchable.
func FtsQuery(q string) (string, bool) {
	s := strx.NFKC(strx.Lower(q))
	// split(/[^\p{L}\p{N}]+/u)
	var parts []string
	var cur strings.Builder
	for _, r := range s {
		if strx.IsLetter(r) || strx.IsNumber(r) {
			cur.WriteRune(r)
			continue
		}
		parts = append(parts, cur.String())
		cur.Reset()
		// consecutive separators make one split, so skip until a word starts
	}
	parts = append(parts, cur.String())
	var terms []string
	for _, p := range parts {
		if strx.Len16(p) > 1 || hasNumber(p) {
			terms = append(terms, p)
		}
		if len(terms) == 32 {
			break
		}
	}
	if len(terms) == 0 {
		return "", false
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range terms {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, `"`+t+`"*`)
	}
	return strings.Join(out, " OR "), true
}

func hasNumber(s string) bool {
	for _, r := range s {
		if strx.IsNumber(r) {
			return true
		}
	}
	return false
}

// Ranked is one index hit: its id, 1-based rank and raw score (bm25 or
// distance). RawNull is a NULL score (vec0 gives NULL for a NaN distance,
// e.g. against a zero vector); JS sees null there.
type Ranked struct {
	ID      string
	Rank    int
	Raw     float64
	RawNull bool
}

// Scope: every read is scoped to one container inside one workspace.
type Scope struct {
	WorkspaceID string
	Container   string
}

func ranked(q db.Q, query string, args ...any) ([]Ranked, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ranked{}
	for rows.Next() {
		var r Ranked
		var raw sql.NullFloat64
		if err := rows.Scan(&r.ID, &raw); err != nil {
			return nil, err
		}
		r.Raw, r.RawNull = raw.Float64, !raw.Valid
		r.Rank = len(out) + 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func ftsMemories(q db.Q, s Scope, match string, limit int) ([]Ranked, error) {
	return ranked(q, `SELECT memory_id AS id, bm25(memories_fts, 1.0, 0.5) AS raw
       FROM memories_fts WHERE memories_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`, match, s.WorkspaceID, s.Container, limit)
}

func ftsChunks(q db.Q, s Scope, match string, limit int) ([]Ranked, error) {
	return ranked(q, `SELECT chunk_id AS id, bm25(chunks_fts) AS raw
       FROM chunks_fts WHERE chunks_fts MATCH ? AND workspace_id = ? AND container = ?
       ORDER BY raw LIMIT ?`, match, s.WorkspaceID, s.Container, limit)
}

// VecMemories is a KNN query over current memories in the scope.
func VecMemories(q db.Q, s Scope, v []float32, k int) ([]Ranked, error) {
	return ranked(q, `SELECT memory_id AS id, distance AS raw FROM memories_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`, VecParam(v), k, s.WorkspaceID, s.Container)
}

func vecChunks(q db.Q, s Scope, v []float32, k int) ([]Ranked, error) {
	return ranked(q, `SELECT chunk_id AS id, distance AS raw FROM chunks_vec
       WHERE embedding MATCH ? AND k = ? AND workspace_id = ? AND container = ? ORDER BY distance`, VecParam(v), k, s.WorkspaceID, s.Container)
}

// Fused is an RRF result. The pointers are nil when that list didn't have it.
// DistanceNull is a null distance (JS: 1 - null is 1).
type Fused struct {
	ID           string
	Score        float64
	FtsRank      *int
	VecRank      *int
	Distance     *float64
	DistanceNull bool
}

// RRF is reciprocal rank fusion: each list adds 1 / (k + rank). Ties keep
// first-seen order, like the stable JS sort over a Map.
func RRF(fts, vec []Ranked) []*Fused {
	byID := map[string]*Fused{}
	var order []*Fused
	for _, r := range fts {
		rank := r.Rank
		f := &Fused{ID: r.ID, Score: 1 / float64(RRFK+r.Rank), FtsRank: &rank}
		if old, ok := byID[r.ID]; ok {
			*old = *f
			continue
		}
		byID[r.ID] = f
		order = append(order, f)
	}
	for _, r := range vec {
		cur, ok := byID[r.ID]
		if !ok {
			cur = &Fused{ID: r.ID}
			byID[r.ID] = cur
			order = append(order, cur)
		}
		cur.Score += 1 / float64(RRFK+r.Rank)
		rank, raw := r.Rank, r.Raw
		cur.VecRank = &rank
		cur.Distance = &raw
		cur.DistanceNull = r.RawNull
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].Score > order[j].Score })
	return order
}

// Hit is a search result: a memory or a chunk. Its JSON is the TS object's,
// key for key; ftsRank, vecRank and similarity are left out when undefined.
type Hit struct {
	Type string
	// memory and chunk
	ID   string
	Text string
	// memory
	Kind             string
	Keywords         string
	SourceDocumentID *string
	CreatedAt        int64
	EventDate        *string
	EventPrecision   *string
	DueDate          *string
	DuePrecision     *string
	// chunk
	Idx           int64
	DocumentID    string
	DocumentTitle *string

	Score      float64
	FtsRank    *int
	VecRank    *int
	Similarity *float64
}

// Input is hybridSearch's input.
type Input struct {
	WorkspaceID string
	Container   string
	Q           string
	Vector      []float32
	// Limit 0 means unset (10).
	Limit   *int
	Include []string
}

// HybridSearch fuses FTS and KNN results per kind, fetches the rows (checking
// the scope again, so a stray index entry can't leak a row) and returns the
// best `limit` across kinds.
func HybridSearch(q db.Q, in Input) ([]Hit, error) {
	var hits []Hit
	err := db.ReadTx(q, func(tx db.Q) error {
		var err error
		hits, err = hybridSearch(tx, in)
		return err
	})
	return hits, err
}

func hybridSearch(q db.Q, in Input) ([]Hit, error) {
	limit := 10
	if in.Limit != nil {
		limit = *in.Limit
	}
	limit = min(max(limit, 1), 100)
	include := in.Include
	if len(include) == 0 {
		include = []string{"memories"}
	}
	has := func(k string) bool {
		for _, x := range include {
			if x == k {
				return true
			}
		}
		return false
	}
	pool := max(limit*3, 30)
	match, ok := FtsQuery(in.Q)
	s := Scope{WorkspaceID: in.WorkspaceID, Container: in.Container}
	var hits []Hit

	if has("memories") {
		var fts []Ranked
		var err error
		if ok {
			if fts, err = ftsMemories(q, s, match, pool); err != nil {
				return nil, err
			}
		}
		vec, err := VecMemories(q, s, in.Vector, pool)
		if err != nil {
			return nil, err
		}
		for _, f := range RRF(fts, vec) {
			h := Hit{Type: "memory"}
			var src, ed, ep, dd, dp sql.NullString
			err := q.QueryRow(`SELECT id, fact, kind, keywords, source_document_id AS sourceDocumentId, created_at AS createdAt,
              event_date AS eventDate, event_precision AS eventPrecision, due_date AS dueDate, due_precision AS duePrecision
       FROM memories WHERE id = ? AND workspace_id = ? AND container = ? AND valid_to IS NULL`, f.ID, in.WorkspaceID, in.Container).
				Scan(&h.ID, &h.Text, &h.Kind, &h.Keywords, &src, &h.CreatedAt, &ed, &ep, &dd, &dp)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			h.SourceDocumentID, h.EventDate, h.EventPrecision, h.DueDate, h.DuePrecision = ptr(src), ptr(ed), ptr(ep), ptr(dd), ptr(dp)
			fill(&h, f)
			hits = append(hits, h)
		}
	}

	if has("chunks") {
		var fts []Ranked
		var err error
		if ok {
			if fts, err = ftsChunks(q, s, match, pool); err != nil {
				return nil, err
			}
		}
		vec, err := vecChunks(q, s, in.Vector, pool)
		if err != nil {
			return nil, err
		}
		for _, f := range RRF(fts, vec) {
			h := Hit{Type: "chunk"}
			var title sql.NullString
			err := q.QueryRow(`SELECT c.id, c.text, c.idx, c.document_id AS documentId, d.title AS documentTitle
       FROM chunks c JOIN documents d ON d.id = c.document_id
       WHERE c.id = ? AND c.workspace_id = ? AND c.container = ? AND d.workspace_id = c.workspace_id`, f.ID, in.WorkspaceID, in.Container).
				Scan(&h.ID, &h.Text, &h.Idx, &h.DocumentID, &title)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			h.DocumentTitle = ptr(title)
			fill(&h, f)
			hits = append(hits, h)
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []Hit{}
	}
	return hits, nil
}

func fill(h *Hit, f *Fused) {
	h.Score = f.Score
	h.FtsRank = f.FtsRank
	h.VecRank = f.VecRank
	if f.Distance != nil {
		sim := 1 - *f.Distance
		if f.DistanceNull {
			sim = 1
		}
		h.Similarity = &sim
	}
}

func ptr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}
