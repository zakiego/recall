package jobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/gateway"
)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func nowMs() int64 { return time.Now().UnixMilli() }

func enq(t *testing.T, d *db.DB, typ string, payload map[string]any, runAfter ...int64) int64 {
	t.Helper()
	at := nowMs()
	if len(runAfter) > 0 {
		at = runAfter[0]
	}
	id, ok, err := Enqueue(d, "ws_a", typ, orderedPayload(payload), at)
	if err != nil || !ok {
		t.Fatalf("enqueue %s: %v %v", typ, ok, err)
	}
	return id
}

// orderedPayload keeps the TS key order for the payloads the tests use.
func orderedPayload(p map[string]any) any {
	keys := []string{"documentId", "container", "question", "includeChunks"}
	var b []byte
	b = append(b, '{')
	first := true
	for _, k := range keys {
		v, ok := p[k]
		if !ok {
			continue
		}
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, fmt.Sprintf("%q:", k)...)
		switch x := v.(type) {
		case string:
			b = append(b, fmt.Sprintf("%q", x)...)
		case bool:
			b = append(b, fmt.Sprint(x)...)
		}
	}
	b = append(b, '}')
	return rawJSON(b)
}

type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }

func row(t *testing.T, d *db.DB, id int64) Job {
	t.Helper()
	j, err := ScanJob(d.QueryRow("SELECT "+Cols+" FROM jobs WHERE id = ?", id))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func claim(t *testing.T, d *db.DB, now int64, f ClaimFilter) (Job, bool) {
	t.Helper()
	j, ok, err := ClaimNext(d, now, f, nowMs())
	if err != nil {
		t.Fatal(err)
	}
	return j, ok
}

func until(t *testing.T, check func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for !check() {
		if time.Now().After(end) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func noop(context.Context, Job) (any, error) { return nil, nil }

func handlers(over map[string]Handler, onGiveUp func(Job, error)) Handlers {
	h := Handlers{ByType: map[string]Handler{}, OnGiveUp: onGiveUp}
	for _, t := range []string{Ingest, Extract, Profile, Ask, Reminders, BackfillDates, PeopleBackfill, AppSync} {
		h.ByType[t] = noop
	}
	for k, v := range over {
		h.ByType[k] = v
	}
	return h
}

func quiet(string) {}

func TestClaimsDueJobsOldestFirst(t *testing.T) {
	d := openDB(t)
	now := nowMs()
	enq(t, d, Ingest, map[string]any{"documentId": "a"}, now+60_000)
	b := enq(t, d, Ingest, map[string]any{"documentId": "b"}, now-10)
	j, ok := claim(t, d, now, ClaimFilter{})
	if !ok || j.ID != b || j.Status != Running || j.Attempts != 1 {
		t.Fatalf("%+v", j)
	}
	if _, ok := claim(t, d, now, ClaimFilter{}); ok {
		t.Fatal("claimed a future job")
	}
}

func TestDedupesQueuedProfileJobs(t *testing.T) {
	d := openDB(t)
	try := func(ws, c string) bool {
		_, ok, err := Enqueue(d, ws, Profile, orderedPayload(map[string]any{"container": c}), nowMs())
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !try("ws_a", "x") || try("ws_a", "x") || !try("ws_a", "y") || !try("ws_b", "x") {
		t.Fatal("dedupe")
	}
}

func TestAsksRunFirst(t *testing.T) {
	d := openDB(t)
	now := nowMs()
	ingest := enq(t, d, Ingest, map[string]any{"documentId": "a"}, now-3000)
	extract := enq(t, d, Extract, map[string]any{"documentId": "b"}, now-2000)
	profile := enq(t, d, Profile, map[string]any{"container": "c"}, now-1000)
	ask1 := enq(t, d, Ask, map[string]any{"container": "c", "question": "first?", "includeChunks": false}, now)
	ask2 := enq(t, d, Ask, map[string]any{"container": "c", "question": "second?", "includeChunks": false}, now)
	var order []int64
	for i := 0; i < 5; i++ {
		j, _ := claim(t, d, now, ClaimFilter{})
		order = append(order, j.ID)
	}
	if fmt.Sprint(order) != fmt.Sprint([]int64{ask1, ask2, ingest, extract, profile}) {
		t.Fatalf("%v", order)
	}
}

func TestSecondSlotOnlyForAsks(t *testing.T) {
	d := openDB(t)
	now := nowMs()
	enq(t, d, Extract, map[string]any{"documentId": "a"}, now-1000)
	ask := enq(t, d, Ask, map[string]any{"container": "c", "question": "q", "includeChunks": false}, now+5000)
	if _, ok := claim(t, d, now, ClaimFilter{InteractiveOnly: true}); ok {
		t.Fatal("claimed a background job for the ask slot")
	}
	if at, ok, _ := NextRunAt(d, ClaimFilter{InteractiveOnly: true}); !ok || at != now+5000 {
		t.Fatal(at)
	}
	if j, _ := claim(t, d, now+5000, ClaimFilter{InteractiveOnly: true}); j.ID != ask {
		t.Fatal(j.ID)
	}
}

func TestLeftoverAsksFail(t *testing.T) {
	d := openDB(t)
	enq(t, d, Ingest, map[string]any{"documentId": "a"})
	claim(t, d, nowMs(), ClaimFilter{})
	ask := enq(t, d, Ask, map[string]any{"container": "c", "question": "q", "includeChunks": false})
	queuedAsk := enq(t, d, Ask, map[string]any{"container": "c", "question": "q2", "includeChunks": false})
	claim(t, d, nowMs(), ClaimFilter{})
	n, _, err := RecoverStale(d, nowMs())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if row(t, d, ask).Status != Failed || row(t, d, queuedAsk).Status != Failed {
		t.Fatal("asks not failed")
	}
}

func TestRequeuesInterruptedJobs(t *testing.T) {
	d := openDB(t)
	enq(t, d, Ingest, map[string]any{"documentId": "a"})
	claim(t, d, nowMs(), ClaimFilter{})
	n, gaveUp, _ := RecoverStale(d, nowMs())
	if n != 1 || len(gaveUp) != 0 {
		t.Fatal(n, gaveUp)
	}
	if _, ok := claim(t, d, nowMs(), ClaimFilter{}); !ok {
		t.Fatal("not requeued")
	}
}

func TestFailsAJobOnItsLastAttemptAtRecovery(t *testing.T) {
	d := openDB(t)
	crashy := enq(t, d, Extract, map[string]any{"documentId": "big.pdf"})
	other := enq(t, d, Extract, map[string]any{"documentId": "small"})
	for i := 1; i < MaxAttempts; i++ {
		if j, _ := claim(t, d, nowMs(), ClaimFilter{}); j.ID != crashy {
			t.Fatalf("attempt %d claimed %d", i, j.ID)
		}
		if n, _, _ := RecoverStale(d, nowMs()); n != 1 {
			t.Fatal(n)
		}
	}
	claim(t, d, nowMs(), ClaimFilter{})
	claim(t, d, nowMs(), ClaimFilter{})
	n, gaveUp, _ := RecoverStale(d, 5000)
	if n != 1 || len(gaveUp) != 1 || gaveUp[0].ID != crashy {
		t.Fatal(n, gaveUp)
	}
	r := row(t, d, crashy)
	if r.Status != Failed || r.Attempts != MaxAttempts || *r.FinishedAt != 5000 || *r.LastError != fmt.Sprintf("the server stopped during attempt %d, the last one (crash or restart)", MaxAttempts) {
		t.Fatalf("%+v %s", r, *r.LastError)
	}
	if o := row(t, d, other); o.Status != Queued || o.Attempts != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestRecordsAttemptTimesAndKeepsTheError(t *testing.T) {
	d := openDB(t)
	id := enq(t, d, Extract, map[string]any{"documentId": "d"})
	if r := row(t, d, id); r.StartedAt != nil || r.FinishedAt != nil {
		t.Fatal("times set")
	}
	before := nowMs()
	first, _ := claim(t, d, nowMs(), ClaimFilter{})
	if *row(t, d, id).StartedAt < before {
		t.Fatal("startedAt")
	}
	if _, err := Fail(d, first, errors.New("gateway hiccup"), nowMs(), 0); err != nil {
		t.Fatal(err)
	}
	if r := row(t, d, id); *r.FinishedAt < *r.StartedAt {
		t.Fatal("finishedAt")
	}
	claim(t, d, nowMs()+BackoffMs[0], ClaimFilter{})
	if row(t, d, id).FinishedAt != nil {
		t.Fatal("a running retry has a finish time")
	}
	Complete(d, id, nowMs())
	r := row(t, d, id)
	if r.Status != Done || r.Attempts != 2 || *r.LastError != "Error: gateway hiccup" || *r.FinishedAt < *r.StartedAt {
		t.Fatalf("%+v", r)
	}
}

func TestPrunesDoneJobsAWeekAfterTheyFinished(t *testing.T) {
	d := openDB(t)
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).UnixMilli()
	n := 0
	job := func(status string, finished *int64, created int64) int64 {
		n++
		id := enq(t, d, Ingest, map[string]any{"documentId": fmt.Sprintf("d%d", n)})
		if _, err := d.Exec("UPDATE jobs SET status = ?, finished_at = ?, created_at = ? WHERE id = ?", status, finished, created, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	p := func(v int64) *int64 { return &v }
	old := now - 30*86_400_000
	job(Done, p(now-PruneAfterMs-1), old)
	job(Done, p(now-PruneAfterMs), old)
	recent := job(Done, p(now-PruneAfterMs+60_000), old)
	job(Done, nil, old)
	legacyNew := job(Done, nil, now-86_400_000)
	failed := job(Failed, p(now-PruneAfterMs-1), old)
	queued := job(Queued, nil, old)
	if k, _ := PruneDone(d, now); k != 3 {
		t.Fatal(k)
	}
	rows, _ := d.Query("SELECT id FROM jobs ORDER BY id")
	var left []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		left = append(left, id)
	}
	rows.Close()
	want := []int64{recent, legacyNew, failed, queued}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if fmt.Sprint(left) != fmt.Sprint(want) {
		t.Fatal(left, want)
	}
}

func TestRunnerPassesThePayload(t *testing.T) {
	d := openDB(t)
	var got Job
	r := NewRunner(d, handlers(map[string]Handler{Ingest: func(_ context.Context, j Job) (any, error) { got = j; return nil, nil }}, nil), quiet)
	id := enq(t, d, Ingest, map[string]any{"documentId": "doc_1"})
	if _, err := r.RunOnce(nowMs()); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.PayloadString("documentId") != "doc_1" || row(t, d, id).Status != Done {
		t.Fatalf("%+v", got)
	}
}

func TestRunnerBacksOffThenGivesUp(t *testing.T) {
	d := openDB(t)
	gaveUp := 0
	r := NewRunner(d, handlers(map[string]Handler{Extract: func(context.Context, Job) (any, error) { return nil, errors.New("boom") }}, func(Job, error) { gaveUp++ }), quiet)
	id := enq(t, d, Extract, map[string]any{"documentId": "d"})
	var delays []int64
	now := nowMs()
	for i := 0; i < 4; i++ {
		before := nowMs()
		r.RunOnce(now)
		j := row(t, d, id)
		if i < 3 {
			if j.Status != Queued {
				t.Fatal(j.Status)
			}
			delays = append(delays, int64(math.Round((j.RunAfter-float64(before))/1000))*1000)
			now = int64(j.RunAfter)
		} else if j.Status != Failed || *j.LastError != "Error: boom" {
			t.Fatalf("%+v", j)
		}
	}
	if fmt.Sprint(delays) != fmt.Sprint(BackoffMs) || gaveUp != 1 {
		t.Fatal(delays, gaveUp)
	}
}

func TestRunnerHonorsRetryAfter(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, handlers(map[string]Handler{Profile: func(context.Context, Job) (any, error) {
		return nil, &gateway.Error{Msg: "busy", Status: 429, Type: "overloaded", RetryAfterMs: 300_000}
	}}, nil), quiet)
	id := enq(t, d, Profile, map[string]any{"container": "c"})
	before := nowMs()
	r.RunOnce(nowMs())
	if j := row(t, d, id); j.RunAfter-float64(before) < 300_000 || *j.LastError != "GatewayError: busy" {
		t.Fatalf("%+v", j)
	}
}

func TestWaiterGetsTheResult(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, handlers(map[string]Handler{Ask: func(_ context.Context, j Job) (any, error) {
		return map[string]string{"echo": j.PayloadString("question")}, nil
	}}, nil), quiet)
	id := enq(t, d, Ask, map[string]any{"container": "c", "question": "hi", "includeChunks": false})
	res := make(chan any, 1)
	go func() { v, _ := WaitForJob(context.Background(), id, 1000); res <- v }()
	time.Sleep(10 * time.Millisecond)
	r.RunOnce(nowMs())
	if v := <-res; fmt.Sprint(v) != "map[echo:hi]" {
		t.Fatal(v)
	}
}

func TestWaiterRejectedWhenAskGivesUp(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, handlers(map[string]Handler{Ask: func(context.Context, Job) (any, error) { return nil, errors.New("nope") }}, nil), quiet)
	id := enq(t, d, Ask, map[string]any{"container": "c", "question": "hi", "includeChunks": false})
	res := make(chan error, 1)
	go func() { _, err := WaitForJob(context.Background(), id, 1000); res <- err }()
	time.Sleep(10 * time.Millisecond)
	r.RunOnce(nowMs())
	if err := <-res; err == nil || err.Error() != "nope" {
		t.Fatal(err)
	}
	if row(t, d, id).Status != Failed {
		t.Fatal("not failed")
	}
	if j, _ := r.RunOnce(nowMs() + 3_600_000); j != nil {
		t.Fatal("ask retried")
	}
}

func TestWaiterTimesOut(t *testing.T) {
	d := openDB(t)
	id := enq(t, d, Ask, map[string]any{"container": "c", "question": "hi", "includeChunks": false})
	_, err := WaitForJob(context.Background(), id, 20)
	var wt *WaitTimeout
	if !errors.As(err, &wt) || err.Error() != fmt.Sprintf("job #%d did not finish within 0 s", id) {
		t.Fatal(err)
	}
}

func TestGiveUpAtBoot(t *testing.T) {
	d := openDB(t)
	var calls []Job
	id := enq(t, d, Extract, map[string]any{"documentId": "big.pdf"})
	d.Exec("UPDATE jobs SET status = 'running', attempts = ? WHERE id = ?", MaxAttempts, id)
	r := NewRunner(d, handlers(nil, func(j Job, _ error) { calls = append(calls, j) }), quiet)
	r.Start()
	r.Stop()
	if len(calls) != 1 || calls[0].ID != id || calls[0].Status != Failed || row(t, d, id).Status != Failed {
		t.Fatalf("%+v", calls)
	}
}

func TestRunsJobsEnqueuedWhileIdle(t *testing.T) {
	d := openDB(t)
	done := make(chan struct{})
	var once sync.Once
	r := NewRunner(d, handlers(map[string]Handler{Ingest: func(context.Context, Job) (any, error) { once.Do(func() { close(done) }); return nil, nil }}, nil), quiet)
	r.Start()
	defer r.Stop()
	time.Sleep(20 * time.Millisecond)
	enq(t, d, Ingest, map[string]any{"documentId": "late"})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job not run")
	}
}

func TestPrunesEveryHour(t *testing.T) {
	d := openDB(t)
	t0 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).UnixMilli()
	var clock atomic.Int64
	clock.Store(t0)
	r := NewRunner(d, handlers(nil, nil), quiet)
	r.now = clock.Load
	r.Start()
	defer r.Stop()
	old := enq(t, d, Ingest, map[string]any{"documentId": "old"}, t0)
	until(t, func() bool { return row(t, d, old).Status == Done })
	d.Exec("UPDATE jobs SET finished_at = ? WHERE id = ?", t0-PruneAfterMs+1000, old)
	clock.Store(t0 + 30*60_000)
	a := enq(t, d, Ingest, map[string]any{"documentId": "a"}, clock.Load())
	until(t, func() bool { return row(t, d, a).Status == Done })
	exists := func() bool {
		var n int
		d.QueryRow("SELECT count(*) FROM jobs WHERE id = ?", old).Scan(&n)
		return n == 1
	}
	if !exists() {
		t.Fatal("pruned inside the hour")
	}
	clock.Store(t0 + 61*60_000)
	b := enq(t, d, Ingest, map[string]any{"documentId": "b"}, clock.Load())
	until(t, func() bool { return !exists() })
	until(t, func() bool { return row(t, d, b).Status == Done })
}

