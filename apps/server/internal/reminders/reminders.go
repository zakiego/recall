// Package reminders is reminders.ts: email reminders for facts with a due
// date. Settings are per user and cover every workspace the user belongs
// to, never any other. The `reminders` job runs at the top of every hour; a
// user gets at most one digest a day, once it is past SendHour in their time
// zone, and each reminder (fact, due date, lead time) is sent once.
package reminders

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/email"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
)

const (
	SendHour    = 8
	MaxLeadDays = 365
	MaxLeads    = 6
	// SystemWorkspace: the reminders job serves every workspace, but
	// jobs.workspace_id can't be empty.
	SystemWorkspace = "_system"
	// AppBase is APP_BASE (src/lib/app-path.ts).
	AppBase = "/app"
)

// DefaultLeadDays is DEFAULT_LEAD_DAYS.
var DefaultLeadDays = []int{30, 1}

// Settings is ReminderSettings.
type Settings struct {
	Enabled   bool    `json:"enabled"`
	Email     *string `json:"email"`
	LeadDays  []int   `json:"leadDays"`
	Timezone  string  `json:"timezone"`
	UpdatedAt *int64  `json:"updatedAt"`
}

// NormalizeLeads is normalizeLeads on parsed JSON numbers: integers from 0
// to MaxLeadDays, unique, largest first, at most MaxLeads.
func NormalizeLeads(leads []float64) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, n := range leads {
		if n != math.Trunc(n) || math.IsInf(n, 0) || n < 0 || n > MaxLeadDays {
			continue
		}
		i := int(n)
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a] > out[b] })
	if len(out) > MaxLeads {
		out = out[:MaxLeads]
	}
	return out
}

func ints(l []int) []float64 {
	out := make([]float64, len(l))
	for i, n := range l {
		out[i] = float64(n)
	}
	return out
}

