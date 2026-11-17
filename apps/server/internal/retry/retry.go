// Package retry requeues a document for ingest. The dashboard's
// retryDocument RPC and the MCP retry_document tool both go through it.
package retry

import (
	"errors"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
)

var (
	// ErrNoDocument means the workspace has no document with that id.
	ErrNoDocument = errors.New("No such document.")
	// ErrNotImporter means the document is an email whose body has to be
	// fetched again with the importer's Gmail access, and the caller isn't
	// that person.
	ErrNotImporter = errors.New("Only the person who imported this email can retry it.")
)

// NotFailedError is a FailedOnly retry of a document that isn't failed.
type NotFailedError struct{ Status string }

func (e *NotFailedError) Error() string {
	return "Only failed documents can be retried. This one is " + e.Status + "."
}

// Options picks how strict Document is.
type Options struct {
	// UserID is the caller, for connectors.MayRetry. Empty means nobody, so
	// emails that need the importer's Gmail access are refused.
	UserID string
	// FailedOnly refuses a document whose status isn't failed. The
	// dashboard leaves it off; MCP turns it on.
	FailedOnly bool
}

// Document marks the workspace's document queued, clears its error and
// enqueues an ingest job for it.
func Document(q db.Q, workspaceID, id string, opt Options, now int64) error {
	doc, ok, err := memories.GetDocument(q, workspaceID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoDocument
	}
	if opt.FailedOnly && doc.Status != "failed" {
		return &NotFailedError{Status: doc.Status}
	}
	if !connectors.MayRetry(doc.SourceType, doc.Content, doc.Metadata, opt.UserID) {
		return ErrNotImporter
	}
	update := `UPDATE documents SET status = 'queued', error = NULL WHERE id = ? AND workspace_id = ?`
	if opt.FailedOnly {
		// Two retries racing would otherwise both enqueue.
		update += ` AND status = 'failed'`
	}
	res, err := q.Exec(update, doc.ID, workspaceID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 && opt.FailedOnly {
		return &NotFailedError{Status: "queued"}
	}
	_, _, err = jobs.Enqueue(q, workspaceID, "ingest", jsonx.ObjectOf("documentId", doc.ID), now)
	return err
}
