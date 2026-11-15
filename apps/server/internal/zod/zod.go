// Package zod is a port of the parts of zod 4.6.7 the connector tools and
// manifests use: parse (the same issues in the same order, zod's English
// messages, defaults filled, unknown keys dropped, objects in shape order),
// the issue list as ZodError's message prints it, and the JSON schema
// z.toJSONSchema gives for a tool's input the way the AI SDK asks for it
// (draft-07, io "input", inline, plus addAdditionalPropertiesToJsonSchema).
//
// internal/flows has its own copy of the parse half (flows/zod.go, M5) for
// the flow definition schema; both follow the same zod source.
//
// Values are jsonx values (nil, bool, float64, string, []any, *jsonx.Object).
// A missing key is Undefined.
package zod

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

type undefT struct{}

// Undefined is JS undefined: a key the input doesn't have.
var Undefined any = undefT{}

// IsUndefined reports whether v is Undefined.
func IsUndefined(v any) bool { _, ok := v.(undefT); return ok }

type cont int

const (
	contUnset cont = iota
	contTrue
	contFalse
)

// Issue is one zod issue. Head holds the issue's fields before path and
// message, in zod's order (code included).
type Issue struct {
	Code    string
	Path    []any
	Message string
	Head    *jsonx.Object
	cont    cont
}

func newIssue(code, message string, c cont, head ...any) Issue {
	h := jsonx.NewMap[any]()
	for i := 0; i+1 < len(head); i += 2 {
		h.Set(head[i].(string), head[i+1])
	}
	return Issue{Code: code, Message: message, Head: h, cont: c}
}

// JSON is the issue as zod prints it: head fields, path, message.
func (i Issue) JSON() *jsonx.Object {
	o := jsonx.NewMap[any]()
	if i.Head != nil {
		for _, k := range i.Head.Keys() {
			v, _ := i.Head.Get(k)
			o.Set(k, v)
		}
	}
	path := make([]any, len(i.Path))
	for n, p := range i.Path {
		if x, ok := p.(int); ok {
			path[n] = float64(x)
		} else {
			path[n] = p
		}
	}
	o.Set("path", path)
	o.Set("message", i.Message)
	return o
}

// PathString is issue.path.join(".").
func (i Issue) PathString() string {
	parts := make([]string, len(i.Path))
	for n, p := range i.Path {
		parts[n] = fmt.Sprint(p)
	}
	return strings.Join(parts, ".")
}

// Error is a failed parse (ZodError).
type Error struct {
	Issues []Issue
	// Input is the value that was parsed.
	Input any
}

// Error is ZodError's message: JSON.stringify(issues, null, 2).
func (e *Error) Error() string { return IssuesJSON(e.Issues) }

// IssuesJSON is JSON.stringify(issues, null, 2).
func IssuesJSON(issues []Issue) string {
	list := make([]any, len(issues))
	for i, is := range issues {
		list[i] = is.JSON()
	}
	return Indent(list)
}

// Indent is JSON.stringify(v, null, 2) for jsonx values.
func Indent(v any) string {
	var b strings.Builder
	indent(&b, v, "")
	return b.String()
}

func indent(b *strings.Builder, v any, pad string) {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, x := range t {
			b.WriteString(pad + "  ")
			indent(b, x, pad+"  ")
			if i < len(t)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "]")
	case *jsonx.Object:
		keys := t.Keys()
		var shown []string
		for _, k := range keys {
			if x, _ := t.Get(k); !IsUndefined(x) {
				shown = append(shown, k)
			}
		}
		if len(shown) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range shown {
			x, _ := t.Get(k)
			b.WriteString(pad + "  " + jsonx.Stringify(k) + ": ")
			indent(b, x, pad+"  ")
			if i < len(shown)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "}")
	default:
		b.WriteString(jsonx.Stringify(v))
	}
}

func aborted(issues []Issue, from int) bool {
	for _, i := range issues[from:] {
		if i.cont != contTrue {
			return true
		}
	}
	return false
}

func explicitlyAborted(issues []Issue) bool {
	for _, i := range issues {
		if i.cont == contFalse {
			return true
		}
	}
	return false
}

func prefix(key any, issues []Issue) []Issue {
	for i := range issues {
		issues[i].Path = append([]any{key}, issues[i].Path...)
	}
	return issues
}

// parsedType is util.parsedType.
func parsedType(v any) string {
	switch t := v.(type) {
	case undefT:
		return "undefined"
	case nil:
		return "null"
	case []any:
		return "array"
	case *jsonx.Object:
		return "object"
	case string:
		return "string"
	case float64:
		if math.IsNaN(t) {
			return "NaN"
		}
		if math.IsInf(t, 0) {
			return "Infinity"
		}
		return "number"
	case bool:
		return "boolean"
	}
	return "object"
}

func typeIssue(expected string, v any) Issue {
	return newIssue("invalid_type", fmt.Sprintf("Invalid input: expected %s, received %s", expected, parsedType(v)), contUnset, "expected", expected, "code", "invalid_type")
}

