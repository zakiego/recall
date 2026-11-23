package connectors

// fake-google.ts: a stand-in for Google's OAuth and Gmail endpoints, served
// from the fixture mailbox. Tests use it; RECALL_GMAIL_FAKE=1 in a dev build
// uses it for the whole app.

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

//go:embed fixtures/gmail.json
var fixturesJSON []byte

// FixtureSet is the fixture mailbox (fixtures/gmail.ts, exported by
// testdata/gen/gmail-goldens.ts).
type FixtureSet struct {
	Messages    []GmailMessage               `json:"messages"`
	Worst       []GmailMessage               `json:"worst"`
	Attachments map[string]map[string]string `json:"attachments"`
}

var (
	fixturesOnce sync.Once
	fixtures     FixtureSet
)

// Fixtures is the fixture mailbox.
func Fixtures() FixtureSet {
	fixturesOnce.Do(func() {
		if err := json.Unmarshal(fixturesJSON, &fixtures); err != nil {
			panic("connectors: fixtures/gmail.json: " + err.Error())
		}
	})
	return fixtures
}

// FakeOptions are createFakeGoogle's options. Revoked: refreshes fail with
// invalid_grant. ExpiresIn nil is 3599.
type FakeOptions struct {
	Email       string
	Messages    []GmailMessage
	Attachments map[string]map[string][]byte
	Revoked     bool
	ExpiresIn   *float64
}

// FakeCall is one request the fake got.
type FakeCall struct {
	Method string
	URL    string
	Auth   string
	Body   *string
}

// FakeGoogle is the fake.
type FakeGoogle struct {
	mu            sync.Mutex
	opts          FakeOptions
	Calls         []FakeCall
	RevokedTokens []string
	issued        int
	Revoked       bool
}

// NewFakeGoogle is createFakeGoogle; empty options serve the fixtures.
func NewFakeGoogle(opts FakeOptions) *FakeGoogle {
	f := Fixtures()
	if opts.Messages == nil {
		opts.Messages = f.Messages
	}
	if opts.Attachments == nil {
		opts.Attachments = map[string]map[string][]byte{}
		for msg, parts := range f.Attachments {
			opts.Attachments[msg] = map[string][]byte{}
			for id, data := range parts {
				opts.Attachments[msg][id] = DecodeBase64URL(data)
			}
		}
	}
	return &FakeGoogle{opts: opts, Revoked: opts.Revoked}
}

func jsonResponse(body any, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(jsonx.Stringify(body)))}
}

func notFound(msg string) *http.Response {
	return jsonResponse(jsonx.ObjectOf("error", jsonx.ObjectOf("code", 404, "message", msg)), 404)
}

