package zod

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Schema is one zod schema: parse plus its JSON schema.
type Schema interface {
	parse(v any) (any, []Issue)
	optin() string
	optout() string
	// json is the schema's draft-07 JSON before compaction and the AI SDK pass.
	json() *jsonx.Object
}

// Parse runs schema.safeParse(v): the output, or an *Error.
func Parse(s Schema, v any) (any, error) {
	out, issues := s.parse(v)
	if len(issues) > 0 {
		return nil, &Error{Issues: issues, Input: v}
	}
	return out, nil
}

// ParseJSON parses JSON text (empty text is undefined) and runs Parse.
func ParseJSON(s Schema, raw []byte) (any, error) {
	var v any = Undefined
	if len(strings.TrimSpace(string(raw))) > 0 {
		p, err := jsonx.Parse(raw)
		if err != nil {
			return nil, err
		}
		v = p
	}
	return Parse(s, v)
}

// --- string ------------------------------------------------------------------------------

type StringS struct {
	checks   []check
	min, max *int
	pattern  string
	format   string
	desc     string
}

func String() *StringS { return &StringS{} }

func (s StringS) clone() *StringS {
	c := s
	c.checks = append([]check(nil), s.checks...)
	return &c
}

func (s *StringS) Min(n int, msg ...string) *StringS {
	c := s.clone()
	c.checks = append(c.checks, minLen(n, first(msg)))
	if c.min == nil || n > *c.min {
		c.min = &n
	}
	return c
}

func (s *StringS) Max(n int, msg ...string) *StringS {
	c := s.clone()
	c.checks = append(c.checks, maxLen(n, first(msg)))
	if c.max == nil || n < *c.max {
		c.max = &n
	}
	return c
}

func (s *StringS) Trim() *StringS {
	c := s.clone()
	c.checks = append(c.checks, trimCheck())
	return c
}

// Regex adds a pattern; jsSource is the JS regex source (what the JSON schema
// and the default message show).
func (s *StringS) Regex(re *regexp.Regexp, jsSource string, msg ...string) *StringS {
	c := s.clone()
	c.checks = append(c.checks, regexCheck(re, jsSource, first(msg)))
	c.pattern = jsSource
	return c
}

// Refine adds a custom check with its message.
func (s *StringS) Refine(fn func(string) bool, msg string) *StringS {
	c := s.clone()
	c.checks = append(c.checks, refineCheck(func(v any) bool { x, _ := v.(string); return fn(x) }, msg))
	return c
}

// URL is z.string().url() / z.url({protocol}): a URL the WHATWG parser
// accepts, with protocol (no colon) matching protocol when set.
func (s *StringS) URL(protocol *regexp.Regexp) *StringS {
	c := s.clone()
	c.format = "uri"
	c.checks = append(c.checks, urlCheck(protocol))
	return c
}

func (s *StringS) Describe(d string) *StringS { c := s.clone(); c.desc = d; return c }

func (s *StringS) Optional() *OptionalS { return Optional(s) }

func (s *StringS) parse(v any) (any, []Issue) {
	var issues []Issue
	if _, ok := v.(string); !ok {
		issues = append(issues, typeIssue("string", v))
	}
	return runChecks(v, issues, s.checks)
}
func (StringS) optin() string  { return "" }
func (StringS) optout() string { return "" }
func (s *StringS) json() *jsonx.Object {
	o := obj("type", "string")
	if s.min != nil {
		o.Set("minLength", float64(*s.min))
	}
	if s.max != nil {
		o.Set("maxLength", float64(*s.max))
	}
	if s.format != "" {
		o.Set("format", s.format)
	}
	if s.pattern != "" {
		o.Set("pattern", s.pattern)
	}
	return withDesc(o, s.desc)
}

