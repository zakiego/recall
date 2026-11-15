package strx

import (
	_ "embed"
	"encoding/json"
	"sort"
	"sync"
)

// \p{L}, \p{N}, \p{M} and \p{Lu} of the Node that runs the TS build (props.json, written by
// testdata/gen/unicode-nfkc.mjs). Go's unicode tables are an older version.
//
//go:embed props.json
var propsJSON []byte

var (
	propsOnce      sync.Once
	letterRanges   [][2]rune
	numberRanges   [][2]rune
	markRanges     [][2]rune
	upperRanges    [][2]rune
	propsUnicodeOf string
)

func props() {
	propsOnce.Do(func() {
		var f struct {
			Unicode string     `json:"unicode"`
			L       [][2]int32 `json:"L"`
			N       [][2]int32 `json:"N"`
			M       [][2]int32 `json:"M"`
			Lu      [][2]int32 `json:"Lu"`
		}
		if err := json.Unmarshal(propsJSON, &f); err != nil {
			panic("strx: props.json: " + err.Error())
		}
		for _, r := range f.L {
			letterRanges = append(letterRanges, [2]rune{r[0], r[1]})
		}
		for _, r := range f.N {
			numberRanges = append(numberRanges, [2]rune{r[0], r[1]})
		}
		for _, r := range f.M {
			markRanges = append(markRanges, [2]rune{r[0], r[1]})
		}
		for _, r := range f.Lu {
			upperRanges = append(upperRanges, [2]rune{r[0], r[1]})
		}
		propsUnicodeOf = f.Unicode
	})
}

func inRanges(rs [][2]rune, r rune) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i][1] >= r })
	return i < len(rs) && rs[i][0] <= r
}

// IsLetter is /\p{L}/u in Node.
func IsLetter(r rune) bool {
	props()
	return inRanges(letterRanges, r)
}

// IsNumber is /\p{N}/u in Node.
func IsNumber(r rune) bool {
	props()
	return inRanges(numberRanges, r)
}

// IsMark is /\p{M}/u in Node.
func IsMark(r rune) bool {
	props()
	return inRanges(markRanges, r)
}

// IsUpperLetter is /\p{Lu}/u in Node.
func IsUpperLetter(r rune) bool {
	props()
	return inRanges(upperRanges, r)
}