// Do is the fake's Do.
func (g *FakeGoogle) Do(r *http.Request) (*http.Response, error) {
	var body *string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		s := string(b)
		body = &s
	}
	u := r.URL.String()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Calls = append(g.Calls, FakeCall{Method: r.Method, URL: u, Auth: r.Header.Get("authorization"), Body: body})
	form := func() url.Values {
		v, _ := url.ParseQuery(s(body))
		return v
	}
	if u == TokenURL && r.Method == "POST" {
		f := form()
		if f.Get("grant_type") == "refresh_token" && (g.Revoked || contains(g.RevokedTokens, f.Get("refresh_token"))) {
			return jsonResponse(jsonx.ObjectOf("error", "invalid_grant", "error_description", "Token has been expired or revoked."), 400), nil
		}
		g.issued++
		expires := 3599.0
		if g.opts.ExpiresIn != nil {
			expires = *g.opts.ExpiresIn
		}
		o := jsonx.ObjectOf("access_token", fmt.Sprintf("fake-access-%d", g.issued), "expires_in", expires, "scope", GmailScope, "token_type", "Bearer")
		if f.Get("grant_type") == "authorization_code" {
			o.Set("refresh_token", fmt.Sprintf("fake-refresh-%d", g.issued))
		}
		return jsonResponse(o, 200), nil
	}
	if strings.HasPrefix(u, RevokeURL) && r.Method == "POST" {
		g.RevokedTokens = append(g.RevokedTokens, form().Get("token"))
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	if !strings.HasPrefix(u, GmailAPI) {
		return notFound("Not Found"), nil
	}
	if !strings.HasPrefix(r.Header.Get("authorization"), "Bearer fake-access-") {
		return jsonResponse(jsonx.ObjectOf("error", jsonx.ObjectOf("code", 401, "message", "Invalid Credentials")), 401), nil
	}
	base, _ := url.Parse(GmailAPI)
	path := strings.Replace(r.URL.EscapedPath(), base.Path, "", 1)
	q := r.URL.Query()
	switch {
	case path == "/profile":
		email := g.opts.Email
		if email == "" {
			email = "alia@example.com"
		}
		n := len(g.opts.Messages)
		return jsonResponse(jsonx.ObjectOf("emailAddress", email, "messagesTotal", n, "threadsTotal", n), 200), nil
	case path == "/messages":
		var found []GmailMessage
		for _, m := range g.opts.Messages {
			if matches(m, q.Get("q")) {
				found = append(found, m)
			}
		}
		max, offset := 100, 0
		if v := q.Get("maxResults"); v != "" {
			max = int(strx.Number(v))
		}
		if v := q.Get("pageToken"); v != "" {
			offset = int(strx.Number(v))
		}
		end := min(offset+max, len(found))
		o := jsonx.NewMap[any]()
		if offset < end {
			list := []any{}
			for _, m := range found[offset:end] {
				// { id, threadId }: an undefined threadId is left out.
				e := jsonx.ObjectOf("id", m.ID)
				if m.ThreadID != nil {
					e.Set("threadId", *m.ThreadID)
				}
				list = append(list, e)
			}
			o.Set("messages", list)
		}
		if offset+max < len(found) {
			o.Set("nextPageToken", fmt.Sprint(offset+max))
		}
		o.Set("resultSizeEstimate", len(found))
		return jsonResponse(o, 200), nil
	}
	if m := attachmentPath.FindStringSubmatch(path); m != nil {
		msg, _ := url.PathUnescape(m[1])
		id, _ := url.PathUnescape(m[2])
		if b, ok := g.opts.Attachments[msg][id]; ok {
			return jsonResponse(jsonx.ObjectOf("size", len(b), "data", base64.RawURLEncoding.EncodeToString(b)), 200), nil
		}
		return notFound("Not Found"), nil
	}
	if m := messagePath.FindStringSubmatch(path); m != nil {
		id, _ := url.PathUnescape(m[1])
		for _, msg := range g.opts.Messages {
			if msg.ID != id {
				continue
			}
			if q.Get("format") == "metadata" {
				var wanted []string
				for _, h := range q["metadataHeaders"] {
					wanted = append(wanted, strx.Lower(h))
				}
				out := msg
				p := &GmailPart{}
				if msg.Payload != nil {
					p.MimeType = msg.Payload.MimeType
					for _, h := range msg.Payload.Headers {
						if contains(wanted, strx.Lower(h.Name)) {
							p.Headers = append(p.Headers, h)
						}
					}
				}
				out.Payload = p
				return jsonResponse(out, 200), nil
			}
			return jsonResponse(msg, 200), nil
		}
		return notFound("Requested entity was not found."), nil
	}
	return notFound("Not Found"), nil
}

var (
	attachmentPath = regexp.MustCompile(`^/messages/([^/]+)/attachments/([^/]+)$`)
	messagePath    = regexp.MustCompile(`^/messages/([^/]+)$`)
	// JS \S: anything but JS whitespace (Unicode spaces included).
	queryTerm = regexp.MustCompile(`[^` + jsSpace + `]+:"[^"]*"|[^` + jsSpace + `]+`)
	quotes    = regexp.MustCompile(`^"|"$`)
)

func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

// matches is enough of Gmail's query syntax for the fixtures: from:, to:,
// subject:, label:, after:/before: (YYYY/MM/DD) and plain words, all ANDed.
func matches(m GmailMessage, q string) bool {
	hay := func(p *string) string { return strx.Lower(s(p)) }
	for _, raw := range queryTerm.FindAllString(q, -1) {
		op, rest := "", raw
		if i := strings.Index(raw, ":"); i >= 0 {
			op, rest = raw[:i], raw[i+1:]
		}
		v := strx.Lower(quotes.ReplaceAllString(rest, ""))
		term := strx.Lower(raw)
		if op != "" {
			term = v
		}
		switch strx.Lower(op) {
		case "from", "to", "subject":
			if !strings.Contains(hay(Header(m.Payload, op)), term) {
				return false
			}
		case "label":
			ok := false
			for _, l := range m.LabelIDs {
				ll := strx.Lower(l)
				ok = ok || ll == term || ll == "label_"+term
			}
			if !ok {
				return false
			}
		case "after", "before":
			at, ok := dates.JSParse(strings.ReplaceAll(term, "/", "-"))
			// Number(m.internalDate ?? 0): NaN compares false.
			t := 0.0
			if m.InternalDate != nil {
				t = strx.Number(*m.InternalDate)
			}
			if ok && ((op == "after" && t < float64(at)) || (op != "after" && t >= float64(at))) {
				return false
			}
		default:
			if !strings.Contains(hay(m.Snippet), term) && !strings.Contains(hay(Header(m.Payload, "subject")), term) && !strings.Contains(hay(Header(m.Payload, "from")), term) {
				return false
			}
		}
	}
	return true
}

// jsSpace is JS's WhiteSpace and LineTerminator, for a character class.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
