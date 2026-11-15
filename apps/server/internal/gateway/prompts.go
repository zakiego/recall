package gateway

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// FenceSafe is fenceSafe: runs of 3+ angle brackets become lookalikes, so
// untrusted text can't close the <<< >>> fence.
func FenceSafe(text string) string {
	var b strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); {
		r := runes[i]
		if r == '<' || r == '>' {
			j := i
			for j < len(runes) && runes[j] == r {
				j++
			}
			n := j - i
			if n >= 3 {
				repl := "‹"
				if r == '>' {
					repl = "›"
				}
				b.WriteString(strings.Repeat(repl, n))
			} else {
				b.WriteString(string(runes[i:j]))
			}
			i = j
			continue
		}
		b.WriteRune(r)
		i++
	}
	return b.String()
}

func fenced(text string) string { return "<<<\n" + FenceSafe(text) + "\n>>>" }

// ExistingMemory is one numbered memory in the extract prompt.
type ExistingMemory struct {
	Ref  int
	Fact string
	Kind string
}

// ExtractInput is extractPrompt's input. DocumentDate "" means today; Source
// is "" or "coding-session".
type ExtractInput struct {
	Content      string
	Existing     []ExistingMemory
	Now          time.Time
	DocumentDate string
	Source       string
}

// jsDateParse is Date.parse of a YYYY-MM-DD string (UTC midnight), NaN when
// it isn't one.
func jsDateParse(s string) float64 {
	t, err := time.Parse("2006-01-02", s)
	if err != nil || len(s) != 10 {
		return math.NaN()
	}
	return float64(t.UnixMilli())
}

// ExtractPrompt is extractPrompt.
func ExtractPrompt(in ExtractInput) string {
	existing := "(none)"
	if len(in.Existing) > 0 {
		parts := make([]string, len(in.Existing))
		for i, m := range in.Existing {
			parts[i] = fmt.Sprintf("%d. [%s] %s", m.Ref, m.Kind, FenceSafe(m.Fact))
		}
		existing = strings.Join(parts, "\n")
	}
	today := dates.ISODate(in.Now.UnixMilli())
	documentDate := in.DocumentDate
	if documentDate == "" {
		documentDate = today
	}
	lines := []string{"Today is " + today + ".", "DOCUMENT DATE: " + documentDate + " (resolve relative dates in the content against this)."}
	age := math.Floor((jsDateParse(today) - jsDateParse(documentDate)) / 86_400_000)
	if age > OldDocumentDays {
		var s string
		if age >= 730 {
			s = jsonx.NumberString(math.Floor(age/365)) + " years"
		} else {
			s = jsonx.NumberString(math.Floor(age/30)) + " months"
		}
		lines = append(lines, "This document is about "+s+` old: follow the "Old content" rules.`)
	}
	if in.Source == "coding-session" {
		lines = append(lines, `SOURCE: a coding-agent session. Follow the "Coding sessions" rules.`)
	}
	return strings.Join(lines, "\n") + "\n\nEXISTING MEMORIES:\n" + existing + "\n\nNEW CONTENT:\n" + fenced(in.Content)
}

// ProfileFact is one line of the profile prompt. AsOf "" is left out.
type ProfileFact struct {
	Fact string
	Kind string
	AsOf string
}

// ProfilePrompt is profilePrompt; now nil leaves the date line out.
func ProfilePrompt(facts []ProfileFact, now *time.Time) string {
	lines := make([]string, len(facts))
	for i, f := range facts {
		as := ""
		if f.AsOf != "" {
			as = ", as of " + f.AsOf
		}
		lines[i] = "- [" + f.Kind + as + "] " + f.Fact
	}
	head := ""
	if now != nil {
		head = "Today is " + dates.ISODate(now.UnixMilli()) + ".\n\n"
	}
	return head + "FACTS:\n" + strings.Join(lines, "\n")
}

// AskFact is one numbered fact in the ask prompt.
type AskFact struct {
	Ref       string
	Text      string
	Kind      string
	Date      string
	Happened  string
	Due       string
	DuePassed bool
}

// AskExcerpt is one numbered excerpt in the ask prompt.
type AskExcerpt struct {
	Ref   string
	Text  string
	Title *string
}

// AskPerson is one person named in the question.
type AskPerson struct {
	Name         string
	Aliases      []string
	Role         *string
	Organization *string
	Summary      string
	Contact      []string
}

// AskInput is askPrompt's input.
type AskInput struct {
	Question      string
	Facts         []AskFact
	Excerpts      []AskExcerpt
	ProfileStatic string
	ProfileDyn    string
	Now           time.Time
	People        []AskPerson
}