// urlCheck is $ZodURL with a protocol: the trimmed text must parse as a
// WHATWG URL (http and https need "://"), its protocol must match, and the
// value becomes the trimmed text without tabs and newlines.
func urlCheck(protocol *regexp.Regexp) check {
	return check{run: func(v *any) *Issue {
		s, _ := (*v).(string)
		trimmed := strx.Trim(s)
		if protocol != nil && protocol.String() == `^https?$` && !httpSlashes.MatchString(trimmed) {
			is := newIssue("invalid_format", "Invalid URL", contTrue, "code", "invalid_format", "format", "url", "note", "Invalid URL format")
			return &is
		}
		u, err := whatwg.ParseURL(trimmed)
		if err != nil {
			is := newIssue("invalid_format", "Invalid URL", contTrue, "code", "invalid_format", "format", "url")
			return &is
		}
		*v = tabNewline.ReplaceAllString(trimmed, "")
		if protocol != nil && !protocol.MatchString(u.Scheme) {
			is := newIssue("invalid_format", "Invalid URL", contTrue, "code", "invalid_format", "format", "url", "note", "Invalid protocol", "pattern", protocol.String())
			return &is
		}
		return nil
	}}
}

var (
	httpSlashes = regexp.MustCompile(`(?i)^https?://`)
	tabNewline  = regexp.MustCompile(`[\t\n\r]`)
)

// --- number and boolean ------------------------------------------------------------------

type NumberS struct {
	checks   []check
	isInt    bool
	min, max *float64
	desc     string
	coerce   bool
}

func Number() *NumberS { return &NumberS{} }

// Int is z.number().int().
func Int() *NumberS { return Number().Int() }

func (s NumberS) clone() *NumberS {
	c := s
	c.checks = append([]check(nil), s.checks...)
	return &c
}

func (s *NumberS) Int() *NumberS {
	c := s.clone()
	c.checks = append(c.checks, intCheck())
	c.isInt = true
	return c
}

func (s *NumberS) Min(n float64) *NumberS {
	c := s.clone()
	c.checks = append(c.checks, gte(n))
	if c.min == nil || n > *c.min {
		c.min = &n
	}
	return c
}

func (s *NumberS) Max(n float64) *NumberS {
	c := s.clone()
	c.checks = append(c.checks, lte(n))
	if c.max == nil || n < *c.max {
		c.max = &n
	}
	return c
}

func (s *NumberS) Describe(d string) *NumberS { c := s.clone(); c.desc = d; return c }
func (s *NumberS) Optional() *OptionalS       { return Optional(s) }

func (s *NumberS) parse(v any) (any, []Issue) {
	var issues []Issue
	if s.coerce {
		v = jsNumber(v)
	}
	if f, ok := v.(float64); !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		issues = append(issues, typeIssue("number", v))
	}
	return runChecks(v, issues, s.checks)
}
func (NumberS) optin() string  { return "" }
func (NumberS) optout() string { return "" }
func (s *NumberS) json() *jsonx.Object {
	o := jsonx.NewMap[any]()
	min, max := s.min, s.max
	if s.isInt {
		o.Set("type", "integer")
		lo, hi := float64(-MaxSafeInt), float64(MaxSafeInt)
		if min == nil || *min < lo {
			min = &lo
		}
		if max == nil || *max > hi {
			max = &hi
		}
	} else {
		o.Set("type", "number")
	}
	if min != nil {
		o.Set("minimum", *min)
	}
	if max != nil {
		o.Set("maximum", *max)
	}
	return withDesc(o, s.desc)
}

type BoolS struct{ desc string }

func Bool() *BoolS                        { return &BoolS{} }
func (s *BoolS) Describe(d string) *BoolS { return &BoolS{desc: d} }
func (s *BoolS) Optional() *OptionalS     { return Optional(s) }
func (BoolS) optin() string               { return "" }
func (BoolS) optout() string              { return "" }
func (s *BoolS) json() *jsonx.Object      { return withDesc(obj("type", "boolean"), s.desc) }
func (s *BoolS) parse(v any) (any, []Issue) {
	if _, ok := v.(bool); !ok {
		return v, []Issue{typeIssue("boolean", v)}
	}
	return v, nil
}

