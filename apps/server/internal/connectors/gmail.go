package connectors

// gmail.ts: stored connections, search, import and the ingest step that
// reads a message. A connection belongs to one user in one workspace, and
// only that user's requests may use it. Mail content never goes to a log or
// an error message: errors name the step and the HTTP status only.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	MaxSearchResults = 20
	MaxImport        = 50
	// fakeSecret is only ever used with RECALL_GMAIL_FAKE in a dev build.
	fakeSecret = "recall-dev-only-fake-connector-secret-not-for-production"
)

// MessageID is what a Gmail message id looks like.
var MessageID = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

// Configured is connectorsConfigured.
func Configured() bool {
	c := config.Load()
	if c.GmailFake {
		return true
	}
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" && strx.Len16(c.ConnectorSecret) >= MinSecretLength
}

// MissingKeys is missingKeys: the env keys that aren't set, by name.
func MissingKeys() []string {
	c := config.Load()
	out := []string{}
	if c.GmailFake {
		return out
	}
	if c.GoogleClientID == "" {
		out = append(out, "GOOGLE_CLIENT_ID")
	}
	if c.GoogleClientSecret == "" {
		out = append(out, "GOOGLE_CLIENT_SECRET")
	}
	if strx.Len16(c.ConnectorSecret) < MinSecretLength {
		out = append(out, "CONNECTOR_SECRET")
	}
	return out
}

// Secret is connectorSecret.
func Secret() string {
	c := config.Load()
	if c.GmailFake && strx.Len16(c.ConnectorSecret) < MinSecretLength {
		return fakeSecret
	}
	return c.ConnectorSecret
}

var (
	fakeOnce sync.Once
	fakeDo   Do
)

// GoogleDo is googleFetch: the real client, or the fixture fake in a dev
// build with RECALL_GMAIL_FAKE=1 (RECALL_GMAIL_FAKE_DATA=worst puts the
// worst-case fixtures first).
func GoogleDo() Do {
	if !config.Load().GmailFake {
		return RealDo
	}
	fakeOnce.Do(func() {
		f := Fixtures()
		msgs := f.Messages
		if os.Getenv("RECALL_GMAIL_FAKE_DATA") == "worst" {
			msgs = append(append([]GmailMessage{}, f.Worst...), f.Messages...)
		}
		fakeDo = NewFakeGoogle(FakeOptions{Messages: msgs}).Do
	})
	return fakeDo
}

// ConnectorError is ConnectorError; Code is "not_configured",
// "not_connected", "reauth" or "invalid".
type ConnectorError struct {
	Msg  string
	Code string
}

func (e *ConnectorError) Error() string  { return e.Msg }
func (e *ConnectorError) JSName() string { return "ConnectorError" }

// --- stored connections ------------------------------------------------------------------

// Account is a row of connector_accounts.
type Account struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspaceId"`
	UserID       string  `json:"userId"`
	Provider     string  `json:"provider"`
	AccountEmail *string `json:"accountEmail"`
	Scope        string  `json:"scope"`
	Tokens       string  `json:"tokens"`
	Status       string  `json:"status"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
	LastUsedAt   *int64  `json:"lastUsedAt"`
}

const accountCols = "id, workspace_id, user_id, provider, account_email, scope, tokens, status, created_at, updated_at, last_used_at"

func scanAccount(s model.Scanner) (Account, error) {
	var a Account
	var email sql.NullString
	var last sql.NullInt64
	err := s.Scan(&a.ID, &a.WorkspaceID, &a.UserID, &a.Provider, &email, &a.Scope, &a.Tokens, &a.Status, &a.CreatedAt, &a.UpdatedAt, &last)
	a.AccountEmail, a.LastUsedAt = model.Str(email), model.Int(last)
	return a, err
}

func tokenContext(id, workspaceID, userID string) string {
	return "connector:" + id + ":" + workspaceID + ":" + userID
}

func sealTokens(id, workspaceID, userID string, t Tokens) (string, error) {
	return Seal(Secret(), jsonx.Stringify(t), tokenContext(id, workspaceID, userID))
}

func openTokens(a Account) (Tokens, error) {
	plain, err := Unseal(Secret(), a.Tokens, tokenContext(a.ID, a.WorkspaceID, a.UserID))
	if err != nil {
		return Tokens{}, err
	}
	var t Tokens
	if err := json.Unmarshal([]byte(plain), &t); err != nil {
		return Tokens{}, &jsonSyntaxError{err.Error()}
	}
	return t, nil
}

type jsonSyntaxError struct{ msg string }

func (e *jsonSyntaxError) Error() string  { return e.msg }
func (e *jsonSyntaxError) JSName() string { return "SyntaxError" }

// GetConnection is getConnection.
func GetConnection(q db.Q, workspaceID, userID string) (Account, bool, error) {
	a, err := scanAccount(q.QueryRow(`SELECT `+accountCols+` FROM connector_accounts WHERE workspace_id = ? AND user_id = ? AND provider = 'gmail'`, workspaceID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	return a, err == nil, err
}

func accountByID(q db.Q, id string) (Account, bool, error) {
	a, err := scanAccount(q.QueryRow(`SELECT `+accountCols+` FROM connector_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	return a, err == nil, err
}

