// Package whatwg parses URLs the way the WHATWG URL parser (new URL() in
// Node) does for http and https URLs.
package whatwg

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// URL is a parsed http(s) URL with the host normalized like the WHATWG URL
// parser (new URL() in the TS build): IDNA to ASCII, lowercase, numeric IPv4
// forms made dotted, IPv6 compressed, default port dropped, empty path "/".
type URL struct {
	Scheme   string
	Username string
	Password string
	// Serialized host: a name, dotted IPv4, or [IPv6].
	Host string
	// "" for the scheme's default port.
	Port     string
	Path     string
	RawQuery string
	Fragment string
	// Set when the query had a "?" even if empty.
	ForceQuery bool
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func defaultPort(scheme string) string {
	switch scheme {
	case "http", "ws":
		return "80"
	case "https", "wss":
		return "443"
	}
	return ""
}

func special(scheme string) bool {
	switch scheme {
	case "http", "https", "ws", "wss", "ftp", "file":
		return true
	}
	return false
}

// cleanInput strips leading and trailing C0 controls and spaces, and removes
// tabs and newlines anywhere, like the WHATWG parser.
func cleanInput(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	return strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
}

var idnaProfile = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(true),
	idna.StrictDomainName(false),
	idna.VerifyDNSLength(false),
	idna.BidiRule(),
)

// ParseURL parses an absolute URL.
func ParseURL(raw string) (*URL, error) {
	return parse(cleanInput(raw), nil)
}

// Resolve is new URL(ref, u).
func (u *URL) Resolve(ref string) (*URL, error) {
	return parse(cleanInput(ref), u)
}

// ErrInvalid is returned for anything new URL() would throw on.
var ErrInvalid = errors.New("Not a valid URL")

func invalid() error { return ErrInvalid }

func parse(s string, base *URL) (*URL, error) {
	scheme := ""
	if m := schemeRe.FindString(s); m != "" {
		scheme = strings.ToLower(m[:len(m)-1])
	}
	if scheme == "" {
		if base == nil {
			return nil, invalid()
		}
		return resolveRelative(s, base)
	}
	if !special(scheme) {
		// Not http(s): only the scheme matters to the caller.
		if _, err := url.Parse(s); err != nil {
			return nil, invalid()
		}
		return &URL{Scheme: scheme}, nil
	}
	rest := strings.ReplaceAll(s[len(scheme)+1:], "\\", "/")
	if base != nil && base.Scheme == scheme && !strings.HasPrefix(rest, "/") {
		// "http:path" relative to an http base.
		return resolveRelative(rest, base)
	}
	rest = strings.TrimLeft(rest, "/")
	return parseAuthority(scheme, "//"+rest)
}

func parseAuthority(scheme, rest string) (*URL, error) {
	pu, err := url.Parse(scheme + ":" + rest)
	if err != nil {
		return nil, invalid()
	}
	out := &URL{Scheme: scheme, RawQuery: pu.RawQuery, Fragment: pu.EscapedFragment(), ForceQuery: pu.ForceQuery}
	if pu.User != nil {
		out.Username = pu.User.Username()
		out.Password, _ = pu.User.Password()
	}
	host, err := normalizeHost(pu.Hostname(), strings.HasPrefix(pu.Host, "["))
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, invalid()
	}
	out.Host = host
	if p := pu.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n > 65535 {
			return nil, invalid()
		}
		if strconv.Itoa(n) != defaultPort(scheme) {
			out.Port = strconv.Itoa(n)
		}
	}
	out.Path = pu.EscapedPath()
	if out.Path == "" {
		out.Path = "/"
	}
	out.Path = removeDotSegments(out.Path)
	return out, nil
}

func resolveRelative(ref string, base *URL) (*URL, error) {
	if strings.HasPrefix(ref, "//") || (special(base.Scheme) && strings.HasPrefix(strings.ReplaceAll(ref, "\\", "/"), "//")) {
		return parseAuthority(base.Scheme, strings.ReplaceAll(ref, "\\", "/"))
	}
	bu, err := url.Parse(base.Href())
	if err != nil {
		return nil, invalid()
	}
	ru, err := url.Parse(strings.ReplaceAll(ref, "\\", "/"))
	if err != nil {
		return nil, invalid()
	}
	return ParseURL(bu.ResolveReference(ru).String())
}

func removeDotSegments(p string) string {
	if !strings.Contains(p, ".") {
		return p
	}
	ref := &url.URL{Path: p}
	resolved := (&url.URL{Scheme: "http", Host: "x", Path: "/"}).ResolveReference(ref)
	if resolved.Path == "" {
		return "/"
	}
	return resolved.EscapedPath()
}

