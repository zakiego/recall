package strx

// asciiCollation is the order ICU's root collation (String.prototype
// .localeCompare in Node) gives printable ASCII: whitespace, punctuation,
// symbols, digits, then letters with each lowercase just before its
// uppercase. Read from Node by testdata/gen/collation-goldens.ts.
const asciiCollation = "\t\n\v\f\r _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789"

var asciiPrimary = func() [128]int {
	var w [128]int
	for i, c := range asciiCollation {
		w[c] = i + 1
	}
	for c := 'a'; c <= 'z'; c++ {
		w[c] = len(asciiCollation) + 1 + int(c-'a')
		w[c-'a'+'A'] = w[c]
	}
	return w
}()

// LocaleCompare is a.localeCompare(b) for ASCII strings: -1, 0 or 1. The
// control characters other than \t through \r are ignored, as ICU ignores
// them; letters compare without case first, and lowercase sorts before
// uppercase only when nothing else differs. A string with non-ASCII falls
// back to UTF-16 code unit order (Known gaps).
func LocaleCompare(a, b string) int {
	for i := 0; i < len(a); i++ {
		if a[i] >= 0x80 {
			return Compare16(a, b)
		}
	}
	for i := 0; i < len(b); i++ {
		if b[i] >= 0x80 {
			return Compare16(a, b)
		}
	}
	pa, ta := collationKeys(a)
	pb, tb := collationKeys(b)
	if c := compareInts(pa, pb); c != 0 {
		return c
	}
	return compareInts(ta, tb)
}

func collationKeys(s string) (primary, tertiary []int) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		w := asciiPrimary[c]
		if w == 0 {
			continue
		}
		primary = append(primary, w)
		t := 0
		if c >= 'A' && c <= 'Z' {
			t = 1
		}
		tertiary = append(tertiary, t)
	}
	return primary, tertiary
}

func compareInts(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
