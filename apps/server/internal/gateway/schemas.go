package gateway

import (
	"math"
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// The response schemas of schemas.ts, written out by hand with zod 4's
// semantics: unknown keys are dropped, .default() fills only a missing value,
// .catch() swallows any failure, z.coerce.number() is JS Number(), string
// limits count UTF-16 units. Values come from jsonx.Parse.

// DateField is a dateValue: unset (the key was missing or the value unusable
// as a date: an update keeps the memory's date), null (clears it) or a date.
type DateField struct {
	Set   bool
	Value *dates.Dated
}

// AddItem is one "add".
type AddItem struct {
	Fact      string
	Keywords  string
	Kind      string
	EventDate DateField
	DueDate   DateField
}

// UpdateItem is one "update".
type UpdateItem struct {
	Ref       int
	Fact      string
	Keywords  string
	EventDate DateField
	DueDate   DateField
}

// ConflictItem is one "conflict".
type ConflictItem struct {
	Ref       int
	Fact      string
	Keywords  string
	Note      string
	EventDate DateField
	DueDate   DateField
}

// PersonMention is one entry of a "people" block.
type PersonMention struct {
	Name         string
	Aliases      []string
	Role         *string
	Organization *string
	Email        *string
	Handle       *string
	Summary      *string
	Facts        []string
}

// ExtractResult is extractResult, or extractWithPeople when People is parsed.
type ExtractResult struct {
	Add        []AddItem
	Update     []UpdateItem
	Invalidate []int
	Conflict   []ConflictItem
	People     []PersonMention
}

// ProfileResult is profileResult.
type ProfileResult struct {
	Static  string
	Dynamic string
}

// AskResult is askResult.
type AskResult struct {
	Answer  string
	Covered bool
}

// --- helpers ---------------------------------------------------------------------

type issues []zod.Issue

func (is *issues) add(path []any, more []zod.Issue) {
	for _, i := range more {
		i.Path = append(append([]any{}, path...), i.Path...)
		*is = append(*is, i)
	}
}

func (is issues) err(input any) error {
	if len(is) == 0 {
		return nil
	}
	return &zod.Error{Issues: is, Input: input}
}

func zodIssues(err error) []zod.Issue {
	if ze, ok := err.(*zod.Error); ok {
		return ze.Issues
	}
	return nil
}

func get(o *jsonx.Object, k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	return o.Get(k)
}

var (
	factSchema = zod.String().Trim().Min(1).Max(500)
	nameSchema = zod.String().Trim().Min(1).Max(120)
	objSchema  = zod.Object()
)

func parseString(s *zod.StringS, v any) (string, []zod.Issue) {
	out, err := zod.Parse(s, v)
	if err != nil {
		return "", zodIssues(err)
	}
	return out.(string), nil
}

func asObject(v any) (*jsonx.Object, []zod.Issue) {
	if o, ok := v.(*jsonx.Object); ok {
		return o, nil
	}
	_, err := zod.Parse(objSchema, v)
	return nil, zodIssues(err)
}

// keywords: union(string, string[]) -> joined, trimmed, first 500 units;
// missing is "".
func parseKeywords(v any, present bool) (string, []zod.Issue) {
	if !present {
		return "", nil
	}
	switch t := v.(type) {
	case string:
		return strx.Slice16(strx.Trim(t), 500), nil
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			s, ok := x.(string)
			if !ok {
				return "", unionIssue(v)
			}
			parts[i] = s
		}
		return strx.Slice16(strx.Trim(strings.Join(parts, " ")), 500), nil
	}
	return "", unionIssue(v)
}

func unionIssue(v any) []zod.Issue {
	_, err := zod.Parse(zod.Union(zod.String(), zod.Array(zod.String())), v)
	return zodIssues(err)
}

func parseDateValue(v any, present bool) DateField {
	if !present {
		return DateField{}
	}
	if v == nil {
		return DateField{Set: true}
	}
	if s, ok := v.(string); ok {
		if d := dates.Parse(s); d != nil {
			return DateField{Set: true, Value: d}
		}
	}
	return DateField{}
}

