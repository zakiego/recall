package apps

// finansial.ts: Finansial's feed to Recall documents. Only notable items
// become documents: recurring rules and loans (one per version of the fields
// that matter), and transactions at or above a per-currency threshold (one
// per transaction, replaced when it changes and removed when it goes).

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// DefaultThresholds is DEFAULT_THRESHOLDS: major units per currency code.
func DefaultThresholds() *jsonx.Map[float64] {
	m := jsonx.NewMap[float64]()
	m.Set("IDR", 1_000_000)
	return m
}

// Settings is FinansialSettings.
type Settings struct {
	Thresholds *jsonx.Map[float64] `json:"thresholds"`
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// FinansialSettings is finansialSettings.
func FinansialSettings(raw *jsonx.Object) Settings {
	var t any
	if raw != nil {
		t, _ = raw.Get("thresholds")
	}
	o, ok := t.(*jsonx.Object)
	if !ok || o == nil {
		return Settings{DefaultThresholds()}
	}
	out := jsonx.NewMap[float64]()
	for _, code := range o.Keys() {
		v, _ := o.Get(code)
		f, isNum := v.(float64)
		if currencyCode.MatchString(code) && isNum && !math.IsInf(f, 0) && !math.IsNaN(f) && f >= 0 {
			out.Set(code, f)
		}
	}
	return Settings{out}
}

// Doc is FinansialDoc; Fingerprint "" for rules and loans.
type Doc struct {
	SourceID     string
	Title        string
	Content      string
	DocumentDate string
	Fingerprint  string
}

// --- field readers ---------------------------------------------------------------

func fstr(v any) string {
	switch t := v.(type) {
	case string:
		return strx.Trim(t)
	case float64:
		return jsonx.NumberString(t)
	}
	return ""
}

var dayPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func fday(v any) (string, bool) {
	s := fstr(v)
	if dayPrefix.MatchString(s) {
		return s[:10], true
	}
	return "", false
}

var intText = regexp.MustCompile(`^-?\d+$`)

// fint is int(): a safe integer, or ok false.
func fint(v any) (float64, bool) {
	n := math.NaN()
	switch t := v.(type) {
	case float64:
		n = t
	case string:
		if s := strx.Trim(t); intText.MatchString(s) {
			n = strx.Number(s)
		}
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || math.Abs(n) > 9007199254740991 {
		return 0, false
	}
	return n, true
}

func digitsOf(v any) int {
	if n, ok := fint(v); ok && n >= 0 && n <= 6 {
		return int(n)
	}
	return 2
}

func currencyOf(v any) string { return strx.Upper(fstr(v)) }

func tagsOf(v any) []string {
	arr, ok := v.([]any)
	out := []string{}
	if !ok {
		return out
	}
	for _, x := range arr {
		if s := fstr(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// kv is one key of a contentHash input; Value is a JSON value.
type kv struct {
	Key   string
	Value any
}

// ContentHash is contentHash: sha256 of the [key, value] pairs sorted by
// key, as JSON, cut to length hex characters.
func ContentHash(values []kv, length int) string {
	sorted := append([]kv(nil), values...)
	sort.SliceStable(sorted, func(i, j int) bool { return strx.Compare16(sorted[i].Key, sorted[j].Key) < 0 })
	pairs := make([]any, len(sorted))
	for i, p := range sorted {
		pairs[i] = []any{p.Key, p.Value}
	}
	sum := sha256.Sum256([]byte(jsonx.Stringify(pairs)))
	return hex.EncodeToString(sum[:])[:length]
}

func group(s string) string {
	// "\B(?=(\d{3})+(?!\d))" -> ",": a comma before every group of three from the right.
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

// FormatMoney is formatMoney: "IDR 1,500,000" or "USD 1,234.50".
func FormatMoney(amountMinor float64, currency string, minorUnitDigits float64) string {
	digits := int(math.Max(0, math.Min(6, math.Trunc(minorUnitDigits))))
	negative := amountMinor < 0
	abs := math.Abs(math.Trunc(amountMinor))
	scale := math.Pow(10, float64(digits))
	major := math.Floor(abs / scale)
	minor := math.Mod(abs, scale)
	grouped := group(strconv.FormatFloat(major, 'f', -1, 64))
	fraction := ""
	if digits > 0 {
		m := strconv.FormatFloat(minor, 'f', -1, 64)
		fraction = "." + strings.Repeat("0", max(0, digits-len(m))) + m
	}
	sign := ""
	if negative {
		sign = "-"
	}
	if currency == "" {
		currency = "?"
	}
	return sign + currency + " " + grouped + fraction
}

func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// MeetsThreshold is meetsThreshold.
func MeetsThreshold(amountMinor float64, currency string, minorUnitDigits int, s Settings) bool {
	threshold, ok := s.Thresholds.Get(currency)
	if !ok {
		return false
	}
	return math.Abs(amountMinor) >= jsRound(threshold*math.Pow(10, float64(minorUnitDigits)))
}

func sentence(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, " ")
}

func when(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

func linkLine(url string) string {
	if url == "" {
		return ""
	}
	return "\nLink: " + url
}

func asOf(it ChangeItem) string { return strx.Slice16(it.UpdatedAt, 10) }

func get(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

// TransactionSourceID is transactionSourceId.
func TransactionSourceID(id string) string { return "fin:txn:" + id }

func nonEmpty(lines ...string) string {
	var keep []string
	for _, l := range lines {
		if l != "" {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func transactionDoc(it ChangeItem, s Settings) *Doc {
	if it.Deleted {
		return nil
	}
	d := it.Data
	amount, ok := fint(get(d, "amountMinor"))
	currency := currencyOf(get(d, "currency"))
	digits := digitsOf(get(d, "minorUnitDigits"))
	if !ok || currency == "" || !MeetsThreshold(amount, currency, digits, s) {
		return nil
	}
	money := FormatMoney(math.Abs(amount), currency, float64(digits))
	kind := fstr(get(d, "kind"))
	if kind == "" {
		kind = "transaction"
	}
	date, ok := fday(get(d, "date"))
	if !ok {
		date = asOf(it)
	}
	payee, account, category, note := fstr(get(d, "payee")), fstr(get(d, "account")), fstr(get(d, "category")), fstr(get(d, "note"))
	tags := tagsOf(get(d, "tags"))
	var what string
	switch kind {
	case "income":
		what = sentence("an income of "+money, when(payee != "", "from "+payee), when(account != "", "into the account "+account))
	case "transfer":
		what = sentence("a transfer of "+money, when(account != "", "from the account "+account), when(payee != "", "to "+payee))
	default:
		what = sentence("an expense of "+money, when(payee != "", "paid to "+payee), when(account != "", "from the account "+account))
	}
	content := nonEmpty("Finansial transaction on "+date+": "+what+".", when(category != "", "Category: "+category+"."), when(note != "", "Note: "+note),
		when(len(tags) > 0, "Tags: "+strings.Join(tags, ", ")+"."))
	label := "Expense"
	dir := "to"
	switch kind {
	case "income":
		label, dir = "Income", "from"
	case "transfer":
		label = "Transfer"
	}
	title := label + " of " + money + when(payee != "", " "+dir+" "+payee) + " on " + date
	sortedTags := append([]string{}, tags...)
	sort.SliceStable(sortedTags, func(i, j int) bool { return strx.Compare16(sortedTags[i], sortedTags[j]) < 0 })
	tagList := make([]any, len(sortedTags))
	for i, t := range sortedTags {
		tagList[i] = t
	}
	fp := ContentHash([]kv{{"date", date}, {"kind", kind}, {"amountMinor", amount}, {"currency", currency}, {"minorUnitDigits", float64(digits)},
		{"account", account}, {"payee", payee}, {"category", category}, {"note", note}, {"tags", tagList}}, 64)
	return &Doc{SourceID: TransactionSourceID(it.ID), Title: title, Content: content + linkLine(it.URL), DocumentDate: date, Fingerprint: fp}
}

func ruleActive(v any) string {
	switch v {
	case true, "active":
		return "It is active."
	case false:
		return "It is no longer active."
	case "paused":
		return "It is paused."
	case "ended":
		return "It has ended."
	}
	return ""
}

// activeOf: a boolean as it is, else the trimmed text, else null.
func activeOf(v any) any {
	if b, ok := v.(bool); ok {
		return b
	}
	if s := fstr(v); s != "" {
		return s
	}
	return nil
}

func or(a, b any) any {
	if a == nil {
		return b
	}
	return a
}

func intOrNull(v any) any {
	if n, ok := fint(v); ok {
		return n
	}
	return nil
}

// RuleVersion is ruleVersion: a hash of the fields that matter, so a rule
// posting every month (its due date moves on) stays one document.
func RuleVersion(it ChangeItem) string {
	if it.Deleted {
		return "deleted"
	}
	d := it.Data
	return ContentHash([]kv{{"name", fstr(get(d, "name"))}, {"kind", fstr(get(d, "kind"))}, {"amountMinor", intOrNull(get(d, "amountMinor"))},
		{"currency", currencyOf(get(d, "currency"))}, {"minorUnitDigits", float64(digitsOf(get(d, "minorUnitDigits")))}, {"frequency", fstr(get(d, "frequency"))},
		{"payee", fstr(get(d, "payee"))}, {"category", fstr(get(d, "category"))}, {"active", activeOf(or(get(d, "active"), get(d, "status")))}}, 16)
}

func ruleDoc(it ChangeItem) *Doc {
	d := it.Data
	name := fstr(get(d, "name"))
	if name == "" {
		name = "rule " + it.ID
	}
	at := asOf(it)
	sourceID := "fin:rule:" + it.ID + ":" + RuleVersion(it)
	if it.Deleted {
		return &Doc{SourceID: sourceID, Title: "Recurring rule removed: " + name,
			Content: `Finansial recurring rule "` + name + `" was deleted on ` + at + `. It no longer applies.` + linkLine(it.URL), DocumentDate: at}
	}
	amount, okAmount := fint(get(d, "amountMinor"))
	currency := currencyOf(get(d, "currency"))
	money := ""
	if okAmount && currency != "" {
		money = FormatMoney(math.Abs(amount), currency, float64(digitsOf(get(d, "minorUnitDigits"))))
	}
	kind := fstr(get(d, "kind"))
	if kind == "" {
		kind = "recurring"
	}
	frequency, payee, category := fstr(get(d, "frequency")), fstr(get(d, "payee")), fstr(get(d, "category"))
	freq := frequency
	if freq == "" {
		freq = "recurring"
	}
	toFrom := "to "
	if kind == "income" {
		toFrom = "from "
	}
	content := nonEmpty(`Finansial recurring rule "`+name+`", as of `+at+`: `+sentence("a "+freq+" "+kind, when(money != "", "of "+money), when(payee != "", toFrom+payee))+".",
		when(category != "", "Category: "+category+"."), ruleActive(or(get(d, "active"), get(d, "status"))))
	title := "Recurring " + kind + ": " + name
	if money != "" {
		title += ", " + money + when(frequency != "", " "+frequency)
	}
	return &Doc{SourceID: sourceID, Title: title, Content: content + linkLine(it.URL), DocumentDate: at}
}

// LoanVersion is loanVersion.
func LoanVersion(it ChangeItem) string {
	if it.Deleted {
		return "deleted"
	}
	d := it.Data
	return ContentHash([]kv{{"name", fstr(get(d, "name"))}, {"principalMinor", intOrNull(get(d, "principalMinor"))}, {"currency", currencyOf(get(d, "currency"))},
		{"minorUnitDigits", float64(digitsOf(get(d, "minorUnitDigits")))}, {"lender", fstr(get(d, "lender"))}, {"status", fstr(get(d, "status"))}}, 16)
}

func loanDoc(it ChangeItem) *Doc {
	d := it.Data
	name := fstr(get(d, "name"))
	if name == "" {
		name = "loan " + it.ID
	}
	at := asOf(it)
	sourceID := "fin:loan:" + it.ID + ":" + LoanVersion(it)
	if it.Deleted {
		return &Doc{SourceID: sourceID, Title: "Loan removed: " + name, Content: `Finansial loan "` + name + `" was deleted on ` + at + `.` + linkLine(it.URL), DocumentDate: at}
	}
	principal, ok := fint(get(d, "principalMinor"))
	currency := currencyOf(get(d, "currency"))
	money := ""
	if ok && currency != "" {
		money = FormatMoney(math.Abs(principal), currency, float64(digitsOf(get(d, "minorUnitDigits"))))
	}
	lender, status := fstr(get(d, "lender")), fstr(get(d, "status"))
	what := "a loan"
	if money != "" {
		what = "a principal of " + money
	}
	content := nonEmpty(`Finansial loan "`+name+`", as of `+at+`: `+sentence(what, when(lender != "", "from "+lender))+".", when(status != "", "Status: "+status+"."))
	return &Doc{SourceID: sourceID, Title: "Loan: " + name + when(money != "", ", "+money), Content: content + linkLine(it.URL), DocumentDate: at}
}

// FinansialDocument is finansialDocument: nil when the item isn't notable.
func FinansialDocument(it ChangeItem, s Settings) *Doc {
	switch it.Kind {
	case "transaction":
		return transactionDoc(it, s)
	case "recurring_rule":
		return ruleDoc(it)
	case "loan":
		return loanDoc(it)
	}
	return nil
}

// FinansialEventDate is finansialEventDate: the day a transaction happened.
func FinansialEventDate(it ChangeItem) (string, bool) {
	if it.Kind != "transaction" {
		return "", false
	}
	return fday(get(it.Data, "date"))
}

// HashObject is contentHash on a parsed object.
func HashObject(o *jsonx.Object, length int) string {
	var values []kv
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		values = append(values, kv{k, v})
	}
	return ContentHash(values, length)
}