// UnknownS is z.unknown(): anything, its JSON schema is {}.
type UnknownS struct{ desc string }

func Unknown() *UnknownS                       { return &UnknownS{} }
func (UnknownS) optin() string                 { return "optional" }
func (UnknownS) optout() string                { return "optional" }
func (s *UnknownS) json() *jsonx.Object        { return withDesc(jsonx.NewMap[any](), s.desc) }
func (s *UnknownS) parse(v any) (any, []Issue) { return v, nil }

// --- enum and literal ----------------------------------------------------------------------

type EnumS struct {
	values []any
	single bool
	desc   string
}

// Enum is z.enum([...]). zod keeps the values as an object's keys, so
// integer-like ones come first in ascending order, as JS orders keys.
func Enum(vals ...string) *EnumS {
	e := &EnumS{}
	var ints []string
	var rest []string
	for _, v := range vals {
		if isArrayIndex(v) {
			ints = append(ints, v)
		} else {
			rest = append(rest, v)
		}
	}
	sort.SliceStable(ints, func(i, j int) bool {
		a, _ := strconv.ParseUint(ints[i], 10, 64)
		b, _ := strconv.ParseUint(ints[j], 10, 64)
		return a < b
	})
	for _, v := range append(ints, rest...) {
		e.values = append(e.values, v)
	}
	return e
}

// isArrayIndex is a canonical array index: "0", or digits without a
// leading zero below 2^32 - 1.
func isArrayIndex(s string) bool {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n < 1<<32-1
}

// Literal is z.literal(v) (a string, float64, bool or nil).
func Literal(v any) *EnumS { return &EnumS{values: []any{v}, single: true} }

func (s *EnumS) Describe(d string) *EnumS { c := *s; c.desc = d; return &c }
func (s *EnumS) Optional() *OptionalS     { return Optional(s) }
func (EnumS) optin() string               { return "" }
func (EnumS) optout() string              { return "" }

// Values is the enum's options.
func (s *EnumS) Values() []string {
	out := make([]string, 0, len(s.values))
	for _, v := range s.values {
		if x, ok := v.(string); ok {
			out = append(out, x)
		}
	}
	return out
}

func (s *EnumS) parse(v any) (any, []Issue) {
	for _, x := range s.values {
		if x == v {
			return v, nil
		}
	}
	if s.single {
		return v, []Issue{newIssue("invalid_value", "Invalid input: expected "+stringifyPrimitive(s.values[0]), contUnset, "code", "invalid_value", "values", append([]any(nil), s.values...))}
	}
	parts := make([]string, len(s.values))
	for i, x := range s.values {
		parts[i] = stringifyPrimitive(x)
	}
	return v, []Issue{newIssue("invalid_value", "Invalid option: expected one of "+strings.Join(parts, "|"), contUnset, "code", "invalid_value", "values", append([]any(nil), s.values...))}
}

func jsType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "object"
}

func (s *EnumS) json() *jsonx.Object {
	o := jsonx.NewMap[any]()
	if s.single {
		o.Set("type", jsType(s.values[0]))
		o.Set("const", s.values[0])
	} else {
		o.Set("type", "string")
		o.Set("enum", append([]any(nil), s.values...))
	}
	return withDesc(o, s.desc)
}

// --- wrappers ----------------------------------------------------------------------------

type OptionalS struct {
	inner Schema
	desc  string
}

func Optional(inner Schema) *OptionalS            { return &OptionalS{inner: inner} }
func (s *OptionalS) Describe(d string) *OptionalS { return &OptionalS{inner: s.inner, desc: d} }
func (s *OptionalS) Inner() Schema                { return s.inner }
func (s *OptionalS) optout() string               { return "optional" }
func (s *OptionalS) json() *jsonx.Object {
	return merge(withDesc(jsonx.NewMap[any](), s.desc), s.inner.json())
}
func (s *OptionalS) Nullable() *NullableS { return Nullable(s) }
func (s *OptionalS) optin() string {
	if s.inner.optin() == "defaulted" {
		return "defaulted"
	}
	return "optional"
}
func (s *OptionalS) parse(v any) (any, []Issue) {
	if IsUndefined(v) && s.inner.optin() != "defaulted" {
		return v, nil
	}
	return s.inner.parse(v)
}

