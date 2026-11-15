package apps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Defaults are APP_DEFAULTS.
var Defaults = map[string]struct{ Name, BaseURL string }{
	"oslo":      {"Oslo", "https://todo.zakiego.com"},
	"finansial": {"Finansial", "https://finansial.zakiego.com"},
}

const (
	// BackfillDays: the first sync only reaches this far back.
	BackfillDays  = 30
	MaxAPIKeyLen  = 500
	syncJobLabel  = "app_sync"
	apiKeyContext = "app-connector:"
)

// Error is AppConnectorError; Code is "not_configured", "invalid", "busy"
// or "not_found".
type Error struct {
	Msg  string
	Code string
}

func (e *Error) Error() string  { return e.Msg }
func (e *Error) JSName() string { return "AppConnectorError" }

// SecretConfigured is appSecretConfigured.
func SecretConfigured() bool { return strx.Len16(connectors.Secret()) >= connectors.MinSecretLength }

// Connector is a row of app_connectors.
type Connector struct {
	ID            string        `json:"id"`
	WorkspaceID   string        `json:"workspaceId"`
	Provider      string        `json:"provider"`
	Container     string        `json:"container"`
	BaseURL       string        `json:"baseUrl"`
	APIKey        *string       `json:"apiKey"`
	Enabled       bool          `json:"enabled"`
	Cursor        *string       `json:"cursor"`
	CaughtUpAt    *int64        `json:"caughtUpAt"`
	LastSyncAt    *int64        `json:"lastSyncAt"`
	LastSuccessAt *int64        `json:"lastSuccessAt"`
	LastError     *string       `json:"lastError"`
	Settings      *jsonx.Object `json:"settings"`
	CreatedBy     *string       `json:"createdBy"`
	CreatedAt     int64         `json:"createdAt"`
	UpdatedAt     int64         `json:"updatedAt"`
}

const connectorCols = "id, workspace_id, provider, container, base_url, api_key, enabled, cursor, caught_up_at, last_sync_at, last_success_at, last_error, settings, created_by, created_at, updated_at"

func scanConnector(s model.Scanner) (Connector, error) {
	var c Connector
	var key, cursor, lastErr, settings, by sql.NullString
	var enabled int64
	var caught, lastSync, lastOK sql.NullInt64
	err := s.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.Container, &c.BaseURL, &key, &enabled, &cursor, &caught, &lastSync, &lastOK, &lastErr, &settings, &by, &c.CreatedAt, &c.UpdatedAt)
	c.APIKey, c.Cursor, c.LastError, c.CreatedBy = model.Str(key), model.Str(cursor), model.Str(lastErr), model.Str(by)
	c.Enabled = enabled != 0
	c.CaughtUpAt, c.LastSyncAt, c.LastSuccessAt = model.Int(caught), model.Int(lastSync), model.Int(lastOK)
	c.Settings = model.JSONObject(settings)
	if c.Settings == nil {
		c.Settings = jsonx.NewMap[any]()
	}
	return c, err
}

func keyContext(id, workspaceID, provider string) string {
	return apiKeyContext + id + ":" + workspaceID + ":" + provider
}

func openKey(c Connector) (string, error) {
	if c.APIKey == nil || *c.APIKey == "" {
		return "", &Error{"Paste an API key first.", "invalid"}
	}
	return connectors.Unseal(connectors.Secret(), *c.APIKey, keyContext(c.ID, c.WorkspaceID, c.Provider))
}

// Get is getAppConnector.
func Get(q db.Q, workspaceID, provider string) (Connector, bool, error) {
	c, err := scanConnector(q.QueryRow(`SELECT `+connectorCols+` FROM app_connectors WHERE workspace_id = ? AND provider = ?`, workspaceID, provider))
	if errors.Is(err, sql.ErrNoRows) {
		return Connector{}, false, nil
	}
	return c, err == nil, err
}

