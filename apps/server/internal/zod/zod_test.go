package zod_test

// The tool catalogs and input errors of every connector are checked against
// the TS build in internal/connectors/registry; these cover the pieces alone.

import (
	"regexp"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

func parse(t *testing.T, s zod.Schema, raw string) (string, error) {
	t.Helper()
	v, err := jsonx.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := zod.Parse(s, v)
	if err != nil {
		return "", err
	}
	return jsonx.Stringify(out), nil
}

var probe = zod.Object(
	zod.F("a", zod.String().Min(1).Max(5).Describe("A desc")),
	zod.F("b", zod.Int().Min(1).Max(50).Optional().Describe("B")),
	zod.F("c", zod.Default(zod.Enum("x", "y"), "x")),
	zod.F("d", zod.String().Regex(regexp.MustCompile(`^\d{4}$`), `^\d{4}$`, "four digits").Describe("D")),
	zod.F("e", zod.Nullable(zod.String()).Optional().Describe("E")),
	zod.F("f", zod.Record(zod.Unknown()).Describe("F")),
	zod.F("g", zod.Array(zod.String().Min(1)).Min(1).Max(3).Optional()),
	zod.F("h", zod.Union(zod.Literal(float64(0)), zod.Int().Min(10).Max(100))),
	zod.F("i", zod.Default(zod.Bool(), true).Describe("I")),
	zod.F("j", zod.Object(zod.F("k", zod.String().Trim().Min(1))).Partial().Optional()),
	zod.F("m", zod.String().URL(nil)),
)

// The output of zod 4.6.7's toJSONSchema through the AI SDK for the same schema.
const probeSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"a":{"type":"string","minLength":1,"maxLength":5,"description":"A desc"},"b":{"description":"B","type":"integer","minimum":1,"maximum":50},"c":{"default":"x","type":"string","enum":["x","y"]},"d":{"type":"string","pattern":"^\\d{4}$","description":"D"},"e":{"description":"E","type":["string","null"]},"f":{"type":"object","propertyNames":{"type":"string"},"additionalProperties":{},"description":"F"},"g":{"minItems":1,"maxItems":3,"type":"array","items":{"type":"string","minLength":1}},"h":{"anyOf":[{"type":"number","const":0},{"type":"integer","minimum":10,"maximum":100}]},"i":{"default":true,"description":"I","type":"boolean"},"j":{"type":"object","properties":{"k":{"type":"string","minLength":1}},"additionalProperties":false},"m":{"type":"string","format":"uri"}},"required":["a","d","f","h","m"],"additionalProperties":false}`

func TestJSONSchemaIsWhatTheAISDKSends(t *testing.T) {
	if got := string(zod.JSONSchemaBytes(probe)); got != probeSchema {
		t.Fatalf("\n got  %s\n want %s", got, probeSchema)
	}
}

func TestParseFillsDefaultsAndDropsUnknownKeysInShapeOrder(t *testing.T) {
	got, err := parse(t, probe, `{"m":"https://x","h":0,"f":{"z":1},"d":"2026","a":"ab","zz":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":"ab","c":"x","d":"2026","f":{"z":1},"h":0,"i":true,"m":"https://x"}` {
		t.Fatal(got)
	}
}

// The ZodError message for the same bad input, as zod 4.6.7 prints it.
const probeIssues = `[
  {
    "origin": "string",
    "code": "too_small",
    "minimum": 1,
    "inclusive": true,
    "path": [
      "a"
    ],
    "message": "Too small: expected string to have >=1 characters"
  },
  {
    "expected": "int",
    "format": "safeint",
    "code": "invalid_type",
    "path": [
      "b"
    ],
    "message": "Invalid input: expected int, received number"
  },
  {
    "code": "invalid_value",
    "values": [
      "x",
      "y"
    ],
    "path": [
      "c"
    ],
    "message": "Invalid option: expected one of \"x\"|\"y\""
  },
  {
    "origin": "string",
    "code": "invalid_format",
    "format": "regex",
    "pattern": "/^\\d{4}$/",
    "path": [
      "d"
    ],
    "message": "four digits"
  },
  {
    "expected": "record",
    "code": "invalid_type",
    "path": [
      "f"
    ],
    "message": "Invalid input: expected record, received number"
  },
  {
    "origin": "number",
    "code": "too_small",
    "minimum": 10,
    "inclusive": true,
    "path": [
      "h"
    ],
    "message": "Too small: expected number to be >=10"
  },
  {
    "expected": "string",
    "code": "invalid_type",
    "path": [
      "m"
    ],
    "message": "Invalid input: expected string, received undefined"
  }
]`

func TestIssuesPrintLikeZodError(t *testing.T) {
	_, err := parse(t, probe, `{"a":"","b":1.5,"c":"z","d":"x","f":1,"h":5}`)
	if err == nil {
		t.Fatal("parsed")
	}
	if err.Error() != probeIssues {
		t.Fatalf("\n%s", err.Error())
	}
}

func TestTypeValidationErrorIsTheAISDKsMessage(t *testing.T) {
	v, _ := jsonx.Parse([]byte(`{"a":1}`))
	_, err := zod.Parse(zod.Object(zod.F("a", zod.String())), v)
	e := &zod.TypeValidationError{Value: v, Cause: err}
	want := "Type validation failed: Value: {\"a\":1}.\nError message: [\n  {\n    \"expected\": \"string\",\n    \"code\": \"invalid_type\",\n    \"path\": [\n      \"a\"\n    ],\n    \"message\": \"Invalid input: expected string, received number\"\n  }\n]"
	if e.Error() != want || e.JSName() != "AI_TypeValidationError" {
		t.Fatalf("%q", e.Error())
	}
}
