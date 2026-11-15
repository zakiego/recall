package oauth

// JS URLSearchParams, the signed OAuth query (signed-query.ts) and the
// crypto primitives Better Auth's oauth-provider 1.7.18 uses.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Params is URLSearchParams: ordered name/value pairs.
type Params struct{ list [][2]string }

// ParseParams is new URLSearchParams(s) (a leading "?" is dropped).
func ParseParams(s string) *Params {
	s = strings.TrimPrefix(s, "?")
	return &Params{list: whatwg.ParseQuery(s)}
}

// Get is params.get: the first value.
func (p *Params) Get(k string) (string, bool) {
	for _, kv := range p.list {
		if kv[0] == k {
			return kv[1], true
		}
	}
	return "", false
}

// GetAll is params.getAll.
func (p *Params) GetAll(k string) []string {
	out := []string{}
	for _, kv := range p.list {
		if kv[0] == k {
			out = append(out, kv[1])
		}
	}
	return out
}

// Has is params.has.
func (p *Params) Has(k string) bool { _, ok := p.Get(k); return ok }

// Set is params.set: the first pair keeps its place, later ones go.
func (p *Params) Set(k, v string) {
	out := p.list[:0:0]
	done := false
	for _, kv := range p.list {
		if kv[0] != k {
			out = append(out, kv)
		} else if !done {
			out = append(out, [2]string{k, v})
			done = true
		}
	}
	if !done {
		out = append(out, [2]string{k, v})
	}
	p.list = out
}

// Append is params.append.
func (p *Params) Append(k, v string) { p.list = append(p.list, [2]string{k, v}) }

// Delete is params.delete.
func (p *Params) Delete(k string) {
	out := p.list[:0:0]
	for _, kv := range p.list {
		if kv[0] != k {
			out = append(out, kv)
		}
	}
	p.list = out
}

// Keys are the distinct names in first-appearance order.
func (p *Params) Keys() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, kv := range p.list {
		if !seen[kv[0]] {
			seen[kv[0]] = true
			out = append(out, kv[0])
		}
	}
	return out
}

// Entries are the pairs.
func (p *Params) Entries() [][2]string { return append([][2]string(nil), p.list...) }

// Clone copies.
func (p *Params) Clone() *Params { return &Params{list: p.Entries()} }

// String is params.toString(): application/x-www-form-urlencoded.
func (p *Params) String() string {
	parts := make([]string, len(p.list))
	for i, kv := range p.list {
		parts[i] = FormEncode(kv[0]) + "=" + FormEncode(kv[1])
	}
	return strings.Join(parts, "&")
}

// FormEncode is the urlencoded serializer for one string: *-._ and
// alphanumerics stay, space is "+", the rest %XX of the UTF-8 bytes (lone
// surrogates are U+FFFD already).
func FormEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// --- the signed query ---------------------------------------------------------

const (
	issuedAtParam   = "ba_iat"
	postLoginParam  = "ba_pl"
	paramNamesParam = "ba_param"
)

func less16(a, b string) bool { return strx.Compare16(a, b) < 0 }

// canonicalize is canonicalizeOAuthQueryParams: sorted by name, then value.
func canonicalize(p *Params) *Params {
	list := p.Entries()
	sort.SliceStable(list, func(i, j int) bool {
		if list[i][0] != list[j][0] {
			return less16(list[i][0], list[j][0])
		}
		return less16(list[i][1], list[j][1])
	})
	return &Params{list: list}
}

// setSignedNames is setSignedOAuthQueryParameterNames.
func setSignedNames(p *Params) {
	p.Delete(paramNamesParam)
	names := append(p.Keys(), paramNamesParam)
	seen := map[string]bool{}
	uniq := []string{}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	sort.SliceStable(uniq, func(i, j int) bool { return less16(uniq[i], uniq[j]) })
	for _, n := range uniq {
		p.Append(paramNamesParam, n)
	}
}

// makeSignature is Better Auth's makeSignature: HMAC-SHA256, standard
// base64 with padding.
func makeSignature(value, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(value))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// VerifyQuery is verifyOAuthQueryParams: one sig, which matches, and exp
// not passed. now is ms.
func VerifyQuery(query, secret string, now int64) bool {
	p := ParseParams(query)
	sigs := p.GetAll("sig")
	sig, _ := p.Get("sig")
	expText, _ := p.Get("exp")
	exp := strx.Number(expText)
	if _, ok := p.Get("exp"); !ok {
		exp = 0
	}
	p.Delete("sig")
	want := makeSignature(canonicalize(p).String(), secret)
	return len(sigs) == 1 && sig != "" && subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1 && exp*1000 >= float64(now)
}

// signedIssuedAt is getSignedQueryIssuedAt: ba_iat in ms, 0 without one.
func signedIssuedAt(query string) int64 {
	raw, _ := ParseParams(query).Get(issuedAtParam)
	if raw == "" {
		return 0
	}
	n := strx.Number(raw)
	if !(n > 0) || n > 1e300 {
		return 0
	}
	return int64(n)
}

// --- crypto -----------------------------------------------------------------

// HashToken is defaultHasher: SHA-256, base64url without padding.
func HashToken(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const (
	upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower  = "abcdefghijklmnopqrstuvwxyz"
	digits = "0123456789"
)

// randomString is generateRandomString(n, ...alphabets) without modulo bias.
func randomString(n int, alphabet string) string {
	out := make([]byte, 0, n)
	limit := 256 - 256%len(alphabet)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out)
}

// newID is Better Auth's generateId(32).
func newID() string { return randomString(32, upper+lower+digits) }

// encryptSecret is symmetricEncrypt with a string key: XChaCha20-Poly1305
// keyed by SHA-256(secret), hex of nonce || ciphertext || tag.
func encryptSecret(secret, data string) (string, error) {
	key := sha256.Sum256([]byte(secret))
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return hex.EncodeToString(aead.Seal(nonce, nonce, []byte(data), nil)), nil
}

// decryptSecret is symmetricDecrypt with a string key.
func decryptSecret(secret, data string) (string, error) {
	key := sha256.Sum256([]byte(secret))
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return "", err
	}
	raw, err := hex.DecodeString(data)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return strx.TextDecode(plain), nil
}

// codeChallenge is generateCodeChallenge (S256).
func codeChallenge(verifier string) string { return HashToken(verifier) }

// lenientBase64 is @better-auth/utils base64.decode: stops at "=", the URL
// alphabet when the text has "-" or "_", an error on any other character.
func lenientBase64(s string) ([]byte, error) {
	alpha := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	if strings.ContainsAny(s, "-_") {
		alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	}
	var out []byte
	buf, bits := 0, 0
	for _, r := range s {
		if r == '=' {
			break
		}
		v := strings.IndexRune(alpha, r)
		if v < 0 || r > 127 {
			return nil, errors.New("invalid base64 character")
		}
		buf = buf<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buf>>bits&255))
		}
		buf &= 1<<bits - 1
	}
	return out, nil
}

// formURLDecode is formUrlDecode.
func formURLDecode(v string) string {
	got, _ := ParseParams("v=" + v).Get("v")
	return got
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