// NewConnection is saveConnection's input.
type NewConnection struct {
	WorkspaceID, UserID string
	Email               *string
	Scope               string
	Tokens              Tokens
	Now                 int64
}

// SaveConnection is saveConnection: creates or replaces the user's
// connection, and returns the refresh token it replaced ("" for none) so the
// caller can revoke it at Google.
func SaveConnection(d *db.DB, in NewConnection) (Account, string, error) {
	var row Account
	replaced := ""
	err := d.Tx(func(tx db.Q) error {
		old, found, err := GetConnection(tx, in.WorkspaceID, in.UserID)
		if err != nil {
			return err
		}
		if found {
			if prev, err := openTokens(old); err == nil && prev.RefreshToken != in.Tokens.RefreshToken {
				replaced = prev.RefreshToken
			}
			if _, err := tx.Exec(`DELETE FROM connector_accounts WHERE id = ?`, old.ID); err != nil {
				return err
			}
		}
		id := ids.New(ids.Connector)
		sealed, err := sealTokens(id, in.WorkspaceID, in.UserID, in.Tokens)
		if err != nil {
			return err
		}
		scope := in.Scope
		if scope == "" {
			scope = GmailScope
		}
		row, err = scanAccount(tx.WQueryRow(`INSERT INTO connector_accounts (id, workspace_id, user_id, provider, account_email, scope, tokens, status, created_at, updated_at)
			VALUES (?, ?, ?, 'gmail', ?, ?, ?, 'active', ?, ?) RETURNING `+accountCols, id, in.WorkspaceID, in.UserID, in.Email, scope, sealed, in.Now, in.Now))
		return err
	})
	return row, replaced, err
}

// Connection is one row of listConnections.
type Connection struct {
	ID           string  `json:"id"`
	UserID       string  `json:"userId"`
	Username     *string `json:"username"`
	Status       string  `json:"status"`
	CreatedAt    int64   `json:"createdAt"`
	LastUsedAt   *int64  `json:"lastUsedAt"`
	Mine         bool    `json:"mine"`
	AccountEmail *string `json:"accountEmail"`
}