type NullableS struct {
	inner Schema
	desc  string
}

func Nullable(inner Schema) *NullableS            { return &NullableS{inner: inner} }
func (s *NullableS) Describe(d string) *NullableS { return &NullableS{inner: s.inner, desc: d} }
func (s *NullableS) Optional() *OptionalS         { return Optional(s) }
func (s *NullableS) optin() string                { return s.inner.optin() }
func (s *NullableS) optout() string               { return s.inner.optout() }
func (s *NullableS) json() *jsonx.Object {
	return withDesc(obj("anyOf", []any{s.inner.json(), obj("type", "null")}), s.desc)
}
func (s *NullableS) parse(v any) (any, []Issue) {
	if v == nil {
		return nil, nil
	}
	return s.inner.parse(v)
}

// DefaultS: an undefined input becomes the default (not validated, as zod).
type DefaultS struct {
	inner  Schema
	def    func() any
	desc   string
	checks []check
}

// Default is inner.default(value). value is a jsonx value; objects and arrays
// are copied on every use.
func Default(inner Schema, value any) *DefaultS {
	return &DefaultS{inner: inner, def: func() any { return clone(value) }}
}

func (s *DefaultS) Describe(d string) *DefaultS { c := *s; c.desc = d; return &c }
func (s *DefaultS) optin() string               { return "defaulted" }
func (s *DefaultS) optout() string              { return "" }
func (s *DefaultS) json() *jsonx.Object {
	o := obj("default", clone(s.def()))
	return merge(withDesc(o, s.desc), s.inner.json())
}
func (s *DefaultS) parse(v any) (any, []Issue) {
	var issues []Issue
	if IsUndefined(v) {
		v = s.def()
	} else {
		v, issues = s.inner.parse(v)
		if IsUndefined(v) {
			v = s.def()
		}
	}
	return runChecks(v, issues, s.checks)
}

func clone(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = clone(x)
		}
		return out
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			out.Set(k, clone(x))
		}
		return out
	}
	return v
}

// --- object, array, record, union ------------------------------------------------------------

type Field struct {
	Key    string
	Schema Schema
}

// F is one object field.
func F(key string, s Schema) Field { return Field{key, s} }

type ObjectS struct {
	fields  []Field
	desc    string
	refines []objectRefine
}

// Object is z.object({...}) (unknown keys are dropped).
func Object(fields ...Field) *ObjectS { return &ObjectS{fields: fields} }

func (o *ObjectS) Describe(d string) *ObjectS { return &ObjectS{fields: o.fields, desc: d} }
func (o *ObjectS) Optional() *OptionalS       { return Optional(o) }
func (o *ObjectS) Fields() []Field            { return o.fields }

// Field returns the schema of one key (nil when missing).
func (o *ObjectS) Field(key string) Schema {
	for _, f := range o.fields {
		if f.Key == key {
			return f.Schema
		}
	}
	return nil
}

// Partial is .partial(): every field optional.
func (o *ObjectS) Partial() *ObjectS {
	out := &ObjectS{desc: o.desc}
	for _, f := range o.fields {
		out.fields = append(out.fields, F(f.Key, Optional(f.Schema)))
	}
	return out
}

func (ObjectS) optin() string  { return "" }
func (ObjectS) optout() string { return "" }

func (o *ObjectS) json() *jsonx.Object {
	props := jsonx.NewMap[any]()
	var required []any
	for _, f := range o.fields {
		props.Set(f.Key, f.Schema.json())
		if f.Schema.optin() == "" {
			required = append(required, f.Key)
		}
	}
	out := obj("type", "object", "properties", props)
	if len(required) > 0 {
		out.Set("required", required)
	}
	out.Set("additionalProperties", false)
	return withDesc(out, o.desc)
}

