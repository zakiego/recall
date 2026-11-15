package oauth

// The zod schemas of the oauth-provider endpoints, as hand-written checks
// that produce zod 4's first issue, and createOAuthEndpoint's mapping of
// that issue to {error, error_description}.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

type issue struct {
	code     string
	path     []any
	message  string
	expected string
	values   []string
	// fatal: zod aborts the schema on it (type errors, fatal custom issues).
	fatal bool
}

type undefinedT struct{}

var undefined = undefinedT{}

func received(v any) string {
	switch v.(type) {
	case undefinedT:
		return "undefined"
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case *jsonx.Object:
		return "object"
	}
	return "number"
}

func typeIssue(expected string, v any) *issue {
	return &issue{code: "invalid_type", expected: expected, message: "Invalid input: expected " + expected + ", received " + received(v), fatal: true}
}

func at(is *issue, key any) *issue {
	if is != nil {
		is.path = append([]any{key}, is.path...)
	}
	return is
}

func dotPath(path []any) string {
	var b strings.Builder
	for _, p := range path {
		switch k := p.(type) {
		case int:
			b.WriteString("[" + strconv.Itoa(k) + "]")
		case string:
			if b.Len() > 0 {
				b.WriteString(".")
			}
			b.WriteString(k)
		}
	}
	return b.String()
}

// mapIssue is mapIssuesToOAuthError for the first issue.
func mapIssue(is *issue, mapping map[string]any, def string) (string, string) {
	field := dotPath(is.path)
	var m any
	if len(is.path) > 0 {
		if k, ok := is.path[0].(string); ok {
			m = mapping[k]
		}
	}
	code := def
	missing := is.code == "invalid_type" && strings.HasSuffix(is.message, "received undefined")
	switch mm := m.(type) {
	case string:
		code = mm
	case map[string]string:
		if missing && mm["missing"] != "" {
			code = mm["missing"]
		} else if !missing && is.code == "invalid_value" && mm["invalid"] != "" {
			code = mm["invalid"]
		}
	}
	desc := is.message
	if field != "" {
		switch {
		case is.code == "invalid_type" && strings.HasSuffix(is.message, "received undefined"):
			desc = field + " is required"
		case is.code == "invalid_type" && strings.HasSuffix(is.message, "received array"):
			desc = field + " must not appear more than once"
		case is.code == "invalid_type":
			e := is.expected
			if e == "" {
				e = "valid value"
			}
			desc = field + " must be a " + e
		case is.code == "invalid_value" && len(is.values) > 0:
			desc = field + " must be one of: " + strings.Join(is.values, ", ")
		default:
			desc = field + ": " + is.message
		}
	}
	return code, desc
}

func get(o *jsonx.Object, k string) any {
	if o == nil {
		return undefined
	}
	v, ok := o.Get(k)
	if !ok {
		return undefined
	}
	return v
}

// --- schemas -----------------------------------------------------------------------

func optString(v any) (string, *issue) {
	if v == undefined {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", typeIssue("string", v)
	}
	return s, nil
}

func tooSmallString(n int) *issue {
	return &issue{code: "too_small", message: "Too small: expected string to have >=" + strconv.Itoa(n) + " characters"}
}

func tooSmallArray(n int) *issue {
	return &issue{code: "too_small", message: "Too small: expected array to have >=" + strconv.Itoa(n) + " items"}
}

var (
	dangerousSchemes = map[string]bool{"javascript": true, "data": true, "vbscript": true}
	tabsNewlines     = regexp.MustCompile(`[\t\n\r]`)
)

// safeURL is SafeUrlSchema (z.url() then the scheme rules): the cleaned
// value or the first issue.
func safeURL(v any) (string, *issue) {
	s, ok := v.(string)
	if !ok {
		return "", typeIssue("string", v)
	}
	trimmed := strx.Trim(s)
	u, err := parseJSURL(trimmed)
	if err != nil {
		return "", &issue{code: "invalid_format", message: "Invalid URL"}
	}
	val := tabsNewlines.ReplaceAllString(trimmed, "")
	if dangerousSchemes[u.scheme] {
		return val, &issue{code: "custom", message: "URL cannot use javascript:, data:, or vbscript: scheme"}
	}
	if strings.Contains(val, "#") {
		return val, &issue{code: "custom", message: "Redirect URI must not contain a fragment component"}
	}
	if u.scheme == "http" && !isLoopbackHost(u.host) {
		return val, &issue{code: "custom", message: "Redirect URI must use HTTPS (HTTP allowed only for loopback hosts)"}
	}
	return val, nil
}

