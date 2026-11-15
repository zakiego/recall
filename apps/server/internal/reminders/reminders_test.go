package reminders_test

// reminders.test.ts.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/email"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/users"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

type mailer struct {
	mu   sync.Mutex
	sent []email.Message
	all  []email.Message
	fail bool
}

func (m *mailer) send(_ context.Context, msg email.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all = append(m.all, msg)
	if m.fail {
		return "", errors.New("Resend answered 500: boom")
	}
	m.sent = append(m.sent, msg)
	return "email_" + string(rune('0'+len(m.sent))), nil
}

type env struct {
	d   *db.DB
	now time.Time
}

func (e *env) deps(m *mailer) reminders.Deps {
	return reminders.Deps{Send: m.send, Now: func() time.Time { return e.now }, Configured: func() bool { return true }}
}

func day(d string) *dates.Dated { return &dates.Dated{Date: d, Precision: dates.Day} }

type fixture struct {
	alice, bob            users.User
	alpha, beta           users.Workspace
	insurance, tax, lease model.Memory
}

func setup(t *testing.T) (*env, fixture) {
	d := testkit.OpenDB(t)
	// 09:00 in Makassar (UTC+8) on 2026-11-10.
	e := &env{d: d, now: time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)}
	var f fixture
	var err error
	if f.alice, err = users.CreateWithPassword(d, "alice", "alice-password-123", ""); err != nil {
		t.Fatal(err)
	}
	f.bob, _ = users.CreateWithPassword(d, "bob", "bob-password-1234", "")
	f.alpha, _ = users.CreateWorkspace(d, "Alpha", "ws_alpha", "alpha")
	f.beta, _ = users.CreateWorkspace(d, "Beta", "ws_beta", "beta")
	users.AddMember(d, f.alpha.ID, f.alice.ID, "owner")
	users.AddMember(d, f.beta.ID, f.bob.ID, "owner")
	add := func(ws, container, fact, keywords, due string) model.Memory {
		m, err := memories.AddMemoryDirect(d, memories.NewMemory{WorkspaceID: ws, Container: container, Fact: fact, Keywords: keywords, Kind: "dynamic", Due: day(due)}, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	f.insurance = add("ws_alpha", "me", "Car insurance renewal", "car insurance", "2026-11-30")
	f.tax = add("ws_alpha", "work", "Tax report due", "tax", "2026-11-11")
	add("ws_alpha", "me", "Far away renewal", "far", "2027-06-01")
	add("ws_alpha", "me", "Expired gym", "gym", "2026-11-01")
	f.lease = add("ws_beta", "me", "Beta office lease ends", "lease", "2026-11-12")
	return e, f
}

func enable(t *testing.T, d *db.DB, userID, addr string) {
	if _, err := reminders.SaveSettings(d, userID, reminders.SettingsInput{Enabled: true, Email: &addr, LeadDays: []float64{30, 1}, Timezone: "Asia/Makassar"}); err != nil {
		t.Fatal(err)
	}
}

func run(e *env, m *mailer) reminders.Run {
	r, err := reminders.RunReminders(context.Background(), e.d, e.deps(m), func(string) {})
	if err != nil {
		panic(err)
	}
	return r
}

func TestActiveLead(t *testing.T) {
	for _, c := range []struct {
		days  int
		leads []int
		want  int
		ok    bool
	}{{40, []int{30, 1}, 0, false}, {30, []int{30, 1}, 30, true}, {20, []int{30, 1}, 30, true}, {1, []int{30, 1}, 1, true}, {0, []int{30, 1}, 1, true}, {0, []int{30, 1, 0}, 0, true}, {-1, []int{30, 1}, 0, false}} {
		if got, ok := reminders.ActiveLead(c.days, c.leads); got != c.want || ok != c.ok {
			t.Errorf("%d %v: %d %v", c.days, c.leads, got, ok)
		}
	}
}

func TestSettingsDefaultsAndCleanup(t *testing.T) {
	e, f := setup(t)
	s, _ := reminders.GetSettings(e.d, f.alice.ID)
	if s.Enabled || s.Email != nil || !reflect.DeepEqual(s.LeadDays, []int{30, 1}) {
		t.Fatalf("%+v", s)
	}
	addr := " a@example.com "
	saved, err := reminders.SaveSettings(e.d, f.alice.ID, reminders.SettingsInput{Enabled: true, Email: &addr, LeadDays: []float64{1, 30, 30, -2, 400, 7.5, 0}, Timezone: "Nope/Zone"})
	if err != nil || !saved.Enabled || *saved.Email != "a@example.com" || !reflect.DeepEqual(saved.LeadDays, []int{30, 1, 0}) || saved.Timezone != "UTC" {
		t.Fatalf("%+v %v", saved, err)
	}
}

func sentRows(t *testing.T, d *db.DB, where string, args ...any) [][2]any {
	rows, err := d.Query(`SELECT lead_days, status FROM reminders_sent `+where+` ORDER BY id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]any
	for rows.Next() {
		var lead int64
		var status string
		rows.Scan(&lead, &status)
		out = append(out, [2]any{lead, status})
	}
	return out
}

func TestOneDigestThenNeverTwice(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	m := &mailer{}
	first := run(e, m)
	if first != (reminders.Run{Users: 1, Emails: 1, Reminders: 2}) {
		t.Fatalf("%+v", first)
	}
	msg := m.sent[0]
	if msg.To != "alice@example.com" || msg.Subject != "2 things due soon" ||
		!strings.Contains(msg.Text, "Tax report due\n  Due Nov 11, 2026, tomorrow (Alpha / work)") ||
		!strings.Contains(msg.Text, "Car insurance renewal\n  Due Nov 30, 2026, in 20 days (Alpha / me)") ||
		strings.Contains(msg.Text, "Far away") || strings.Contains(msg.Text, "Expired gym") ||
		!regexp.MustCompile(`^recall-reminders-[0-9a-f]{32}$`).MatchString(msg.IdempotencyKey) {
		t.Fatalf("%+v", msg)
	}
	// Same day, next hour: nothing new.
	e.now = e.now.Add(time.Hour)
	if r := run(e, m); r.Emails != 0 {
		t.Fatal(r)
	}
	// Next day: nothing either.
	e.now = e.now.Add(24 * time.Hour)
	if r := run(e, m); r.Emails != 0 {
		t.Fatal(r)
	}
	// The day before the insurance is due, its 1-day reminder goes out.
	e.now = time.Date(2026, 11, 29, 1, 0, 0, 0, time.UTC)
	run(e, m)
	if len(m.sent) != 2 || m.sent[1].Subject != "Due tomorrow: Car insurance renewal" {
		t.Fatalf("%+v", m.sent)
	}
	if got := sentRows(t, e.d, ""); !reflect.DeepEqual(got, [][2]any{{int64(1), "sent"}, {int64(30), "sent"}, {int64(1), "sent"}}) {
		t.Fatal(got)
	}
}

func TestWaitsUntilEightLocal(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	m := &mailer{}
	e.now = time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC) // 07:30 in Makassar
	run(e, m)
	if len(m.sent) != 0 {
		t.Fatal("sent before 08:00")
	}
	e.now = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) // 08:00
	run(e, m)
	if len(m.sent) != 1 {
		t.Fatal("not sent at 08:00")
	}
}

func TestFailedSendRetriesWithSameKey(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	broken := &mailer{fail: true}
	var log []string
	r, _ := reminders.RunReminders(context.Background(), e.d, e.deps(broken), func(s string) { log = append(log, s) })
	if r.Emails != 0 || r.Failed != 1 || !strings.Contains(log[0], "Resend answered 500") {
		t.Fatal(r, log)
	}
	for _, row := range sentRows(t, e.d, "") {
		if row[1] != "pending" {
			t.Fatal(row)
		}
	}
	key := broken.all[0].IdempotencyKey
	ok := &mailer{}
	e.now = e.now.Add(time.Hour)
	if r := run(e, ok); r.Emails != 1 || r.Reminders != 2 {
		t.Fatal(r)
	}
	if ok.sent[0].IdempotencyKey != key {
		t.Fatal("key changed")
	}
	var n, sentWithTime int
	e.d.QueryRow(`SELECT count(*), sum(status = 'sent' AND sent_at IS NOT NULL) FROM reminders_sent`).Scan(&n, &sentWithTime)
	if n != 2 || sentWithTime != 2 {
		t.Fatal(n, sentWithTime)
	}
}

func TestEditDoesntResendMovingDueDoes(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	m := &mailer{}
	run(e, m)
	fact := "Car insurance with Astra renewal"
	v2, _, err := memories.EditMemory(e.d, "ws_alpha", f.insurance.ID, memories.Patch{Fact: &fact}, func() int64 { return time.Now().UnixMilli() })
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Hour)
	run(e, m)
	if len(m.sent) != 1 {
		t.Fatal(len(m.sent))
	}
	if _, _, err := memories.EditMemory(e.d, "ws_alpha", v2.ID, memories.Patch{Due: memories.Value(day("2026-11-25"))}, func() int64 { return time.Now().UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	run(e, m)
	if len(m.sent) != 2 || !strings.Contains(m.sent[1].Text, "Due Nov 25, 2026") {
		t.Fatalf("%+v", m.sent)
	}
}

func TestSkipsDeletedDisabledAndEmailless(t *testing.T) {
	e, f := setup(t)
	addr := "alice@example.com"
	reminders.SaveSettings(e.d, f.alice.ID, reminders.SettingsInput{Enabled: false, Email: &addr, LeadDays: []float64{30, 1}, Timezone: "UTC"})
	reminders.SaveSettings(e.d, f.bob.ID, reminders.SettingsInput{Enabled: true, LeadDays: []float64{30, 1}, Timezone: "UTC"})
	m := &mailer{}
	run(e, m)
	if len(m.sent) != 0 {
		t.Fatal("sent")
	}
	memories.ForgetMemory(e.d, "ws_alpha", f.tax.ID, time.Now().UnixMilli())
	memories.ForgetMemory(e.d, "ws_alpha", f.insurance.ID, time.Now().UnixMilli())
	enable(t, e.d, f.alice.ID, "alice@example.com")
	run(e, m)
	if len(m.sent) != 0 {
		t.Fatal("sent for forgotten facts")
	}
}

func TestNothingWithoutEmailConfigured(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	m := &mailer{}
	deps := e.deps(m)
	deps.Configured = func() bool { return false }
	r, _ := reminders.RunReminders(context.Background(), e.d, deps, func(string) {})
	if r.Skipped != "email_not_configured" || r.Emails != 0 || len(m.sent) != 0 || len(sentRows(t, e.d, "")) != 0 {
		t.Fatal(r)
	}
}

func TestUserOnlyHearsOwnWorkspaces(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	enable(t, e.d, f.bob.ID, "bob@example.com")
	m := &mailer{}
	run(e, m)
	var toAlice, toBob email.Message
	for _, msg := range m.sent {
		switch msg.To {
		case "alice@example.com":
			toAlice = msg
		case "bob@example.com":
			toBob = msg
		}
	}
	if strings.Contains(toAlice.Text, "Beta") || !strings.Contains(toBob.Text, "Beta office lease ends") || regexp.MustCompile(`Car insurance|Tax report|Alpha`).MatchString(toBob.Text) {
		t.Fatal(toAlice.Text, toBob.Text)
	}
	due, _ := reminders.DueFor(e.d, f.alice.ID, []int{365}, "2026-11-10")
	for _, r := range due {
		if r.WorkspaceID != "ws_alpha" {
			t.Fatal(r.WorkspaceID)
		}
	}
	// Joining beta adds its facts to Alice's next digest.
	users.AddMember(e.d, f.beta.ID, f.alice.ID, "member")
	e.now = e.now.Add(time.Hour)
	run(e, m)
	last := m.sent[len(m.sent)-1]
	if last.To != "alice@example.com" || !strings.Contains(last.Text, "Beta office lease ends\n  Due Nov 12, 2026, in 2 days (Beta / me)") {
		t.Fatalf("%+v", last)
	}
	if n := len(sentRows(t, e.d, "WHERE user_id = ?", f.bob.ID)); n != 1 {
		t.Fatal(n)
	}
}

func TestTestEmail(t *testing.T) {
	e, f := setup(t)
	m := &mailer{}
	if _, err := reminders.SendTest(context.Background(), e.d, f.alice.ID, e.deps(m)); err == nil || err.Error() != "Save an email address first." {
		t.Fatal(err)
	}
	enable(t, e.d, f.alice.ID, "alice@example.com")
	off := e.deps(m)
	off.Configured = func() bool { return false }
	if _, err := reminders.SendTest(context.Background(), e.d, f.alice.ID, off); err == nil || err.Error() != "Email isn't configured on this server." {
		t.Fatal(err)
	}
	res, err := reminders.SendTest(context.Background(), e.d, f.alice.ID, e.deps(m))
	if err != nil || res.To != "alice@example.com" || res.Items != 2 || m.sent[0].Subject != "Recall test: reminders work" || len(sentRows(t, e.d, "")) != 0 {
		t.Fatal(res, err)
	}
	// The real run still sends afterwards.
	run(e, m)
	if len(m.sent) != 2 {
		t.Fatal(len(m.sent))
	}
}

func TestEscapesFactText(t *testing.T) {
	_, _, html := reminders.Email([]reminders.Item{{Fact: "<script>x</script> & co", Container: "me", Due: *day("2026-11-11"), DaysLeft: 1, WorkspaceName: "A"}}, false)
	if !strings.Contains(html, "&lt;script&gt;x&lt;/script&gt; &amp; co") || strings.Contains(html, "<script>") {
		t.Fatal(html)
	}
}

func TestOneJobAtTheNextHour(t *testing.T) {
	d := testkit.OpenDB(t)
	at := time.Date(2026, 11, 10, 1, 17, 0, 0, time.UTC).UnixMilli()
	if _, ok, err := reminders.Schedule(d, at); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, ok, _ := reminders.Schedule(d, at); ok {
		t.Fatal("second job")
	}
	var ws string
	var runAfter int64
	d.QueryRow(`SELECT workspace_id, run_after FROM jobs WHERE type = 'reminders'`).Scan(&ws, &runAfter)
	if ws != reminders.SystemWorkspace || runAfter != time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatal(ws, runAfter)
	}
	if reminders.NextHour(time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli()) != time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatal("nextHour")
	}
}

func TestJobSendsThenQueuesNextEvenOnFailure(t *testing.T) {
	e, f := setup(t)
	enable(t, e.d, f.alice.ID, "alice@example.com")
	m := &mailer{}
	r := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Reminders: e.deps(m)}), func(string) {})
	// Only the reminders job: the profile rebuilds setup() queued would call the gateway.
	e.d.Exec(`DELETE FROM jobs`)
	reminders.Schedule(e.d, e.now.UnixMilli()-3_600_000)
	if _, err := r.RunOnce(e.now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if len(m.sent) != 1 {
		t.Fatal(len(m.sent))
	}
	queued := func() []string {
		rows, _ := e.d.Query(`SELECT type FROM jobs WHERE status = 'queued'`)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	if q := queued(); !reflect.DeepEqual(q, []string{"reminders"}) {
		t.Fatal(q)
	}
	broken := &mailer{fail: true}
	r2 := jobs.NewRunner(e.d, pipeline.Handlers(e.d, pipeline.Deps{Reminders: e.deps(broken)}), func(string) {})
	e.d.Exec(`UPDATE jobs SET run_after = 0 WHERE status = 'queued'`)
	r2.RunOnce(time.Now().UnixMilli())
	var failed int
	e.d.QueryRow(`SELECT count(*) FROM jobs WHERE status = 'failed'`).Scan(&failed)
	if len(queued()) != 1 || failed != 0 {
		t.Fatal(queued(), failed)
	}
}

// --- Resend sender -----------------------------------------------------------------

func TestResendPostsMessage(t *testing.T) {
	var got *http.Request
	var body string
	send := email.NewResendSender("re_test", "Recall <reminders@example.com>", func(r *http.Request) (*http.Response, error) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"re_123"}`))}, nil
	})
	id, err := send(context.Background(), email.Message{To: "a@example.com", Subject: "s", Text: "t", HTML: "<p>t</p>", IdempotencyKey: "k1"})
	if err != nil || id != "re_123" {
		t.Fatal(id, err)
	}
	if got.URL.String() != "https://api.resend.com/emails" || got.Header.Get("Authorization") != "Bearer re_test" || got.Header.Get("Idempotency-Key") != "k1" {
		t.Fatal(got.URL, got.Header)
	}
	if body != `{"from":"Recall <reminders@example.com>","to":["a@example.com"],"subject":"s","text":"t","html":"<p>t</p>"}` {
		t.Fatal(body)
	}
}

func TestResendError(t *testing.T) {
	send := email.NewResendSender("re_test", "x@example.com", func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"name":"validation_error","message":"The domain is not verified"}`))}, nil
	})
	if _, err := send(context.Background(), email.Message{To: "a@example.com", Subject: "s", Text: "t"}); err == nil || err.Error() != "Resend answered 403: The domain is not verified" {
		t.Fatal(err)
	}
}

func TestConfiguredNeedsBothKeys(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RECALL_EMAIL_FROM", "")
	if email.Configured() {
		t.Fatal("configured without keys")
	}
	t.Setenv("RESEND_API_KEY", "re_x")
	if email.Configured() {
		t.Fatal("configured with one key")
	}
	t.Setenv("RECALL_EMAIL_FROM", "Recall <reminders@example.com>")
	if !email.Configured() {
		t.Fatal("not configured with both")
	}
}