// NormalizeBaseURL is normalizeBaseUrl.
func NormalizeBaseURL(input string) (string, error) {
	u, err := whatwg.ParseURL(strx.Trim(input))
	if err != nil {
		return "", &Error{"Enter the app's address, like https://todo.zakiego.com.", "invalid"}
	}
	// The key goes out as a bearer token, so plain http is only for an app on this machine.
	// url.hostname: the serialized host, so "localhost." is not local.
	local := u.Host == "localhost" || u.Host == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", &Error{"The address has to start with https://.", "invalid"}
	}
	if u.Username != "" || u.Password != "" {
		return "", &Error{"Leave the user name and password out of the address.", "invalid"}
	}
	return u.Origin() + strings.TrimRight(u.Path, "/"), nil
}

func originOf(base string) string {
	u, err := whatwg.ParseURL(base)
	if err != nil {
		return ""
	}
	return u.Origin()
}

// SaveInput is SaveAppConnector. APIKey: Set false keeps the stored key;
// Value nil or blank removes it; a value replaces it.
type SaveInput struct {
	WorkspaceID string
	Provider    string
	UserID      *string
	Container   string
	BaseURL     string
	APIKey      struct {
		Set   bool
		Value *string
	}
	Enabled  bool
	Settings *jsonx.Object
	Now      int64
}

