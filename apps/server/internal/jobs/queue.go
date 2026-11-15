// Package jobs is src/server/jobs: the job queue in the jobs table, the
// runner that works it, and the waiters a request can block on.
package jobs

import (
	"database/sql"
	"errors"
	"math"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/logx"
)

// Job types.
const (
	Ingest         = "ingest"
	Extract        = "extract"
	Profile        = "profile"
	Ask            = "ask"
	Reminders      = "reminders"
	BackfillDates  = "backfill_dates"
	PeopleBackfill = "people_backfill"
	AppSync        = "app_sync"
)

// Statuses.
const (
	Queued  = "queued"
	Running = "running"
	Done    = "done"
	Failed  = "failed"
)

// BackoffMs are the retry delays after the 1st, 2nd and 3rd failure. The 4th
// failure is final.
var BackoffMs = []int64{30_000, 120_000, 600_000}

// MaxAttempts is len(BackoffMs) + 1.
var MaxAttempts = len(BackoffMs) + 1

// Interactive jobs: someone is waiting, so they run before background work
// and are never retried.
var Interactive = []string{Ask}

func isInteractiveType(t string) bool {
	for _, x := range Interactive {
		if x == t {
			return true
		}
	}
	return false
}

func maxAttempts(t string) int {
	if isInteractiveType(t) {
		return 1
	}
	return MaxAttempts
}

// Job is a row of jobs, in schema.ts field order.
type Job struct {
	ID          int64         `json:"id"`
	WorkspaceID string        `json:"workspaceId"`
	Type        string        `json:"type"`
	Payload     *jsonx.Object `json:"payload"`
	Status      string        `json:"status"`
	Attempts    int           `json:"attempts"`
	RunAfter    float64       `json:"runAfter"` // REAL when a Retry-After had a fraction
	LastError   *string       `json:"lastError"`
	CreatedAt   int64         `json:"createdAt"`
	StartedAt   *int64        `json:"startedAt"`
	FinishedAt  *int64        `json:"finishedAt"`
}

// Cols is the column list ScanJob reads.
const Cols = "id, workspace_id, type, payload, status, attempts, run_after, last_error, created_at, started_at, finished_at"

// ScanJob reads a row selected with Cols.
func ScanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var payload string
	var lastError sql.NullString
	var started, finished sql.NullInt64
	if err := row.Scan(&j.ID, &j.WorkspaceID, &j.Type, &payload, &j.Status, &j.Attempts, &j.RunAfter, &lastError, &j.CreatedAt, &started, &finished); err != nil {
		return j, err
	}
	p, err := jsonx.ParseObject([]byte(payload))
	if err != nil {
		p = jsonx.NewMap[any]()
	}
	j.Payload = p
	if lastError.Valid {
		j.LastError = &lastError.String
	}
	if started.Valid {
		j.StartedAt = &started.Int64
	}
	if finished.Valid {
		j.FinishedAt = &finished.Int64
	}
	return j, nil
}

// PayloadString reads a string field of the payload ("" when missing).
func (j Job) PayloadString(k string) string {
	if j.Payload == nil {
		return ""
	}
	v, _ := j.Payload.Get(k)
	s, _ := v.(string)
	return s
}

var (
	listenMu        sync.Mutex
	enqueueSubs     = map[int]func(){}
	finishedSubs    = map[int]func(Job){}
	nextSub         int
	enqueueSubOrder []int
)

// OnEnqueue registers fn to run after every enqueue; the result unsubscribes.
func OnEnqueue(fn func()) func() {
	listenMu.Lock()
	defer listenMu.Unlock()
	nextSub++
	id := nextSub
	enqueueSubs[id] = fn
	return func() {
		listenMu.Lock()
		delete(enqueueSubs, id)
		listenMu.Unlock()
	}
}

// OnJobFinished registers fn to run after every attempt ends.
func OnJobFinished(fn func(Job)) func() {
	listenMu.Lock()
	defer listenMu.Unlock()
	nextSub++
	id := nextSub
	finishedSubs[id] = fn
	return func() {
		listenMu.Lock()
		delete(finishedSubs, id)
		listenMu.Unlock()
	}
}

// NotifyJobFinished runs the finished listeners; one that panics is logged.
func NotifyJobFinished(job Job) {
	listenMu.Lock()
	fns := make([]func(Job), 0, len(finishedSubs))
	for _, fn := range finishedSubs {
		fns = append(fns, fn)
	}
	listenMu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logx.Error("[recall:jobs] job listener failed: %v", r)
				}
			}()
			fn(job)
		}()
	}
}