// jsToNumber is Number(v) for a parsed JSON value.
func jsToNumber(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case string:
		return strx.Number(t)
	case []any:
		return strx.Number(jsArrayString(t))
	}
	return math.NaN()
}

func jsArrayString(a []any) string {
	parts := make([]string, len(a))
	for i, x := range a {
		switch t := x.(type) {
		case nil:
			parts[i] = ""
		case []any:
			parts[i] = jsArrayString(t)
		case string:
			parts[i] = t
		case float64:
			parts[i] = jsonx.NumberString(t)
		case bool:
			if t {
				parts[i] = "true"
			} else {
				parts[i] = "false"
			}
		default:
			parts[i] = "[object Object]"
		}
	}
	return strings.Join(parts, ",")
}

// z.coerce.number().int().positive()
func parseRef(v any, present bool) (int, []zod.Issue) {
	if !present {
		v = nil
	}
	n := math.NaN()
	if present {
		n = jsToNumber(v)
	}
	if math.IsNaN(n) {
		return 0, []zod.Issue{{Code: "invalid_type", Message: "Invalid input: expected number, received NaN", Head: head("expected", "number", "code", "invalid_type", "received", "NaN")}}
	}
	var out []zod.Issue
	if n != math.Trunc(n) || math.IsInf(n, 0) {
		out = append(out, zod.Issue{Code: "invalid_type", Message: "Invalid input: expected int, received number", Head: head("expected", "int", "format", "safeint", "code", "invalid_type")})
	} else if math.Abs(n) > 9007199254740991 {
		out = append(out, zod.Issue{Code: "too_big", Message: "Too big: expected int to be <=9007199254740991", Head: head("origin", "number", "code", "too_big", "maximum", 9007199254740991.0, "inclusive", true)})
	}
	if !(n > 0) {
		out = append(out, zod.Issue{Code: "too_small", Message: "Too small: expected number to be >0", Head: head("origin", "number", "code", "too_small", "minimum", 0.0, "inclusive", false)})
	}
	if len(out) > 0 {
		return 0, out
	}
	return int(n), nil
}