// resourceURI is ResourceUriSchema.
func resourceURI(v any) *issue {
	s, ok := v.(string)
	if !ok {
		return typeIssue("string", v)
	}
	u, err := parseJSURL(s)
	if err != nil {
		return &issue{code: "custom", message: "resource must be an absolute URI", fatal: true}
	}
	if strings.Contains(s, "#") {
		return &issue{code: "custom", message: "resource must not contain a fragment"}
	}
	if dangerousSchemes[u.scheme] {
		return &issue{code: "custom", message: "resource cannot use javascript:, data:, or vbscript: scheme"}
	}
	return nil
}

// resourceParam is z.union([ResourceUriSchema, z.array(ResourceUriSchema).min(1)]).
func resourceParam(v any) *issue {
	strIssue := resourceURI(v)
	if strIssue == nil {
		return nil
	}
	var arrIssue *issue
	arr, isArr := v.([]any)
	if !isArr {
		arrIssue = typeIssue("array", v)
	} else {
		for i, e := range arr {
			if is := resourceURI(e); is != nil {
				arrIssue = at(is, i)
				break
			}
		}
		if arrIssue == nil && len(arr) < 1 {
			arrIssue = tooSmallArray(1)
		}
		if arrIssue == nil {
			return nil
		}
	}
	// zod 4 unions: one branch that failed without aborting reports its own issues.
	if !strIssue.fatal && arrIssue.fatal {
		return strIssue
	}
	if strIssue.fatal && !arrIssue.fatal {
		return arrIssue
	}
	return &issue{code: "invalid_union", message: "Invalid input"}
}

// --- JS URL ------------------------------------------------------------------------

// jsURL is the parts of new URL(s) the provider reads.
type jsURL struct {
	scheme   string
	username string
	password string
	// host is url.host (with the port); hostname without it.
	host, hostname string
	pathname       string
	search         string
	special        bool
	w              *whatwg.URL
	// raw is the input for a non-special URL.
	raw string
}

var (
	schemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	authorityRe  = regexp.MustCompile(`^//([^/?#]*)`)
)

// parseJSURL is new URL(s): WHATWG for http(s), ws(s), ftp and file; a
// simple split for other schemes.
func parseJSURL(s string) (*jsURL, error) {
	s = strx.Trim(s)
	m := schemePrefix.FindString(s)
	if m == "" {
		return nil, whatwg.ErrInvalid
	}
	scheme := strings.ToLower(m[:len(m)-1])
	switch scheme {
	case "http", "https":
		w, err := whatwg.ParseURL(s)
		if err != nil {
			return nil, err
		}
		return &jsURL{scheme: scheme, username: w.Username, password: w.Password, host: w.HostPort(), hostname: w.Host, pathname: w.Path, search: searchOf(w), special: true, w: w}, nil
	}
	rest := tabsNewlines.ReplaceAllString(s[len(m):], "")
	u := &jsURL{scheme: scheme, raw: tabsNewlines.ReplaceAllString(s, "")}
	if am := authorityRe.FindStringSubmatch(rest); am != nil {
		auth := am[1]
		if i := strings.LastIndex(auth, "@"); i >= 0 {
			cred := auth[:i]
			auth = auth[i+1:]
			if j := strings.Index(cred, ":"); j >= 0 {
				u.username, u.password = cred[:j], cred[j+1:]
			} else {
				u.username = cred
			}
		}
		u.host = strings.ToLower(auth)
		u.hostname = u.host
		if j := strings.LastIndex(u.host, ":"); j >= 0 && !strings.HasSuffix(u.host, "]") {
			u.hostname = u.host[:j]
		}
		rest = rest[len(am[0]):]
	}
	path := rest
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = rest[:i]
		if rest[i] == '?' {
			q := rest[i:]
			if j := strings.Index(q, "#"); j >= 0 {
				q = q[:j]
			}
			if len(q) > 1 {
				u.search = q
			}
		}
	}
	u.pathname = path
	return u, nil
}

func searchOf(w *whatwg.URL) string {
	if w.RawQuery == "" {
		return ""
	}
	return "?" + w.RawQuery
}

// withQuery is the URL with its query replaced (url.search = q).
func (u *jsURL) withQuery(q string) string {
	if u.special {
		w := *u.w
		w.RawQuery, w.ForceQuery = q, false
		return w.Href()
	}
	base, frag := u.raw, ""
	if i := strings.Index(base, "#"); i >= 0 {
		base, frag = base[:i], base[i:]
	}
	if i := strings.Index(base, "?"); i >= 0 {
		base = base[:i]
	}
	if q != "" {
		base += "?" + q
	}
	return base + frag
}

// searchParams is url.searchParams.
func (u *jsURL) searchParams() *Params { return ParseParams(u.search) }

// setParams is url.searchParams.set(...) for each pair, then href.
func (u *jsURL) setParams(pairs ...[2]string) string {
	p := u.searchParams()
	for _, kv := range pairs {
		p.Set(kv[0], kv[1])
	}
	return u.withQuery(p.String())
}