func TestAskRunsBesideAnExtractionNeverTwoBackground(t *testing.T) {
	d := openDB(t)
	slow := make(chan struct{})
	var mu sync.Mutex
	running := map[string]bool{}
	maxBackground := 0
	r := NewRunner(d, handlers(map[string]Handler{
		Extract: func(_ context.Context, j Job) (any, error) {
			doc := j.PayloadString("documentId")
			mu.Lock()
			running[doc] = true
			maxBackground = max(maxBackground, len(running))
			mu.Unlock()
			if doc == "slow" {
				<-slow
			}
			mu.Lock()
			delete(running, doc)
			mu.Unlock()
			return nil, nil
		},
		Ask: func(context.Context, Job) (any, error) { return "answer", nil },
	}, nil), quiet)
	first := enq(t, d, Extract, map[string]any{"documentId": "slow"})
	second := enq(t, d, Extract, map[string]any{"documentId": "next"})
	r.Start()
	until(t, func() bool { mu.Lock(); defer mu.Unlock(); return running["slow"] })
	ask := enq(t, d, Ask, map[string]any{"container": "c", "question": "q", "includeChunks": false})
	v, err := WaitForJob(context.Background(), ask, 1000)
	if err != nil || v != "answer" {
		t.Fatal(v, err)
	}
	if row(t, d, first).Status != Running || row(t, d, second).Status != Queued {
		t.Fatal("slots")
	}
	close(slow)
	until(t, func() bool { return row(t, d, second).Status == Done })
	r.Stop()
	if maxBackground != 1 || MaxRunning != 2 {
		t.Fatal(maxBackground)
	}
}