func notifyEnqueued() {
	listenMu.Lock()
	fns := make([]func(), 0, len(enqueueSubs))
	for _, fn := range enqueueSubs {
		fns = append(fns, fn)
	}
	listenMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Enqueue adds a job of the workspace. A profile rebuild or people backfill
// already queued for the container makes this a no-op (ok false). The payload
// is stored as JSON with its keys in order.
func Enqueue(q db.Q, workspaceID, typ string, payload any, runAfter int64) (int64, bool, error) {
	body, err := jsonx.Marshal(payload)
	if err != nil {
		return 0, false, err
	}
	var id int64
	inserted := false
	err = q.Tx(func(tx db.Q) error {
		if typ == Profile || typ == PeopleBackfill {
			p, _ := jsonx.ParseObject(body)
			c, _ := p.Get("container")
			var existing int64
			err := tx.QueryRow(`SELECT id FROM jobs WHERE type = ? AND status = 'queued' AND workspace_id = ? AND json_extract(payload, '$.container') = ? LIMIT 1`,
				typ, workspaceID, c).Scan(&existing)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := tx.WQueryRow(`INSERT INTO jobs (workspace_id, type, payload, run_after) VALUES (?, ?, ?, ?) RETURNING id`, workspaceID, typ, string(body), runAfter).Scan(&id); err != nil {
			return err
		}
		inserted = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if inserted {
		notifyEnqueued()
	}
	return id, inserted, nil
}

// ClaimFilter limits what a free slot may take.
type ClaimFilter struct{ InteractiveOnly bool }

const interactiveSQL = "type IN ('ask')"

// ClaimNext takes the next due job and marks it running, in one statement.
// Interactive jobs jump ahead; otherwise oldest first. ok is false when
// nothing is due.
func ClaimNext(q db.Q, now int64, filter ClaimFilter, startedAt int64) (Job, bool, error) {
	where := "status = 'queued' AND run_after <= ?"
	if filter.InteractiveOnly {
		where += " AND " + interactiveSQL
	}
	j, err := ScanJob(q.WQueryRow(`UPDATE jobs SET status = 'running', attempts = attempts + 1, started_at = ?, finished_at = NULL
		WHERE id = (SELECT id FROM jobs WHERE `+where+` ORDER BY (`+interactiveSQL+`) DESC, run_after ASC, id ASC LIMIT 1)
		RETURNING `+Cols, startedAt, now))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return j, err == nil, err
}

// NextRunAt is the earliest run_after among queued jobs, ok false when none.
func NextRunAt(q db.Q, filter ClaimFilter) (int64, bool, error) {
	where := "status = 'queued'"
	if filter.InteractiveOnly {
		where += " AND " + interactiveSQL
	}
	var at float64
	err := q.QueryRow(`SELECT run_after FROM jobs WHERE ` + where + ` ORDER BY run_after ASC LIMIT 1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return int64(math.Ceil(at)), err == nil, err
}

// Complete marks a job done. last_error stays: a job that needed a retry
// keeps the error that cost it.
func Complete(q db.Q, id int64, now int64) error {
	_, err := q.Exec(`UPDATE jobs SET status = 'done', finished_at = ? WHERE id = ?`, now, id)
	return err
}

// Fail records a failed attempt: it returns true when the job will be
// retried after the backoff (at least minDelayMs), false when it gave up.
func Fail(q db.Q, job Job, cause error, now int64, minDelayMs float64) (bool, error) {
	message := jserr.Format(cause)
	if job.Attempts >= maxAttempts(job.Type) {
		_, err := q.Exec(`UPDATE jobs SET status = 'failed', last_error = ?, finished_at = ? WHERE id = ?`, message, now, job.ID)
		return false, err
	}
	i := job.Attempts - 1
	delay := BackoffMs[len(BackoffMs)-1]
	if i >= 0 && i < len(BackoffMs) {
		delay = BackoffMs[i]
	}
	// now + delay is a JS number: a Retry-After with a fraction stores REAL.
	var runAfter any = float64(now) + math.Max(float64(delay), minDelayMs)
	if f := runAfter.(float64); f == math.Trunc(f) {
		runAfter = int64(f)
	}
	_, err := q.Exec(`UPDATE jobs SET status = 'queued', last_error = ?, run_after = ?, finished_at = ? WHERE id = ?`, message, runAfter, now, job.ID)
	return true, err
}

// CancelQueued drops a queued job nobody waits for. False when it already started.
func CancelQueued(q db.Q, id int64, reason string, now int64) (bool, error) {
	n, err := db.Changes(q.Exec(`UPDATE jobs SET status = 'failed', last_error = ?, finished_at = ? WHERE id = ? AND status = 'queued'`, reason, now, id))
	return n > 0, err
}

// RecoverStale handles jobs left "running" by a process that died: they run
// again, except interactive ones and ones on their last attempt, which fail.
// It returns how many were requeued and the given-up jobs.
func RecoverStale(q db.Q, now int64) (int64, []Job, error) {
	var requeued int64
	var gaveUp []Job
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec(`UPDATE jobs SET status = 'failed', last_error = 'server restarted', finished_at = ? WHERE type IN ('ask') AND status IN ('queued', 'running')`, now); err != nil {
			return err
		}
		rows, err := tx.WQuery(`UPDATE jobs SET status = 'failed', last_error = 'the server stopped during attempt ' || attempts || ', the last one (crash or restart)', finished_at = ?
			WHERE status = 'running' AND attempts >= ? RETURNING `+Cols, now, MaxAttempts)
		if err != nil {
			return err
		}
		for rows.Next() {
			j, err := ScanJob(rows)
			if err != nil {
				rows.Close()
				return err
			}
			gaveUp = append(gaveUp, j)
		}
		rows.Close()
		requeued, err = db.Changes(tx.Exec(`UPDATE jobs SET status = 'queued' WHERE status = 'running'`))
		return err
	})
	return requeued, gaveUp, err
}

// PruneAfterMs: done jobs older than a week are noise.
const PruneAfterMs = 7 * 86_400_000

// PruneDone deletes done jobs that finished (or, before migration 0010, were
// queued) more than a week ago.
func PruneDone(q db.Q, now int64) (int64, error) {
	return db.Changes(q.Exec(`DELETE FROM jobs WHERE status = 'done' AND coalesce(finished_at, created_at) <= ?`, now-PruneAfterMs))
}
