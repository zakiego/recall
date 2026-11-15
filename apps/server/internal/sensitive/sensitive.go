// Package sensitive is sensitive.ts: it detects sensitive data so it never
// becomes a memory fact, and masks secrets in raw text before it is chunked
// and indexed. Every rule is conservative on purpose: ordinary emails, contact
// phone numbers, dates, money amounts and company registration numbers must
// pass untouched. The patterns are the TS ones, run by internal/jsre, and
// every position is a UTF-16 index like in the TS code.
package sensitive

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsre"
)

// Kind is a SensitiveKind.
type Kind string

const (
	Card        Kind = "card"
	NIK         Kind = "nik"
	Passport    Kind = "passport"
	IBAN        Kind = "iban"
	Secret      Kind = "secret"
	OTP         Kind = "otp"
	Credentials Kind = "credentials"
)

// AllKinds in DETECTORS order (Object.keys order), which also orders ties.
var AllKinds = []Kind{Card, NIK, Passport, IBAN, Secret, OTP, Credentials}

// Hit is a SensitiveHit: [Start, End) in UTF-16 units.
type Hit struct {
	Kind  Kind `json:"kind"`
	Start int  `json:"start"`
	End   int  `json:"end"`
}

// What maskSecrets puts in place of a secret. Facts the model writes around a
// placeholder ("API key is [redacted:secret]") are dropped too.
var placeholderRE = regexp.MustCompile(`\[redacted:[a-z]+\]`)

func placeholder(k Kind) string { return "[redacted:" + string(k) + "]" }

var nonDigit = regexp.MustCompile(`\D`)

func digitsOf(s string) string { return nonDigit.ReplaceAllString(s, "") }

// --- cards --------------------------------------------------------------------

// 14 to 19 digits, optionally grouped by single spaces or dashes. A leading +
// or digit means it's part of a longer number (a phone number, an id).
var cardRE = jsre.MustCompile(`(?<![\d+])\d(?:[ -]?\d){13,18}(?![\d])`, "g")