func TestStopWaitsForRunningJobs(t *testing.T) {
	d := openDB(t)
	slow := make(chan struct{})
	finished := atomic.Bool{}
	r := NewRunner(d, handlers(map[string]Handler{Extract: func(context.Context, Job) (any, error) {
		<-slow
		finished.Store(true)
		return nil, nil
	}}, nil), quiet)
	id := enq(t, d, Extract, map[string]any{"documentId": "d"})
	r.Start()
	until(t, func() bool { return row(t, d, id).Status == Running })
	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	close(slow)
	<-stopped
	if !finished.Load() || row(t, d, id).Status != Done {
		t.Fatal("stop did not wait")
	}
}

// A Retry-After with a fraction gives a REAL run_after, as now + delay does
// in JS, and such a row (Go- or TS-made) still reads and claims.
func TestFractionalRetryAfterStoresREAL(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, handlers(map[string]Handler{Profile: func(context.Context, Job) (any, error) {
		return nil, &gateway.Error{Msg: "busy", Status: 429, Type: "overloaded", RetryAfterMs: 300_000.5}
	}}, nil), quiet)
	id := enq(t, d, Profile, map[string]any{"container": "c"})
	r.RunOnce(nowMs())
	var typ string
	if err := d.QueryRow(`SELECT typeof(run_after) FROM jobs WHERE id = ?`, id).Scan(&typ); err != nil || typ != "real" {
		t.Fatalf("typeof %q %v", typ, err)
	}
	j := row(t, d, id)
	if j.RunAfter != math.Trunc(j.RunAfter)+0.5 {
		t.Fatalf("run_after %v", j.RunAfter)
	}
	if at, ok, err := NextRunAt(d, ClaimFilter{}); err != nil || !ok || at != int64(math.Ceil(j.RunAfter)) {
		t.Fatalf("next %d %v %v", at, ok, err)
	}
	if got, _ := r.RunOnce(int64(j.RunAfter) + 1); got == nil || got.ID != id {
		t.Fatal("not claimed")
	}
}

