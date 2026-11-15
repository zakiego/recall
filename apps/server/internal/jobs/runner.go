package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"math"
	"sync"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/mem"
)

// Handler runs one job. Its value goes to whoever waits on the job.
type Handler func(ctx context.Context, job Job) (any, error)

// Handlers maps job types to handlers. OnGiveUp runs once a job has used up
// its retries.
type Handlers struct {
	ByType   map[string]Handler
	OnGiveUp func(job Job, err error)
}

// RetryAfter is implemented by errors that ask for a minimum delay (the
// gateway's Retry-After).
type RetryAfter interface{ RetryAfterMillis() float64 }

const (
	idlePollMs = 5_000
	pruneEvery = 3_600_000
	maxRunning = 2
	minWaitMs  = 50
)

// MaxRunning: the gateway has 4 slots shared with every other project, so
// recall holds at most 2: one background job and one more only an ask may take.
const MaxRunning = maxRunning

// Runner runs jobs at most MaxRunning at once.
type Runner struct {
	d        *db.DB
	handlers Handlers
	log      func(string)
	now      func() int64

	mu          sync.Mutex
	running     bool
	active      map[int64]Job
	activeWG    sync.WaitGroup
	poke        chan struct{}
	stopped     chan struct{}
	unsubscribe func()
	lastPruneAt int64
}

// NewRunner makes a runner; log nil logs "[recall:jobs] <msg>".
func NewRunner(d *db.DB, h Handlers, log func(string)) *Runner {
	if log == nil {
		log = func(m string) { logx.Info("[recall:jobs] %s", m) }
	}
	return &Runner{d: d, handlers: h, log: log, now: func() int64 { return time.Now().UnixMilli() }, active: map[int64]Job{}, poke: make(chan struct{}, 1)}
}

// Start recovers stale jobs, prunes and starts the loop.
func (r *Runner) Start() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	requeued, gaveUp, err := RecoverStale(r.d, r.now())
	if err != nil {
		r.log("loop error: " + err.Error())
	}
	if requeued > 0 {
		r.log(fmt.Sprintf("requeued %d interrupted job(s)", requeued))
	}
	for _, job := range gaveUp {
		le := "interrupted"
		if job.LastError != nil {
			le = *job.LastError
		}
		r.log(fmt.Sprintf("%s #%d gave up: %s", job.Type, job.ID, le))
		if r.handlers.OnGiveUp != nil {
			r.handlers.OnGiveUp(job, errors.New(le))
		}
		NotifyJobFinished(job)
	}
	r.prune()
	r.unsubscribe = OnEnqueue(r.Poke)
	r.stopped = make(chan struct{})
	go r.loop()
}

// Stop ends the loop and waits for running jobs.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	r.mu.Unlock()
	if r.unsubscribe != nil {
		r.unsubscribe()
	}
	r.Poke()
	<-r.stopped
}

// Poke wakes the loop.
func (r *Runner) Poke() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
}

func (r *Runner) isRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// RunOnce runs the next due job, if any, and waits for it (tests).
func (r *Runner) RunOnce(now int64) (*Job, error) {
	job, ok, err := ClaimNext(r.d, now, ClaimFilter{}, r.now())
	if err != nil || !ok {
		return nil, err
	}
	r.execute(job)
	return &job, nil
}

func (r *Runner) execute(job Job) {
	started := r.now()
	value, err := r.call(job)
	if err == nil {
		// complete() throwing lands in the same catch as a failed handler.
		err = Complete(r.d, job.ID, r.now())
	}
	if err == nil {
		r.log(fmt.Sprintf("%s #%d done in %d ms", job.Type, job.ID, r.now()-started))
		if job.Type == Ingest {
			r.log(fmt.Sprintf("memory after ingest #%d: %s", job.ID, mem.Line()))
		}
		SettleJob(job.ID, Outcome{OK: true, Value: value})
	} else {
		minDelay := 0.0
		var ra RetryAfter
		if errors.As(err, &ra) {
			minDelay = ra.RetryAfterMillis()
		}
		retrying, ferr := Fail(r.d, job, err, r.now(), minDelay)
		if ferr != nil {
			r.log(fmt.Sprintf("%s #%d bookkeeping failed: %s", job.Type, job.ID, ferr.Error()))
			NotifyJobFinished(job)
			return
		}
		how := ", giving up"
		if retrying {
			how = ", will retry"
		}
		r.log(fmt.Sprintf("%s #%d attempt %d failed%s: %s", job.Type, job.ID, job.Attempts, how, err.Error()))
		if !retrying {
			if r.handlers.OnGiveUp != nil {
				r.handlers.OnGiveUp(job, err)
			}
			SettleJob(job.ID, Outcome{Err: err})
		}
	}
	NotifyJobFinished(job)
}

func (r *Runner) call(job Job) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	h := r.handlers.ByType[job.Type]
	if h == nil {
		return nil, jserr.New("TypeError", "handler is not a function")
	}
	return h(context.Background(), job)
}

func (r *Runner) backgroundBusy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.active {
		if !isInteractiveType(j.Type) {
			return true
		}
	}
	return false
}

func (r *Runner) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// fill starts due jobs until the slots are full or nothing is due. Once a
// background job holds a slot, the free one only takes asks.
func (r *Runner) fill() error {
	for r.isRunning() && r.activeCount() < maxRunning {
		job, ok, err := ClaimNext(r.d, r.now(), ClaimFilter{InteractiveOnly: r.backgroundBusy()}, r.now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		r.mu.Lock()
		r.active[job.ID] = job
		r.mu.Unlock()
		r.activeWG.Add(1)
		go func(job Job) {
			defer r.activeWG.Done()
			r.execute(job)
			r.mu.Lock()
			delete(r.active, job.ID)
			r.mu.Unlock()
			r.Poke()
		}(job)
	}
	return nil
}

func (r *Runner) prune() {
	now := r.now()
	if now-r.lastPruneAt < pruneEvery {
		return
	}
	r.lastPruneAt = now
	n, err := PruneDone(r.d, now)
	if err != nil {
		r.log("loop error: " + err.Error())
		return
	}
	if n > 0 {
		r.log(fmt.Sprintf("pruned %d old done job(s)", n))
	}
}

func (r *Runner) nextWaitMs() (int64, error) {
	if r.activeCount() >= maxRunning {
		return idlePollMs, nil
	}
	next, ok, err := NextRunAt(r.d, ClaimFilter{InteractiveOnly: r.backgroundBusy()})
	if err != nil || !ok {
		return idlePollMs, err
	}
	return int64(math.Min(math.Max(float64(next-r.now()), minWaitMs), idlePollMs)), nil
}

func (r *Runner) loop() {
	defer close(r.stopped)
	for r.isRunning() {
		wait := int64(idlePollMs)
		func() {
			defer func() {
				if p := recover(); p != nil {
					r.log(fmt.Sprintf("loop error: %v", p))
				}
			}()
			r.prune()
			if err := r.fill(); err != nil {
				r.log("loop error: " + err.Error())
				return
			}
			w, err := r.nextWaitMs()
			if err != nil {
				r.log("loop error: " + err.Error())
				return
			}
			wait = w
		}()
		t := time.NewTimer(time.Duration(wait) * time.Millisecond)
		select {
		case <-r.poke:
		case <-t.C:
		}
		t.Stop()
	}
	r.activeWG.Wait()
}
