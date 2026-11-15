// Package model holds the row types of Recall's own tables, with the column
// lists and scanners the queries share. Fields are in schema.ts order with its
// JSON names, so a row serializes like the drizzle row the TS build returned.
package model

import (
	"database/sql"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// Memory is a row of memories.
type Memory struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspaceId"`
	Container        string  `json:"container"`
	Fact             string  `json:"fact"`
	Keywords         string  `json:"keywords"`
	Kind             string  `json:"kind"`
	SourceDocumentID *string `json:"sourceDocumentId"`
	SupersedesID     *string `json:"supersedesId"`
	ValidFrom        int64   `json:"validFrom"`
	ValidTo          *int64  `json:"validTo"`
	CreatedAt        int64   `json:"createdAt"`
	ReviewNote       *string `json:"reviewNote"`
	ConflictsWithID  *string `json:"conflictsWithId"`
	EventDate        *string `json:"eventDate"`
	EventPrecision   *string `json:"eventPrecision"`
	DueDate          *string `json:"dueDate"`
	DuePrecision     *string `json:"duePrecision"`
}

// MemoryCols is the column list ScanMemory reads.
const MemoryCols = "id, workspace_id, container, fact, keywords, kind, source_document_id, supersedes_id, valid_from, valid_to, created_at, review_note, conflicts_with_id, event_date, event_precision, due_date, due_precision"

// PrefixedMemoryCols is MemoryCols with a table alias.
func PrefixedMemoryCols(alias string) string {
	cols := []string{"id", "workspace_id", "container", "fact", "keywords", "kind", "source_document_id", "supersedes_id", "valid_from", "valid_to", "created_at", "review_note", "conflicts_with_id", "event_date", "event_precision", "due_date", "due_precision"}
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

// Scanner is *sql.Row or *sql.Rows.
type Scanner interface{ Scan(...any) error }

// ScanMemory reads a row selected with MemoryCols.
func ScanMemory(s Scanner) (Memory, error) {
	var m Memory
	var src, sup, rn, cw, ed, ep, dd, dp sql.NullString
	var vt sql.NullInt64
	err := s.Scan(&m.ID, &m.WorkspaceID, &m.Container, &m.Fact, &m.Keywords, &m.Kind, &src, &sup, &m.ValidFrom, &vt, &m.CreatedAt, &rn, &cw, &ed, &ep, &dd, &dp)
	if err != nil {
		return m, err
	}
	m.SourceDocumentID, m.SupersedesID, m.ReviewNote, m.ConflictsWithID = Str(src), Str(sup), Str(rn), Str(cw)
	m.EventDate, m.EventPrecision, m.DueDate, m.DuePrecision = Str(ed), Str(ep), Str(dd), Str(dp)
	m.ValidTo = Int(vt)
	return m, nil
}

// Document is a row of documents.
type Document struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspaceId"`
	Container   string        `json:"container"`
	SourceType  string        `json:"sourceType"`
	Title       *string       `json:"title"`
	URL         *string       `json:"url"`
	Content     *string       `json:"content"`
	Status      string        `json:"status"`
	Error       *string       `json:"error"`
	Metadata    *jsonx.Object `json:"metadata"`
	SourceID    *string       `json:"sourceId"`
	CreatedAt   int64         `json:"createdAt"`
}

// DocumentCols is the column list ScanDocument reads.
const DocumentCols = "id, workspace_id, container, source_type, title, url, content, status, error, metadata, source_id, created_at"

// ScanDocument reads a row selected with DocumentCols.
func ScanDocument(s Scanner) (Document, error) {
	var d Document
	var title, url, content, errs, meta, sid sql.NullString
	if err := s.Scan(&d.ID, &d.WorkspaceID, &d.Container, &d.SourceType, &title, &url, &content, &d.Status, &errs, &meta, &sid, &d.CreatedAt); err != nil {
		return d, err
	}
	d.Title, d.URL, d.Content, d.Error, d.SourceID = Str(title), Str(url), Str(content), Str(errs), Str(sid)
	d.Metadata = JSONObject(meta)
	return d, nil
}

// JSONObject reads a JSON text column the way drizzle's json mode does:
// NULL is nil; text that isn't an object is kept as parsed when possible.
func JSONObject(s sql.NullString) *jsonx.Object {
	if !s.Valid {
		return nil
	}
	o, err := jsonx.ParseObject([]byte(s.String))
	if err != nil {
		return nil
	}
	return o
}

// Str is a nullable text column.
func Str(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// Int is a nullable integer column.
func Int(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