// Luhn is the Luhn checksum over a digit string.
func Luhn(digits string) bool {
	sum := 0
	for i := 0; i < len(digits); i++ {
		d := int(digits[len(digits)-1-i]) - 48
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

var (
	visaRE     = regexp.MustCompile(`^4`)
	masterRE   = regexp.MustCompile(`^(5[1-5]|222[1-9]|22[3-9]\d|2[3-6]\d\d|27[01]\d|2720)`)
	amexRE     = regexp.MustCompile(`^3[47]`)
	dinersRE   = regexp.MustCompile(`^3(0[0-5]|[689])`)
	discoverRE = regexp.MustCompile(`^(6011|64[4-9]|65|62|35(2[89]|[3-8]\d))`)
)

// Issuer prefix and length, so a random Luhn-valid number (1 in 10) isn't enough.
func cardShaped(d string) bool {
	n := len(d)
	switch {
	case visaRE.MatchString(d):
		return n == 16 || n == 19
	case masterRE.MatchString(d):
		return n == 16
	case amexRE.MatchString(d):
		return n == 15
	case dinersRE.MatchString(d):
		return n >= 14 && n <= 19
	case discoverRE.MatchString(d):
		return n >= 16 && n <= 19
	}
	return false
}

var groupSep = regexp.MustCompile(`[ -]`)

// Grouped numbers must be grouped like a card (4-4-4-4, 4-6-5, ...), not a list of years.
func cardGrouping(raw string) bool {
	groups := groupSep.Split(raw, -1)
	if len(groups) == 1 {
		return true
	}
	for i, g := range groups[:len(groups)-1] {
		if !(len(g) == 4 || (i == 1 && len(g) == 6)) {
			return false
		}
	}
	return true
}

func cards(u jsre.Units) []Hit {
	var hits []Hit
	for _, m := range cardRE.FindAll(u) {
		d := digitsOf(m.Text())
		if cardShaped(d) && cardGrouping(m.Text()) && Luhn(d) {
			hits = append(hits, Hit{Card, m.Index, m.End})
		}
	}
	return hits
}

// --- NIK (Indonesian national id) ---------------------------------------------

// 16 digits: province, regency, district, birth date DDMMYY (women add 40 to DD), serial.
var nikRE = jsre.MustCompile(`(?<![\d+]|\d[ .-])\d(?:[ .-]?\d){15}(?![ .-]?\d)`, "g")

var provinces = map[int]bool{
	11: true, 12: true, 13: true, 14: true, 15: true, 16: true, 17: true, 18: true, 19: true, 21: true, 31: true, 32: true,
	33: true, 34: true, 35: true, 36: true, 51: true, 52: true, 53: true, 61: true, 62: true, 63: true, 64: true, 65: true,
	71: true, 72: true, 73: true, 74: true, 75: true, 76: true, 81: true, 82: true, 91: true, 92: true, 93: true, 94: true,
	95: true, 96: true,
}

// NikShaped checks a 16-digit string's province, birth date and serial.
func NikShaped(d string) bool {
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	if len(d) < 16 || !provinces[num(d[0:2])] {
		return false
	}
	day, month := num(d[6:8]), num(d[8:10])
	dayOK := (day >= 1 && day <= 31) || (day >= 41 && day <= 71)
	return dayOK && month >= 1 && month <= 12 && d[12:] != "0000"
}

func niks(u jsre.Units) []Hit {
	var hits []Hit
	for _, m := range nikRE.FindAll(u) {
		if NikShaped(digitsOf(m.Text())) {
			hits = append(hits, Hit{NIK, m.Index, m.End})
		}
	}
	return hits
}

// --- passport -----------------------------------------------------------------

var passportRE = jsre.MustCompile(`\b(?:[Pp]assport|PASSPORT|[Pp]aspor|PASPOR)\b[^\n]{0,40}?(?<![A-Za-z0-9])([A-Z]{1,2} ?\d{6,8}|\d{8,9})(?![A-Za-z0-9])`, "g")

func passports(u jsre.Units) []Hit {
	var hits []Hit
	for _, m := range passportRE.FindAll(u) {
		g := m.Groups[0]
		hits = append(hits, Hit{Passport, m.End - (g[1] - g[0]), m.End})
	}
	return hits
}

// --- IBAN ---------------------------------------------------------------------

var ibanRE = jsre.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?\b`, "g")

// IbanValid is the mod-97 check.
func IbanValid(raw string) bool {
	s := strings.ReplaceAll(raw, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	moved := s[4:] + s[:4]
	rem := 0
	for _, ch := range moved {
		v := string(ch)
		if ch >= 'A' {
			v = strconv.Itoa(int(ch) - 55)
		}
		for _, c := range v {
			rem = (rem*10 + (int(c) - 48)) % 97
		}
	}
	return rem == 1
}

var trailingGroup = regexp.MustCompile(` ?[A-Z0-9]+$`)

func ibans(u jsre.Units) []Hit {
	var hits []Hit
	for _, m := range ibanRE.FindAll(u) {
		// A trailing uppercase word ("... 00 BANK") can be swallowed; drop groups until it checks.
		s := m.Text()
		for len(strings.ReplaceAll(s, " ", "")) >= 15 {
			if IbanValid(s) {
				hits = append(hits, Hit{IBAN, m.Index, m.Index + len(s)})
				break
			}
			s = trailingGroup.ReplaceAllString(s, "")
		}
	}
	return hits
}

// --- API keys, tokens and other secrets ---------------------------------------

var keyREs = []*jsre.Regexp{
	jsre.MustCompile(`\brcl_[A-Za-z0-9]{16,}`, "g"),
	jsre.MustCompile(`\bzkg_[A-Za-z0-9_-]{16,}`, "g"),
	jsre.MustCompile(`\bsk-(?=[A-Za-z0-9_-]*\d)[A-Za-z0-9_-]{20,}`, "g"),
	jsre.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`, "g"),
	jsre.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`, "g"),
	jsre.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}`, "g"),
	jsre.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`, "g"),
	jsre.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`, "g"),
	jsre.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`, "g"),
	jsre.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`, "g"),
	jsre.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`, "g"),
}

// A long value right after a word like "token" or "api key". Only the value is masked.
var labeledSecretRE = jsre.MustCompile(`\b(?:api[_ -]?key|secret|token|bearer|access[_ -]?key|client[_ -]?secret|auth[_ -]?key)\b["']?\s*(?:[:=]|is|adalah)?\s*["']?([A-Za-z0-9+/_\-.=]{20,})`, "gi")

// A long, high-entropy base64-ish string standing on its own.
var looseSecretRE = jsre.MustCompile(`(?<![A-Za-z0-9+/_=.:@%&?#~-])[A-Za-z0-9+_-]{32,}={0,2}(?![A-Za-z0-9+/_=.-])`, "g")

// Entropy is the Shannon entropy per character (code points, length in UTF-16 units).
func Entropy(s string) float64 {
	counts := map[rune]int{}
	var order []rune
	for _, ch := range s {
		if counts[ch] == 0 {
			order = append(order, ch)
		}
		counts[ch]++
	}
	n := float64(len(jsre.ToUnits(s)))
	h := 0.0
	for _, ch := range order {
		p := float64(counts[ch]) / n
		h -= p * math.Log2(p)
	}
	return h
}

var (
	lowerRE = regexp.MustCompile(`[a-z]`)
	upperRE = regexp.MustCompile(`[A-Z]`)
	digitRE = regexp.MustCompile(`\d`)
	alphaRE = regexp.MustCompile(`[A-Za-z]`)
)

func mixed(s string) bool {
	return lowerRE.MatchString(s) && upperRE.MatchString(s) && len(digitRE.FindAllString(s, -1)) >= 2
}

func secrets(u jsre.Units) []Hit {
	var hits []Hit
	for _, re := range keyREs {
		for _, m := range re.FindAll(u) {
			hits = append(hits, Hit{Secret, m.Index, m.End})
		}
	}
	for _, m := range labeledSecretRE.FindAll(u) {
		v, _ := m.Group(1)
		if digitRE.MatchString(v) && alphaRE.MatchString(v) {
			g := m.Groups[0]
			start := m.End - (g[1] - g[0])
			hits = append(hits, Hit{Secret, start, start + (g[1] - g[0])})
		}
	}
	for _, m := range looseSecretRE.FindAll(u) {
		t := m.Text()
		if mixed(t) && Entropy(t) >= 4.2 {
			hits = append(hits, Hit{Secret, m.Index, m.End})
		}
	}
	return hits
}

// --- OTP codes ----------------------------------------------------------------

const otpWords = "OTP|one[- ]time (?:password|passcode|code|pin)|verification code|kode verifikasi|security code|kode keamanan|login code|kode login|(?:2FA|MFA) code|auth(?:entication)? code|kode OTP"

var (
	otpAfterWord  = jsre.MustCompile(`\b(?:`+otpWords+`)\b([^\d\n]{0,20})(\d{4,8})\b`, "gi")
	otpBeforeWord = jsre.MustCompile(`\b(\d{4,8})\s+(?:is your|adalah)\b[^\n]{0,30}\b(?:code|kode|OTP|password)\b`, "gi")
	yearRE        = regexp.MustCompile(`^(?:19|20)\d\d$`)
	codeContext   = jsre.MustCompile(`(?::|\bis|\badalah|\bkode)\s*$`, "i")
)

func otps(u jsre.Units) []Hit {
	var hits []Hit
	for _, m := range otpAfterWord.FindAll(u) {
		before, _ := m.Group(1)
		code, _ := m.Group(2)
		// "Logs in with OTP via SMS since 2021" is a year, not a code.
		if yearRE.MatchString(code) && !codeContext.Test(before) {
			continue
		}
		hits = append(hits, Hit{OTP, m.Index, m.End})
	}
	for _, m := range otpBeforeWord.FindAll(u) {
		hits = append(hits, Hit{OTP, m.Index, m.End})
	}
	return hits
}

// --- email + password -----------------------------------------------------------

var (
	emailRE    = jsre.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, "")
	passwordRE = jsre.MustCompile(`\b(?:password|passwd|pwd|passcode|kata sandi|sandi)\b\s*(?:([:=])|is|adalah)\s*["']?(\S{4,})`, "gi")
	pairRE     = jsre.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::(?=\S)|\s*\|\s*|\s+\/\s+)(\S{4,})`, "g")
	digitSym   = jsre.MustCompile(`[\d!#$%^&*()_+=\[\]{};'"\\|<>?~`+"`"+`]`, "")
	phoneLike  = jsre.MustCompile(`^[\d\s()+.-]+$`, "")
	httpRE     = jsre.MustCompile(`^https?:`, "i")
)

