// Package graph is graph.ts: documents and their memories, with version
// chains, shaped for @supermemory/memory-graph.
package graph

import (
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/model"
)

// MaxDocuments is GRAPH_MAX_DOCUMENTS: only the newest documents are drawn.
const MaxDocuments = 150

// ISO is new Date(ms).toISOString().
func ISO(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// Memory is one memory node.
type Memory struct {
	ID                string  `json:"id"`
	Memory            string  `json:"memory"`
	Content           string  `json:"content"`
	IsStatic          bool    `json:"isStatic"`
	SpaceID           string  `json:"spaceId"`
	IsLatest          bool    `json:"isLatest"`
	IsForgotten       bool    `json:"isForgotten"`
	ForgetAfter       *string `json:"forgetAfter"`
	ForgetReason      *string `json:"forgetReason"`
	Version           int     `json:"version"`
	ParentMemoryID    *string `json:"parentMemoryId"`
	RootMemoryID      string  `json:"rootMemoryId"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
	Relation          *string `json:"relation"`
	UpdatesMemoryID   *string `json:"updatesMemoryId"`
	NextVersionID     *string `json:"nextVersionId"`
	SpaceContainerTag string  `json:"spaceContainerTag"`
}

// Document is one document node.
type Document struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Summary      *string  `json:"summary"`
	DocumentType string   `json:"documentType"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	Memories     []Memory `json:"memories"`
}

// Graph is buildGraph's result.
type Graph struct {
	Documents      []Document `json:"documents"`
	ShownDocuments int        `json:"shownDocuments"`
	TotalDocuments int64      `json:"totalDocuments"`
	Capped         bool       `json:"capped"`
	// Facts (current and older versions) from documents outside the window.
	HiddenFacts int `json:"hiddenFacts"`
}

type doc struct {
	id         string
	title, url *string
	sourceType string
	createdAt  int64
}

// Build is buildGraph: the newest MaxDocuments documents with their own
// facts. Facts with no source document hang off one synthetic node; facts
// from older documents are left out and counted in HiddenFacts.
func Build(q db.Q, workspaceID, container string) (Graph, error) {
	var g Graph
	rows, err := q.Query(`select "id", "title", "url", "source_type", "created_at" from "documents" where ("documents"."workspace_id" = ? and "documents"."container" = ?) order by "documents"."created_at" desc, "documents"."id" desc limit ?`,
		workspaceID, container, MaxDocuments)
	if err != nil {
		return g, err
	}
	var docs []doc
	for rows.Next() {
		var d doc
		var title, url sql.NullString
		if err := rows.Scan(&d.id, &title, &url, &d.sourceType, &d.createdAt); err != nil {
			rows.Close()
			return g, err
		}
		d.title, d.url = model.Str(title), model.Str(url)
		docs = append(docs, d)
	}
	rows.Close()
	if err := q.QueryRow(`SELECT count(*) AS n FROM documents WHERE workspace_id = ? AND container = ?`, workspaceID, container).Scan(&g.TotalDocuments); err != nil {
		return g, err
	}
	where := `"memories"."source_document_id" is null`
	args := []any{workspaceID, container}
	if len(docs) > 0 {
		where = `("memories"."source_document_id" in (` + strings.TrimSuffix(strings.Repeat("?, ", len(docs)), ", ") + `) or "memories"."source_document_id" is null)`
		for _, d := range docs {
			args = append(args, d.id)
		}
	}
	shown, err := memoriesOf(q, `select `+model.MemoryCols+` from "memories" where ("memories"."workspace_id" = ? and "memories"."container" = ? and `+where+`)`, args...)
	if err != nil {
		return g, err
	}
	// Version links for the whole container, so version numbers and "is there
	// a newer version" stay right when the other end of a chain isn't drawn.
	parentOf := map[string]string{}
	nextOf := map[string]string{}
	links := 0
	lrows, err := q.Query(`select "id", "supersedes_id" from "memories" where ("memories"."workspace_id" = ? and "memories"."container" = ?)`, workspaceID, container)
	if err != nil {
		return g, err
	}
	for lrows.Next() {
		var id string
		var sup sql.NullString
		if err := lrows.Scan(&id, &sup); err != nil {
			lrows.Close()
			return g, err
		}
		links++
		if sup.Valid && sup.String != "" {
			parentOf[id] = sup.String
			nextOf[sup.String] = id
		}
	}
	lrows.Close()

	version := func(m model.Memory) (int, string) {
		n, cur := 1, m.ID
		for parent, ok := parentOf[cur]; ok && parent != "" && n < 100; parent, ok = parentOf[cur] {
			cur = parent
			n++
		}
		return n, cur
	}
	toMemory := func(m model.Memory) Memory {
		n, root := version(m)
		updated := m.CreatedAt
		if m.ValidTo != nil {
			updated = *m.ValidTo
		}
		var next *string
		if v, ok := nextOf[m.ID]; ok {
			next = &v
		}
		var relation *string
		if m.SupersedesID != nil && *m.SupersedesID != "" {
			r := "updates"
			relation = &r
		}
		return Memory{
			ID: m.ID, Memory: m.Fact, Content: m.Keywords, IsStatic: m.Kind == "static", SpaceID: container,
			IsLatest: next == nil && m.ValidTo == nil, IsForgotten: m.ValidTo != nil && next == nil,
			Version: n, ParentMemoryID: m.SupersedesID, RootMemoryID: root,
			CreatedAt: ISO(m.CreatedAt), UpdatedAt: ISO(updated), Relation: relation, UpdatesMemoryID: m.SupersedesID,
			NextVersionID: next, SpaceContainerTag: container,
		}
	}

	grouped := map[string][]model.Memory{}
	var orphans []model.Memory
	for _, m := range shown {
		if m.SourceDocumentID != nil && *m.SourceDocumentID != "" {
			grouped[*m.SourceDocumentID] = append(grouped[*m.SourceDocumentID], m)
		} else {
			orphans = append(orphans, m)
		}
	}
	g.Documents = []Document{}
	for _, d := range docs {
		title := "Untitled"
		switch {
		case d.title != nil:
			title = *d.title
		case d.url != nil:
			title = *d.url
		}
		node := Document{ID: d.id, Title: title, DocumentType: d.sourceType, CreatedAt: ISO(d.createdAt), UpdatedAt: ISO(d.createdAt), Memories: []Memory{}}
		for _, m := range grouped[d.id] {
			node.Memories = append(node.Memories, toMemory(m))
		}
		g.Documents = append(g.Documents, node)
	}
	if len(orphans) > 0 {
		lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
		node := Document{ID: "manual:" + container, Title: "No source", DocumentType: "text", Memories: []Memory{}}
		for _, m := range orphans {
			lo, hi = min(lo, m.CreatedAt), max(hi, m.CreatedAt)
			node.Memories = append(node.Memories, toMemory(m))
		}
		node.CreatedAt, node.UpdatedAt = ISO(lo), ISO(hi)
		g.Documents = append(g.Documents, node)
	}
	g.ShownDocuments = len(docs)
	g.Capped = g.TotalDocuments > int64(len(docs))
	g.HiddenFacts = links - len(shown)
	return g, nil
}

func memoriesOf(q db.Q, query string, args ...any) ([]model.Memory, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Memory
	for rows.Next() {
		m, err := model.ScanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