// AskPrompt is askPrompt.
func AskPrompt(in AskInput) string {
	var profile []string
	for _, p := range []string{in.ProfileStatic, in.ProfileDyn} {
		if p != "" {
			profile = append(profile, p)
		}
	}
	parts := []string{"Today is " + dates.ISODate(in.Now.UnixMilli()) + "."}
	if len(profile) > 0 {
		parts = append(parts, "PROFILE (background, not citable):\n"+fenced(strings.Join(profile, " ")))
	}
	if len(in.People) > 0 {
		parts = append(parts, AskPeopleSection(in.People))
	}
	facts := "(none)"
	if len(in.Facts) > 0 {
		lines := make([]string, len(in.Facts))
		for i, f := range in.Facts {
			meta := []string{f.Kind, f.Date}
			if f.Happened != "" {
				meta = append(meta, "happened "+f.Happened)
			}
			if f.Due != "" {
				d := "due " + f.Due
				if f.DuePassed {
					d += ", passed"
				}
				meta = append(meta, d)
			}
			var kept []string
			for _, m := range meta {
				if m != "" {
					kept = append(kept, m)
				}
			}
			lines[i] = "[" + f.Ref + "] (" + strings.Join(kept, ", ") + ") " + f.Text
		}
		facts = fenced(strings.Join(lines, "\n"))
	}
	parts = append(parts, "FACTS:\n"+facts)
	if len(in.Excerpts) > 0 {
		ex := make([]string, len(in.Excerpts))
		for i, e := range in.Excerpts {
			from := "an untitled document"
			if e.Title != nil && *e.Title != "" {
				from = `"` + *e.Title + `"`
			}
			ex[i] = "[" + e.Ref + "] from " + from + ":\n" + e.Text
		}
		parts = append(parts, "EXCERPTS:\n"+fenced(strings.Join(ex, "\n\n")))
	}
	parts = append(parts, "QUESTION:\n"+fenced(in.Question))
	return strings.Join(parts, "\n\n")
}

// AskPeopleSection is askPeopleSection.
func AskPeopleSection(people []AskPerson) string {
	lines := make([]string, len(people))
	for i, p := range people {
		parts := []string{p.Name}
		if len(p.Aliases) > 0 {
			parts = append(parts, "also called "+strings.Join(p.Aliases, ", "))
		}
		role, org := "", ""
		if p.Role != nil {
			role = *p.Role
		}
		if p.Organization != nil {
			org = *p.Organization
		}
		if role != "" || org != "" {
			var ro []string
			for _, x := range []string{role, org} {
				if x != "" {
					ro = append(ro, x)
				}
			}
			parts = append(parts, strings.Join(ro, " at "))
		}
		if len(p.Contact) > 0 {
			parts = append(parts, "contact "+strings.Join(p.Contact, ", "))
		}
		line := "- " + strings.Join(parts, "; ")
		if p.Summary != "" {
			line += ". " + p.Summary
		}
		lines[i] = line
	}
	return "PEOPLE named in the question (background, not citable; the FACTS about them are listed first):\n" + fenced(strings.Join(lines, "\n"))
}

func quotedContainer(container string) string { return jsonx.Stringify(FenceSafe(container)) }

// PeopleRules is peopleRules.
func PeopleRules(container string) string {
	return peopleRulesPrefix + quotedContainer(container) + peopleRulesSuffix
}

// PeopleBackfillSystem is peopleBackfillSystem.
func PeopleBackfillSystem(container string) string {
	return peopleBackfillPrefix + quotedContainer(container) + peopleBackfillSuffix
}

// RefFact is a numbered fact.
type RefFact struct {
	Ref  int
	Fact string
}

// PeopleBackfillPrompt is peopleBackfillPrompt.
func PeopleBackfillPrompt(facts []RefFact) string {
	lines := make([]string, len(facts))
	for i, f := range facts {
		lines[i] = fmt.Sprintf("%d. %s", f.Ref, f.Fact)
	}
	return "FACTS:\n" + fenced(strings.Join(lines, "\n"))
}

// BackfillFact is one fact of the date backfill prompt.
type BackfillFact struct {
	Ref    int
	Fact   string
	Stored string
}

// BackfillPrompt is backfillPrompt.
func BackfillPrompt(facts []BackfillFact) string {
	lines := make([]string, len(facts))
	for i, f := range facts {
		lines[i] = fmt.Sprintf("%d. (stored %s) %s", f.Ref, f.Stored, FenceSafe(f.Fact))
	}
	return "FACTS:\n<<<\n" + strings.Join(lines, "\n") + "\n>>>"
}

// BackfillDate is one entry of backfillResult.
type BackfillDate struct {
	Ref       int
	EventDate DateField
	DueDate   DateField
}

// ParseBackfill is backfillResult.parse: a bad "dates" array is [] (catch),
// and each date is a parsed string or null.
func ParseBackfill(v any) ([]BackfillDate, error) {
	o, oi := asObject(v)
	if oi != nil {
		return nil, issues(oi).err(v)
	}
	out := []BackfillDate{}
	dv, ok := get(o, "dates")
	if !ok {
		return out, nil
	}
	arr, ok := dv.([]any)
	if !ok {
		return out, nil
	}
	var res []BackfillDate
	for _, x := range arr {
		item, ok := x.(*jsonx.Object)
		if !ok {
			return out, nil
		}
		rv, rp := get(item, "ref")
		ref, ri := parseRef(rv, rp)
		if ri != nil {
			return out, nil
		}
		d := BackfillDate{Ref: ref}
		for _, k := range []string{"event_date", "due_date"} {
			v, present := get(item, k)
			var f DateField
			if present {
				f.Set = true
				if s, ok := v.(string); ok {
					f.Value = dates.Parse(s)
				}
			}
			if k == "event_date" {
				d.EventDate = f
			} else {
				d.DueDate = f
			}
		}
		res = append(res, d)
	}
	if res == nil {
		return out, nil
	}
	return res, nil
}

var _ = strx.Trim