// normalizeHost applies the WHATWG host parser to a special URL's host.
func normalizeHost(h string, bracketed bool) (string, error) {
	if bracketed {
		if strings.Contains(h, "%") {
			return "", invalid()
		}
		a, err := netip.ParseAddr(h)
		if err != nil || !a.Is6() {
			return "", invalid()
		}
		return "[" + serializeV6(a) + "]", nil
	}
	if h == "" {
		return "", nil
	}
	decoded, err := url.PathUnescape(h)
	if err != nil {
		return "", invalid()
	}
	ascii, err := idnaProfile.ToASCII(decoded)
	if err != nil {
		// Fall back to a plain lowercase ASCII check: WHATWG accepts some names
		// the strict profile refuses (underscores, for one).
		lower := strings.ToLower(decoded)
		for _, r := range lower {
			if r > 0x7e || r <= 0x20 || strings.ContainsRune("#%/:<>?@[\\]^|", r) {
				return "", invalid()
			}
		}
		ascii = lower
	}
	if strings.ContainsAny(ascii, " #%/:<>?@[\\]^|") {
		return "", invalid()
	}
	if endsInNumber(ascii) {
		n, ok := parseIPv4(ascii)
		if !ok {
			return "", invalid()
		}
		return strconv.Itoa(int(n>>24)) + "." + strconv.Itoa(int(n>>16&255)) + "." + strconv.Itoa(int(n>>8&255)) + "." + strconv.Itoa(int(n&255)), nil
	}
	return ascii, nil
}

func endsInNumber(h string) bool {
	parts := strings.Split(h, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := 10
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
		radix = 16
	} else if len(s) >= 2 && s[0] == '0' {
		s = s[1:]
		radix = 8
	}
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(s, radix, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseIPv4(h string) (uint32, bool) {
	parts := strings.Split(h, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return 0, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := parseIPv4Number(p)
		if !ok {
			return 0, false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return 0, false
		}
	}
	last := nums[len(nums)-1]
	if last >= uint64(1)<<(8*(5-len(nums))) {
		return 0, false
	}
	ip := last
	for i, n := range nums[:len(nums)-1] {
		ip += n << (8 * (3 - i))
	}
	return uint32(ip), true
}

// serializeV6 is the WHATWG IPv6 serializer: lowercase hex, the first longest
// run of two or more zero groups compressed, no embedded dotted IPv4.
func serializeV6(a netip.Addr) string {
	b := a.As16()
	var g [8]uint16
	for i := range g {
		g[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	bestStart, bestLen := -1, 1
	for i := 0; i < 8; {
		if g[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && g[j] == 0 {
			j++
		}
		if j-i > bestLen {
			bestStart, bestLen = i, j-i
		}
		i = j
	}
	var sb strings.Builder
	for i := 0; i < 8; i++ {
		if i == bestStart {
			if i == 0 {
				sb.WriteString("::")
			} else {
				sb.WriteString(":")
			}
			i += bestLen - 1
			continue
		}
		sb.WriteString(strconv.FormatUint(uint64(g[i]), 16))
		if i < 7 {
			sb.WriteString(":")
		}
	}
	return sb.String()
}

// HostOf is the hostname without IPv6 brackets or a trailing dot, lowercased.
func (u *URL) HostOf() string {
	h := strings.ToLower(u.Host)
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return strings.TrimSuffix(h, ".")
}

// EffectivePort is the port, or the scheme's default.
func (u *URL) EffectivePort() int {
	p := u.Port
	if p == "" {
		p = defaultPort(u.Scheme)
	}
	n, _ := strconv.Atoi(p)
	return n
}

// HostPort is host[:port] as it appears in the URL.
func (u *URL) HostPort() string {
	if u.Port == "" {
		return u.Host
	}
	return u.Host + ":" + u.Port
}

// Origin is scheme://host[:port].
func (u *URL) Origin() string { return u.Scheme + "://" + u.HostPort() }

// RequestURI is the path plus the query.
func (u *URL) RequestURI() string {
	if u.RawQuery != "" || u.ForceQuery {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}

// Href is the serialized URL.
func (u *URL) Href() string {
	var sb strings.Builder
	sb.WriteString(u.Scheme)
	sb.WriteString("://")
	if u.Username != "" || u.Password != "" {
		sb.WriteString(url.UserPassword(u.Username, u.Password).String())
		if u.Password == "" {
			s := sb.String()
			sb.Reset()
			sb.WriteString(strings.TrimSuffix(s, ":"))
		}
		sb.WriteString("@")
	}
	sb.WriteString(u.HostPort())
	sb.WriteString(u.RequestURI())
	if u.Fragment != "" {
		sb.WriteString("#" + u.Fragment)
	}
	return sb.String()
}

func (u *URL) String() string { return u.Href() }
