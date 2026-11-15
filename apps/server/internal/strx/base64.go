package strx

import "unicode/utf16"

// DecodeBase64Node is Buffer.from(s, "base64") (and "base64url", which Node
// decodes the same way): both alphabets are accepted, characters outside them
// are skipped, decoding stops at the first "=", and a dangling sixth bit
// group is dropped. It never fails.
//
// Node decodes the string's UTF-16 code units, each cut to its low byte, so a
// character above U+00FF can stand for a base64 digit ("中" is U+4E2D, read
// as "-").
func DecodeBase64Node(s string) []byte {
	units := utf16.Encode([]rune(s))
	vals := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		c := byte(units[i])
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			i = len(units)
			continue
		default:
			continue
		}
		vals = append(vals, v)
	}
	out := make([]byte, 0, len(vals)*3/4)
	i := 0
	for ; i+4 <= len(vals); i += 4 {
		n := uint32(vals[i])<<18 | uint32(vals[i+1])<<12 | uint32(vals[i+2])<<6 | uint32(vals[i+3])
		out = append(out, byte(n>>16), byte(n>>8), byte(n))
	}
	switch len(vals) - i {
	case 2:
		n := uint32(vals[i])<<18 | uint32(vals[i+1])<<12
		out = append(out, byte(n>>16))
	case 3:
		n := uint32(vals[i])<<18 | uint32(vals[i+1])<<12 | uint32(vals[i+2])<<6
		out = append(out, byte(n>>16), byte(n>>8))
	}
	return out
}
