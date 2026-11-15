package connectors

// testdata/charsets.json from testdata/gen/charset-goldens.ts: Node's
// TextDecoder on every WHATWG label, valid and invalid bytes.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/zakiego/recall/apps/server/internal/testutil"
)

// cjkDiffers lists the cases where Go's decoders (golang.org/x/text, the
// WHATWG tables) and Node's (ICU) disagree: the CJK multi-byte encodings on
// invalid bytes, extension ranges (HKSCS, UHC, user-defined areas) and how
// many bytes an error consumes. Known gap in docs/GO-PORT.md. The test
// fails if this set changes in either direction.
var cjkDiffers = map[string][]string{
	"gbk":            {"allBytes", "utf16leBom", "utf16beBom", "big5", "gb18030"},
	"gb2312":         {"allBytes", "utf16leBom", "utf16beBom", "big5", "gb18030"},
	"gb18030":        {"allBytes", "utf16leBom", "utf16beBom", "big5"},
	"big5":           {"allBytes", "random", "utf8", "utf16leBom", "utf16beBom", "sjis", "eucjp", "big5", "gb18030", "euckr"},
	"euc-jp":         {"allBytes", "random", "utf8", "utf16beBom", "truncatedLead", "leadSpace", "sjis", "big5", "gb18030", "euckr"},
	"iso-2022-jp":    {"allBytes", "iso2022jp"},
	"shift_jis":      {"allBytes", "random", "utf8", "gb18030"},
	"sjis":           {"allBytes", "random", "utf8", "gb18030"},
	"euc-kr":         {"allBytes", "random", "utf8", "utf8Bom", "truncatedLead", "leadSpace", "sjis", "eucjp", "big5", "gb18030", "euckr"},
	"ks_c_5601-1987": {"allBytes", "random", "utf8", "utf8Bom", "truncatedLead", "leadSpace", "sjis", "eucjp", "big5", "gb18030", "euckr"},
}

func TestCharsetsMatchTextDecoder(t *testing.T) {
	var g struct {
		Inputs map[string][]int `json:"inputs"`
		Cases  []struct {
			Label string `json:"label"`
			Input string `json:"input"`
			Text  string `json:"text"`
		} `json:"cases"`
	}
	testutil.LoadJSON(t, "charsets.json", &g)
	differs := map[string][]string{}
	for _, c := range g.Cases {
		b := make([]byte, len(g.Inputs[c.Input]))
		for i, v := range g.Inputs[c.Input] {
			b[i] = byte(v)
		}
		var units []string
		for _, u := range utf16.Encode([]rune(decodeCharset(c.Label, b))) {
			units = append(units, fmt.Sprintf("%x", u))
		}
		got := strings.Join(units, " ")
		if got == c.Text {
			continue
		}
		differs[c.Label] = append(differs[c.Label], c.Input)
		known := false
		for _, in := range cjkDiffers[c.Label] {
			known = known || in == c.Input
		}
		if !known {
			t.Errorf("%s %s:\n got %s\nwant %s", c.Label, c.Input, got, c.Text)
		}
	}
	if !reflect.DeepEqual(differs, cjkDiffers) {
		t.Errorf("the known CJK differences changed:\n got %v\nwant %v", differs, cjkDiffers)
	}
}
