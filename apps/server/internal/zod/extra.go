package zod

import (
	"math"
	"regexp"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// CoerceNumber is z.coerce.number(): the input goes through Number() first.
func CoerceNumber() *NumberS { return &NumberS{coerce: true} }

// jsNumber is Number(v) for the values a query or JSON body holds.
func jsNumber(v any) any {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		return strx.Number(t)
	case bool:
		if t {
			return 1.0
		}
		return 0.0
	case nil:
		return 0.0
	case []any:
		switch len(t) {
		case 0:
			return 0.0
		case 1:
			// String([x]) is x's text, and "" for null.
			if t[0] == nil {
				return 0.0
			}
			return jsNumber(jsonx.JSString(t[0]))
		}
	}
	return math.NaN()
}

// Refine is .refine(fn, message) on an object: run after the fields when
// none of their issues aborted, with an empty path.
func (o *ObjectS) Refine(fn func(*jsonx.Object) bool, msg string) *ObjectS {
	c := &ObjectS{fields: o.fields, desc: o.desc, refines: append(append([]objectRefine(nil), o.refines...), objectRefine{fn, msg})}
	return c
}

type objectRefine struct {
	fn  func(*jsonx.Object) bool
	msg string
}

// RefinedS is inner.refine(fn, message) on any schema.
type RefinedS struct {
	inner Schema
	fn    func(any) bool
	msg   string
}

// Refined is inner.refine(fn, message).
func Refined(inner Schema, fn func(any) bool, msg string) *RefinedS {
	return &RefinedS{inner: inner, fn: fn, msg: msg}
}

func (s *RefinedS) Optional() *OptionalS { return Optional(s) }
func (s *RefinedS) optin() string        { return s.inner.optin() }
func (s *RefinedS) optout() string       { return s.inner.optout() }
func (s *RefinedS) json() *jsonx.Object  { return s.inner.json() }
func (s *RefinedS) parse(v any) (any, []Issue) {
	out, issues := s.inner.parse(v)
	return runChecks(out, issues, []check{refineCheck(s.fn, s.msg)})
}

// Transformed is inner.transform(fn): the output mapped when there are no
// issues. A transform has no optout, so an object keeps its output even for
// a missing key (a default container, a false flag).
func Transformed(inner Schema, fn func(any) any) *TransformPipeS {
	return TransformPipe(inner, fn, transformOut{})
}

// transformOut is the ZodTransform end of a pipe: any value, no optin or
// optout.
type transformOut struct{}

func (transformOut) optin() string              { return "" }
func (transformOut) optout() string             { return "" }
func (transformOut) json() *jsonx.Object        { return jsonx.NewMap[any]() }
func (transformOut) parse(v any) (any, []Issue) { return v, nil }

// Null is z.null().
func Null() *EnumS { return Literal(nil) }

// PrettifyError is z.prettifyError: each issue as "✖ message", with
// "  → at path" under it when it has one, shortest paths first.
func PrettifyError(issues []Issue) string {
	sorted := append([]Issue(nil), issues...)
	// A stable sort by path length, as Array.prototype.sort is.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j].Path) < len(sorted[j-1].Path); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var lines []string
	for _, is := range sorted {
		lines = append(lines, "✖ "+is.Message)
		if len(is.Path) > 0 {
			lines = append(lines, "  → at "+dotPath(is.Path))
		}
	}
	return strings.Join(lines, "\n")
}

var nonWord = regexp.MustCompile(`[^\w$]`)

// dotPath is zod's toDotPath.
func dotPath(path []any) string {
	var b strings.Builder
	for _, seg := range path {
		switch s := seg.(type) {
		case int:
			b.WriteString("[" + jsonx.JSString(float64(s)) + "]")
		case float64:
			b.WriteString("[" + jsonx.JSString(s) + "]")
		case string:
			if nonWord.MatchString(s) {
				b.WriteString("[" + jsonx.Stringify(s) + "]")
			} else {
				if b.Len() > 0 {
					b.WriteString(".")
				}
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

var emailRe = regexp.MustCompile(`^(?:[A-Za-z0-9_'+\-]+\.)*[A-Za-z0-9_'+\-]*[A-Za-z0-9_+-]@(?:[A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

const emailSource = `^(?:[A-Za-z0-9_'+\-]+\.)*[A-Za-z0-9_'+\-]*[A-Za-z0-9_+-]@(?:[A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`

// Email is z.email({ message }): zod 4's email pattern (msg "" is zod's
// "Invalid email address").
func Email(msg ...string) *StringS {
	m := "Invalid email address"
	if len(msg) > 0 && msg[0] != "" {
		m = msg[0]
	}
	c := String()
	c.checks = append(c.checks, check{run: func(v *any) *Issue {
		s, _ := (*v).(string)
		if emailRe.MatchString(s) {
			return nil
		}
		is := newIssue("invalid_format", m, contTrue, "origin", "string", "code", "invalid_format", "format", "email", "pattern", "/"+emailSource+"/")
		return &is
	}})
	return c
}