// GetSettings is getReminderSettings.
func GetSettings(q db.Q, userID string) (Settings, error) {
	var enabled int64
	var mail sql.NullString
	var leads, tz string
	var updated int64
	err := q.QueryRow(`SELECT enabled, email, lead_days, timezone, updated_at FROM reminder_settings WHERE user_id = ?`, userID).Scan(&enabled, &mail, &leads, &tz, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{LeadDays: DefaultLeadDays, Timezone: "UTC"}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	s := Settings{Enabled: enabled != 0, Email: model.Str(mail), LeadDays: DefaultLeadDays, Timezone: "UTC", UpdatedAt: &updated}
	if v, err := jsonx.Parse([]byte(leads)); err == nil {
		if arr, ok := v.([]any); ok {
			var nums []float64
			for _, x := range arr {
				if f, ok := x.(float64); ok {
					nums = append(nums, f)
				}
			}
			s.LeadDays = NormalizeLeads(nums)
		}
	}
	if dates.IsTimezone(tz) {
		s.Timezone = tz
	}
	return s, nil
}

// SettingsInput is saveReminderSettings' input.
type SettingsInput struct {
	Enabled  bool
	Email    *string
	LeadDays []float64
	Timezone string
}

// SaveSettings is saveReminderSettings.
func SaveSettings(q db.Q, userID string, in SettingsInput) (Settings, error) {
	var mail *string
	if in.Email != nil {
		if t := strx.Trim(*in.Email); t != "" {
			mail = &t
		}
	}
	tz := "UTC"
	if dates.IsTimezone(in.Timezone) {
		tz = in.Timezone
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	leads := jsonx.Stringify(NormalizeLeads(in.LeadDays))
	now := time.Now().UnixMilli()
	if _, err := q.Exec(`INSERT INTO reminder_settings (user_id, enabled, email, lead_days, timezone, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET enabled = excluded.enabled, email = excluded.email, lead_days = excluded.lead_days, timezone = excluded.timezone, updated_at = excluded.updated_at`,
		userID, enabled, mail, leads, tz, now); err != nil {
		return Settings{}, err
	}
	return GetSettings(q, userID)
}

// --- which reminders are due -------------------------------------------------------

// ActiveLead is activeLead: the smallest lead that covers the days left; ok
// false when none does yet, or the fact is past due.
func ActiveLead(daysLeft int, leads []int) (int, bool) {
	if daysLeft < 0 {
		return 0, false
	}
	best, ok := 0, false
	for _, l := range leads {
		if l >= daysLeft && (!ok || l < best) {
			best, ok = l, true
		}
	}
	return best, ok
}

// Due is DueReminder.
type Due struct {
	Memory        model.Memory
	WorkspaceID   string
	WorkspaceName string
	Due           dates.Dated
	DaysLeft      int
	Lead          int
}

// chainBack: this memory and every version before it. A reminder sent for
// an older version counts, so editing a fact doesn't send it again.
func chainBack(q db.Q, workspaceID, id string) ([]string, error) {
	rows, err := q.Query(`WITH RECURSIVE chain(id, prev, n) AS (
         SELECT id, supersedes_id, 0 FROM memories WHERE id = ? AND workspace_id = ?
         UNION ALL SELECT m.id, m.supersedes_id, chain.n + 1 FROM memories m JOIN chain ON m.id = chain.prev
         WHERE chain.n < 100 AND m.workspace_id = ?
       ) SELECT id FROM chain`, id, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func alreadySent(q db.Q, userID string, memIDs []string, dueDate string, lead int) (bool, error) {
	if len(memIDs) == 0 {
		return false, nil
	}
	args := []any{userID, dueDate, lead}
	for _, id := range memIDs {
		args = append(args, id)
	}
	var one int
	err := q.QueryRow(`SELECT 1 FROM reminders_sent WHERE user_id = ? AND due_date = ? AND lead_days = ? AND status = 'sent'
       AND memory_id IN (`+placeholders(len(memIDs))+`) LIMIT 1`, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DueFor is dueRemindersFor: what a user should hear about today, only from
// workspaces they belong to, soonest first.
func DueFor(q db.Q, userID string, leadDays []int, today string) ([]Due, error) {
	leads := NormalizeLeads(ints(leadDays))
	out := []Due{}
	if len(leads) == 0 {
		return out, nil
	}
	maxLead := leads[0]
	for _, l := range leads {
		maxLead = max(maxLead, l)
	}
	horizon := dates.AddDays(today, maxLead)
	ms, err := users.ListMemberships(q, userID)
	if err != nil {
		return nil, err
	}
	limit := 500
	for _, ws := range ms {
		upcoming, err := memories.ListUpcoming(q, memories.UpcomingRange{WorkspaceID: ws.ID, From: today, To: horizon}, &limit)
		if err != nil {
			return nil, err
		}
		for _, m := range upcoming {
			due := memories.DueOf(m)
			if due == nil {
				continue
			}
			daysLeft := dates.DaysBetween(today, due.Date)
			lead, ok := ActiveLead(daysLeft, leads)
			if !ok {
				continue
			}
			chain, err := chainBack(q, ws.ID, m.ID)
			if err != nil {
				return nil, err
			}
			sent, err := alreadySent(q, userID, chain, due.Date, lead)
			if err != nil {
				return nil, err
			}
			if sent {
				continue
			}
			out = append(out, Due{Memory: m, WorkspaceID: ws.ID, WorkspaceName: ws.Name, Due: *due, DaysLeft: daysLeft, Lead: lead})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DaysLeft < out[j].DaysLeft })
	return out, nil
}

// --- the email ---------------------------------------------------------------------

func upcomingURL() string { return config.Load().URL + AppBase + "/upcoming" }

// Item is what one line of the email needs.
type Item struct {
	Fact          string
	Container     string
	Due           dates.Dated
	DaysLeft      int
	WorkspaceName string
}

// ItemOf is the email line of a due reminder.
func ItemOf(d Due) Item {
	return Item{Fact: d.Memory.Fact, Container: d.Memory.Container, Due: d.Due, DaysLeft: d.DaysLeft, WorkspaceName: d.WorkspaceName}
}

// Email is reminderEmail: the subject, text and HTML of a digest (or of the
// test email).
func Email(items []Item, test bool) (subject, text, html string) {
	when := func(i Item) string { return dates.Format(i.Due) + ", " + dates.RelativeDays(i.DaysLeft) }
	where := func(i Item) string { return i.WorkspaceName + " / " + i.Container }
	switch {
	case test:
		subject = "Recall test: reminders work"
	case len(items) == 1:
		f := items[0].Fact
		if strx.Len16(f) > 60 {
			f = strx.Slice16(f, 57) + "..."
		}
		subject = "Due " + dates.RelativeDays(items[0].DaysLeft) + ": " + f
	default:
		subject = fmt.Sprintf("%d things due soon", len(items))
	}
	var intro string
	switch {
	case test && len(items) > 0:
		intro = "This is a test from Recall. Real reminders look like this, with these facts that are coming up:"
	case test:
		intro = "This is a test from Recall. Reminders will reach this address. Nothing is due soon right now."
	case len(items) == 1:
		intro = "One fact in your memory is coming due:"
	default:
		intro = fmt.Sprintf("%d facts in your memory are coming due:", len(items))
	}
	lines := []string{intro, ""}
	for _, i := range items {
		lines = append(lines, "- "+i.Fact+"\n  Due "+when(i)+" ("+where(i)+")")
	}
	lines = append(lines, "", "See everything coming up: "+upcomingURL(), "", "You get this because reminders are on in Recall. Turn them off under Settings, Reminders.")
	text = strings.Join(lines, "\n")
	var rows strings.Builder
	for _, i := range items {
		rows.WriteString(`<li style="margin:0 0 12px"><div>` + email.EscapeHTML(i.Fact) + `</div><div style="color:#71717a;font-size:13px">Due ` + email.EscapeHTML(when(i)) + ` · ` + email.EscapeHTML(where(i)) + `</div></li>`)
	}
	list := ""
	if rows.Len() > 0 {
		list = `<ul style="padding-left:18px">` + rows.String() + `</ul>`
	}
	html = `<div style="font-family:system-ui,sans-serif;font-size:15px;line-height:1.5;color:#18181b">
<p>` + email.EscapeHTML(intro) + `</p>` + list + `
<p><a href="` + email.EscapeHTML(upcomingURL()) + `">See everything coming up</a></p>
<p style="color:#71717a;font-size:13px">You get this because reminders are on in Recall. Turn them off under Settings, Reminders.</p></div>`
	return subject, text, html
}

// --- the job -----------------------------------------------------------------------

// Deps are ReminderDeps; zero fields are the real ones.
type Deps struct {
	Send       email.Send
	Now        func() time.Time
	Configured func() bool
}

func (d Deps) fill() Deps {
	if d.Send == nil {
		d.Send = email.SendEmail
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Configured == nil {
		d.Configured = email.Configured
	}
	return d
}

// Run is ReminderRun.
type Run struct {
	Users     int    `json:"users"`
	Emails    int    `json:"emails"`
	Reminders int    `json:"reminders"`
	Failed    int    `json:"failed"`
	Skipped   string `json:"skipped,omitempty"`
}

func claim(d *db.DB, userID string, items []Due) ([]int64, error) {
	var out []int64
	err := d.Tx(func(tx db.Q) error {
		for _, i := range items {
			if _, err := tx.Exec(`INSERT INTO reminders_sent (user_id, workspace_id, memory_id, due_date, lead_days) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
				userID, i.WorkspaceID, i.Memory.ID, i.Due.Date, i.Lead); err != nil {
				return err
			}
			var id int64
			if err := tx.QueryRow(`SELECT id FROM reminders_sent WHERE user_id = ? AND memory_id = ? AND due_date = ? AND lead_days = ?`, userID, i.Memory.ID, i.Due.Date, i.Lead).Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return nil
	})
	return out, err
}

// DefaultLog is runReminders' default log.
func DefaultLog(m string) { logx.Info("[recall:reminders] %s", m) }

// RunReminders is runReminders: every due digest. It never fails: one
// user's failure is logged and the next run retries it.
func RunReminders(ctx context.Context, d *db.DB, deps Deps, log func(string)) (Run, error) {
	deps = deps.fill()
	if log == nil {
		log = DefaultLog
	}
	var run Run
	if !deps.Configured() {
		run.Skipped = "email_not_configured"
		return run, nil
	}
	now := deps.Now().UnixMilli()
	rows, err := d.Query(`SELECT s.user_id FROM reminder_settings s JOIN user u ON u.id = s.user_id WHERE s.enabled = 1 AND s.email IS NOT NULL`)
	// A database error outside one user's send fails the run, as the TS
	// throw did: the job fails and the runner retries it.
	if err != nil {
		return run, err
	}
	var userIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return run, err
		}
		userIDs = append(userIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return run, err
	}
	for _, userID := range userIDs {
		settings, err := GetSettings(d, userID)
		if err != nil {
			return run, err
		}
		if settings.Email == nil || *settings.Email == "" || dates.HourIn(settings.Timezone, now) < SendHour {
			continue
		}
		run.Users++
		if err := sendDigest(ctx, d, deps, userID, settings, now, &run); err != nil {
			run.Failed++
			log("user " + userID + ": " + err.Error())
		}
	}
	if run.Emails > 0 || run.Failed > 0 {
		log(fmt.Sprintf("sent %d email(s) with %d reminder(s), %d failed", run.Emails, run.Reminders, run.Failed))
	}
	return run, nil
}

func sendDigest(ctx context.Context, d *db.DB, deps Deps, userID string, settings Settings, now int64, run *Run) error {
	items, err := DueFor(d, userID, settings.LeadDays, dates.TodayIn(settings.Timezone, now))
	if err != nil || len(items) == 0 {
		return err
	}
	claimed, err := claim(d, userID, items)
	if err != nil {
		return err
	}
	sorted := append([]int64(nil), claimed...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	parts := make([]string, len(sorted))
	for i, id := range sorted {
		parts[i] = fmt.Sprint(id)
	}
	sum := sha256.Sum256([]byte(userID + ":" + strings.Join(parts, ",")))
	key := hex.EncodeToString(sum[:])[:32]
	list := make([]Item, len(items))
	for i, it := range items {
		list[i] = ItemOf(it)
	}
	subject, text, html := Email(list, false)
	if _, err := deps.Send(ctx, email.Message{To: *settings.Email, Subject: subject, Text: text, HTML: html, IdempotencyKey: "recall-reminders-" + key}); err != nil {
		return err
	}
	args := []any{time.Now().UnixMilli()}
	for _, id := range claimed {
		args = append(args, id)
	}
	if _, err := d.Exec(`UPDATE reminders_sent SET status = 'sent', sent_at = ? WHERE id IN (`+placeholders(len(claimed))+`)`, args...); err != nil {
		return err
	}
	run.Emails++
	run.Reminders += len(items)
	return nil
}

// TestResult is sendTestReminder's result.
type TestResult struct {
	ID    string `json:"id"`
	To    string `json:"to"`
	Items int    `json:"items"`
}

// SendTest is sendTestReminder: one test email with what's coming up.
// Records nothing.
func SendTest(ctx context.Context, d *db.DB, userID string, deps Deps) (TestResult, error) {
	deps = deps.fill()
	settings, err := GetSettings(d, userID)
	if err != nil {
		return TestResult{}, err
	}
	if !deps.Configured() {
		return TestResult{}, errors.New("Email isn't configured on this server.")
	}
	if settings.Email == nil || *settings.Email == "" {
		return TestResult{}, errors.New("Save an email address first.")
	}
	today := dates.TodayIn(settings.Timezone, deps.Now().UnixMilli())
	lead := 30
	for _, l := range settings.LeadDays {
		lead = max(lead, l)
	}
	items, err := DueFor(d, userID, []int{lead}, today)
	if err != nil {
		return TestResult{}, err
	}
	if len(items) > 10 {
		items = items[:10]
	}
	list := make([]Item, len(items))
	for i, it := range items {
		list[i] = ItemOf(it)
	}
	subject, text, html := Email(list, true)
	id, err := deps.Send(ctx, email.Message{To: *settings.Email, Subject: subject, Text: text, HTML: html})
	if err != nil {
		return TestResult{}, err
	}
	return TestResult{ID: id, To: *settings.Email, Items: len(items)}, nil
}

// --- scheduling --------------------------------------------------------------------

// NextHour is nextHour: the next top of the hour.
func NextHour(now int64) int64 { return now/3_600_000*3_600_000 + 3_600_000 }

// Schedule is scheduleReminders: makes sure exactly one reminders job is
// queued. ok false when one already was.
func Schedule(q db.Q, now int64) (int64, bool, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM jobs WHERE type = 'reminders' AND status = 'queued'`).Scan(&id)
	if err == nil {
		return 0, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	id, ok, err := jobs.Enqueue(q, SystemWorkspace, jobs.Reminders, jsonx.NewMap[any](), NextHour(now))
	return id, ok, err
}

// Handler is the `reminders` job: it always queues the next run (try,
// finally), and fails only when the database does.
func Handler(d *db.DB, deps Deps) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) (any, error) {
		run, err := RunReminders(ctx, d, deps, nil)
		if _, _, serr := Schedule(d, time.Now().UnixMilli()); serr != nil {
			return nil, serr
		}
		if err != nil {
			return nil, err
		}
		return run, nil
	}
}