// handler(job.payload, job) on a type without a handler throws a TypeError.
func TestMissingHandlerIsATypeError(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, Handlers{ByType: map[string]Handler{}}, quiet)
	id := enq(t, d, Reminders, map[string]any{})
	r.RunOnce(nowMs())
	if j := row(t, d, id); j.LastError == nil || *j.LastError != "TypeError: handler is not a function" {
		t.Fatalf("%+v", j)
	}
}

// EnqueueWatched registers before the job can run, so even a job that
// finishes at once is heard.
func TestEnqueueWatchedHearsAnInstantJob(t *testing.T) {
	d := openDB(t)
	r := NewRunner(d, handlers(map[string]Handler{Ask: func(context.Context, Job) (any, error) { return "fast", nil }}, nil), quiet)
	r.Start()
	defer r.Stop()
	for i := 0; i < 20; i++ {
		id, ok, w, err := EnqueueWatched(d, "ws_test", Ask, orderedPayload(map[string]any{"container": "c", "question": "hi", "includeChunks": false}), nowMs())
		if err != nil || !ok || w == nil || id == 0 {
			t.Fatal(id, ok, err)
		}
		v, err := w.Wait(context.Background(), 5_000)
		if err != nil || v != "fast" {
			t.Fatalf("run %d: %v %v", i, v, err)
		}
	}
}
