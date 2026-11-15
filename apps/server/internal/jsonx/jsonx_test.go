package jsonx_test

import (
	"math"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

func TestStringifyMatchesJSON(t *testing.T) {
	v, err := jsonx.Parse([]byte(`{"b":1,"2":true,"a":"<&>\u2028","1":[1.5,null,1e21,0.000001,1e-7]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := jsonx.Stringify(v)
	want := "{\"1\":[1.5,null,1e+21,0.000001,1e-7],\"2\":true,\"b\":1,\"a\":\"<&>\u2028\"}"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if jsonx.Stringify(`a\u2028`) != `"a\\u2028"` {
		t.Fatal(jsonx.Stringify(`a\u2028`))
	}
}

func TestMapKeepsInsertionOrderAndAssign(t *testing.T) {
	m := jsonx.NewMap[any]()
	m.Set("z", 1.0)
	m.Set("a", 2.0)
	m.Set("z", 3.0)
	other := jsonx.NewMap[any]()
	other.Set("b", 4.0)
	other.Set("a", 5.0)
	m.Clone().Assign(other)
	if jsonx.Stringify(m) != `{"z":3,"a":2}` {
		t.Fatal(jsonx.Stringify(m))
	}
	if jsonx.Stringify(m.Clone().Assign(other)) != `{"z":3,"a":5,"b":4}` {
		t.Fatal("assign")
	}
	m.Delete("z")
	if jsonx.Stringify(m) != `{"a":2}` {
		t.Fatal(jsonx.Stringify(m))
	}
}

func TestCanonicalSortsKeys(t *testing.T) {
	a, _ := jsonx.Parse([]byte(`{"b":{"y":1,"x":[{"d":1,"c":2}]},"a":1}`))
	b, _ := jsonx.Parse([]byte(`{"a":1,"b":{"x":[{"c":2,"d":1}],"y":1}}`))
	if jsonx.Canonical(a) != jsonx.Canonical(b) || jsonx.Canonical(a) != `{"a":1,"b":{"x":[{"c":2,"d":1}],"y":1}}` {
		t.Fatal(jsonx.Canonical(a))
	}
}

func TestNumberString(t *testing.T) {
	for f, want := range map[float64]string{7: "7", 1.5: "1.5", 1e21: "1e+21", 1e-7: "1e-7", -0.5: "-0.5"} {
		if got := jsonx.NumberString(f); got != want {
			t.Errorf("%v: %s", f, got)
		}
	}
}

func TestStrx(t *testing.T) {
	if strx.Len16("a😀") != 3 || strx.Slice16("a😀b", 2) != "a\uFFFD" || strx.Slice16("a😀b", 3) != "a😀" || strx.Tail16("abc😀", 2) != "😀" {
		t.Fatal("utf-16")
	}
	if strx.Trim("\u00a0 x\ufeff") != "x" || strx.CollapseSpace("a \n\t b") != "a b" {
		t.Fatal("space")
	}
	if strx.TextDecode([]byte{0xEF, 0xBB, 0xBF, 'a', 0xE2, 0x82, 'b', 0xff}) != "a\uFFFDb\uFFFD" {
		t.Fatal(strx.TextDecode([]byte{0xEF, 0xBB, 0xBF, 'a', 0xE2, 0x82, 'b', 0xff}))
	}
	if strx.Compare16("\uff61", "😀") <= 0 {
		t.Fatal("UTF-16 order puts surrogates before U+FF61")
	}
}

func TestJSNumbers(t *testing.T) {
	v, err := jsonx.Parse([]byte(`{"a":-0,"b":[1e400,-1e400,1e-400],"c":{"d":-0.0}}`))
	if err != nil {
		t.Fatal(err)
	}
	// JSON.stringify(JSON.parse(...)) in Node.
	if got := jsonx.Stringify(v); got != `{"a":0,"b":[null,null,0],"c":{"d":0}}` {
		t.Fatalf("got %s", got)
	}
	for f, want := range map[float64]string{0: "0", math.Inf(1): "Infinity", math.Inf(-1): "-Infinity", math.NaN(): "NaN", 1e21: "1e+21", -1.5: "-1.5"} {
		if got := jsonx.NumberString(f); got != want {
			t.Errorf("NumberString(%v) = %s, want %s", f, got, want)
		}
	}
	if got := jsonx.NumberString(math.Copysign(0, -1)); got != "0" {
		t.Errorf("NumberString(-0) = %s", got)
	}
}