func (o *ObjectS) parse(v any) (any, []Issue) {
	in, ok := v.(*jsonx.Object)
	if !ok {
		return v, []Issue{typeIssue("object", v)}
	}
	out := jsonx.NewMap[any]()
	var issues []Issue
	for _, fl := range o.fields {
		raw, present := in.Get(fl.Key)
		if !present {
			raw = Undefined
		}
		val, is := fl.Schema.parse(raw)
		optin, optout := fl.Schema.optin(), fl.Schema.optout()
		switch {
		case optin != "" && optout == "optional":
			if len(is) == 0 || present {
				issues = append(issues, prefix(fl.Key, is)...)
				assign := present
				if optin != "optional" {
					assign = !IsUndefined(val) || present
				}
				if assign {
					out.Set(fl.Key, val)
				}
			}
		case optin == "":
			issues = append(issues, prefix(fl.Key, is)...)
			if !present && len(is) == 0 {
				iss := newIssue("invalid_type", "Invalid input: expected nonoptional, received undefined", contUnset, "expected", "nonoptional", "code", "invalid_type")
				iss.Path = []any{fl.Key}
				issues = append(issues, iss)
			}
			if present {
				out.Set(fl.Key, val)
			}
		default:
			issues = append(issues, prefix(fl.Key, is)...)
			if optin == "defaulted" {
				out.Set(fl.Key, val)
			} else if !IsUndefined(val) || present {
				out.Set(fl.Key, val)
			}
		}
	}
	for _, r := range o.refines {
		if aborted(issues, 0) {
			break
		}
		if !r.fn(out) {
			issues = append(issues, newIssue("custom", r.msg, contTrue, "code", "custom"))
		}
	}
	return out, issues
}

type ArrayS struct {
	elem     Schema
	checks   []check
	min, max *int
	desc     string
}

func Array(elem Schema) *ArrayS { return &ArrayS{elem: elem} }

func (a ArrayS) clone() *ArrayS { c := a; c.checks = append([]check(nil), a.checks...); return &c }
func (a *ArrayS) Min(n int) *ArrayS {
	c := a.clone()
	c.checks = append(c.checks, minLen(n, ""))
	c.min = &n
	return c
}
func (a *ArrayS) Max(n int) *ArrayS {
	c := a.clone()
	c.checks = append(c.checks, maxLen(n, ""))
	c.max = &n
	return c
}
func (a *ArrayS) Describe(d string) *ArrayS { c := a.clone(); c.desc = d; return c }
func (a *ArrayS) Optional() *OptionalS      { return Optional(a) }
func (ArrayS) optin() string                { return "" }
func (ArrayS) optout() string               { return "" }
func (a *ArrayS) json() *jsonx.Object {
	o := jsonx.NewMap[any]()
	if a.min != nil {
		o.Set("minItems", float64(*a.min))
	}
	if a.max != nil {
		o.Set("maxItems", float64(*a.max))
	}
	o.Set("type", "array")
	o.Set("items", a.elem.json())
	return withDesc(o, a.desc)
}
func (a *ArrayS) parse(v any) (any, []Issue) {
	in, ok := v.([]any)
	if !ok {
		return runChecks(v, []Issue{typeIssue("array", v)}, a.checks)
	}
	out := make([]any, len(in))
	var issues []Issue
	for i, x := range in {
		val, is := a.elem.parse(x)
		issues = append(issues, prefix(i, is)...)
		if IsUndefined(val) {
			val = nil
		}
		out[i] = val
	}
	return runChecks(out, issues, a.checks)
}

// RecordS is z.record(z.string(), value), or z.record(key, value) with Keys.
type RecordS struct {
	value Schema
	desc  string
	// key checks each key (z.record(z.string().max(64), ...)); nil takes any.
	key Schema
}

