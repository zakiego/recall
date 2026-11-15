// Package people is people.ts: the contacts named in a container's facts
// (colleagues, clients, family), each linked to the facts that mention them.
// Every read and write is scoped to one workspace, and people never cross
// containers.
package people

import (
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// FuzzyPersonSimilarity: a fuzzy name match ("Clara" for "Clara Simmons",
// "Budi Santosa" for "Budi Santoso") only counts when the mention and the
// person read alike. Measured on e5-small passage vectors of "name. role org.
// summary. facts": the same person scored 0.86 to 0.89, a different person
// with the same first name 0.82 to 0.84.
const FuzzyPersonSimilarity = 0.855

const (
	maxAliases      = 20
	maxContacts     = 10
	descriptorFacts = 3
)

// --- names ---------------------------------------------------------------------

// stripMarks is .normalize("NFKD").replace(/\p{M}/gu, "").
func stripMarks(s string) string {
	d := strx.NFKD(s)
	var b strings.Builder
	for _, r := range d {
		if !strx.IsMark(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isLN(r rune) bool { return strx.IsLetter(r) || strx.IsNumber(r) }

// NameKey is nameKey: case, accents and punctuation don't make a different
// name.
func NameKey(s string) string {
	lower := strx.Lower(stripMarks(s))
	var b strings.Builder
	gap := false
	for _, r := range lower {
		if isLN(r) {
			if gap {
				b.WriteByte(' ')
				gap = false
			}
			b.WriteRune(r)
		} else {
			gap = true
		}
	}
	if gap {
		b.WriteByte(' ')
	}
	return strx.Trim(b.String())
}

func tokens(s string) []string {
	out := []string{}
	for _, t := range strings.Split(NameKey(s), " ") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func handleKey(h string) string {
	return strx.Lower(strings.TrimLeft(strx.Trim(h), "@"))
}

func emailKey(e string) string { return strx.Lower(strx.Trim(e)) }

// selfWords point at the subject, never at a contact.
var selfWords = set("i", "me", "myself", "user", "the user", "subject", "the subject", "you", "he", "she", "they", "him", "her", "them", "someone", "unknown")

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// localPart is email.split("@")[0].
func localPart(e string) string { return strings.SplitN(e, "@", 2)[0] }

// IsContainerOwner is isContainerOwner: the mention is the container's own
// subject (facts about "Zaki" in container "zaki" or "zaki-work").
// ownerNames are other names for the subject (SubjectNames).
func IsContainerOwner(container string, m gateway.PersonMention, ownerNames []string) bool {
	parts := map[string]bool{}
	for _, t := range tokens(container) {
		if strx.Len16(t) >= 3 {
			parts[t] = true
		}
	}
	whole := NameKey(container)
	owners := map[string]bool{}
	for _, n := range ownerNames {
		if k := NameKey(n); k != "" {
			owners[k] = true
		}
	}
	for _, n := range append([]string{m.Name}, m.Aliases...) {
		key := NameKey(n)
		if key == "" || selfWords[key] || key == whole || owners[key] {
			return true
		}
		if ts := tokens(n); len(ts) > 0 && parts[ts[0]] {
			return true
		}
	}
	if m.Handle != nil && *m.Handle != "" {
		h := handleKey(*m.Handle)
		if h == whole || parts[h] {
			return true
		}
	}
	if m.Email != nil {
		if local := localPart(*m.Email); local != "" {
			k := NameKey(local)
			return k == whole || parts[k]
		}
	}
	return false
}

// orgWords make a name a team, company or mailbox rather than a person. Left
// out on purpose: real surnames like Sales, Kantor or Billing, and "tim"
// (Indonesian for team, but also Tim).
var orgWords = set(strings.Split("team support info admin administrator helpdesk newsletter noreply notifications customer service services office department dept "+
	"committee foundation inc ltd llc corp corporation company group official hq staff community institute university association "+
	"ministry agency sekret sekretariat secretariat panitia humas redaksi yayasan kementerian lembaga koperasi komunitas divisi layanan pt cv tbk", " ")...)

// Local parts of team mailboxes. Long words match as a prefix
// ("sekretwargalapor"), short ones only as a whole word ("info.vorneo").
var (
	mailboxPrefix = regexp.MustCompile(`^(sekret|secretariat|support|no-?reply|do-?not-?reply|newsletter|notification|helpdesk|redaksi|panitia|layanan|postmaster|webmaster|mailer-daemon)`)
	mailboxWord   = regexp.MustCompile(`^(info|admin|hello|halo|team|contact|office|billing|sales|marketing|hr|cs|help|news|humas)(?:$|[._+-]|[0-9])`)
)

// IsSharedMailbox is isSharedMailbox: a mailbox several people write from
// says nothing about who wrote.
func IsSharedMailbox(email string) bool {
	local := localPart(strx.Lower(strx.Trim(email)))
	return mailboxPrefix.MatchString(local) || mailboxWord.MatchString(local)
}

// personalEmail is the trimmed email unless it is empty or shared; ok false
// is undefined.
func personalEmail(e *string) (string, bool) {
	if e == nil || *e == "" || IsSharedMailbox(*e) {
		return "", false
	}
	t := strx.Trim(*e)
	return t, t != ""
}

// LooksLikeOrganization is looksLikeOrganization: email display names put
// teams and mailboxes where a person's name goes ("Sekret WargaLapor" for
// sekretwargalapor@example.com).
func LooksLikeOrganization(name string, email *string) bool {
	words := tokens(name)
	for _, w := range words {
		if orgWords[w] {
			return true
		}
	}
	if email == nil {
		return false
	}
	local := localPart(*email)
	return local != "" && IsSharedMailbox(*email) && strings.Join(words, "") == strings.Join(tokens(local), "")
}

// levenshtein runs on UTF-16 code units, like the TS function on JS strings.
func levenshtein(as, bs string) int {
	a, b := utf16.Encode([]rune(as)), utf16.Encode([]rune(bs))
	if d := len(a) - len(b); d > 2 || d < -2 {
		return 3
	}
	prev := make([]int, len(b)+1)
	for i := range prev {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			sub := 1
			if a[i-1] == b[j-1] {
				sub = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+sub)
		}
		prev = cur
	}
	return prev[len(b)]
}

// NameMatch is how alike two names are.
type NameMatch string

const (
	Same   NameMatch = "same"
	Strong NameMatch = "strong"
	Weak   NameMatch = "weak"
)

var digit = regexp.MustCompile(`[0-9]`)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// MatchNames is nameMatch: "" when the names name different people.
// "same": equal ignoring case, accents and punctuation. "strong": two
// spelled-out words agree, or a one-letter typo in a name of two or more
// words. "weak": only the first name does.
func MatchNames(a, b string) NameMatch {
	x, y := tokens(a), tokens(b)
	if len(x) == 0 || len(y) == 0 {
		return ""
	}
	ka, kb := strings.Join(x, " "), strings.Join(y, " ")
	if ka == kb {
		return Same
	}
	// A one-letter typo, but never a different number.
	if min(strx.Len16(ka), strx.Len16(kb)) >= 5 && !digit.MatchString(ka+kb) && levenshtein(ka, kb) <= 1 {
		if len(x) > 1 && len(y) > 1 {
			return Strong
		}
		return Weak
	}
	if x[0] != y[0] {
		return ""
	}
	short, long := x, y
	if len(x) > len(y) {
		short, long = y, x
	}
	spelled := 0
	for _, t := range short {
		tl := strx.Len16(t)
		switch {
		case contains(long, t):
			if tl > 1 {
				spelled++
			}
		case tl == 1 && anyPrefix(long, t, 0):
			continue
		// A word cut short, as email display names do ("Ferd" for "Ferdiansyah").
		case tl >= 3 && !digit.MatchString(t) && anyPrefix(long, t, tl):
			spelled++
		default:
			return ""
		}
	}
	if spelled >= 2 {
		return Strong
	}
	return Weak
}

// anyPrefix: some word in long starts with t and is longer than minLen units.
func anyPrefix(long []string, t string, minLen int) bool {
	for _, l := range long {
		if strx.Len16(l) > minLen && strings.HasPrefix(l, t) {
			return true
		}
	}
	return false
}

// NamesLookAlike is namesLookAlike.
func NamesLookAlike(a, b string) bool { return MatchNames(a, b) != "" }

// isPartialOf: "Clara" or "Clara S." is a partial of "Clara Simmons".
func isPartialOf(short, full string) bool {
	return len(tokens(short)) < len(tokens(full)) && NamesLookAlike(short, full)
}

// --- named in a text -------------------------------------------------------------

var standaloneI = jsre.MustCompile(`\bI\b`, "g")

// words is text.normalize("NFKD") without marks, split by /[^\p{L}\p{N}]+/u.
func words(s string) []string {
	var out []string
	var cur strings.Builder
	inSep := false
	for _, r := range stripMarks(s) {
		if isLN(r) {
			inSep = false
			cur.WriteRune(r)
			continue
		}
		if !inSep {
			out = append(out, cur.String())
			cur.Reset()
			inSep = true
		}
	}
	return append(out, cur.String())
}
