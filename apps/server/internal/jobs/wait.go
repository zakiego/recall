package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/zakiego/recall/apps/server/internal/db"
	"math"
	"sync"
	"time"
)

// Outcome is how a job ended for its waiters: Value on success, Err otherwise.
type Outcome struct {
	OK    bool
	Value any
	Err   error
}

var (
	waitMu  sync.Mutex
	waiters = map[int64]map[*waiter]bool{}
)

type waiter struct{ ch chan Outcome }

// SettleJob hands the outcome to everyone waiting on the job. In memory only:
// register right after enqueue and always pass a timeout.
func SettleJob(id int64, o Outcome) {
	waitMu.Lock()
	set := waiters[id]
	delete(waiters, id)
	waitMu.Unlock()
	for w := range set {
		w.ch <- o
	}
}

// WaitTimeout is JobWaitTimeout.
type WaitTimeout struct {
	JobID     int64
	TimeoutMs float64
}

func (e *WaitTimeout) Error() string {
	return fmt.Sprintf("job #%d did not finish within %s s", e.JobID, roundJS(e.TimeoutMs/1000))
}
func (e *WaitTimeout) JSName() string { return "JobWaitTimeout" }

func roundJS(x float64) string {
	r := math.Floor(x + 0.5)
	if r == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", int64(r))
}

// ErrAborted is a wait called off without a reason.
var ErrAborted = errors.New("aborted")

// Waiter is one registered wait on a job (a waitForJob listener). Register
// it before the job can settle: EnqueueWatched does that for a new job.
type Waiter struct {
	id int64
	w  *waiter
}

// Watch registers a waiter for job id.
func Watch(id int64) *Waiter {
	w := &waiter{ch: make(chan Outcome, 1)}
	waitMu.Lock()
	set := waiters[id]
	if set == nil {
		set = map[*waiter]bool{}
		waiters[id] = set
	}
	set[w] = true
	waitMu.Unlock()
	return &Waiter{id: id, w: w}
}

// Cancel unregisters the waiter.
func (wt *Waiter) Cancel() {
	waitMu.Lock()
	if s := waiters[wt.id]; s != nil {
		delete(s, wt.w)
		if len(s) == 0 {
			delete(waiters, wt.id)
		}
	}
	waitMu.Unlock()
}

// EnqueueWatched is Enqueue with a waiter registered before the insert
// commits. The runner claims on the same single write connection, so the
// job can't run, let alone settle, before the waiter exists; the TS code
// gets the same by registering in the same tick. The waiter is nil when
// nothing was inserted.
func EnqueueWatched(q db.Q, workspaceID, typ string, payload any, runAfter int64) (int64, bool, *Waiter, error) {
	var id int64
	var inserted bool
	var wt *Waiter
	err := q.Tx(func(tx db.Q) error {
		var err error
		id, inserted, err = Enqueue(tx, workspaceID, typ, payload, runAfter)
		if err == nil && inserted {
			wt = Watch(id)
		}
		return err
	})
	if err != nil {
		if wt != nil {
			wt.Cancel()
		}
		return 0, false, nil, err
	}
	return id, inserted, wt, nil
}

// WaitForJob waits for the job's outcome, the timeout or ctx (its cause is
// returned, like signal.reason). It registers now: a job that already
// settled is never heard of (use Watch or EnqueueWatched first).
func WaitForJob(ctx context.Context, id int64, timeoutMs float64) (any, error) {
	return Watch(id).Wait(ctx, timeoutMs)
}

// Wait waits for the outcome, the timeout or ctx.
func (wt *Waiter) Wait(ctx context.Context, timeoutMs float64) (any, error) {
	if ctx.Err() != nil {
		wt.Cancel()
		return nil, abortReason(ctx)
	}
	timer := time.NewTimer(time.Duration(timeoutMs * float64(time.Millisecond)))
	defer timer.Stop()
	select {
	case o := <-wt.w.ch:
		if o.OK {
			return o.Value, nil
		}
		return nil, o.Err
	case <-timer.C:
		wt.Cancel()
		return nil, &WaitTimeout{JobID: wt.id, TimeoutMs: timeoutMs}
	case <-ctx.Done():
		wt.Cancel()
		return nil, abortReason(ctx)
	}
}

func abortReason(ctx context.Context) error {
	if c := context.Cause(ctx); c != nil && !errors.Is(c, context.Canceled) {
		return c
	}
	return ErrAborted
}

var (
	abortMu sync.Mutex
	aborts  = map[int64]context.CancelCauseFunc{}
)

// JobAbortContext is jobAbortSignal: a context the job's handler runs under,
// which AbortJob cancels. Pair with ReleaseJobAbort.
func JobAbortContext(parent context.Context, id int64) context.Context {
	ctx, cancel := context.WithCancelCause(parent)
	abortMu.Lock()
	aborts[id] = cancel
	abortMu.Unlock()
	return ctx
}

// ReleaseJobAbort forgets the job's abort.
func ReleaseJobAbort(id int64) {
	abortMu.Lock()
	delete(aborts, id)
	abortMu.Unlock()
}

// AbortJob cancels a running job's context with an error carrying reason.
// False when the job isn't running in this process.
func AbortJob(id int64, reason string) bool {
	abortMu.Lock()
	cancel, ok := aborts[id]
	delete(aborts, id)
	abortMu.Unlock()
	if !ok {
		return false
	}
	cancel(errors.New(reason))
	return true
}
