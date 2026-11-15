// Package jsonx reads and writes JSON the way the TS build did, so stored JSON
// keeps its bytes across the port: JSON.stringify output (no HTML escaping, JS
// key order) and objects that remember their key order (Map, Object).
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Marshal is JSON.stringify(v): no HTML escaping, U+2028 and U+2029 written as
// is, objects in their own key order.
func Marshal(v any) ([]byte, error) {
	v = jsNumbers(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return unescapeLineSeparators(out), nil
}

// Stringify is Marshal as a string. It panics only on values JSON can't hold
// (channels, functions), which is a programming error.
func Stringify(v any) string {
	b, err := Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("jsonx.Stringify: %v", err))
	}
	return string(b)
}

// jsNumbers gives parsed values JSON.stringify's number rules: NaN and
// ±Infinity are null, -0 is 0. Only *Object, []any and float64 are walked;
// values without such numbers are returned as they are.
func jsNumbers(v any) any {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil
		}
		if t == 0 {
			return float64(0)
		}
	case []any:
		var out []any
		for i, x := range t {
			y := jsNumbers(x)
			if out == nil && !same(x, y) {
				out = append(make([]any, 0, len(t)), t[:i]...)
			}
			if out != nil {
				out = append(out, y)
			}
		}
		if out != nil {
			return out
		}
	case *Object:
		if t == nil {
			return v
		}
		var out *Object
		for _, k := range t.keys {
			x := t.vals[k]
			y := jsNumbers(x)
			if out == nil && !same(x, y) {
				out = t.Clone()
			}
			if out != nil {
				out.vals[k] = y
			}
		}
		if out != nil {
			return out
		}
	}
	return v
}

// same reports whether jsNumbers left x alone.
func same(x, y any) bool {
	switch a := x.(type) {
	case float64:
		b, ok := y.(float64)
		return ok && math.Float64bits(a) == math.Float64bits(b)
	case []any:
		b, ok := y.([]any)
		return ok && len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
	case *Object:
		b, ok := y.(*Object)
		return ok && a == b
	}
	return true
}

// encoding/json always escapes U+2028 and U+2029; JSON.stringify doesn't.
func unescapeLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			if b[i+1] == 'u' && i+5 < len(b) && string(b[i+2:i+5]) == "202" && (b[i+5] == '8' || b[i+5] == '9') {
				if b[i+5] == '8' {
					out = append(out, "\u2028"...)
				} else {
					out = append(out, "\u2029"...)
				}
				i += 5
				continue
			}
			out = append(out, b[i], b[i+1])
			i++
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// arrayIndex reports whether k is a canonical array index ("0", "17", not "01").
func arrayIndex(k string) (uint64, bool) {
	if k == "" || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(k, 10, 32)
	if err != nil || n == 1<<32-1 {
		return 0, false
	}
	return n, true
}

// Map is a JSON object with JS property order: array-index keys ascending,
// then the other keys in insertion order. The zero value is ready to use.
type Map[V any] struct {
	keys []string
	vals map[string]V
}

// NewMap returns an empty map.
func NewMap[V any]() *Map[V] { return &Map[V]{} }

