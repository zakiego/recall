// Package memories is memories.ts: documents and memories, their versions,
// conflicts, upcoming dates, containers and profiles.
package memories

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/sensitive"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// MaxContainerLen bounds a container name.
const MaxContainerLen = 128

// Text is memoryText: what gets embedded for a memory.
func Text(fact, keywords string) string {
	if keywords != "" {
		return fact + ". " + keywords
	}
	return fact
}

// --- documents ------------------------------------------------------------------

// File is an uploaded file of a new document.
type File struct {
	Name  string
	Mime  string
	Bytes []byte
}

// NewDocument is createDocument's input. Nil pointers are undefined.
type NewDocument struct {
	WorkspaceID string
	Container   string
	SourceType  string
	Content     *string
	URL         *string
	Title       *string
	Metadata    *jsonx.Object
	File        *File
	SourceID    *string
}

// UploadsDir is where uploaded files wait for their ingest.
func UploadsDir() string { return filepath.Join(config.Load().DataDir, "uploads") }

// CreateDocument creates the document and queues its ingest. With a sourceId
// that already exists in the container it returns that document instead and
// queues nothing (duplicate true). Lookup and insert run in one transaction.
func CreateDocument(q db.Q, in NewDocument, now int64) (model.Document, bool, error) {
	id := ids.New(ids.Document)
	var doc model.Document
	dup := false
	err := q.Tx(func(tx db.Q) error {
		if in.SourceID != nil && *in.SourceID != "" {
			existing, ok, err := FindBySourceID(tx, in.WorkspaceID, in.Container, *in.SourceID)
			if err != nil {
				return err
			}
			if ok {
				doc, dup = existing, true
				return nil
			}
		}
		metadata := jsonx.NewMap[any]()
		if in.Metadata != nil {
			metadata = in.Metadata.Clone()
		}
		if in.File != nil {
			metadata.Set("filename", in.File.Name)
			metadata.Set("mime", in.File.Mime)
			metadata.Set("size", float64(len(in.File.Bytes)))
		}
		var title *string
		switch {
		case in.Title != nil:
			title = in.Title
		case in.File != nil:
			title = &in.File.Name
		}
		meta, err := jsonx.Marshal(metadata)
		if err != nil {
			return err
		}
		var sourceID any
		if in.SourceID != nil {
			sourceID = *in.SourceID
		}
		d, err := model.ScanDocument(tx.WQueryRow(`INSERT INTO documents (id, workspace_id, container, source_type, source_id, title, url, content, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+model.DocumentCols,
			id, in.WorkspaceID, in.Container, in.SourceType, sourceID, MaskTitle(title), in.URL, in.Content, string(meta)))
		if err != nil {
			return err
		}
		doc = d
		if in.File != nil {
			if err := os.MkdirAll(UploadsDir(), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(UploadsDir(), id), in.File.Bytes, 0o644); err != nil {
				return err
			}
		}
		_, _, err = jobs.Enqueue(tx, in.WorkspaceID, jobs.Ingest, jsonx.ObjectOf("documentId", id), now)
		return err
	})
	return doc, dup, err
}

// MaskTitle masks secrets in a title; "" and nil are nil.
func MaskTitle(title *string) *string {
	if title == nil || *title == "" {
		return nil
	}
	s, _ := sensitive.Mask(*title)
	return &s
}

// FindBySourceID finds the container's document with that source id.
func FindBySourceID(q db.Q, workspaceID, container, sourceID string) (model.Document, bool, error) {
	d, err := model.ScanDocument(q.QueryRow(`SELECT `+model.DocumentCols+` FROM documents WHERE workspace_id = ? AND container = ? AND source_id = ?`, workspaceID, container, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// GetDocumentTitle is just the title (nil for none or no document).
func GetDocumentTitle(q db.Q, workspaceID, id string) (*string, error) {
	var t sql.NullString
	err := q.QueryRow(`SELECT title FROM documents WHERE id = ? AND workspace_id = ?`, id, workspaceID).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return model.Str(t), err
}

// GetDocument reads a document of the workspace.
func GetDocument(q db.Q, workspaceID, id string) (model.Document, bool, error) {
	d, err := model.ScanDocument(q.QueryRow(`SELECT `+model.DocumentCols+` FROM documents WHERE id = ? AND workspace_id = ?`, id, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// DocumentListItem is one row of ListDocuments.
type DocumentListItem struct {
	ID          string        `json:"id"`
	Container   string        `json:"container"`
	SourceType  string        `json:"sourceType"`
	Title       *string       `json:"title"`
	URL         *string       `json:"url"`
	Status      string        `json:"status"`
	Error       *string       `json:"error"`
	Metadata    *jsonx.Object `json:"metadata"`
	CreatedAt   int64         `json:"createdAt"`
	Preview     string        `json:"preview"`
	MemoryCount int64         `json:"memoryCount"`
}

// Cursor is a keyset position: the last row's (createdAt, id).
type Cursor struct {
	CreatedAt float64
	ID        string
}

// EncodeCursor is base64url of "<createdAt>:<id>".
func EncodeCursor(createdAt int64, id string) string {
	return base64URL([]byte(strconv.FormatInt(createdAt, 10) + ":" + id))
}

// DecodeCursor reads a cursor; nil when it isn't one.
func DecodeCursor(c string) *Cursor {
	if c == "" {
		return nil
	}
	s := strx.DecodeUTF8(strx.DecodeBase64Node(c))
	parts := strings.Split(s, ":")
	at := strx.Number(parts[0])
	if len(parts) < 2 || parts[1] == "" || at != at || at > 1.7976931348623157e308 || at < -1.7976931348623157e308 {
		return nil
	}
	return &Cursor{CreatedAt: at, ID: parts[1]}
}

func clampLimit(limit *int, def, maxN int) int {
	n := def
	if limit != nil {
		n = *limit
	}
	return min(max(n, 1), maxN)
}

// ListDocuments lists the container's documents, newest first, a page at a
// time. A non-empty status keeps only documents with that status.
func ListDocuments(q db.Q, workspaceID, container, status, cursor string, limit *int) ([]DocumentListItem, *string, error) {
	n := clampLimit(limit, 50, 200)
	cur := DecodeCursor(cursor)
	where := `"documents"."workspace_id" = ? AND "documents"."container" = ?`
	args := []any{workspaceID, container}
	if status != "" {
		where += ` AND "documents"."status" = ?`
		args = append(args, status)
	}
	if cur != nil {
		where += ` AND ("documents"."created_at" < ? OR ("documents"."created_at" = ? AND "documents"."id" < ?))`
		args = append(args, cur.CreatedAt, cur.CreatedAt, cur.ID)
	}
	args = append(args, n+1)
	rows, err := q.Query(`SELECT "id", "container", "source_type", "title", "url", "status", "error", "metadata", "created_at",
		substr(coalesce("documents"."content", ''), 1, 280),
		(SELECT count(*) FROM memories m WHERE m.source_document_id = "documents"."id" AND m.valid_to IS NULL)
		FROM "documents" WHERE `+where+` ORDER BY "documents"."created_at" DESC, "documents"."id" DESC LIMIT ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []DocumentListItem{}
	for rows.Next() {
		var d DocumentListItem
		var title, url, errs, meta sql.NullString
		if err := rows.Scan(&d.ID, &d.Container, &d.SourceType, &title, &url, &d.Status, &errs, &meta, &d.CreatedAt, &d.Preview, &d.MemoryCount); err != nil {
			return nil, nil, err
		}
		d.Title, d.URL, d.Error = model.Str(title), model.Str(url), model.Str(errs)
		d.Metadata = model.JSONObject(meta)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > n {
		out = out[:n]
		last := out[len(out)-1]
		c := EncodeCursor(last.CreatedAt, last.ID)
		next = &c
	}
	return out, next, nil
}

// CountDocuments counts the container's documents.
func CountDocuments(q db.Q, workspaceID, container string) (int64, error) {
	var n int64
	err := q.QueryRow(`SELECT count(*) FROM documents WHERE workspace_id = ? AND container = ?`, workspaceID, container).Scan(&n)
	return n, err
}

// DeleteDocument deletes the document and its chunks. Memories it produced
// stay, unlinked.
func DeleteDocument(q db.Q, workspaceID, id string) (bool, error) {
	if _, ok, err := GetDocument(q, workspaceID, id); err != nil || !ok {
		return false, err
	}
	err := q.Tx(func(tx db.Q) error {
		chunkIDs, err := stringsOf(tx, `SELECT id FROM chunks WHERE document_id = ?`, id)
		if err != nil {
			return err
		}
		if err := search.UnindexChunks(tx, chunkIDs); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM documents WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return false, err
	}
	os.Remove(filepath.Join(UploadsDir(), id))
	return true, nil
}

// DeleteDocumentAndMemories forgets the current memories the document wrote,
// then deletes the document.
func DeleteDocumentAndMemories(q db.Q, workspaceID, id string, now int64) (bool, error) {
	if _, ok, err := GetDocument(q, workspaceID, id); err != nil || !ok {
		return false, err
	}
	err := q.Tx(func(tx db.Q) error {
		current, err := stringsOf(tx, `SELECT id FROM memories WHERE workspace_id = ? AND source_document_id = ? AND valid_to IS NULL`, workspaceID, id)
		if err != nil {
			return err
		}
		for _, m := range current {
			if _, _, err := ForgetMemory(tx, workspaceID, m, now); err != nil {
				return err
			}
		}
		_, err = DeleteDocument(tx, workspaceID, id)
		return err
	})
	return err == nil, err
}

func stringsOf(q db.Q, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- memories --------------------------------------------------------------------

// ListOptions are listMemories' options.
type ListOptions struct {
	WorkspaceID string
	Container   string
	Cursor      string
	History     bool
	Limit       *int
	Kind        string
	Review      bool
}

// ListMemories lists memories newest first, a page at a time.
func ListMemories(q db.Q, o ListOptions) ([]model.Memory, *string, error) {
	n := clampLimit(o.Limit, 50, 200)
	cur := DecodeCursor(o.Cursor)
	where := []string{"workspace_id = ?", "container = ?"}
	args := []any{o.WorkspaceID, o.Container}
	if !(o.History && !o.Review) {
		where = append(where, "valid_to IS NULL")
	}
	if o.Review {
		where = append(where, "review_note IS NOT NULL")
	}
	if o.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, o.Kind)
	}
	if cur != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, cur.CreatedAt, cur.CreatedAt, cur.ID)
	}
	args = append(args, n+1)
	list, err := memoriesQuery(q, `SELECT `+model.MemoryCols+` FROM memories WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(list) > n {
		list = list[:n]
		last := list[len(list)-1]
		c := EncodeCursor(last.CreatedAt, last.ID)
		next = &c
	}
	return list, next, nil
}

func memoriesQuery(q db.Q, query string, args ...any) ([]model.Memory, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Memory{}
	for rows.Next() {
		m, err := model.ScanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReviewCount counts current facts flagged for review in a container.
func ReviewCount(q db.Q, workspaceID, container string) (int64, error) {
	var n int64
	err := q.QueryRow(`SELECT count(*) FROM memories WHERE workspace_id = ? AND container = ? AND valid_to IS NULL AND review_note IS NOT NULL`, workspaceID, container).Scan(&n)
	return n, err
}

// GetMemory reads a memory of the workspace.
func GetMemory(q db.Q, workspaceID, id string) (model.Memory, bool, error) {
	m, err := model.ScanMemory(q.QueryRow(`SELECT `+model.MemoryCols+` FROM memories WHERE id = ? AND workspace_id = ?`, id, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

const maxChain = 100_000

// Chain is memoryChain: the version chain a memory belongs to, oldest first,
// walking at most limit steps each way.
func Chain(q db.Q, workspaceID, id string, limit int) ([]model.Memory, error) {
	start, ok, err := GetMemory(q, workspaceID, id)
	if err != nil || !ok {
		return []model.Memory{}, err
	}
	chain := []model.Memory{start}
	back := start
	for i := 0; i < limit && back.SupersedesID != nil && *back.SupersedesID != ""; i++ {
		prev, ok, err := GetMemory(q, workspaceID, *back.SupersedesID)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		chain = append([]model.Memory{prev}, chain...)
		back = prev
	}
	fwd := start
	for i := 0; i < limit; i++ {
		next, err := model.ScanMemory(q.QueryRow(`SELECT `+model.MemoryCols+` FROM memories WHERE supersedes_id = ? AND workspace_id = ? LIMIT 1`, fwd.ID, workspaceID))
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, next)
		fwd = next
	}
	return chain, nil
}

// HasNewerVersion reports whether a newer version replaced id.
func HasNewerVersion(q db.Q, workspaceID, id string) (bool, error) {
	var x string
	err := q.QueryRow(`SELECT id FROM memories WHERE supersedes_id = ? AND workspace_id = ? LIMIT 1`, id, workspaceID).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// VersionCounts is how many older versions each memory has.
func VersionCounts(q db.Q, workspaceID string, memIDs []string) (*jsonx.Map[float64], error) {
	out := jsonx.NewMap[float64]()
	for _, id := range memIDs {
		var n sql.NullInt64
		err := q.QueryRow(fmt.Sprintf(`WITH RECURSIVE chain(id, prev, n) AS (
       SELECT id, supersedes_id, 0 FROM memories WHERE id = ? AND workspace_id = ?
       UNION ALL SELECT m.id, m.supersedes_id, chain.n + 1 FROM memories m JOIN chain ON m.id = chain.prev
       WHERE chain.n < %d AND m.workspace_id = ?
     ) SELECT max(n) AS n FROM chain`, maxChain), id, workspaceID, workspaceID).Scan(&n)
		if err != nil {
			return nil, err
		}
		out.Set(id, float64(n.Int64))
	}
	return out, nil
}

// DateFields are the dates of a write: Set false keeps what is there (on a
// new version, the old version's date); Set with a nil Value clears it.
type DateFields struct {
	Event DateValue
	Due   DateValue
}

// DateValue is undefined (Set false), null or a date.
type DateValue struct {
	Set   bool
	Value *dates.Dated
}

// Value makes a set DateValue.
func Value(d *dates.Dated) DateValue { return DateValue{Set: true, Value: d} }

// EventOf and DueOf read a memory's stored dates.
func EventOf(m model.Memory) *dates.Dated { return dates.FromColumns(m.EventDate, m.EventPrecision) }
func DueOf(m model.Memory) *dates.Dated   { return dates.FromColumns(m.DueDate, m.DuePrecision) }

func sameDate(a, b *dates.Dated) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Date == b.Date && a.Precision == b.Precision
}

func cols(d *dates.Dated) (any, any) {
	if d == nil {
		return nil, nil
	}
	return d.Date, d.Precision
}

// NewMemory is insertMemory's input.
type NewMemory struct {
	WorkspaceID      string
	Container        string
	Fact             string
	Keywords         string
	Kind             string
	Event            *dates.Dated
	Due              *dates.Dated
	SourceDocumentID *string
	SupersedesID     *string
	ReviewNote       *string
	ConflictsWithID  *string
}

// InsertMemory writes the row and its index entries. The vector is computed
// by the caller first.
func InsertMemory(q db.Q, in NewMemory, vector []float32) (model.Memory, error) {
	ed, ep := cols(in.Event)
	dd, dp := cols(in.Due)
	row, err := model.ScanMemory(q.WQueryRow(`INSERT INTO memories (id, workspace_id, container, fact, keywords, kind, source_document_id, supersedes_id, review_note, conflicts_with_id, event_date, event_precision, due_date, due_precision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+model.MemoryCols,
		ids.New(ids.Memory), in.WorkspaceID, in.Container, in.Fact, in.Keywords, in.Kind, in.SourceDocumentID, in.SupersedesID, in.ReviewNote, in.ConflictsWithID, ed, ep, dd, dp))
	if err != nil {
		return row, err
	}
	err = search.IndexMemory(q, search.MemoryRef{ID: row.ID, WorkspaceID: row.WorkspaceID, Container: row.Container, Fact: row.Fact, Keywords: row.Keywords}, vector)
	return row, err
}

// CloseMemory ends a current memory at `at` and unindexes it. False when it
// wasn't current.
func CloseMemory(q db.Q, id string, at int64) (bool, error) {
	n, err := db.Changes(q.Exec(`UPDATE memories SET valid_to = ? WHERE id = ? AND valid_to IS NULL`, at, id))
	if err != nil {
		return false, err
	}
	if err := search.UnindexMemory(q, id); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Next is supersedeMemory's new version. Kind "" keeps the old kind; a nil
// SourceDocumentID keeps the old one.
type Next struct {
	Fact             string
	Keywords         string
	Kind             string
	Event            DateValue
	Due              DateValue
	SourceDocumentID *string
}

// SupersedeMemory closes `old` and writes the new version. ok is false when
// `old` is no longer current; nothing is written then.
func SupersedeMemory(q db.Q, old model.Memory, next Next, vector []float32, now int64) (model.Memory, bool, error) {
	var row model.Memory
	ok := false
	err := q.Tx(func(tx db.Q) error {
		closed, err := CloseMemory(tx, old.ID, now)
		if err != nil || !closed {
			return err
		}
		kind := next.Kind
		if kind == "" {
			kind = old.Kind
		}
		src := next.SourceDocumentID
		if src == nil {
			src = old.SourceDocumentID
		}
		event := EventOf(old)
		if next.Event.Set {
			event = next.Event.Value
		}
		due := DueOf(old)
		if next.Due.Set {
			due = next.Due.Value
		}
		r, err := InsertMemory(tx, NewMemory{WorkspaceID: old.WorkspaceID, Container: old.Container, Fact: next.Fact, Keywords: next.Keywords, Kind: kind,
			SourceDocumentID: src, SupersedesID: &old.ID, Event: event, Due: due}, vector)
		if err != nil {
			return err
		}
		// The new version mentions the same people as the one it replaces.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO person_memories (person_id, memory_id, workspace_id) SELECT person_id, ?, workspace_id FROM person_memories WHERE memory_id = ? AND workspace_id = ?`, r.ID, old.ID, old.WorkspaceID); err != nil {
			return err
		}
		row, ok = r, true
		return nil
	})
	return row, ok, err
}

// SetReviewNote sets or clears the note on a current memory; ok false when
// it isn't current.
func SetReviewNote(q db.Q, workspaceID, id string, note *string, conflictsWithID *string) (model.Memory, bool, error) {
	var cw any
	if note != nil && conflictsWithID != nil {
		cw = *conflictsWithID
	}
	m, err := model.ScanMemory(q.WQueryRow(`UPDATE memories SET review_note = ?, conflicts_with_id = ? WHERE id = ? AND workspace_id = ? AND valid_to IS NULL RETURNING `+model.MemoryCols, note, cw, id, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

// ResolveConflict clears the note on `id` and on every current fact whose
// conflict points at it.
func ResolveConflict(q db.Q, workspaceID, id string) (model.Memory, bool, error) {
	var row model.Memory
	ok := false
	err := q.Tx(func(tx db.Q) error {
		r, found, err := SetReviewNote(tx, workspaceID, id, nil, nil)
		if err != nil {
			return err
		}
		row, ok = r, found
		return clearConflictsWith(tx, workspaceID, id)
	})
	return row, ok, err
}

func clearConflictsWith(q db.Q, workspaceID, id string) error {
	others, err := stringsOf(q, `SELECT id FROM memories WHERE workspace_id = ? AND valid_to IS NULL AND conflicts_with_id = ?`, workspaceID, id)
	if err != nil {
		return err
	}
	for _, other := range others {
		var open string
		err := q.QueryRow(`SELECT id FROM memories WHERE workspace_id = ? AND valid_to IS NULL AND conflicts_with_id = ? AND id != ? LIMIT 1`, workspaceID, other, id).Scan(&open)
		switch {
		case err == nil:
			_, err = q.Exec(`UPDATE memories SET conflicts_with_id = ? WHERE id = ?`, open, other)
		case errors.Is(err, sql.ErrNoRows):
			_, err = q.Exec(`UPDATE memories SET review_note = NULL, conflicts_with_id = NULL WHERE id = ?`, other)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// EmbedPassage embeds one memory text for editMemory and addMemoryDirect.
// Tests replace it to run code while the vector is computed (the await in
// the TS functions).
var EmbedPassage = func(text string) ([]float32, error) { return embed.EmbedOne(text, embed.Passage) }

// Patch is editMemory's patch; nil fields are undefined.
type Patch struct {
	Fact     *string
	Keywords *string
	Kind     *string
	Event    DateValue
	Due      DateValue
}

// EditMemory writes a manual edit as a new version (which starts without a
// review note, resolving a conflict on both sides). An edit that changes
// nothing only resolves the note. ok false when the memory isn't current, or
// changed while the vector was computed.
func EditMemory(d *db.DB, workspaceID, id string, p Patch, now func() int64) (model.Memory, bool, error) {
	old, found, err := GetMemory(d, workspaceID, id)
	if err != nil || !found || old.ValidTo != nil {
		return old, false, err
	}
	fact := old.Fact
	if p.Fact != nil && strx.Trim(*p.Fact) != "" {
		fact = strx.Trim(*p.Fact)
	}
	keywords := old.Keywords
	if p.Keywords != nil {
		keywords = strx.Trim(*p.Keywords)
	}
	kind := old.Kind
	if p.Kind != nil {
		kind = *p.Kind
	}
	event := EventOf(old)
	if p.Event.Set {
		event = p.Event.Value
	}
	due := DueOf(old)
	if p.Due.Set {
		due = p.Due.Value
	}
	if fact == old.Fact && keywords == old.Keywords && kind == old.Kind && sameDate(event, EventOf(old)) && sameDate(due, DueOf(old)) {
		if old.ReviewNote == nil {
			return old, true, nil
		}
		r, ok, err := ResolveConflict(d, workspaceID, old.ID)
		if err != nil {
			return old, false, err
		}
		if !ok {
			return old, true, nil
		}
		return r, true, nil
	}
	vector, err := EmbedPassage(Text(fact, keywords))
	if err != nil {
		return old, false, err
	}
	var row model.Memory
	written := false
	err = d.Tx(func(tx db.Q) error {
		r, ok, err := SupersedeMemory(tx, old, Next{Fact: fact, Keywords: keywords, Kind: kind, Event: Value(event), Due: Value(due)}, vector, now())
		if err != nil || !ok {
			return err
		}
		row, written = r, true
		return clearConflictsWith(tx, workspaceID, old.ID)
	})
	if err != nil {
		return row, false, err
	}
	if written {
		if _, _, err := jobs.Enqueue(d, workspaceID, jobs.Profile, jsonx.ObjectOf("container", old.Container), now()); err != nil {
			return row, true, err
		}
	}
	return row, written, nil
}

// AddMemoryDirect embeds and stores a fact, then queues a profile rebuild.
func AddMemoryDirect(d *db.DB, in NewMemory, now int64) (model.Memory, error) {
	vector, err := EmbedPassage(Text(in.Fact, in.Keywords))
	if err != nil {
		return model.Memory{}, err
	}
	var row model.Memory
	err = d.Tx(func(tx db.Q) error {
		r, err := InsertMemory(tx, in, vector)
		row = r
		return err
	})
	if err != nil {
		return row, err
	}
	_, _, err = jobs.Enqueue(d, in.WorkspaceID, jobs.Profile, jsonx.ObjectOf("container", in.Container), now)
	return row, err
}

// ForgetMemory closes a memory and settles any conflict it was part of, then
// queues a profile rebuild. ok false when there is no such memory; the row
// is returned as it is after the close.
func ForgetMemory(q db.Q, workspaceID, id string, now int64) (model.Memory, bool, error) {
	m, found, err := GetMemory(q, workspaceID, id)
	if err != nil || !found {
		return m, false, err
	}
	closed := false
	err = q.Tx(func(tx db.Q) error {
		done, err := CloseMemory(tx, id, now)
		if err != nil {
			return err
		}
		closed = done
		if done {
			return clearConflictsWith(tx, workspaceID, id)
		}
		return nil
	})
	if err != nil {
		return m, false, err
	}
	if closed {
		if _, _, err := jobs.Enqueue(q, workspaceID, jobs.Profile, jsonx.ObjectOf("container", m.Container), now); err != nil {
			return m, false, err
		}
	}
	after, _, err := GetMemory(q, workspaceID, id)
	return after, true, err
}

// UpcomingRange: current facts due between two calendar dates (inclusive);
// Container "" is every container of the workspace.
type UpcomingRange struct {
	WorkspaceID string
	Container   string
	From, To    string
}

func upcomingWhere(o UpcomingRange) (string, []any) {
	where := []string{"workspace_id = ?"}
	args := []any{o.WorkspaceID}
	if o.Container != "" {
		where = append(where, "container = ?")
		args = append(args, o.Container)
	}
	where = append(where, "valid_to IS NULL", "due_date IS NOT NULL", "due_date >= ?", "due_date <= ?")
	args = append(args, o.From, o.To)
	return strings.Join(where, " AND "), args
}

// ListUpcoming lists facts due in the range, soonest first.
func ListUpcoming(q db.Q, o UpcomingRange, limit *int) ([]model.Memory, error) {
	where, args := upcomingWhere(o)
	args = append(args, clampLimit(limit, 200, 500))
	return memoriesQuery(q, `SELECT `+model.MemoryCols+` FROM memories WHERE `+where+` ORDER BY due_date ASC, created_at ASC LIMIT ?`, args...)
}

// CountUpcoming counts what ListUpcoming would return without its limit.
func CountUpcoming(q db.Q, o UpcomingRange) (int64, error) {
	where, args := upcomingWhere(o)
	var n int64
	err := q.QueryRow(`SELECT count(*) FROM memories WHERE `+where, args...).Scan(&n)
	return n, err
}

// --- containers and profile ------------------------------------------------------

// ContainerInfo is one row of ListContainers.
type ContainerInfo struct {
	Container    string `json:"container"`
	Memories     int64  `json:"memories"`
	Documents    int64  `json:"documents"`
	LastActivity int64  `json:"lastActivity"`
}

// ListContainers lists the workspace's containers, most recently active first.
func ListContainers(q db.Q, workspaceID string) ([]ContainerInfo, error) {
	rows, err := q.Query(`SELECT container,
              sum(CASE WHEN kind = 'mem' THEN 1 ELSE 0 END) AS memories,
              sum(CASE WHEN kind = 'doc' THEN 1 ELSE 0 END) AS documents,
              max(at) AS lastActivity
       FROM (
         SELECT container, 'mem' AS kind, created_at AS at FROM memories WHERE workspace_id = ? AND valid_to IS NULL
         UNION ALL SELECT container, 'doc', created_at FROM documents WHERE workspace_id = ?
       ) GROUP BY container ORDER BY lastActivity DESC`, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContainerInfo{}
	for rows.Next() {
		var c ContainerInfo
		if err := rows.Scan(&c.Container, &c.Memories, &c.Documents, &c.LastActivity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Profile is getProfile's result.
type Profile struct {
	Container       string         `json:"container"`
	Static          string         `json:"static"`
	Dynamic         string         `json:"dynamic"`
	UpdatedAt       *int64         `json:"updatedAt"`
	StaticMemories  []model.Memory `json:"staticMemories"`
	DynamicMemories []model.Memory `json:"dynamicMemories"`
}

// GetProfile reads the container's profile and its 25 newest facts of each kind.
func GetProfile(q db.Q, workspaceID, container string) (Profile, error) {
	p := Profile{Container: container}
	var st, dy string
	var up int64
	err := q.QueryRow(`SELECT static_text, dynamic_text, updated_at FROM profiles WHERE workspace_id = ? AND container = ?`, workspaceID, container).Scan(&st, &dy, &up)
	switch {
	case err == nil:
		p.Static, p.Dynamic, p.UpdatedAt = st, dy, &up
	case !errors.Is(err, sql.ErrNoRows):
		return p, err
	}
	current := func(kind string) ([]model.Memory, error) {
		return memoriesQuery(q, `SELECT `+model.MemoryCols+` FROM memories WHERE workspace_id = ? AND container = ? AND kind = ? AND valid_to IS NULL ORDER BY created_at DESC LIMIT 25`, workspaceID, container, kind)
	}
	if p.StaticMemories, err = current("static"); err != nil {
		return p, err
	}
	p.DynamicMemories, err = current("dynamic")
	return p, err
}

// FromDocument lists the memories a document wrote, oldest first.
func FromDocument(q db.Q, workspaceID, documentID string) ([]model.Memory, error) {
	return memoriesQuery(q, `SELECT `+model.MemoryCols+` FROM memories WHERE workspace_id = ? AND source_document_id = ? ORDER BY created_at, id`, workspaceID, documentID)
}

// ByIDs reads the workspace's memories with these ids (any order).
func ByIDs(q db.Q, workspaceID string, memIDs []string) ([]model.Memory, error) {
	if len(memIDs) == 0 {
		return []model.Memory{}, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(memIDs)), ", ")
	args := []any{workspaceID}
	for _, id := range memIDs {
		args = append(args, id)
	}
	return memoriesQuery(q, `SELECT `+model.MemoryCols+` FROM memories WHERE workspace_id = ? AND id IN (`+ph+`)`, args...)
}

func base64URL(b []byte) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		out.WriteByte(enc[n>>18&63])
		out.WriteByte(enc[n>>12&63])
		if rem > 1 {
			out.WriteByte(enc[n>>6&63])
		}
		if rem > 2 {
			out.WriteByte(enc[n&63])
		}
	}
	return out.String()
}
