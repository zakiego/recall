// Package api is api.ts: the /v1 operations the REST routes and the MCP tools
// share. The workspace always comes from the caller's key or session.
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	MaxContentChars = 1_000_000
	MaxFileBytes    = 20 * 1024 * 1024
	DefaultWaitMs   = 60_000
	MaxWaitMs       = 120_000
	MaxSourceIDLen  = 200
)

// ChatMessage is one message of a chat add.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AddInput is addInput after validation. Nil pointers are undefined.
type AddInput struct {
	Container    string
	Content      *string
	Messages     []ChatMessage
	URL          *string
	Type         string
	Title        *string
	Metadata     *jsonx.Object
	SourceID     *string
	DocumentDate *string
	Wait         *bool
	WaitTimeout  *float64
}

// AddResult is what every add returns. Duplicate means source_id matched an
// existing document and nothing new was queued.
type AddResult struct {
	ID        string  `json:"id"`
	Container string  `json:"container"`
	Status    string  `json:"status"`
	SourceID  *string `json:"sourceId"`
	Duplicate bool    `json:"duplicate"`
}

func addResult(doc model.Document, dup bool) AddResult {
	return AddResult{ID: doc.ID, Container: doc.Container, Status: doc.Status, SourceID: doc.SourceID, Duplicate: dup}
}

// firstLine is the content's first line, cut to 80 units; nil when empty.
func firstLine(s string) *string {
	line := strings.SplitN(strx.Trim(s), "\n", 2)[0]
	if strx.Len16(line) > 80 {
		l := strx.Slice16(line, 77) + "..."
		return &l
	}
	if line == "" {
		return nil
	}
	return &line
}

// AddContent is addContent.
func AddContent(q db.Q, workspaceID string, in AddInput, now int64) (AddResult, error) {
	sourceType := "text"
	switch {
	case in.URL != nil:
		sourceType = "url"
	case in.Messages != nil || in.Type == "chat":
		sourceType = "chat"
	}
	title := in.Title
	if title == nil && in.URL == nil {
		switch {
		case in.Content != nil:
			title = firstLine(*in.Content)
		case len(in.Messages) > 0:
			title = firstLine(in.Messages[0].Content)
		default:
			title = firstLine("")
		}
	}
	meta := jsonx.NewMap[any]()
	if in.Metadata != nil {
		meta = in.Metadata.Clone()
	}
	if in.Messages != nil {
		list := make([]any, len(in.Messages))
		for i, m := range in.Messages {
			list[i] = jsonx.ObjectOf("role", m.Role, "content", m.Content)
		}
		meta.Set("messages", list)
	}
	if in.DocumentDate != nil && *in.DocumentDate != "" {
		// parseDate(...)!.date: MCP's looser regex lets "2026-02-30" through,
		// and reading .date of null throws.
		dd := dates.Parse(*in.DocumentDate)
		if dd == nil {
			return AddResult{}, jserr.New("TypeError", "Cannot read properties of null (reading 'date')")
		}
		meta.Set("documentDate", dd.Date)
	}
	var content *string
	if in.Messages == nil {
		content = in.Content
	}
	doc, dup, err := memories.CreateDocument(q, memories.NewDocument{
		WorkspaceID: workspaceID, Container: in.Container, SourceType: sourceType, URL: in.URL, Title: title,
		Content: content, Metadata: meta, SourceID: in.SourceID,
	}, now)
	if err != nil {
		return AddResult{}, err
	}
	return addResult(doc, dup), nil
}

// FileInput is addFile's input.
type FileInput struct {
	Container string
	Title     *string
	Metadata  *jsonx.Object
	SourceID  *string
	Name      string
	Mime      string
	Bytes     []byte
}

// AddFile is addFile.
func AddFile(q db.Q, workspaceID string, in FileInput, now int64) (AddResult, error) {
	if len(in.Bytes) > MaxFileBytes {
		return AddResult{}, httpx.New(413, "too_large", fmt.Sprintf("file is larger than %d MB", MaxFileBytes/1024/1024))
	}
	title := in.Title
	if title == nil {
		n := in.Name
		title = &n
	}
	name := in.Name
	if name == "" {
		name = "upload"
	}
	mime := in.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	doc, dup, err := memories.CreateDocument(q, memories.NewDocument{
		WorkspaceID: workspaceID, Container: in.Container, SourceType: "file", Title: title, Metadata: in.Metadata,
		File: &memories.File{Name: name, Mime: mime, Bytes: in.Bytes}, SourceID: in.SourceID,
	}, now)
	if err != nil {
		return AddResult{}, err
	}
	return addResult(doc, dup), nil
}

func settled(doc model.Document) bool { return doc.Status == "done" || doc.Status == "failed" }

// WaitForDocument resolves with the document once it is done or failed, or ok
// false on timeout, cancellation or deletion. It wakes when a job for the
// document finishes, so it never polls.
func WaitForDocument(ctx context.Context, q db.Q, workspaceID, id string, timeoutMs float64) (model.Document, bool, error) {
	type res struct {
		doc model.Document
		ok  bool
	}
	ch := make(chan res, 1)
	send := func(r res) {
		select {
		case ch <- r:
		default:
		}
	}
	// Listen before the first read, so a job that finishes in between is
	// still heard (the TS code reads and subscribes in one tick).
	off := jobs.OnJobFinished(func(j jobs.Job) {
		if j.PayloadString("documentId") != id {
			return
		}
		d, found, err := memories.GetDocument(q, workspaceID, id)
		if err != nil || !found {
			send(res{})
			return
		}
		if settled(d) {
			send(res{d, true})
		}
	})
	defer off()
	doc, found, err := memories.GetDocument(q, workspaceID, id)
	if err != nil || !found {
		return doc, false, err
	}
	if settled(doc) {
		return doc, true, nil
	}
	if timeoutMs <= 0 || ctx.Err() != nil {
		return doc, false, nil
	}
	t := time.NewTimer(time.Duration(timeoutMs * float64(time.Millisecond)))
	defer t.Stop()
	select {
	case r := <-ch:
		return r.doc, r.ok, nil
	case <-t.C:
		return doc, false, nil
	case <-ctx.Done():
		return doc, false, nil
	}
}

