package whatwg

import (
	"strings"
	"unicode/utf8"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// ParseQuery is the application/x-www-form-urlencoded parser behind
// URLSearchParams: "&"-separated pairs, "+" is a space, percent escapes are
// bytes (a bad escape stays as written), and the bytes are UTF-8 with U+FFFD
// for invalid sequences. raw has no leading "?".
func ParseQuery(raw string) [][2]string {
	var out [][2]string
	for _, seq := range strings.Split(raw, "&") {
		if seq == "" {
			continue
		}
		name, value := seq, ""
		if i := strings.IndexByte(seq, '='); i >= 0 {
			name, value = seq[:i], seq[i+1:]
		}
		out = append(out, [2]string{formDecode(name), formDecode(value)})
	}
	return out
}

// QueryMap is Object.fromEntries(searchParams): a later duplicate wins.
func QueryMap(raw string) map[string]string {
	m := map[string]string{}
	for _, p := range ParseQuery(raw) {
		m[p[0]] = p[1]
	}
	return m
}

func formDecode(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '+':
			b = append(b, ' ')
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
		default:
			b = append(b, c)
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	// U+FFFD per maximal bad subpart, as UTF-8 decode does.
	return strx.DecodeUTF8(b)
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