func (m *Map[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

func (m *Map[V]) Get(k string) (V, bool) {
	var zero V
	if m == nil || m.vals == nil {
		return zero, false
	}
	v, ok := m.vals[k]
	return v, ok
}

// Set adds or replaces a key. A replaced key keeps its position.
func (m *Map[V]) Set(k string, v V) {
	if m.vals == nil {
		m.vals = map[string]V{}
	}
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *Map[V]) Delete(k string) {
	if m == nil || m.vals == nil {
		return
	}
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, x := range m.keys {
		if x == k {
			m.keys = append(m.keys[:i:i], m.keys[i+1:]...)
			break
		}
	}
}

// Keys in JS property order.
func (m *Map[V]) Keys() []string {
	if m == nil {
		return nil
	}
	var idx []string
	var rest []string
	for _, k := range m.keys {
		if _, ok := arrayIndex(k); ok {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := arrayIndex(idx[i])
		b, _ := arrayIndex(idx[j])
		return a < b
	})
	return append(idx, rest...)
}

// Clone is a shallow copy.
func (m *Map[V]) Clone() *Map[V] {
	out := &Map[V]{}
	if m == nil {
		return out
	}
	for _, k := range m.keys {
		out.Set(k, m.vals[k])
	}
	return out
}

// Assign is Object.assign(m, other): other's keys are set on m in order.
func (m *Map[V]) Assign(other *Map[V]) *Map[V] {
	if other == nil {
		return m
	}
	for _, k := range other.Keys() {
		m.Set(k, other.vals[k])
	}
	return m
}

func (m *Map[V]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.Keys() {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (m *Map[V]) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('{') {
		return errors.New("jsonx: not an object")
	}
	*m = Map[V]{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		var v V
		if err := unmarshalValue(raw, &v); err != nil {
			return err
		}
		m.Set(key, v)
	}
	_, err = dec.Token()
	return err
}

// unmarshalValue decodes into v, turning objects inside an `any` into *Object.
func unmarshalValue(raw []byte, v any) error {
	if p, ok := v.(*any); ok {
		val, err := Parse(raw)
		if err != nil {
			return err
		}
		*p = val
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Object is a JSON object of arbitrary values (JSON.parse of an object).
type Object = Map[any]

// Parse is JSON.parse: objects become *Object, arrays []any, numbers float64.
func Parse(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("jsonx: unexpected data after JSON value")
	}
	return v, nil
}

// MaxDepth bounds nesting: V8 parses any depth, but a recursive parser on a
// 10 MB body of "[" would overflow the stack (Known gaps).
const MaxDepth = 10_000

func parseValue(dec *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, errors.New("jsonx: nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &Object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				val, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				obj.Set(key, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("jsonx: unexpected %v", t)
	case json.Number:
		// JSON.parse: out of range is ±Infinity (or 0), and -0 prints as 0.
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, err
		}
		if f == 0 {
			f = 0
		}
		return f, nil
	default:
		return t, nil
	}
}

// ParseObject parses JSON that must be an object.
func ParseObject(b []byte) (*Object, error) {
	v, err := Parse(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, errors.New("jsonx: not an object")
	}
	return o, nil
}

// ToObject turns a Go value (map, struct, *Object) into an *Object by a JSON
// round trip. Nil gives nil.
func ToObject(v any) (*Object, error) {
	if v == nil {
		return nil, nil
	}
	if o, ok := v.(*Object); ok {
		return o, nil
	}
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return nil, nil
	}
	return ParseObject(b)
}

// Canonical is JSON with every object's keys sorted (JS `<` order), so the same
// value written in any key order compares equal. Undefined gives "".
func Canonical(v any) string {
	b, err := Marshal(v)
	if err != nil {
		return ""
	}
	parsed, err := Parse(b)
	if err != nil {
		return ""
	}
	return Stringify(sortKeys(parsed))
}

func sortKeys(v any) any {
	switch t := v.(type) {
	case *Object:
		keys := append([]string(nil), t.keys...)
		sort.SliceStable(keys, func(i, j int) bool { return strx.Compare16(keys[i], keys[j]) < 0 })
		out := &Object{}
		for _, k := range keys {
			out.Set(k, sortKeys(t.vals[k]))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sortKeys(x)
		}
		return out
	}
	return v
}

// NumberString is String(n) for a JS number.
func NumberString(f float64) string {
	switch {
	case f == 0:
		return "0"
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	b, err := json.Marshal(f)
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return string(b)
}

// Truthy is JS truthiness of a parsed JSON value.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	case json.Number:
		f, _ := t.Float64()
		return f != 0
	}
	return true
}

// Num is a float64 that serializes like JSON.stringify: NaN and ±Infinity
// become null (encoding/json refuses them).
type Num float64

func (n Num) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return json.Marshal(f)
}

// ObjectOf builds an object from key, value pairs, in that order.
func ObjectOf(kv ...any) *Object {
	o := NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// JSString is String(v) for a parsed JSON value.
func JSString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return NumberString(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			if x != nil {
				parts[i] = JSString(x)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}