// FactView is a memory as waitForAdd shows it.
type FactView struct {
	ID             string  `json:"id"`
	Fact           string  `json:"fact"`
	Keywords       string  `json:"keywords"`
	Kind           string  `json:"kind"`
	SupersedesID   *string `json:"supersedesId"`
	ReviewNote     *string `json:"reviewNote"`
	EventDate      *string `json:"eventDate"`
	EventPrecision *string `json:"eventPrecision"`
	DueDate        *string `json:"dueDate"`
	DuePrecision   *string `json:"duePrecision"`
}

// WaitResult is waitForAdd's result: the add result plus, once settled, what
// the document wrote. Before that only Settled false is added.
type WaitResult struct {
	AddResult
	Settled     bool
	Error       *string
	Added       []FactView
	Updated     []FactView
	Invalidated float64
}

// MarshalJSON writes the TS object: the add result's keys, then settled, and
// the rest only once settled.
func (w WaitResult) MarshalJSON() ([]byte, error) {
	o := jsonx.ObjectOf("id", w.ID, "container", w.Container, "status", w.Status, "sourceId", w.SourceID, "duplicate", w.Duplicate, "settled", w.Settled)
	if w.Settled {
		o.Set("error", w.Error)
		o.Set("added", w.Added)
		o.Set("updated", w.Updated)
		o.Set("invalidated", w.Invalidated)
	}
	return jsonx.Marshal(o)
}

// WaitForAdd waits for an add's document and reports its facts.
func WaitForAdd(ctx context.Context, q db.Q, workspaceID string, res AddResult, timeoutMs float64) (WaitResult, error) {
	doc, ok, err := WaitForDocument(ctx, q, workspaceID, res.ID, min(timeoutMs, MaxWaitMs))
	if err != nil {
		return WaitResult{}, err
	}
	if !ok {
		out := WaitResult{AddResult: res}
		if cur, found, err := memories.GetDocument(q, workspaceID, res.ID); err == nil && found {
			out.Status = cur.Status
		}
		return out, nil
	}
	facts, err := memories.FromDocument(q, workspaceID, doc.ID)
	if err != nil {
		return WaitResult{}, err
	}
	out := WaitResult{AddResult: res, Settled: true, Error: doc.Error, Added: []FactView{}, Updated: []FactView{}}
	out.Status = doc.Status
	for _, m := range facts {
		v := FactView{ID: m.ID, Fact: m.Fact, Keywords: m.Keywords, Kind: m.Kind, SupersedesID: m.SupersedesID, ReviewNote: m.ReviewNote,
			EventDate: m.EventDate, EventPrecision: m.EventPrecision, DueDate: m.DueDate, DuePrecision: m.DuePrecision}
		if m.SupersedesID != nil && *m.SupersedesID != "" {
			out.Updated = append(out.Updated, v)
		} else {
			out.Added = append(out.Added, v)
		}
	}
	if doc.Metadata != nil {
		if ex, ok := doc.Metadata.Get("extract"); ok {
			if o, ok := ex.(*jsonx.Object); ok {
				if n, ok := o.Get("invalidated"); ok {
					if f, ok := n.(float64); ok {
						out.Invalidated = f
					}
				}
			}
		}
	}
	return out, nil
}

// PatchInput is patchInput after validation; nil and unset fields are
// undefined.
type PatchInput struct {
	Fact     *string
	Keywords *string
	Kind     *string
	Event    memories.DateValue
	Due      memories.DateValue
	Resolve  bool
}

// PatchResult is patchMemory's result.
type PatchResult struct {
	Memory model.Memory `json:"memory"`
}

func notCurrent() error {
	return httpx.New(409, "not_current", "this memory was superseded or deleted; edit the current version")
}

// PatchMemory is patchMemory: an edit writes a new version; a patch with only
// resolve settles the conflict note.
func PatchMemory(d *db.DB, workspaceID, id string, in PatchInput, now func() int64) (PatchResult, error) {
	existing, found, err := memories.GetMemory(d, workspaceID, id)
	if err != nil {
		return PatchResult{}, err
	}
	if !found {
		return PatchResult{}, httpx.New(404, "not_found", "no such memory")
	}
	if existing.ValidTo != nil {
		return PatchResult{}, notCurrent()
	}
	if in.Fact == nil && in.Keywords == nil && in.Kind == nil && !in.Event.Set && !in.Due.Set {
		if in.Resolve {
			r, ok, err := memories.ResolveConflict(d, workspaceID, existing.ID)
			if err != nil {
				return PatchResult{}, err
			}
			if ok {
				return PatchResult{Memory: r}, nil
			}
		}
		return PatchResult{Memory: existing}, nil
	}
	// Not ok: it was superseded or forgotten while the new version was embedded.
	m, ok, err := memories.EditMemory(d, workspaceID, existing.ID, memories.Patch{Fact: in.Fact, Keywords: in.Keywords, Kind: in.Kind, Event: in.Event, Due: in.Due}, now)
	if err != nil {
		return PatchResult{}, err
	}
	if !ok {
		return PatchResult{}, notCurrent()
	}
	return PatchResult{Memory: m}, nil
}