func credentials(u jsre.Units) []Hit {
	var hits []Hit
	if emailRE.Find(u) != nil {
		for _, m := range passwordRE.FindAll(u) {
			// "password: x" counts as is; "password is x" needs x to look like a password.
			_, sep := m.Group(1)
			v, _ := m.Group(2)
			if sep || digitSym.Test(v) {
				hits = append(hits, Hit{Credentials, m.Index, m.End})
			}
		}
	}
	for _, m := range pairRE.FindAll(u) {
		v, _ := m.Group(1)
		if !strings.Contains(v, "@") && !httpRE.Test(v) && !phoneLike.Test(v) && digitSym.Test(v) {
			hits = append(hits, Hit{Credentials, m.Index, m.End})
		}
	}
	return hits
}

// --- public API -----------------------------------------------------------------

var detectors = map[Kind]func(jsre.Units) []Hit{
	Card: cards, NIK: niks, Passport: passports, IBAN: ibans, Secret: secrets, OTP: otps, Credentials: credentials,
}

// Find is findSensitive: the hits of the given kinds (all when nil), sorted by
// start, longer first, otherwise in detector order.
func Find(text string, kinds []Kind) []Hit {
	if kinds == nil {
		kinds = AllKinds
	}
	u := jsre.ToUnits(text)
	hits := []Hit{}
	for _, k := range kinds {
		hits = append(hits, detectors[k](u)...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Start != hits[j].Start {
			return hits[i].Start < hits[j].Start
		}
		return hits[i].End > hits[j].End
	})
	return hits
}