// Save is saveAppConnector: creates or updates the workspace's connector.
// A new address starts the feed over, and a new host needs the key again.
func Save(d *db.DB, in SaveInput) (Connector, error) {
	now := in.Now
	if now == 0 {
		now = time.Now().UnixMilli()
	}
	baseURL, err := NormalizeBaseURL(in.BaseURL)
	if err != nil {
		return Connector{}, err
	}
	newKey := ""
	if in.APIKey.Set && in.APIKey.Value != nil {
		newKey = *in.APIKey.Value
	}
	if newKey != "" && strx.Len16(newKey) > MaxAPIKeyLen {
		return Connector{}, &Error{"That API key is too long.", "invalid"}
	}
	if newKey != "" && !SecretConfigured() {
		return Connector{}, &Error{"Set CONNECTOR_SECRET on the server before saving a key.", "not_configured"}
	}
	var out Connector
	err = d.Tx(func(tx db.Q) error {
		old, found, err := Get(tx, in.WorkspaceID, in.Provider)
		if err != nil {
			return err
		}
		id := ids.New(ids.Connector)
		if found {
			id = old.ID
		}
		moved := found && old.BaseURL != baseURL
		newHost := found && originOf(old.BaseURL) != originOf(baseURL)
		if newHost && !in.APIKey.Set && old.APIKey != nil && *old.APIKey != "" && in.Enabled {
			return &Error{"The address changed, so paste the API key again.", "invalid"}
		}
		var apiKey *string
		switch {
		case !in.APIKey.Set:
			if !newHost && found {
				apiKey = old.APIKey
			}
		case in.APIKey.Value == nil || strx.Trim(*in.APIKey.Value) == "":
		default:
			sealed, err := connectors.Seal(connectors.Secret(), strx.Trim(*in.APIKey.Value), keyContext(id, in.WorkspaceID, in.Provider))
			if err != nil {
				return err
			}
			apiKey = &sealed
		}
		settings := jsonx.NewMap[any]()
		if found && old.Settings != nil {
			settings = old.Settings.Clone()
		}
		if in.Settings != nil {
			settings.Assign(in.Settings)
		}
		enabled := 0
		if in.Enabled {
			enabled = 1
		}
		if found {
			set := []string{"container = ?", "base_url = ?", "api_key = ?", "enabled = ?", "settings = ?", "updated_at = ?"}
			args := []any{in.Container, baseURL, apiKey, enabled, jsonx.Stringify(settings), now}
			if moved {
				set = append(set, "cursor = NULL", "caught_up_at = NULL")
			}
			// A new key or address clears the old complaint about the old one.
			if moved || in.APIKey.Set {
				set = append(set, "last_error = NULL")
			}
			out, err = scanConnector(tx.WQueryRow(`UPDATE app_connectors SET `+strings.Join(set, ", ")+` WHERE id = ? RETURNING `+connectorCols, append(args, old.ID)...))
			return err
		}
		out, err = scanConnector(tx.WQueryRow(`INSERT INTO app_connectors (id, workspace_id, provider, container, base_url, api_key, enabled, settings, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+connectorCols, id, in.WorkspaceID, in.Provider, in.Container, baseURL, apiKey, enabled, jsonx.Stringify(settings), in.UserID, now, now))
		return err
	})
	return out, err
}

// View is AppConnectorView: what the settings page shows, never the key.
type View struct {
	Provider      string              `json:"provider"`
	Name          string              `json:"name"`
	Configured    bool                `json:"configured"`
	Container     *string             `json:"container"`
	BaseURL       string              `json:"baseUrl"`
	HasKey        bool                `json:"hasKey"`
	Enabled       bool                `json:"enabled"`
	LastSyncAt    *int64              `json:"lastSyncAt"`
	LastSuccessAt *int64              `json:"lastSuccessAt"`
	LastError     *string             `json:"lastError"`
	CaughtUp      bool                `json:"caughtUp"`
	Thresholds    *jsonx.Map[float64] `json:"thresholds"`
}

// ConnectorView is appConnectorView.
func ConnectorView(q db.Q, workspaceID, provider string) (View, error) {
	row, found, err := Get(q, workspaceID, provider)
	if err != nil {
		return View{}, err
	}
	def := Defaults[provider]
	v := View{Provider: provider, Name: def.Name, Configured: found, BaseURL: def.BaseURL}
	var settings *jsonx.Object
	if found {
		c := row.Container
		v.Container, v.BaseURL = &c, row.BaseURL
		v.HasKey = row.APIKey != nil && *row.APIKey != ""
		v.Enabled, v.LastSyncAt, v.LastSuccessAt, v.LastError = row.Enabled, row.LastSyncAt, row.LastSuccessAt, row.LastError
		v.CaughtUp = row.CaughtUpAt != nil && *row.CaughtUpAt != 0
		settings = row.Settings
	}
	if provider == "finansial" {
		v.Thresholds = FinansialSettings(settings).Thresholds
	}
	return v, nil
}

// Timezone is connectorTimezone: the connector's own setting, else the
// reminders time zone of whoever set it up, else Asia/Makassar.
func Timezone(q db.Q, c Connector) (string, error) {
	if own, ok := get(c.Settings, "timezone").(string); ok && dates.IsTimezone(own) {
		return own, nil
	}
	if c.CreatedBy != nil && *c.CreatedBy != "" {
		var tz string
		err := q.QueryRow(`SELECT timezone FROM reminder_settings WHERE user_id = ?`, *c.CreatedBy).Scan(&tz)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if tz != "" && dates.IsTimezone(tz) {
			return tz, nil
		}
	}
	return DefaultTimezone, nil
}

// --- sync ----------------------------------------------------------------------------

// SyncDeps are SyncDeps; zero fields are the real ones.
type SyncDeps struct {
	Do        connectors.Do
	Now       func() int64
	MaxPages  int
	PageLimit int
	TimeoutMs int
}

func (d SyncDeps) fill() SyncDeps {
	if d.Do == nil {
		d.Do = connectors.RealDo
	}
	if d.Now == nil {
		d.Now = func() int64 { return time.Now().UnixMilli() }
	}
	return d
}

// SyncResult is SyncResult.
type SyncResult struct {
	Provider  string  `json:"provider"`
	OK        bool    `json:"ok"`
	Pages     int     `json:"pages"`
	Items     int     `json:"items"`
	Skipped   int     `json:"skipped"`
	Staged    int     `json:"staged"`
	Documents int     `json:"documents"`
	Removed   int     `json:"removed"`
	CaughtUp  bool    `json:"caughtUp"`
	Error     *string `json:"error"`
}

var (
	runningMu sync.Mutex
	running   = map[string]bool{}
)

type docInput struct {
	sourceID, title, content, documentDate string
	metadata                               *jsonx.Object
}

func writeDoc(q db.Q, c Connector, in docInput, now int64) (bool, error) {
	meta := in.metadata.Clone()
	if in.documentDate != "" {
		meta.Set("documentDate", in.documentDate)
	}
	sid, title, content := in.sourceID, in.title, in.content
	_, dup, err := memories.CreateDocument(q, memories.NewDocument{WorkspaceID: c.WorkspaceID, Container: c.Container, SourceType: "text", SourceID: &sid, Title: &title, Content: &content, Metadata: meta}, now)
	return !dup, err
}

// syncTransaction keeps one document per Finansial transaction: a delete or
// an edit under the threshold removes it with its facts, an edit that
// changes what it says replaces it, anything else costs nothing.
// backfillFrom ("" for none) skips old transactions never seen.
func syncTransaction(q db.Q, c Connector, it ChangeItem, s Settings, backfillFrom string, now int64) (removed, written bool, err error) {
	existing, found, err := memories.FindBySourceID(q, c.WorkspaceID, c.Container, TransactionSourceID(it.ID))
	if err != nil {
		return false, false, err
	}
	doc := FinansialDocument(it, s)
	if doc == nil {
		if !found {
			return false, false, nil
		}
		removed, err = memories.DeleteDocumentAndMemories(q, c.WorkspaceID, existing.ID, now)
		return removed, false, err
	}
	if found {
		if fp, _ := get(existing.Metadata, "fingerprint").(string); fp == doc.Fingerprint {
			return false, false, nil
		}
		if removed, err = memories.DeleteDocumentAndMemories(q, c.WorkspaceID, existing.ID, now); err != nil {
			return false, false, err
		}
	} else if date, ok := FinansialEventDate(it); backfillFrom != "" && ok && date < backfillFrom {
		return false, false, nil
	}
	meta := jsonx.ObjectOf("source", "finansial", "kind", it.Kind, "id", it.ID, "url", it.URL, "fingerprint", doc.Fingerprint)
	written, err = writeDoc(q, c, docInput{doc.SourceID, doc.Title, doc.Content, doc.DocumentDate, meta}, now)
	return removed, written, err
}

// closeOsloDays writes the digest of every Oslo day that is over.
func closeOsloDays(d *db.DB, c Connector, tz string, now int64) (int, error) {
	days, err := ClosedDays(d, c, dates.TodayIn(tz, now))
	if err != nil {
		return 0, err
	}
	written := 0
	for _, day := range days {
		err := d.Tx(func(tx db.Q) error {
			events, err := DayEvents(tx, c, day)
			if err != nil {
				return err
			}
			if len(events) > 0 {
				title, content := OsloDigest(day, events, tz)
				ok, err := writeDoc(tx, c, docInput{OsloSourceID(day), title, content, day, jsonx.ObjectOf("source", "oslo", "kind", "day", "day", day, "events", len(events))}, now)
				if err != nil {
					return err
				}
				if ok {
					written++
				}
			}
			return MarkDayWritten(tx, c, day, now)
		})
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// Sync is syncAppConnector: one connector, never two at once.
func Sync(ctx context.Context, d *db.DB, workspaceID, provider string, deps SyncDeps) (SyncResult, error) {
	deps = deps.fill()
	row, found, err := Get(d, workspaceID, provider)
	if err != nil {
		return SyncResult{}, err
	}
	name := Defaults[provider].Name
	if !found {
		return SyncResult{}, &Error{"Set up " + name + " first.", "not_found"}
	}
	runningMu.Lock()
	if running[row.ID] {
		runningMu.Unlock()
		return SyncResult{}, &Error{name + " is already syncing.", "busy"}
	}
	running[row.ID] = true
	runningMu.Unlock()
	defer func() {
		runningMu.Lock()
		delete(running, row.ID)
		runningMu.Unlock()
	}()
	result := SyncResult{Provider: provider}
	if err := sync1(ctx, d, row, provider, deps, &result); err != nil {
		msg := syncErrorMessage(err)
		result.Error = &msg
		if _, uerr := d.Exec(`UPDATE app_connectors SET last_sync_at = ?, last_error = ? WHERE id = ?`, deps.Now(), msg, row.ID); uerr != nil {
			return result, uerr
		}
	}
	return result, nil
}

func sync1(ctx context.Context, d *db.DB, row Connector, provider string, deps SyncDeps, result *SyncResult) error {
	startedAt := deps.Now()
	if !SecretConfigured() {
		return &Error{"CONNECTOR_SECRET isn't set on the server.", "not_configured"}
	}
	apiKey, err := openKey(row)
	if err != nil {
		return err
	}
	tz, err := Timezone(d, row)
	if err != nil {
		return err
	}
	caughtUpBefore := row.CaughtUpAt != nil && *row.CaughtUpAt != 0
	backfillFrom := dates.AddDays(dates.TodayIn(tz, startedAt), -BackfillDays)
	settings := FinansialSettings(row.Settings)
	pulled, err := PullChanges(ctx, deps.Do, PullOptions{FetchOptions: FetchOptions{BaseURL: row.BaseURL, APIKey: apiKey, Cursor: row.Cursor, Limit: deps.PageLimit, TimeoutMs: deps.TimeoutMs}, MaxPages: deps.MaxPages},
		func(page FeedPage, cursorAfter *string) error {
			// A page's documents and the cursor after it are stored together.
			return d.Tx(func(tx db.Q) error {
				if provider == "oslo" {
					n, err := StageOsloItems(tx, row, page.Items, StageContext{Timezone: tz, CaughtUp: caughtUpBefore, BackfillFrom: backfillFrom, Now: deps.Now()})
					result.Staged += n
					if err != nil {
						return err
					}
				} else {
					for _, it := range page.Items {
						if it.Kind == "transaction" {
							from := backfillFrom
							if caughtUpBefore {
								from = ""
							}
							removed, written, err := syncTransaction(tx, row, it, settings, from, deps.Now())
							if err != nil {
								return err
							}
							if removed {
								result.Removed++
							}
							if written {
								result.Documents++
							}
							continue
						}
						// Rules and loans: a new version is a new document, so source_id dedupe is enough.
						doc := FinansialDocument(it, settings)
						if doc == nil {
							continue
						}
						ok, err := writeDoc(tx, row, docInput{doc.SourceID, doc.Title, doc.Content, doc.DocumentDate, jsonx.ObjectOf("source", "finansial", "kind", it.Kind, "id", it.ID, "url", it.URL)}, deps.Now())
						if err != nil {
							return err
						}
						if ok {
							result.Documents++
						}
					}
				}
				_, err := tx.Exec(`UPDATE app_connectors SET cursor = ? WHERE id = ?`, cursorAfter, row.ID)
				return err
			})
		})
	if err != nil {
		return err
	}
	result.Pages, result.Items, result.Skipped, result.CaughtUp = pulled.Pages, pulled.Items, pulled.Skipped, pulled.CaughtUp
	now := deps.Now()
	if provider == "oslo" && pulled.CaughtUp {
		n, err := closeOsloDays(d, row, tz, now)
		result.Documents += n
		if err != nil {
			return err
		}
	}
	set := "last_sync_at = ?, last_success_at = ?, last_error = NULL"
	args := []any{now, now}
	if pulled.CaughtUp && !caughtUpBefore {
		set += ", caught_up_at = ?"
		args = append(args, now)
	}
	if _, err := d.Exec(`UPDATE app_connectors SET `+set+` WHERE id = ?`, append(args, row.ID)...); err != nil {
		return err
	}
	result.OK = true
	return nil
}

// syncErrorMessage is short, safe text for last_error: never the key,
// never item content.
func syncErrorMessage(err error) string {
	var fe *FeedError
	var ae *Error
	var se *connectors.SealError
	switch {
	case errors.As(err, &fe):
		return fe.Msg
	case errors.As(err, &ae):
		return ae.Msg
	case errors.As(err, &se):
		return "The stored API key can't be read (CONNECTOR_SECRET changed). Paste the key again."
	}
	logx.Error("[recall:apps] sync failed: %v", err)
	return "Sync failed inside Recall. The server log has the details."
}

// Run is AppSyncRun.
type Run struct {
	Connectors int          `json:"connectors"`
	OK         int          `json:"ok"`
	Failed     int          `json:"failed"`
	Documents  int          `json:"documents"`
	Results    []SyncResult `json:"results"`
}

// RunSync is runAppSync: every enabled connector with a key, one after
// another. It never fails.
func RunSync(ctx context.Context, d *db.DB, deps SyncDeps, log func(string)) Run {
	if log == nil {
		log = func(m string) { logx.Info("[recall:apps] %s", m) }
	}
	run := Run{Results: []SyncResult{}}
	rows, err := d.Query(`SELECT workspace_id, provider FROM app_connectors WHERE enabled = 1 AND api_key IS NOT NULL`)
	if err != nil {
		log(err.Error())
		return run
	}
	type ref struct{ ws, provider string }
	var list []ref
	for rows.Next() {
		var r ref
		if rows.Scan(&r.ws, &r.provider) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	for _, r := range list {
		run.Connectors++
		res, err := Sync(ctx, d, r.ws, r.provider, deps)
		if err != nil {
			// Busy (a "Sync now" is running) or gone since the list was read.
			run.Failed++
			log(fmt.Sprintf("%s in %s: %s", r.provider, r.ws, err.Error()))
			continue
		}
		run.Results = append(run.Results, res)
		run.Documents += res.Documents
		if res.OK {
			run.OK++
		} else {
			run.Failed++
			log(fmt.Sprintf("%s in %s: %s", r.provider, r.ws, *res.Error))
		}
	}
	if run.Connectors > 0 {
		log(fmt.Sprintf("synced %d of %d connector(s), %d new document(s)", run.OK, run.Connectors, run.Documents))
	}
	return run
}

// Schedule is scheduleAppSync: exactly one app_sync job queued while some
// connector can sync, and none otherwise.
func Schedule(q db.Q, now int64) (int64, bool, error) {
	var id string
	err := q.QueryRow(`SELECT id FROM app_connectors WHERE enabled = 1 AND api_key IS NOT NULL`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var job int64
	err = q.QueryRow(`SELECT id FROM jobs WHERE type = 'app_sync' AND status = 'queued'`).Scan(&job)
	if err == nil {
		return 0, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	return jobs.Enqueue(q, reminders.SystemWorkspace, jobs.AppSync, jsonx.NewMap[any](), reminders.NextHour(now))
}

// JobResult is what the app_sync job returns.
type JobResult struct {
	Connectors int `json:"connectors"`
	OK         int `json:"ok"`
	Failed     int `json:"failed"`
	Documents  int `json:"documents"`
}

// Handler is the app_sync job: it never fails, then schedules the next run.
func Handler(d *db.DB, deps SyncDeps) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) (any, error) {
		run := RunSync(ctx, d, deps, nil)
		_, _, err := Schedule(d, time.Now().UnixMilli())
		return JobResult{run.Connectors, run.OK, run.Failed, run.Documents}, err
	}
}

var _ = syncJobLabel
