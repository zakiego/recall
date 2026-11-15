// Package pipeline is pipeline.ts: the ingest, extract and profile jobs, and
// the handlers the job runner calls.
package pipeline

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/backfill"
	"github.com/zakiego/recall/apps/server/internal/chunk"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/content"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/sensitive"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	extractWindowWords = 3_000
	ExtractMaxWindows  = 10
	relatedMemories    = 20
)

// EmbedPassages embeds chunk and memory texts. Tests replace it to replay
// the TS build's vectors on any platform.
var EmbedPassages = func(texts []string) ([][]float32, error) { return embed.Embed(texts, embed.Passage) }

// Deps are what the jobs talk to: the gateway (Do nil is the real one) and
// the clock the prompts read. M4 adds Gmail and the app feeds.
type Deps struct {
	Do  gateway.Do
	Now func() time.Time
	// Ask is the ask job's gateway and clock (askDeps in createHandlers).
	Ask ask.Deps
	// Reminders is the reminders job's sender and clock (reminderDeps).
	Reminders reminders.Deps
	// GmailDo talks to Google for email ingest (gmailFetch); nil is
	// connectors.GoogleDo().
	GmailDo connectors.Do
	// AppSync is the app feeds' fetch and clock (deps.appSync).
	AppSync apps.SyncDeps
}

// DefaultDeps is the real gateway and clock.
func DefaultDeps() Deps { return Deps{Now: time.Now} }

func nowMs() int64 { return time.Now().UnixMilli() }

func setStatus(q db.Q, id, status string, errText *string) error {
	_, err := q.Exec(`UPDATE documents SET status = ?, error = ? WHERE id = ?`, status, errText, id)
	return err
}