func Record(value Schema) *RecordS            { return &RecordS{value: value} }
func (r *RecordS) Describe(d string) *RecordS { return &RecordS{value: r.value, desc: d, key: r.key} }

// Keys sets the key schema. A key it refuses is zod's invalid_key issue and
// its value isn't parsed.
func (r *RecordS) Keys(k Schema) *RecordS { return &RecordS{value: r.value, desc: r.desc, key: k} }

func (r *RecordS) Optional() *OptionalS { return Optional(r) }
func (RecordS) optin() string           { return "" }
func (RecordS) optout() string          { return "" }
func (r *RecordS) json() *jsonx.Object {
	names := obj("type", "string")
	if r.key != nil {
		names = r.key.json()
	}
	return withDesc(obj("type", "object", "propertyNames", names, "additionalProperties", r.value.json()), r.desc)
}
func (r *RecordS) parse(v any) (any, []Issue) {
	in, ok := v.(*jsonx.Object)
	if !ok {
		return v, []Issue{typeIssue("record", v)}
	}
	out := jsonx.NewMap[any]()
	var issues []Issue
	for _, k := range in.Keys() {
		if k == "__proto__" {
			continue
		}
		key := k
		if r.key != nil {
			kv, kis := r.key.parse(k)
			if len(kis) > 0 {
				inner := make([]any, len(kis))
				for n, is := range kis {
					inner[n] = is.JSON()
				}
				bad := newIssue("invalid_key", "Invalid key in record", contUnset, "code", "invalid_key", "origin", "record", "issues", inner)
				bad.Path = []any{k}
				issues = append(issues, bad)
				continue
			}
			key, _ = kv.(string)
		}
		raw, _ := in.Get(k)
		val, is := r.value.parse(raw)
		issues = append(issues, prefix(k, is)...)
		if !IsUndefined(val) {
			out.Set(key, val)
		}
	}
	return out, issues
}

type UnionS struct {
	options []Schema
	desc    string
}

func Union(opts ...Schema) *UnionS          { return &UnionS{options: opts} }
func (u *UnionS) Describe(d string) *UnionS { return &UnionS{options: u.options, desc: d} }
func (u *UnionS) Optional() *OptionalS      { return Optional(u) }
func (u *UnionS) json() *jsonx.Object {
	opts := make([]any, len(u.options))
	for i, o := range u.options {
		opts[i] = o.json()
	}
	return withDesc(obj("anyOf", opts), u.desc)
}
func (u *UnionS) optin() string {
	for _, o := range u.options {
		if o.optin() == "defaulted" {
			return "defaulted"
		}
	}
	for _, o := range u.options {
		if o.optin() != "" {
			return "optional"
		}
	}
	return ""
}
func (u *UnionS) optout() string {
	for _, o := range u.options {
		if o.optout() == "optional" {
			return "optional"
		}
	}
	return ""
}
func (u *UnionS) parse(v any) (any, []Issue) {
	type res struct {
		v      any
		issues []Issue
	}
	var results []res
	for _, o := range u.options {
		val, is := o.parse(v)
		if len(is) == 0 {
			return val, nil
		}
		results = append(results, res{val, is})
	}
	var nonAborted []res
	for _, r := range results {
		if !aborted(r.issues, 0) {
			nonAborted = append(nonAborted, r)
		}
	}
	if len(nonAborted) == 1 {
		return nonAborted[0].v, nonAborted[0].issues
	}
	errs := make([]any, len(results))
	for i, r := range results {
		list := make([]any, len(r.issues))
		for n, is := range r.issues {
			list[n] = is.JSON()
		}
		errs[i] = list
	}
	return v, []Issue{newIssue("invalid_union", "Invalid input", contUnset, "code", "invalid_union", "errors", errs)}
}

// --- JSON schema -------------------------------------------------------------------------

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func withDesc(o *jsonx.Object, d string) *jsonx.Object {
	if d != "" {
		o.Set("description", d)
	}
	return o
}