func stringifyPrimitive(v any) string {
	switch t := v.(type) {
	case string:
		return `"` + t + `"`
	case float64:
		return jsonx.NumberString(t)
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// --- checks ----------------------------------------------------------------------------

type check struct {
	when func(v any) bool
	run  func(v *any) *Issue
}

func hasLength(v any) bool {
	switch v.(type) {
	case string, []any:
		return true
	}
	return false
}

func lengthOrigin(v any) string {
	switch v.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	}
	return "unknown"
}

// lengthOf is zod's length: strings in code points, arrays in items.
func lengthOf(v any) int {
	switch t := v.(type) {
	case string:
		return utf8.RuneCountInString(t)
	case []any:
		return len(t)
	}
	return 0
}

func sizing(origin string) (string, bool) {
	switch origin {
	case "string":
		return "characters", true
	case "array":
		return "items", true
	}
	return "", false
}

func minLen(n int, msg string) check {
	return check{when: hasLength, run: func(v *any) *Issue {
		if lengthOf(*v) >= n {
			return nil
		}
		o := lengthOrigin(*v)
		m := msg
		if m == "" {
			if unit, ok := sizing(o); ok {
				m = fmt.Sprintf("Too small: expected %s to have >=%d %s", o, n, unit)
			} else {
				m = fmt.Sprintf("Too small: expected %s to be >=%d", o, n)
			}
		}
		is := newIssue("too_small", m, contTrue, "origin", o, "code", "too_small", "minimum", float64(n), "inclusive", true)
		return &is
	}}
}

func maxLen(n int, msg string) check {
	return check{when: hasLength, run: func(v *any) *Issue {
		if lengthOf(*v) <= n {
			return nil
		}
		o := lengthOrigin(*v)
		m := msg
		if m == "" {
			if unit, ok := sizing(o); ok {
				m = fmt.Sprintf("Too big: expected %s to have <=%d %s", o, n, unit)
			} else {
				m = fmt.Sprintf("Too big: expected %s to be <=%d", o, n)
			}
		}
		is := newIssue("too_big", m, contTrue, "origin", o, "code", "too_big", "maximum", float64(n), "inclusive", true)
		return &is
	}}
}

func trimCheck() check {
	return check{run: func(v *any) *Issue {
		if s, ok := (*v).(string); ok {
			*v = strx.Trim(s)
		}
		return nil
	}}
}

func regexCheck(re *regexp.Regexp, jsSource, msg string) check {
	return check{run: func(v *any) *Issue {
		s, _ := (*v).(string)
		if re.MatchString(s) {
			return nil
		}
		m := msg
		if m == "" {
			m = "Invalid string: must match pattern /" + jsSource + "/"
		}
		is := newIssue("invalid_format", m, contTrue, "origin", "string", "code", "invalid_format", "format", "regex", "pattern", "/"+jsSource+"/")
		return &is
	}}
}

// MaxSafeInt is Number.MAX_SAFE_INTEGER.
const MaxSafeInt = 9007199254740991

func intCheck() check {
	return check{run: func(v *any) *Issue {
		f, _ := (*v).(float64)
		if f != math.Trunc(f) || math.IsInf(f, 0) {
			is := newIssue("invalid_type", "Invalid input: expected int, received number", contFalse, "expected", "int", "format", "safeint", "code", "invalid_type")
			return &is
		}
		if f > MaxSafeInt {
			is := newIssue("too_big", fmt.Sprintf("Too big: expected int to be <=%d", int64(MaxSafeInt)), contTrue, "origin", "number", "code", "too_big", "maximum", float64(MaxSafeInt), "inclusive", true)
			return &is
		}
		if f < -MaxSafeInt {
			is := newIssue("too_small", fmt.Sprintf("Too small: expected int to be >=%d", int64(-MaxSafeInt)), contTrue, "origin", "number", "code", "too_small", "minimum", float64(-MaxSafeInt), "inclusive", true)
			return &is
		}
		return nil
	}}
}

func gte(n float64) check {
	return check{run: func(v *any) *Issue {
		if f, _ := (*v).(float64); f >= n {
			return nil
		}
		is := newIssue("too_small", "Too small: expected number to be >="+jsonx.NumberString(n), contTrue, "origin", "number", "code", "too_small", "minimum", n, "inclusive", true)
		return &is
	}}
}

func lte(n float64) check {
	return check{run: func(v *any) *Issue {
		if f, _ := (*v).(float64); f <= n {
			return nil
		}
		is := newIssue("too_big", "Too big: expected number to be <="+jsonx.NumberString(n), contTrue, "origin", "number", "code", "too_big", "maximum", n, "inclusive", true)
		return &is
	}}
}

func refineCheck(fn func(v any) bool, msg string) check {
	return check{run: func(v *any) *Issue {
		if fn(*v) {
			return nil
		}
		is := newIssue("custom", msg, contTrue, "code", "custom")
		return &is
	}}
}

func runChecks(v any, issues []Issue, checks []check) (any, []Issue) {
	isAborted := aborted(issues, 0)
	for _, ch := range checks {
		if ch.when != nil {
			if explicitlyAborted(issues) || !ch.when(v) {
				continue
			}
		} else if isAborted {
			continue
		}
		before := len(issues)
		if is := ch.run(&v); is != nil {
			issues = append(issues, *is)
		}
		if len(issues) != before && !isAborted {
			isAborted = aborted(issues, before)
		}
	}
	return v, issues
}

// TypeValidationError is the AI SDK's TypeValidationError around a failed
// parse: what a tool call with a bad input reports.
type TypeValidationError struct {
	Value any
	Cause error
}

func (e *TypeValidationError) Error() string {
	v := "undefined"
	if !IsUndefined(e.Value) {
		v = jsonx.Stringify(e.Value)
	}
	return "Type validation failed: Value: " + v + ".\nError message: " + e.Cause.Error()
}

// JSName is the JS error name (String(error) prints "<name>: <message>").
func (e *TypeValidationError) JSName() string { return "AI_TypeValidationError" }

func (e *TypeValidationError) Unwrap() error { return e.Cause }

// JSName is the error's JS name.
func (e *Error) JSName() string { return "ZodError" }