// Kinds is sensitiveKinds: the kinds found in a fact, or [] when it's safe to
// store. A fact that only exists to point at a masked secret counts as secret.
func Kinds(text string) []Kind {
	seen := map[Kind]bool{}
	out := []Kind{}
	for _, h := range Find(text, nil) {
		if !seen[h.Kind] {
			seen[h.Kind] = true
			out = append(out, h.Kind)
		}
	}
	if placeholderRE.MatchString(text) && !seen[Secret] {
		out = append(out, Secret)
	}
	return out
}

// maskedKinds: ids like NIK or a passport number stay in the source text; only
// facts are guarded against them.
var maskedKinds = []Kind{Secret, Card}

// Mask is maskSecrets: secrets and card numbers replaced by placeholders, and
// how many were replaced.
func Mask(text string) (string, int) {
	hits := Find(text, maskedKinds)
	if len(hits) == 0 {
		return text, 0
	}
	u := jsre.ToUnits(text)
	var b strings.Builder
	pos, masked := 0, 0
	for _, h := range hits {
		if h.Start < pos {
			// Overlaps a hit already masked: swallow whatever sticks out past it.
			pos = max(pos, h.End)
			continue
		}
		b.WriteString(u.Slice(pos, h.Start))
		b.WriteString(placeholder(h.Kind))
		pos = h.End
		masked++
	}
	b.WriteString(u.Slice(pos, len(u)))
	return b.String(), masked
}
