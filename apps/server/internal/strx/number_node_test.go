package strx

import (
	"math"
	"testing"
)

// Values checked with Number() in Node 24.
func TestNumberEdgeValues(t *testing.T) {
	cases := map[string]float64{
		"1e999": math.Inf(1), "-1e999": math.Inf(-1), "1e-999": 0, "0xFFFFFFFFFFFFFFFFFFFF": 1.2089258196146292e+24,
		"0b11": 3, "0o17": 15, " 0x10 ": 16, "1e3": 1000, "-5": -5, "": 0, "Infinity": math.Inf(1),
	}
	for in, want := range cases {
		if got := Number(in); got != want {
			t.Errorf("Number(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"abc", "0x", "0x-1", "0x_1", "1_000", "--1"} {
		if got := Number(in); !math.IsNaN(got) {
			t.Errorf("Number(%q) = %v, want NaN", in, got)
		}
	}
}

// Buffer.from(s, "base64"), checked in Node 24.
func TestDecodeBase64NodeLowByte(t *testing.T) {
	cases := map[string]string{"QUJD中REVG": "414243f91115", "YWJj": "616263", "YW=Jj": "61", "YWJjZ": "616263", "-_-_": "fbffbf", "Y": ""}
	for in, want := range cases {
		got := ""
		for _, b := range DecodeBase64Node(in) {
			got += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&15])
		}
		if got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}