// documentGone: a document can be deleted while its job waits on the network.
func documentGone(q db.Q, id string) (bool, error) {
	var x string
	err := q.QueryRow(`SELECT id FROM documents WHERE id = ?`, id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

func getDocument(q db.Q, id string) (model.Document, bool, error) {
	d, err := model.ScanDocument(q.QueryRow(`SELECT `+model.DocumentCols+` FROM documents WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// --- ingest ------------------------------------------------------------------------

type resolved struct {
	title    *string
	text     string
	metadata *jsonx.Object
}

func metaString(m *jsonx.Object, k string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m.Get(k)
	s, isStr := v.(string)
	return s, ok && isStr
}

func resolveText(ctx context.Context, d *db.DB, doc model.Document, gmailDo connectors.Do) (resolved, error) {
	stored := doc.Content != nil && strx.Trim(*doc.Content) != ""
	switch doc.SourceType {
	case "email":
		if stored {
			return resolved{title: doc.Title, text: *doc.Content}, nil
		}
		if gmailDo == nil {
			gmailDo = connectors.GoogleDo()
		}
		t, text, meta, err := connectors.ResolveEmail(ctx, d, doc, gmailDo)
		return resolved{title: t, text: text, metadata: meta}, err
	case "url":
		if stored {
			return resolved{title: doc.Title, text: *doc.Content}, nil
		}
		u := ""
		if doc.URL != nil {
			u = *doc.URL
		}
		r, err := content.FetchURL(ctx, u, nil, nil)
		if err != nil {
			return resolved{}, err
		}
		title := doc.Title
		if title == nil {
			title = r.Title
		}
		return resolved{title: title, text: r.Text}, nil
	case "file":
		if stored {
			return resolved{title: doc.Title, text: *doc.Content}, nil
		}
		b, err := os.ReadFile(filepath.Join(memories.UploadsDir(), doc.ID))
		if err != nil {
			return resolved{}, fmt.Errorf("ENOENT: no such file or directory, open '%s'", filepath.Join(memories.UploadsDir(), doc.ID))
		}
		// (meta.mime as string) ?? null, (meta.filename as string) ?? doc.title
		var mime, name *string
		if v, ok := metaString(doc.Metadata, "mime"); ok {
			mime = &v
		}
		if v, ok := metaString(doc.Metadata, "filename"); ok {
			name = &v
		} else {
			name = doc.Title
		}
		r, err := content.BytesToText(b, mime, name)
		if err != nil {
			return resolved{}, err
		}
		title := doc.Title
		if title == nil {
			title = r.Title
		}
		return resolved{title: title, text: r.Text}, nil
	case "chat":
		if doc.Metadata != nil {
			if v, ok := doc.Metadata.Get("messages"); ok {
				if arr, ok := v.([]any); ok {
					text, err := content.FormatChat(arr)
					if err != nil {
						return resolved{}, err
					}
					return resolved{title: doc.Title, text: text}, nil
				}
			}
		}
	}
	text := ""
	if doc.Content != nil {
		text = *doc.Content
	}
	return resolved{title: doc.Title, text: text}, nil
}

// maskedMetadata masks the raw messages a chat document keeps in metadata.
func maskedMetadata(meta *jsonx.Object) *jsonx.Object {
	out := jsonx.NewMap[any]()
	if meta != nil {
		out = meta.Clone()
	}
	if v, ok := out.Get("messages"); ok {
		if arr, ok := v.([]any); ok {
			masked := make([]any, len(arr))
			for i, x := range arr {
				o, isObj := x.(*jsonx.Object)
				if isObj {
					if c, isStr := metaString(o, "content"); isStr {
						m := o.Clone()
						s, _ := sensitive.Mask(c)
						m.Set("content", s)
						masked[i] = m
						continue
					}
				}
				masked[i] = x
			}
			out.Set("messages", masked)
		}
	}
	return out
}

// Ingest turns a document into masked, chunked, indexed text and queues its
// extract.
func Ingest(ctx context.Context, d *db.DB, documentID string, gmailDo connectors.Do) error {
	doc, ok, err := getDocument(d, documentID)
	if err != nil || !ok {
		return err
	}
	if err := setStatus(d, doc.ID, "processing", nil); err != nil {
		return err
	}
	r, err := resolveText(ctx, d, doc, gmailDo)
	if err != nil {
		return err
	}
	// Keys, tokens and card numbers never reach the stored text, the index or the model.
	text, masked := sensitive.Mask(strx.Trim(strx.Slice16(r.text, content.MaxTextChars)))
	if text == "" {
		return errors.New("no text could be extracted")
	}
	pieces := chunk.Text(text, chunk.Options{})
	vectors, err := EmbedPassages(pieces)
	if err != nil {
		return err
	}
	stored := false
	err = d.Tx(func(tx db.Q) error {
		gone, err := documentGone(tx, doc.ID)
		if err != nil || gone {
			return err
		}
		old, err := stringList(tx, `SELECT id FROM chunks WHERE document_id = ?`, doc.ID)
		if err != nil {
			return err
		}
		if err := search.UnindexChunks(tx, old); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM chunks WHERE document_id = ?`, doc.ID); err != nil {
			return err
		}
		for idx, piece := range pieces {
			id := ids.New(ids.Chunk)
			if _, err := tx.Exec(`INSERT INTO chunks (id, document_id, workspace_id, container, idx, text) VALUES (?, ?, ?, ?, ?, ?)`, id, doc.ID, doc.WorkspaceID, doc.Container, idx, piece); err != nil {
				return err
			}
			if err := search.IndexChunk(tx, search.ChunkRef{ID: id, WorkspaceID: doc.WorkspaceID, Container: doc.Container, Text: piece}, vectors[idx]); err != nil {
				return err
			}
		}
		meta := maskedMetadata(doc.Metadata)
		if r.metadata != nil {
			meta.Assign(r.metadata)
		}
		meta.Set("chunks", float64(len(pieces)))
		meta.Set("masked", float64(masked))
		mb, err := jsonx.Marshal(meta)
		if err != nil {
			return err
		}
		title := r.title
		if title == nil {
			title = doc.Title
		}
		if _, err := tx.Exec(`UPDATE documents SET content = ?, title = ?, metadata = ? WHERE id = ?`, text, memories.MaskTitle(title), string(mb), doc.ID); err != nil {
			return err
		}
		stored = true
		return nil
	})
	if err != nil || !stored {
		return err
	}
	if doc.SourceType == "file" {
		os.Remove(filepath.Join(memories.UploadsDir(), doc.ID))
	}
	_, _, err = jobs.Enqueue(d, doc.WorkspaceID, jobs.Extract, jsonx.ObjectOf("documentId", doc.ID), nowMs())
	return err
}

func stringList(q db.Q, query string, args ...any) ([]string, error) {
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

// --- extract -----------------------------------------------------------------------

type window struct {
	text     string
	chunkIDs []string
}

// splitCount is text.split(/\s+/).length.
func splitCount(s string) int {
	n := 1
	inSpace := false
	for _, r := range s {
		if strx.IsSpace(r) {
			if !inSpace {
				n++
			}
			inSpace = true
			continue
		}
		inSpace = false
	}
	return n
}

func windows(q db.Q, documentID string) ([]window, error) {
	rows, err := q.Query(`SELECT id, text FROM chunks WHERE document_id = ? ORDER BY idx ASC`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []window
	var curText, curIDs []string
	words := 0
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		n := splitCount(text)
		if words > 0 && words+n > extractWindowWords {
			out = append(out, window{strings.Join(curText, "\n\n"), curIDs})
			curText, curIDs, words = nil, nil, 0
		}
		curText = append(curText, text)
		curIDs = append(curIDs, id)
		words += n
	}
	if words > 0 {
		out = append(out, window{strings.Join(curText, "\n\n"), curIDs})
	}
	return out, rows.Err()
}

func chunkVector(q db.Q, id string) ([]float32, bool, error) {
	var b []byte
	err := q.QueryRow(`SELECT embedding FROM chunks_vec WHERE chunk_id = ?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, true, nil
}

// RelatedMemories is relatedMemories: the current memories closest to any of
// the vectors, fused by RRF.
func RelatedMemories(q db.Q, scope search.Scope, vectors [][]float32, limit int) ([]model.Memory, error) {
	score := map[string]float64{}
	var order []string
	for _, v := range vectors {
		hits, err := search.VecMemories(q, scope, v, limit)
		if err != nil {
			return nil, err
		}
		for _, r := range hits {
			if _, ok := score[r.ID]; !ok {
				order = append(order, r.ID)
			}
			score[r.ID] += 1 / float64(60+r.Rank)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return score[order[i]] > score[order[j]] })
	if len(order) > limit {
		order = order[:limit]
	}
	rows, err := memories.ByIDs(q, scope.WorkspaceID, order)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.Memory{}
	for _, m := range rows {
		if m.ValidTo == nil && m.Container == scope.Container {
			byID[m.ID] = m
		}
	}
	out := []model.Memory{}
	for _, id := range order {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// ApplyStats are applyExtract's counts, in the TS key order.
type ApplyStats struct {
	Added               int `json:"added"`
	Updated             int `json:"updated"`
	Invalidated         int `json:"invalidated"`
	Deduped             int `json:"deduped"`
	Conflicts           int `json:"conflicts"`
	Redacted            int `json:"redacted"`
	Stale               int `json:"stale"`
	CappedInvalidations int `json:"cappedInvalidations"`
}

// MaxInvalidations: a window that tries to close many facts is more likely
// an injected instruction than news.
const MaxInvalidations = 5

// DedupeSimilarity: an add at least this close to a current memory or another
// fact of the same result is a duplicate.
const DedupeSimilarity = 0.975

const dedupeCandidates = 5

func isSensitive(fact, keywords string) bool { return len(sensitive.Kinds(fact+"\n"+keywords)) > 0 }

func normalizeFact(fact string) string {
	s := strx.CollapseSpace(strx.Lower(fact))
	s = strings.TrimRightFunc(s, func(r rune) bool { return r == '.' || r == '!' || r == '?' || strx.IsSpace(r) })
	return strx.Trim(s)
}

var digits = regexp.MustCompile(`\d+`)

func numbersIn(fact string) map[string]bool {
	out := map[string]bool{}
	for _, n := range digits.FindAllString(fact, -1) {
		out[jsonx.NumberString(strx.Number(n))] = true
	}
	return out
}

func conflictingNumbers(a, b string) bool {
	x, y := numbersIn(a), numbersIn(b)
	some := func(p, q map[string]bool) bool {
		for n := range p {
			if !q[n] {
				return true
			}
		}
		return false
	}
	return some(x, y) && some(y, x)
}

func cosine(a, b []float32) float64 {
	s := 0.0
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func isDuplicate(fact, other string, similarity float64) bool {
	if normalizeFact(fact) == normalizeFact(other) {
		return true
	}
	return similarity >= DedupeSimilarity && !conflictingNumbers(fact, other)
}

const maxReviewNote = 1_000

func reviewNote(other, note string) string {
	quoted := other
	if strx.Len16(other) > 160 {
		quoted = strx.Slice16(other, 157) + "..."
	}
	if note != "" {
		return `Conflicts with "` + quoted + `": ` + note
	}
	return `Conflicts with "` + quoted + `"`
}

func appendNote(existing *string, note string) string {
	s := note
	if existing != nil && *existing != "" {
		s = *existing + "\n" + note
	}
	return strx.Slice16(s, maxReviewNote)
}

func dateOrNull(f gateway.DateField) *dates.Dated { return f.Value }

func dateValue(f gateway.DateField) memories.DateValue {
	if !f.Set {
		return memories.DateValue{}
	}
	return memories.Value(f.Value)
}

// ApplyExtract writes one extract result (see pipeline.ts for the rules).
// documentID "" is none.
func ApplyExtract(ctx context.Context, d *db.DB, scope search.Scope, documentID string, refs []model.Memory, result gateway.ExtractResult) (ApplyStats, error) {
	byRef := func(n int) (model.Memory, bool) {
		if n >= 1 && n <= len(refs) {
			return refs[n-1], true
		}
		return model.Memory{}, false
	}
	type upd struct {
		u   gateway.UpdateItem
		idx int
	}
	type con struct {
		c   gateway.ConflictItem
		idx int
	}
	type add struct {
		a   gateway.AddItem
		idx int
	}
	var allUpdates []upd
	for i, u := range result.Update {
		if _, ok := byRef(u.Ref); ok {
			allUpdates = append(allUpdates, upd{u, i})
		}
	}
	updatedRef := func(ref int) bool {
		for _, u := range allUpdates {
			if u.u.Ref == ref {
				return true
			}
		}
		return false
	}
	var allConflicts []con
	for i, c := range result.Conflict {
		if _, ok := byRef(c.Ref); ok && !updatedRef(c.Ref) {
			allConflicts = append(allConflicts, con{c, i})
		}
	}
	conflictRef := func(ref int) bool {
		for _, c := range allConflicts {
			if c.c.Ref == ref {
				return true
			}
		}
		return false
	}
	seen := map[int]bool{}
	var allInvalidations []int
	for _, n := range result.Invalidate {
		if seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := byRef(n); ok && !updatedRef(n) && !conflictRef(n) {
			allInvalidations = append(allInvalidations, n)
		}
	}
	invalidations := allInvalidations
	if len(invalidations) > MaxInvalidations {
		invalidations = invalidations[:MaxInvalidations]
	}
	var addFacts []add
	for i, a := range result.Add {
		if !isSensitive(a.Fact, a.Keywords) {
			addFacts = append(addFacts, add{a, i})
		}
	}
	var safeUpdates []upd
	for _, u := range allUpdates {
		if !isSensitive(u.u.Fact, u.u.Keywords) {
			safeUpdates = append(safeUpdates, u)
		}
	}
	var conflicts []con
	for _, c := range allConflicts {
		if !isSensitive(c.c.Fact, c.c.Keywords) {
			conflicts = append(conflicts, c)
		}
	}
	redacted := len(result.Add) - len(addFacts) + len(allUpdates) - len(safeUpdates) + len(allConflicts) - len(conflicts)
	// One update per memory: the first wins.
	var updates []upd
	for i, u := range safeUpdates {
		first := -1
		for j, o := range safeUpdates {
			if o.u.Ref == u.u.Ref {
				first = j
				break
			}
		}
		if first == i {
			updates = append(updates, u)
		}
	}
	var texts []string
	for _, a := range addFacts {
		texts = append(texts, memories.Text(a.a.Fact, a.a.Keywords))
	}
	for _, u := range updates {
		texts = append(texts, memories.Text(u.u.Fact, u.u.Keywords))
	}
	for _, c := range conflicts {
		texts = append(texts, memories.Text(c.c.Fact, c.c.Keywords))
	}
	vectors, err := EmbedPassages(texts)
	if err != nil {
		return ApplyStats{}, err
	}
	stats := ApplyStats{Redacted: redacted, CappedInvalidations: len(allInvalidations) - len(invalidations)}

	leaving := map[string]bool{}
	for _, u := range updates {
		m, _ := byRef(u.u.Ref)
		leaving[m.ID] = true
	}
	for _, n := range invalidations {
		m, _ := byRef(n)
		leaving[m.ID] = true
	}
	type written struct {
		fact     string
		vector   []float32
		conflict bool
	}
	var batch []written
	for i, u := range updates {
		batch = append(batch, written{fact: u.u.Fact, vector: vectors[len(addFacts)+i]})
	}
	duplicate := func(fact string, vector []float32, conflictWith string) (bool, error) {
		hits, err := search.VecMemories(d, search.Scope{WorkspaceID: scope.WorkspaceID, Container: scope.Container}, vector, dedupeCandidates)
		if err != nil {
			return false, err
		}
		var near []search.Ranked
		for _, r := range hits {
			if !leaving[r.ID] && r.ID != conflictWith {
				near = append(near, r)
			}
		}
		nearIDs := make([]string, len(near))
		for i, r := range near {
			nearIDs[i] = r.ID
		}
		current, err := memories.ByIDs(d, scope.WorkspaceID, nearIDs)
		if err != nil {
			return false, err
		}
		facts := map[string]string{}
		for _, m := range current {
			if m.Container == scope.Container && m.ValidTo == nil {
				facts[m.ID] = m.Fact
			}
		}
		for _, r := range near {
			if f, ok := facts[r.ID]; ok {
				sim := 1 - r.Raw
				if r.RawNull {
					sim = 1
				}
				if isDuplicate(fact, f, sim) {
					return true, nil
				}
			}
		}
		for _, b := range batch {
			if conflictWith != "" && b.conflict {
				if normalizeFact(fact) == normalizeFact(b.fact) {
					return true, nil
				}
			} else if isDuplicate(fact, b.fact, cosine(vector, b.vector)) {
				return true, nil
			}
		}
		return false, nil
	}
	type keptConflict struct {
		c      con
		vector []float32
	}
	var kept []keptConflict
	for i, c := range conflicts {
		vector := vectors[len(addFacts)+len(updates)+i]
		other, _ := byRef(c.c.Ref)
		if normalizeFact(c.c.Fact) == normalizeFact(other.Fact) {
			stats.Deduped++
			continue
		}
		dup, err := duplicate(c.c.Fact, vector, other.ID)
		if err != nil {
			return stats, err
		}
		if dup {
			stats.Deduped++
			continue
		}
		batch = append(batch, written{fact: c.c.Fact, vector: vector, conflict: true})
		kept = append(kept, keptConflict{c, vector})
	}
	type keptAdd struct {
		a      add
		vector []float32
	}
	var adds []keptAdd
	for i, a := range addFacts {
		vector := vectors[i]
		dup, err := duplicate(a.a.Fact, vector, "")
		if err != nil {
			return stats, err
		}
		if dup {
			stats.Deduped++
			continue
		}
		batch = append(batch, written{fact: a.a.Fact, vector: vector})
		adds = append(adds, keptAdd{a, vector})
	}

	var src *string
	if documentID != "" {
		src = &documentID
	}
	// "a1", "u2", "c1" -> rows, in the order they were written.
	var writtenRows []people.Written
	err = d.Tx(func(tx db.Q) error {
		if documentID != "" {
			gone, err := documentGone(tx, documentID)
			if err != nil || gone {
				return err
			}
		}
		for _, a := range adds {
			row, err := memories.InsertMemory(tx, memories.NewMemory{WorkspaceID: scope.WorkspaceID, Container: scope.Container, Fact: a.a.a.Fact, Keywords: a.a.a.Keywords, Kind: a.a.a.Kind,
				Event: dateOrNull(a.a.a.EventDate), Due: dateOrNull(a.a.a.DueDate), SourceDocumentID: src}, a.vector)
			if err != nil {
				return err
			}
			writtenRows = append(writtenRows, people.Written{Ref: fmt.Sprintf("a%d", a.a.idx+1), Row: row})
			stats.Added++
		}
		for i, u := range updates {
			old, _ := byRef(u.u.Ref)
			row, ok, err := memories.SupersedeMemory(tx, old, memories.Next{Fact: u.u.Fact, Keywords: u.u.Keywords, Event: dateValue(u.u.EventDate), Due: dateValue(u.u.DueDate), SourceDocumentID: src}, vectors[len(addFacts)+i], nowMs())
			if err != nil {
				return err
			}
			if ok {
				writtenRows = append(writtenRows, people.Written{Ref: fmt.Sprintf("u%d", u.idx+1), Row: row})
				stats.Updated++
			} else {
				stats.Stale++
			}
		}
		for _, n := range invalidations {
			m, _ := byRef(n)
			closed, err := memories.CloseMemory(tx, m.ID, nowMs())
			if err != nil {
				return err
			}
			if closed {
				stats.Invalidated++
			}
		}
		for _, k := range kept {
			ref, _ := byRef(k.c.c.Ref)
			old, found, err := memories.GetMemory(tx, scope.WorkspaceID, ref.ID)
			if err != nil {
				return err
			}
			if !found || old.ValidTo != nil {
				continue
			}
			note := reviewNote(old.Fact, k.c.c.Note)
			added, err := memories.InsertMemory(tx, memories.NewMemory{WorkspaceID: scope.WorkspaceID, Container: scope.Container, Fact: k.c.c.Fact, Keywords: k.c.c.Keywords, Kind: old.Kind,
				Event: dateOrNull(k.c.c.EventDate), Due: dateOrNull(k.c.c.DueDate), ReviewNote: &note, ConflictsWithID: &old.ID, SourceDocumentID: src}, k.vector)
			if err != nil {
				return err
			}
			writtenRows = append(writtenRows, people.Written{Ref: fmt.Sprintf("c%d", k.c.idx+1), Row: added})
			appended := appendNote(old.ReviewNote, reviewNote(k.c.c.Fact, k.c.c.Note))
			if _, _, err := memories.SetReviewNote(tx, scope.WorkspaceID, old.ID, &appended, &added.ID); err != nil {
				return err
			}
			stats.Conflicts++
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	// People are extra: a failure here must not fail a document whose facts are saved.
	if _, err := people.LinkExtracted(d, scope, result.People, writtenRows); err != nil {
		logx.Error("[recall:people] linking people failed: %v", err)
	}
	return stats, nil
}

var months = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}

var emailDay = jsre.MustCompile(`\b(\d{1,2}) ([a-z]{3})[a-z]* (\d{4})\b`, "i")

// EmailDate is emailDate: the date of an email's "Date:" line (the first line
// of its first 2000 characters that starts so), in the sender's calendar.
func EmailDate(text string) string {
	head := strx.Slice16(text, 2000)
	line := ""
	found := false
	for _, l := range strings.FieldsFunc(head, func(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }) {
		if strings.HasPrefix(l, "Date: ") && len(l) > len("Date: ") {
			line, found = l[len("Date: "):], true
			break
		}
	}
	if !found {
		return ""
	}
	m := emailDay.Find(jsre.ToUnits(line))
	if m == nil {
		return ""
	}
	day, _ := m.Group(1)
	mon, _ := m.Group(2)
	year, _ := m.Group(3)
	month := -1
	for i, x := range months {
		if x == strx.Lower(mon) {
			month = i + 1
		}
	}
	if month < 1 {
		return ""
	}
	if len(day) == 1 {
		day = "0" + day
	}
	dd := dates.Parse(fmt.Sprintf("%s-%02d-%s", year, month, day))
	if dd == nil || dd.Precision != dates.Day {
		return ""
	}
	return dd.Date
}

// DocInfo is what documentDate reads.
type DocInfo struct {
	Metadata   *jsonx.Object
	CreatedAt  int64
	SourceType string
	Content    *string
}

// DocumentDate is documentDate: the caller's document_date, metadata.date, an
// email's Date header, else the day the document was added.
func DocumentDate(doc DocInfo) string {
	for _, k := range []string{"documentDate", "date"} {
		if v, ok := metaString(doc.Metadata, k); ok {
			if dd := dates.Parse(v); dd != nil && dd.Precision == dates.Day {
				return dd.Date
			}
		}
	}
	if doc.SourceType == "email" {
		c := ""
		if doc.Content != nil {
			c = *doc.Content
		}
		if sent := EmailDate(c); sent != "" {
			return sent
		}
	}
	return dates.ISODate(doc.CreatedAt)
}

func addStats(total *ApplyStats, s ApplyStats) {
	total.Added += s.Added
	total.Updated += s.Updated
	total.Invalidated += s.Invalidated
	total.Deduped += s.Deduped
	total.Conflicts += s.Conflicts
	total.Redacted += s.Redacted
	total.Stale += s.Stale
	total.CappedInvalidations += s.CappedInvalidations
}

// Extract asks the model for facts, window by window, and writes them.
func Extract(ctx context.Context, d *db.DB, documentID string, deps Deps) error {
	doc, ok, err := getDocument(d, documentID)
	if err != nil || !ok {
		return err
	}
	docDate := DocumentDate(DocInfo{Metadata: doc.Metadata, CreatedAt: doc.CreatedAt, SourceType: doc.SourceType, Content: doc.Content})
	source := ""
	if doc.SourceID != nil && strings.HasPrefix(*doc.SourceID, "claude-code:") {
		source = "coding-session"
	}
	all, err := windows(d, doc.ID)
	if err != nil {
		return err
	}
	var total ApplyStats
	limit := min(len(all), ExtractMaxWindows)
	for _, w := range all[:limit] {
		var vectors [][]float32
		for _, id := range w.chunkIDs {
			v, ok, err := chunkVector(d, id)
			if err != nil {
				return err
			}
			if ok {
				vectors = append(vectors, v)
			}
		}
		scope := search.Scope{WorkspaceID: doc.WorkspaceID, Container: doc.Container}
		refs, err := RelatedMemories(d, scope, vectors, relatedMemories)
		if err != nil {
			return err
		}
		existing := make([]gateway.ExistingMemory, len(refs))
		for i, m := range refs {
			existing[i] = gateway.ExistingMemory{Ref: i + 1, Fact: m.Fact, Kind: m.Kind}
		}
		result, _, err := gateway.ChatJSON(ctx, gateway.Request{
			System: gateway.ExtractSystem + "\n\n" + gateway.PeopleRules(doc.Container),
			Prompt: gateway.ExtractPrompt(gateway.ExtractInput{Content: w.text, Existing: existing, Now: deps.Now(), DocumentDate: docDate, Source: source}),
		}, gateway.ParseExtractWithPeople, deps.Do)
		if err != nil {
			return err
		}
		if gone, err := documentGone(d, doc.ID); err != nil || gone {
			return err
		}
		s, err := ApplyExtract(ctx, d, scope, doc.ID, refs, result)
		if err != nil {
			return err
		}
		addStats(&total, s)
	}
	meta := jsonx.NewMap[any]()
	if doc.Metadata != nil {
		meta = doc.Metadata.Clone()
	}
	ex := jsonx.ObjectOf("added", total.Added, "updated", total.Updated, "invalidated", total.Invalidated, "deduped", total.Deduped, "conflicts", total.Conflicts,
		"redacted", total.Redacted, "stale", total.Stale, "cappedInvalidations", total.CappedInvalidations, "windows", len(all), "truncated", len(all) > ExtractMaxWindows)
	meta.Set("extract", ex)
	mb, err := jsonx.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err := d.Exec(`UPDATE documents SET status = 'done', error = NULL, metadata = ? WHERE id = ?`, string(mb), doc.ID); err != nil {
		return err
	}
	if total.Added+total.Updated+total.Invalidated+total.Conflicts > 0 {
		_, _, err = jobs.Enqueue(d, doc.WorkspaceID, jobs.Profile, jsonx.ObjectOf("container", doc.Container), nowMs())
	}
	return err
}

// --- profile -----------------------------------------------------------------------

const (
	ProfileStaticFacts  = 80
	ProfileDynamicFacts = 120
)

// FactAsOf is factAsOf: the latest of a dynamic fact's event, due and source
// document dates, capped at today.
func FactAsOf(event, due *string, document, today string) string {
	var list []string
	for _, p := range []*string{event, due} {
		if p != nil && *p != "" {
			list = append(list, *p)
		}
	}
	if document != "" {
		list = append(list, document)
	}
	sort.Slice(list, func(i, j int) bool { return strx.Compare16(list[i], list[j]) < 0 })
	latest := list[len(list)-1]
	if strx.Compare16(latest, today) > 0 {
		return today
	}
	return latest
}

func profileFacts(q db.Q, workspaceID, container, today string) ([]gateway.ProfileFact, error) {
	var out []gateway.ProfileFact
	rows, err := q.Query(`SELECT fact, kind FROM memories WHERE workspace_id = ? AND container = ? AND valid_to IS NULL AND kind = 'static' ORDER BY created_at DESC LIMIT ?`, workspaceID, container, ProfileStaticFacts)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f gateway.ProfileFact
		if err := rows.Scan(&f.Fact, &f.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, f)
	}
	rows.Close()
	type dyn struct {
		fact, asOf string
		createdAt  int64
	}
	rows, err = q.Query(`SELECT m.fact, m.created_at, m.event_date, m.due_date, d.id, d.source_type, d.metadata, substr(d.content, 1, 2000), d.created_at
		FROM memories m LEFT JOIN documents d ON d.id = m.source_document_id
		WHERE m.workspace_id = ? AND m.container = ? AND m.valid_to IS NULL AND m.kind = 'dynamic'`, workspaceID, container)
	if err != nil {
		return nil, err
	}
	var dyns []dyn
	for rows.Next() {
		var fact string
		var created int64
		var ev, due, docID, st, meta, cnt sql.NullString
		var docCreated sql.NullInt64
		if err := rows.Scan(&fact, &created, &ev, &due, &docID, &st, &meta, &cnt, &docCreated); err != nil {
			rows.Close()
			return nil, err
		}
		document := dates.ISODate(created)
		if docID.Valid {
			document = DocumentDate(DocInfo{Metadata: model.JSONObject(meta), CreatedAt: docCreated.Int64, SourceType: st.String, Content: model.Str(cnt)})
		}
		dyns = append(dyns, dyn{fact: fact, asOf: FactAsOf(model.Str(ev), model.Str(due), document, today), createdAt: created})
	}
	rows.Close()
	sort.SliceStable(dyns, func(i, j int) bool {
		if c := strx.LocaleCompare(dyns[i].asOf, dyns[j].asOf); c != 0 {
			return c > 0
		}
		return dyns[i].createdAt > dyns[j].createdAt
	})
	if len(dyns) > ProfileDynamicFacts {
		dyns = dyns[:ProfileDynamicFacts]
	}
	for _, x := range dyns {
		out = append(out, gateway.ProfileFact{Fact: x.fact, Kind: "dynamic", AsOf: x.asOf})
	}
	return out, nil
}

// BuildProfile writes the container's profile from its current facts (empty
// texts and no model call when there are none).
func BuildProfile(ctx context.Context, d *db.DB, workspaceID, container string, deps Deps) error {
	now := deps.Now()
	current, err := profileFacts(d, workspaceID, container, dates.ISODate(now.UnixMilli()))
	if err != nil {
		return err
	}
	var result gateway.ProfileResult
	if len(current) > 0 {
		result, _, err = gateway.ChatJSON(ctx, gateway.Request{System: gateway.ProfileSystem, Prompt: gateway.ProfilePrompt(current, &now)}, gateway.ParseProfile, deps.Do)
		if err != nil {
			return err
		}
	}
	updatedAt := nowMs()
	_, err = d.Exec(`INSERT INTO profiles (workspace_id, container, static_text, dynamic_text, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, container) DO UPDATE SET static_text = excluded.static_text, dynamic_text = excluded.dynamic_text, updated_at = excluded.updated_at`,
		workspaceID, container, result.Static, result.Dynamic, updatedAt)
	return err
}

// --- wiring ------------------------------------------------------------------------

// Handlers returns the job handlers of this package; M4 adds ask, the
// backfills, reminders and app_sync to the map.
func Handlers(d *db.DB, deps Deps) jobs.Handlers {
	return jobs.Handlers{
		ByType: map[string]jobs.Handler{
			jobs.Ask:           ask.Handler(d, deps.Ask),
			jobs.Reminders:     reminders.Handler(d, deps.Reminders),
			jobs.BackfillDates: backfill.Handler(d, deps.Do),
			jobs.AppSync:       apps.Handler(d, deps.AppSync),
			jobs.Ingest: func(ctx context.Context, j jobs.Job) (any, error) {
				return nil, Ingest(ctx, d, j.PayloadString("documentId"), deps.GmailDo)
			},
			jobs.Extract: func(ctx context.Context, j jobs.Job) (any, error) {
				return nil, Extract(ctx, d, j.PayloadString("documentId"), deps)
			},
			jobs.Profile: func(ctx context.Context, j jobs.Job) (any, error) {
				return nil, BuildProfile(ctx, d, j.WorkspaceID, j.PayloadString("container"), deps)
			},
			// One batch (one gateway call) per job, then queue the next, so asks
			// and new documents get their turn between batches of a big backfill.
			jobs.PeopleBackfill: func(ctx context.Context, j jobs.Job) (any, error) {
				container := j.PayloadString("container")
				stats, err := people.Backfill(ctx, d, search.Scope{WorkspaceID: j.WorkspaceID, Container: container}, deps.Do, 1)
				if err != nil {
					return nil, err
				}
				pending, err := people.ContainersToBackfill(d, j.WorkspaceID)
				if err != nil {
					return nil, err
				}
				more := false
				for _, p := range pending {
					more = more || p.Container == container
				}
				next := ""
				if more {
					if _, _, err := jobs.Enqueue(d, j.WorkspaceID, jobs.PeopleBackfill, jsonx.ObjectOf("container", container), nowMs()); err != nil {
						return nil, err
					}
					next = ", next batch queued"
				}
				logx.Info("[recall:people] backfilled %s/%s: %s%s", j.WorkspaceID, container, jsonx.Stringify(stats), next)
				return stats, nil
			},
		},
		OnGiveUp: func(j jobs.Job, err error) {
			if id := j.PayloadString("documentId"); id != "" {
				msg := err.Error()
				setStatus(d, id, "failed", &msg)
			}
		},
	}
}