// ListConnections is listConnections: who connected Gmail in the
// workspace, newest first; the Google address only on the viewer's own row.
func ListConnections(q db.Q, workspaceID, viewerID string) ([]Connection, error) {
	rows, err := q.Query(`SELECT c.id, c.user_id, u.display_username, u.name, c.account_email, c.status, c.created_at, c.last_used_at
		FROM connector_accounts c JOIN user u ON u.id = c.user_id WHERE c.workspace_id = ? AND c.provider = 'gmail' ORDER BY c.created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		var c Connection
		var display, email sql.NullString
		var name string
		var last sql.NullInt64
		if err := rows.Scan(&c.ID, &c.UserID, &display, &name, &email, &c.Status, &c.CreatedAt, &last); err != nil {
			return nil, err
		}
		c.Username = &name
		if display.Valid && display.String != "" {
			c.Username = &display.String
		}
		c.LastUsedAt = model.Int(last)
		c.Mine = c.UserID == viewerID
		if c.Mine {
			c.AccountEmail = model.Str(email)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func requireConnection(q db.Q, workspaceID, userID string) (Account, error) {
	if !Configured() {
		return Account{}, &ConnectorError{"Gmail isn't set up on this server.", "not_configured"}
	}
	row, found, err := GetConnection(q, workspaceID, userID)
	if err != nil {
		return Account{}, err
	}
	if !found {
		return Account{}, &ConnectorError{"Connect Gmail first.", "not_connected"}
	}
	if row.Status == "reauth" {
		return Account{}, &ConnectorError{"Gmail access was revoked or expired. Connect again.", "reauth"}
	}
	return row, nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

// AccessToken is accessToken: a valid access token for the row, refreshed
// (and sealed again) when it is about to expire. A refused refresh marks the
// row for reconnecting.
func AccessToken(ctx context.Context, d *db.DB, row Account, do Do, now int64, force bool) (string, error) {
	t, err := openTokens(row)
	if err != nil {
		return "", err
	}
	if !force && t.ExpiresAt-60_000 > float64(now) {
		return t.AccessToken, nil
	}
	fresh, err := RefreshAccessToken(ctx, do, t.RefreshToken, now)
	if err != nil {
		var ge *GoogleError
		if errors.As(err, &ge) && ge.Code == "reauth" {
			if _, err := d.Exec(`UPDATE connector_accounts SET status = 'reauth', updated_at = ? WHERE id = ?`, now, row.ID); err != nil {
				return "", err
			}
			return "", &ConnectorError{"Gmail access was revoked or expired. Connect again.", "reauth"}
		}
		return "", err
	}
	sealed, err := sealTokens(row.ID, row.WorkspaceID, row.UserID, fresh)
	if err != nil {
		return "", err
	}
	if _, err := d.Exec(`UPDATE connector_accounts SET tokens = ?, updated_at = ? WHERE id = ?`, sealed, now, row.ID); err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}

// withToken runs a Gmail call with the row's token. A 401 gets one forced
// refresh; a second 401 means the grant is gone.
func withToken[T any](ctx context.Context, d *db.DB, row Account, do Do, fn func(token string) (T, error)) (T, error) {
	var zero T
	token, err := AccessToken(ctx, d, row, do, nowMs(), false)
	if err != nil {
		return zero, err
	}
	defer func() { d.Exec(`UPDATE connector_accounts SET last_used_at = ? WHERE id = ?`, nowMs(), row.ID) }()
	v, err := fn(token)
	var ge *GoogleError
	if err == nil || !errors.As(err, &ge) || ge.Code != "reauth" {
		return v, err
	}
	current, found, err := accountByID(d, row.ID)
	if err != nil {
		return zero, err
	}
	if !found {
		current = row
	}
	token, err = AccessToken(ctx, d, current, do, nowMs(), true)
	if err != nil {
		return zero, err
	}
	return fn(token)
}

// DisconnectResult is disconnect's result.
type DisconnectResult struct {
	Removed bool `json:"removed"`
	Revoked bool `json:"revoked"`
}

// Disconnect is disconnect: the user's connection is deleted here and
// revoked at Google; Revoked false when Google couldn't confirm.
func Disconnect(ctx context.Context, d *db.DB, workspaceID, userID string, do Do) (DisconnectResult, error) {
	row, found, err := GetConnection(d, workspaceID, userID)
	if err != nil {
		return DisconnectResult{}, err
	}
	if !found {
		return DisconnectResult{Removed: false, Revoked: true}, nil
	}
	token := ""
	if t, err := openTokens(row); err == nil {
		token = t.RefreshToken
	}
	if _, err := d.Exec(`DELETE FROM connector_accounts WHERE id = ?`, row.ID); err != nil {
		return DisconnectResult{}, err
	}
	revoked := false
	if token != "" {
		revoked = RevokeToken(ctx, do, token)
	}
	return DisconnectResult{Removed: true, Revoked: revoked}, nil
}

// --- search ------------------------------------------------------------------------------

// Result is GmailResult: Imported means already a document in this
// container; ImportFailed that the document failed.
type Result struct {
	ID           string   `json:"id"`
	ThreadID     *string  `json:"threadId"`
	From         string   `json:"from"`
	Subject      string   `json:"subject"`
	Snippet      string   `json:"snippet"`
	Date         *float64 `json:"date"`
	Imported     bool     `json:"imported"`
	ImportFailed bool     `json:"importFailed"`
}

// SearchResult is searchGmail's result.
type SearchResult struct {
	Messages      []Result `json:"messages"`
	NextPageToken *string  `json:"nextPageToken"`
	Estimate      float64  `json:"estimate"`
}

// SearchInput is searchGmail's input.
type SearchInput struct {
	WorkspaceID, UserID, Container, Q, PageToken string
}

// Search is searchGmail.
func Search(ctx context.Context, d *db.DB, in SearchInput, do Do) (SearchResult, error) {
	row, err := requireConnection(d, in.WorkspaceID, in.UserID)
	if err != nil {
		return SearchResult{}, err
	}
	return withToken(ctx, d, row, do, func(token string) (SearchResult, error) {
		list, err := ListMessages(ctx, do, token, in.Q, in.PageToken, MaxSearchResults)
		if err != nil {
			return SearchResult{}, err
		}
		idList := make([]string, len(list.Messages))
		for i, m := range list.Messages {
			idList[i] = m.ID
		}
		metas, err := mapLimit(idList, 8, func(id string) (GmailMessage, error) { return GetMessageMetadata(ctx, do, token, id) })
		if err != nil {
			return SearchResult{}, err
		}
		imported, err := importedIDs(d, in.WorkspaceID, in.Container, idList)
		if err != nil {
			return SearchResult{}, err
		}
		out := SearchResult{Messages: []Result{}, NextPageToken: list.NextPageToken, Estimate: float64(len(idList))}
		if list.ResultSizeEstimate != nil {
			out.Estimate = *list.ResultSizeEstimate
		}
		for _, m := range metas {
			r := Result{ID: m.ID, ThreadID: m.ThreadID, From: headerOr(m.Payload, "From"), Subject: headerOr(m.Payload, "Subject"), Snippet: DecodeEntities(s(m.Snippet))}
			if m.InternalDate != nil && *m.InternalDate != "" {
				// Number("abc") is NaN, which JSON.stringify writes as null.
				if n := strx.Number(*m.InternalDate); !math.IsNaN(n) {
					r.Date = &n
				}
			}
			status, ok := imported[m.ID]
			r.Imported, r.ImportFailed = ok, status == "failed"
			out.Messages = append(out.Messages, r)
		}
		return out, nil
	})
}

// mapLimit is mapLimit: limit workers take the next item in order. Like
// Promise.all, the first error in time is returned at once; the other
// workers carry on in the background, as they do in TS.
func mapLimit[T, R any](items []T, limit int, fn func(T) (R, error)) ([]R, error) {
	out := make([]R, len(items))
	var mu sync.Mutex
	next := 0
	failed := make(chan error, 1)
	var wg sync.WaitGroup
	for w := 0; w < min(limit, len(items)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if next >= len(items) {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				r, err := fn(items[i])
				if err != nil {
					select {
					case failed <- err:
					default:
					}
					return
				}
				mu.Lock()
				out[i] = r
				mu.Unlock()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case err := <-failed:
		return nil, err
	case <-done:
		select {
		case err := <-failed:
			return nil, err
		default:
		}
		mu.Lock()
		defer mu.Unlock()
		return out, nil
	}
}

// EmailSourceID is emailSourceId.
func EmailSourceID(messageID string) string { return "gmail:" + messageID }

// AttachmentSourceID is attachmentSourceId: Gmail's attachmentId changes
// between fetches, so the stable part id identifies an attachment.
func AttachmentSourceID(messageID, partID string) string { return "gmail:" + messageID + ":" + partID }

func importedIDs(q db.Q, workspaceID, container string, msgIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(msgIDs) == 0 {
		return out, nil
	}
	args := []any{workspaceID, container}
	for _, id := range msgIDs {
		args = append(args, EmailSourceID(id))
	}
	// Only real imports count; a text add that borrowed a gmail: source_id doesn't.
	rows, err := q.Query(`SELECT source_id, status FROM documents WHERE workspace_id = ? AND container = ? AND source_type = 'email' AND source_id IN (`+
		strings.TrimSuffix(strings.Repeat("?, ", len(msgIDs)), ", ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, status string
		if err := rows.Scan(&sid, &status); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(sid, "gmail:")] = status
	}
	return out, rows.Err()
}

// --- import ------------------------------------------------------------------------------

// ImportMessage is one message to import; Subject nil when unknown.
type ImportMessage struct {
	ID      string
	Subject *string
}

// ImportedDoc is one document importGmail made or found.
type ImportedDoc struct {
	ID        string `json:"id"`
	MessageID string `json:"messageId"`
	Duplicate bool   `json:"duplicate"`
}

// ImportResult is importGmail's result.
type ImportResult struct {
	Queued     int           `json:"queued"`
	Duplicates int           `json:"duplicates"`
	Documents  []ImportedDoc `json:"documents"`
}

// ImportInput is importGmail's input.
type ImportInput struct {
	WorkspaceID, UserID, Container string
	Messages                       []ImportMessage
}

// Import is importGmail: one email document per message, read later by the
// ingest job. A message already imported into the container is a no-op,
// unless that import failed: then it is queued again with the current
// connection.
func Import(d *db.DB, in ImportInput) (ImportResult, error) {
	row, err := requireConnection(d, in.WorkspaceID, in.UserID)
	if err != nil {
		return ImportResult{}, err
	}
	if len(in.Messages) > MaxImport {
		return ImportResult{}, &ConnectorError{fmt.Sprintf("Import up to %d messages at a time.", MaxImport), "invalid"}
	}
	res := ImportResult{Documents: []ImportedDoc{}}
	for _, m := range in.Messages {
		if !MessageID.MatchString(m.ID) {
			return res, &ConnectorError{"That isn't a Gmail message id.", "invalid"}
		}
		var title *string
		if m.Subject != nil && *m.Subject != "" {
			t := strx.Slice16(*m.Subject, 300)
			title = &t
		}
		sid := EmailSourceID(m.ID)
		meta := jsonx.ObjectOf("connector", "gmail", "messageId", m.ID, "connectionId", row.ID, "userId", in.UserID, "account", row.AccountEmail)
		doc, dup, err := memories.CreateDocument(d, memories.NewDocument{WorkspaceID: in.WorkspaceID, Container: in.Container, SourceType: "email", SourceID: &sid, Title: title, Metadata: meta}, nowMs())
		if err != nil {
			return res, err
		}
		if dup && doc.Status == "failed" && doc.SourceType == "email" {
			next := jsonx.NewMap[any]()
			if doc.Metadata != nil {
				next = doc.Metadata.Clone()
			}
			next.Set("connectionId", row.ID)
			next.Set("userId", in.UserID)
			next.Set("account", row.AccountEmail)
			if _, err := d.Exec(`UPDATE documents SET status = 'queued', error = NULL, metadata = ? WHERE id = ?`, jsonx.Stringify(next), doc.ID); err != nil {
				return res, err
			}
			if _, _, err := jobs.Enqueue(d, in.WorkspaceID, jobs.Ingest, jsonx.ObjectOf("documentId", doc.ID), nowMs()); err != nil {
				return res, err
			}
			res.Documents = append(res.Documents, ImportedDoc{doc.ID, m.ID, false})
			res.Queued++
			continue
		}
		res.Documents = append(res.Documents, ImportedDoc{doc.ID, m.ID, dup})
		if dup {
			res.Duplicates++
		} else {
			res.Queued++
		}
	}
	return res, nil
}

func metaStr(m *jsonx.Object, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m.Get(k)
	s, _ := v.(string)
	return s
}

func plainErr(msg string) error { return errors.New(msg) }

// ResolveEmail is resolveEmail: the ingest step for an email document. It
// reads the message with the stored connection, returns its text, and
// queues each PDF attachment as its own file document.
func ResolveEmail(ctx context.Context, d *db.DB, doc model.Document, do Do) (*string, string, *jsonx.Object, error) {
	meta := doc.Metadata
	messageID, connectionID, userID := metaStr(meta, "messageId"), metaStr(meta, "connectionId"), metaStr(meta, "userId")
	if messageID == "" || connectionID == "" {
		return nil, "", nil, plainErr("email document has no Gmail message reference")
	}
	// A reconnect replaces the row, so fall back to the same user's current
	// connection in this workspace, only while it's the same Google account.
	row, found, err := accountByID(d, connectionID)
	if err != nil {
		return nil, "", nil, err
	}
	if !found && userID != "" {
		current, ok, err := GetConnection(d, doc.WorkspaceID, userID)
		if err != nil {
			return nil, "", nil, err
		}
		var account any
		if meta != nil {
			account, _ = meta.Get("account")
		}
		if ok && current.AccountEmail != nil && *current.AccountEmail != "" {
			if a, isStr := account.(string); isStr && a == *current.AccountEmail {
				row, found = current, true
			}
		}
	}
	// The connection must still exist and belong to this document's workspace.
	if !found || row.WorkspaceID != doc.WorkspaceID {
		return nil, "", nil, plainErr("the Gmail connection used for this import was removed")
	}
	if row.Status == "reauth" {
		return nil, "", nil, plainErr("Gmail access was revoked or expired. Connect again, then retry")
	}
	if !Configured() {
		return nil, "", nil, plainErr("Gmail isn't set up on this server")
	}
	type out struct {
		title *string
		text  string
		meta  *jsonx.Object
	}
	r, err := withToken(ctx, d, row, do, func(token string) (out, error) {
		msg, err := GetMessage(ctx, do, token, messageID)
		if err != nil {
			return out{}, err
		}
		parsed, err := ParseMessage(msg)
		if err != nil {
			return out{}, err
		}
		attachments, skipped := 0, 0
		for _, pdf := range parsed.Pdfs {
			if pdf.Size > api.MaxFileBytes {
				skipped++
				continue
			}
			sid := AttachmentSourceID(messageID, pdf.PartID)
			if _, exists, err := memories.FindBySourceID(d, doc.WorkspaceID, doc.Container, sid); err != nil {
				return out{}, err
			} else if exists {
				attachments++
				continue
			}
			var b []byte
			if pdf.AttachmentID != nil && *pdf.AttachmentID != "" {
				if b, err = GetAttachment(ctx, do, token, messageID, *pdf.AttachmentID); err != nil {
					return out{}, err
				}
			} else {
				b = DecodeBase64URL(s(pdf.InlineData))
			}
			if len(b) > api.MaxFileBytes {
				skipped++
				continue
			}
			name := pdf.Filename
			if _, _, err := memories.CreateDocument(d, memories.NewDocument{WorkspaceID: doc.WorkspaceID, Container: doc.Container, SourceType: "file", SourceID: &sid, Title: &name,
				Metadata: jsonx.ObjectOf("connector", "gmail", "messageId", messageID, "emailDocumentId", doc.ID),
				File:     &memories.File{Name: name, Mime: "application/pdf", Bytes: b}}, nowMs()); err != nil {
				return out{}, err
			}
			attachments++
		}
		title := "(no subject)"
		if parsed.Subject != nil && *parsed.Subject != "" {
			title = *parsed.Subject
		}
		m := jsonx.ObjectOf("attachments", attachments)
		if skipped > 0 {
			m.Set("attachmentsSkipped", skipped)
		}
		return out{&title, parsed.Text, m}, nil
	})
	return r.title, r.text, r.meta, err
}

// MayRetry is mayRetry: retrying an email whose text isn't stored yet
// reads Gmail with the importer's grant, so only the importer may do it.
func MayRetry(sourceType string, content *string, metadata *jsonx.Object, userID string) bool {
	if sourceType != "email" || (content != nil && strx.Trim(*content) != "") {
		return true
	}
	if metadata == nil {
		return false
	}
	v, ok := metadata.Get("userId")
	s, isStr := v.(string)
	return ok && isStr && s == userID
}

// ImportView is one row of listGmailImports.
type ImportView struct {
	ID         string  `json:"id"`
	SourceType string  `json:"sourceType"`
	SourceID   *string `json:"sourceId"`
	Title      *string `json:"title"`
	Status     string  `json:"status"`
	Error      *string `json:"error"`
	CreatedAt  int64   `json:"createdAt"`
	CanRetry   bool    `json:"canRetry"`
}

// ListImports is listGmailImports: recent Gmail imports (emails and their
// attachments) in a container, newest first. limit <= 0 is 30.
func ListImports(q db.Q, workspaceID, container, viewerID string, limit int) ([]ImportView, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := q.Query(`SELECT id, source_type, source_id, title, status, error, created_at, substr(content, 1, 1), metadata FROM documents
		WHERE workspace_id = ? AND container = ? AND source_type IN ('email', 'file') AND source_id LIKE 'gmail:%' ORDER BY created_at DESC LIMIT ?`, workspaceID, container, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportView{}
	for rows.Next() {
		var v ImportView
		var sid, title, errs, content, meta sql.NullString
		if err := rows.Scan(&v.ID, &v.SourceType, &sid, &title, &v.Status, &errs, &v.CreatedAt, &content, &meta); err != nil {
			return nil, err
		}
		v.SourceID, v.Title, v.Error = model.Str(sid), model.Str(title), model.Str(errs)
		v.CanRetry = MayRetry(v.SourceType, model.Str(content), model.JSONObject(meta), viewerID)
		out = append(out, v)
	}
	return out, rows.Err()
}