// head builds an issue's fields (everything before path and message) in order.
func head(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// arrayDefault reads an array field with .default([]): missing is empty.
func arrayDefault(o *jsonx.Object, key string, is *issues) ([]any, bool) {
	v, present := get(o, key)
	if !present {
		return nil, true
	}
	arr, ok := v.([]any)
	if !ok {
		_, err := zod.Parse(zod.Array(zod.Unknown()), v)
		is.add([]any{key}, zodIssues(err))
		return nil, false
	}
	return arr, true
}

// --- extractResult ------------------------------------------------------------------

func parseExtract(v any, withPeople bool) (ExtractResult, error) {
	var r ExtractResult
	var is issues
	o, oi := asObject(v)
	if oi != nil {
		return r, issues(oi).err(v)
	}
	r.Add, r.Update, r.Invalidate, r.Conflict = []AddItem{}, []UpdateItem{}, []int{}, []ConflictItem{}
	if arr, ok := arrayDefault(o, "add", &is); ok {
		for i, x := range arr {
			path := []any{"add", float64(i)}
			item, oi := asObject(x)
			if oi != nil {
				is.add(path, oi)
				continue
			}
			var a AddItem
			fv, fp := get(item, "fact")
			if !fp {
				fv = nil
			}
			var fi []zod.Issue
			if fp {
				a.Fact, fi = parseString(factSchema, fv)
			} else {
				_, fi = parseString(factSchema, zod.Undefined)
			}
			is.add(append(path, "fact"), fi)
			kv, kp := get(item, "keywords")
			var ki []zod.Issue
			a.Keywords, ki = parseKeywords(kv, kp)
			is.add(append(path, "keywords"), ki)
			kind, _ := get(item, "kind")
			if k, ok := kind.(string); ok && (k == "static" || k == "dynamic") {
				a.Kind = k
			} else {
				a.Kind = "dynamic"
			}
			ev, ep := get(item, "event_date")
			a.EventDate = parseDateValue(ev, ep)
			dv, dp := get(item, "due_date")
			a.DueDate = parseDateValue(dv, dp)
			r.Add = append(r.Add, a)
		}
	}
	if arr, ok := arrayDefault(o, "update", &is); ok {
		for i, x := range arr {
			path := []any{"update", float64(i)}
			item, oi := asObject(x)
			if oi != nil {
				is.add(path, oi)
				continue
			}
			var u UpdateItem
			rv, rp := get(item, "ref")
			var ri []zod.Issue
			u.Ref, ri = parseRef(rv, rp)
			is.add(append(path, "ref"), ri)
			u.Fact = parseFact(item, append(path, "fact"), &is)
			kv, kp := get(item, "keywords")
			var ki []zod.Issue
			u.Keywords, ki = parseKeywords(kv, kp)
			is.add(append(path, "keywords"), ki)
			ev, ep := get(item, "event_date")
			u.EventDate = parseDateValue(ev, ep)
			dv, dp := get(item, "due_date")
			u.DueDate = parseDateValue(dv, dp)
			r.Update = append(r.Update, u)
		}
	}
	if arr, ok := arrayDefault(o, "invalidate", &is); ok {
		for i, x := range arr {
			n, ni := parseRef(x, true)
			is.add([]any{"invalidate", float64(i)}, ni)
			r.Invalidate = append(r.Invalidate, n)
		}
	}
	if arr, ok := arrayDefault(o, "conflict", &is); ok {
		for i, x := range arr {
			path := []any{"conflict", float64(i)}
			item, oi := asObject(x)
			if oi != nil {
				is.add(path, oi)
				continue
			}
			var c ConflictItem
			rv, rp := get(item, "ref")
			var ri []zod.Issue
			c.Ref, ri = parseRef(rv, rp)
			is.add(append(path, "ref"), ri)
			c.Fact = parseFact(item, append(path, "fact"), &is)
			kv, kp := get(item, "keywords")
			var ki []zod.Issue
			c.Keywords, ki = parseKeywords(kv, kp)
			is.add(append(path, "keywords"), ki)
			// z.string().default("").catch("").transform(trim, slice 300)
			if nv, ok := get(item, "note"); ok {
				if s, ok := nv.(string); ok {
					c.Note = strx.Slice16(strx.Trim(s), 300)
				}
			}
			ev, ep := get(item, "event_date")
			c.EventDate = parseDateValue(ev, ep)
			dv, dp := get(item, "due_date")
			c.DueDate = parseDateValue(dv, dp)
			r.Conflict = append(r.Conflict, c)
		}
	}
	if withPeople {
		pv, pp := get(o, "people")
		r.People = parsePeopleBlock(pv, pp)
	}
	return r, is.err(v)
}

func parseFact(item *jsonx.Object, path []any, is *issues) string {
	fv, fp := get(item, "fact")
	var s string
	var fi []zod.Issue
	if fp {
		s, fi = parseString(factSchema, fv)
	} else {
		_, fi = parseString(factSchema, zod.Undefined)
	}
	is.add(path, fi)
	return s
}

// ParseExtract is extractResult.parse.
func ParseExtract(v any) (ExtractResult, error) { return parseExtract(v, false) }

// ParseExtractWithPeople is extractWithPeople.parse.
func ParseExtractWithPeople(v any) (ExtractResult, error) { return parseExtract(v, true) }

// ParsePeopleBackfill is peopleBackfillResult.parse.
func ParsePeopleBackfill(v any) ([]PersonMention, error) {
	o, oi := asObject(v)
	if oi != nil {
		return nil, issues(oi).err(v)
	}
	pv, pp := get(o, "people")
	return parsePeopleBlock(pv, pp), nil
}

// --- profile and ask --------------------------------------------------------------

var profileText = zod.String().Trim().Max(4000)

// ParseProfile is profileResult.parse.
func ParseProfile(v any) (ProfileResult, error) {
	var r ProfileResult
	var is issues
	o, oi := asObject(v)
	if oi != nil {
		return r, issues(oi).err(v)
	}
	for _, k := range []string{"static", "dynamic"} {
		x, ok := get(o, k)
		if !ok {
			continue
		}
		s, si := parseString(profileText, x)
		is.add([]any{k}, si)
		if k == "static" {
			r.Static = s
		} else {
			r.Dynamic = s
		}
	}
	return r, is.err(v)
}

var answerSchema = zod.String().Trim().Min(1).Max(6000)

// ParseAsk is askResult.parse.
func ParseAsk(v any) (AskResult, error) {
	var r AskResult
	var is issues
	o, oi := asObject(v)
	if oi != nil {
		return r, issues(oi).err(v)
	}
	av, ap := get(o, "answer")
	var ai []zod.Issue
	if ap {
		r.Answer, ai = parseString(answerSchema, av)
	} else {
		_, ai = parseString(answerSchema, zod.Undefined)
	}
	is.add([]any{"answer"}, ai)
	cv, cp := get(o, "covered")
	switch {
	case !cp:
		r.Covered = true
	case cv == true || cv == "true":
		r.Covered = true
	case cv == false || cv == "false":
		r.Covered = false
	default:
		_, err := zod.Parse(zod.Union(zod.Bool(), zod.Enum("true", "false")), cv)
		is.add([]any{"covered"}, zodIssues(err))
	}
	return r, is.err(v)
}

// --- people -----------------------------------------------------------------------

// zod 4's email pattern.
var emailRE = regexp.MustCompile(`^(?:[A-Za-z0-9_'+\-]+\.)*[A-Za-z0-9_'+\-]*[A-Za-z0-9_+-]@(?:[A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

// optionalText is optional(max): trimmed, at most max units, "" and failures
// become unset.
func optionalText(o *jsonx.Object, key string, max int) *string {
	v, ok := get(o, key)
	if !ok {
		return nil
	}
	s, isStr := v.(string)
	if !isStr {
		return nil
	}
	s = strx.Trim(s)
	if strx.Len16(s) > max || s == "" {
		return nil
	}
	return &s
}

func parsePeopleBlock(v any, present bool) []PersonMention {
	out := []PersonMention{}
	if !present {
		return out
	}
	items, ok := v.([]any)
	if !ok {
		return out
	}
	for _, it := range items {
		if p, ok := parsePerson(it); ok {
			out = append(out, p)
		}
	}
	return out
}

func parsePerson(v any) (PersonMention, bool) {
	var p PersonMention
	o, ok := v.(*jsonx.Object)
	if !ok {
		return p, false
	}
	nv, ok := get(o, "name")
	if !ok {
		return p, false
	}
	name, ni := parseString(nameSchema, nv)
	if ni != nil {
		return p, false
	}
	p.Name = name
	// aliases: array(string trim min1 max120).max(10).default([]).catch([])
	p.Aliases = []string{}
	if av, ok := get(o, "aliases"); ok {
		if arr, ok := av.([]any); ok && len(arr) <= 10 {
			var aliases []string
			good := true
			for _, x := range arr {
				s, si := parseString(nameSchema, x)
				if si != nil {
					good = false
					break
				}
				aliases = append(aliases, s)
			}
			if good && aliases != nil {
				p.Aliases = aliases
			}
		}
	}
	p.Role = optionalText(o, "role", 120)
	p.Organization = optionalText(o, "organization", 120)
	// z.email().max(254).optional().catch(undefined): the raw value, unchanged.
	if ev, ok := get(o, "email"); ok {
		if s, ok := ev.(string); ok && emailRE.MatchString(s) && strx.Len16(s) <= 254 {
			p.Email = &s
		}
	}
	p.Handle = optionalText(o, "handle", 80)
	p.Summary = optionalText(o, "summary", 300)
	// facts: array(union(string trim, number) -> String).max(100).default([]).catch([])
	p.Facts = []string{}
	if fv, ok := get(o, "facts"); ok {
		if arr, ok := fv.([]any); ok && len(arr) <= 100 {
			var facts []string
			good := true
			for _, x := range arr {
				switch t := x.(type) {
				case string:
					facts = append(facts, strx.Trim(t))
				case float64:
					facts = append(facts, jsonx.NumberString(t))
				default:
					good = false
				}
				if !good {
					break
				}
			}
			if good && facts != nil {
				p.Facts = facts
			}
		}
	}
	return p, true
}