// merge is flattenRef: the wrapper's own keys first (its values win), then
// the inner schema's other keys in their order.
func merge(own, inner *jsonx.Object) *jsonx.Object {
	for _, k := range inner.Keys() {
		if _, ok := own.Get(k); ok {
			continue
		}
		v, _ := inner.Get(k)
		own.Set(k, v)
	}
	return own
}

// compact is compactTypeUnion: anyOf of bare {type} branches becomes a type list.
func compact(v any) {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			compact(x)
		}
		return
	case *jsonx.Object:
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			compact(x)
		}
		compactTypeUnion(t)
	}
}

func compactTypeUnion(s *jsonx.Object) {
	raw, ok := s.Get("anyOf")
	options, isList := raw.([]any)
	if !ok || !isList || len(options) == 0 {
		return
	}
	if _, has := s.Get("type"); has {
		return
	}
	var types []any
	for _, o := range options {
		opt, ok := o.(*jsonx.Object)
		if !ok {
			return
		}
		keys := opt.Keys()
		if len(keys) != 1 || keys[0] != "type" {
			return
		}
		t, _ := opt.Get("type")
		members, isArr := t.([]any)
		if !isArr {
			members = []any{t}
		}
		for _, m := range members {
			ms, ok := m.(string)
			if !ok {
				return
			}
			dup := false
			for _, x := range types {
				if x == ms {
					dup = true
				}
			}
			if !dup {
				types = append(types, ms)
			}
		}
	}
	s.Delete("anyOf")
	if len(types) == 1 {
		s.Set("type", types[0])
	} else {
		s.Set("type", types)
	}
}

// addAdditionalProperties is the AI SDK's addAdditionalPropertiesToJsonSchema.
func addAdditionalProperties(s *jsonx.Object) {
	if t, _ := s.Get("type"); t == "object" || hasObjectType(t) {
		ap, _ := s.Get("additionalProperties")
		if sub, ok := ap.(*jsonx.Object); ok {
			addAdditionalProperties(sub)
		} else {
			s.Set("additionalProperties", false)
		}
		if p, ok := s.Get("properties"); ok {
			if props, ok := p.(*jsonx.Object); ok {
				for _, k := range props.Keys() {
					x, _ := props.Get(k)
					if sub, ok := x.(*jsonx.Object); ok {
						addAdditionalProperties(sub)
					}
				}
			}
		}
	}
	if items, ok := s.Get("items"); ok {
		visitAll(items)
	}
	for _, k := range []string{"anyOf", "allOf", "oneOf"} {
		if x, ok := s.Get(k); ok {
			visitAll(x)
		}
	}
}

func hasObjectType(t any) bool {
	list, ok := t.([]any)
	if !ok {
		return false
	}
	for _, x := range list {
		if x == "object" {
			return true
		}
	}
	return false
}

func visitAll(v any) {
	switch t := v.(type) {
	case *jsonx.Object:
		addAdditionalProperties(t)
	case []any:
		for _, x := range t {
			if o, ok := x.(*jsonx.Object); ok {
				addAdditionalProperties(o)
			}
		}
	}
}

// JSONSchema is asSchema(zodSchema).jsonSchema: what a tool's input schema
// looks like on the wire.
func JSONSchema(s Schema) *jsonx.Object {
	body := s.json()
	compact(body)
	out := obj("$schema", "http://json-schema.org/draft-07/schema#")
	merge(out, body)
	addAdditionalProperties(out)
	return out
}

// JSONSchemaBytes is JSONSchema as JSON text.
func JSONSchemaBytes(s Schema) json.RawMessage {
	return json.RawMessage(jsonx.Stringify(JSONSchema(s)))
}

func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

// Must panics on a parse error; for constant inputs.
func Must(v any, err error) any {
	if err != nil {
		panic(fmt.Sprintf("zod: %v", err))
	}
	return v
}
