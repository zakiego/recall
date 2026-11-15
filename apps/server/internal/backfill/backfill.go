// Package backfill is backfill.ts: a one-off that dates the facts stored
// before dated facts existed. Each `backfill_dates` job sends one batch to
// the gateway, writes what it found onto the current rows (not new
// versions) and queues the next batch.
package backfill

import (
	"context"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/model"
)

// Batch is how many facts one call sends.
const Batch = 40

// Cursor is the last fact a batch checked.
type Cursor struct {
	CreatedAt float64
	ID        string
}

// Payload is the backfill_dates job's payload. Container "" is every
// container; Cursor nil starts at the oldest fact.
type Payload struct {
	Container string
	Cursor    *Cursor
	Checked   float64
	Dated     float64
}

// PayloadOf reads a job's payload.
func PayloadOf(j jobs.Job) Payload {
	p := Payload{}
	if j.Payload == nil {
		return p
	}
	if v, ok := j.Payload.Get("container"); ok && jsonx.Truthy(v) {
		p.Container, _ = v.(string)
	}
	if v, ok := j.Payload.Get("cursor"); ok && jsonx.Truthy(v) {
		if o, ok := v.(*jsonx.Object); ok {
			c := &Cursor{}
			if n, ok := o.Get("createdAt"); ok {
				c.CreatedAt, _ = n.(float64)
			}
			if id, ok := o.Get("id"); ok {
				c.ID, _ = id.(string)
			}
			p.Cursor = c
		}
	}
	if v, ok := j.Payload.Get("checked"); ok {
		p.Checked, _ = v.(float64)
	}
	if v, ok := j.Payload.Get("dated"); ok {
		p.Dated, _ = v.(float64)
	}
	return p
}

func undatedWhere(workspaceID, container string) (string, []any) {
	where := []string{"workspace_id = ?"}
	args := []any{workspaceID}
	if container != "" {
		where = append(where, "container = ?")
		args = append(args, container)
	}
	where = append(where, "valid_to IS NULL", "event_date IS NULL", "due_date IS NULL")
	return strings.Join(where, " AND "), args
}

// CountUndated is countUndated; container "" is every container.
func CountUndated(q db.Q, workspaceID, container string) (int64, error) {
	where, args := undatedWhere(workspaceID, container)
	var n int64
	err := q.QueryRow(`SELECT count(*) FROM memories WHERE `+where, args...).Scan(&n)
	return n, err
}

func undatedAfter(q db.Q, workspaceID string, p Payload, limit int) ([]model.Memory, error) {
	where, args := undatedWhere(workspaceID, p.Container)
	if p.Cursor != nil {
		where += " AND (created_at > ? OR (created_at = ? AND id > ?))"
		args = append(args, p.Cursor.CreatedAt, p.Cursor.CreatedAt, p.Cursor.ID)
	}
	args = append(args, limit)
	rows, err := q.Query(`SELECT `+model.MemoryCols+` FROM memories WHERE `+where+` ORDER BY created_at ASC, id ASC LIMIT ?`, args...)
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

// Result is BackfillBatch.
type Result struct {
	Checked int  `json:"checked"`
	Dated   int  `json:"dated"`
	Next    bool `json:"next"`
}

func cols(f gateway.DateField) (any, any) {
	if f.Value == nil {
		return nil, nil
	}
	return f.Value.Date, f.Value.Precision
}

// RunBatch is backfillBatch: one batch dated, the next one queued.
func RunBatch(ctx context.Context, d *db.DB, workspaceID string, p Payload, do gateway.Do) (Result, error) {
	rows, err := undatedAfter(d, workspaceID, p, Batch)
	if err != nil || len(rows) == 0 {
		return Result{}, err
	}
	facts := make([]gateway.BackfillFact, len(rows))
	for i, m := range rows {
		facts[i] = gateway.BackfillFact{Ref: i + 1, Fact: m.Fact, Stored: dates.ISODate(m.CreatedAt)}
	}
	result, _, err := gateway.ChatJSON(ctx, gateway.Request{System: gateway.BackfillSystem, Prompt: gateway.BackfillPrompt(facts)}, gateway.ParseBackfill, do)
	if err != nil {
		return Result{}, err
	}
	dated := 0
	err = d.Tx(func(tx db.Q) error {
		for _, x := range result {
			if x.Ref < 1 || x.Ref > len(rows) || (x.EventDate.Value == nil && x.DueDate.Value == nil) {
				continue
			}
			m := rows[x.Ref-1]
			ed, ep := cols(x.EventDate)
			dd, dp := cols(x.DueDate)
			n, err := db.Changes(tx.Exec(`UPDATE memories SET event_date = ?, event_precision = ?, due_date = ?, due_precision = ?
				WHERE id = ? AND workspace_id = ? AND valid_to IS NULL AND event_date IS NULL AND due_date IS NULL`, ed, ep, dd, dp, m.ID, workspaceID))
			if err != nil {
				return err
			}
			dated += int(n)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	last := rows[len(rows)-1]
	next := len(rows) == Batch
	if next {
		payload := jsonx.NewMap[any]()
		if p.Container != "" {
			payload.Set("container", p.Container)
		}
		payload.Set("cursor", jsonx.ObjectOf("createdAt", last.CreatedAt, "id", last.ID))
		payload.Set("checked", p.Checked+float64(len(rows)))
		payload.Set("dated", p.Dated+float64(dated))
		if _, _, err := jobs.Enqueue(d, workspaceID, jobs.BackfillDates, payload, nowMs()); err != nil {
			return Result{}, err
		}
	}
	return Result{Checked: len(rows), Dated: dated, Next: next}, nil
}

// Handler is the backfill_dates job.
func Handler(d *db.DB, do gateway.Do) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) (any, error) {
		p := PayloadOf(j)
		res, err := RunBatch(ctx, d, j.WorkspaceID, p, do)
		if err != nil {
			return nil, err
		}
		if !res.Next {
			logx.Info("[recall:backfill] %s: done, checked %s fact(s), dated %s", j.WorkspaceID,
				jsonx.NumberString(p.Checked+float64(res.Checked)), jsonx.NumberString(p.Dated+float64(res.Dated)))
		}
		return res, nil
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }
