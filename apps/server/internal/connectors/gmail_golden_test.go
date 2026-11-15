package connectors_test

// testdata/gmail.json from testdata/gen/gmail-goldens.ts: parseMessage on
// every fixture message and the extra cases, decodeEntities, header, the
// authorization URL, and every request the Google client makes with what it
// returns.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type gmailGolden struct {
	Parsed []struct {
		ID    string          `json:"id"`
		OK    json.RawMessage `json:"ok"`
		Error string          `json:"error"`
	} `json:"parsed"`
	ExtraParsed []struct {
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
		OK      json.RawMessage `json:"ok"`
		Error   string          `json:"error"`
	} `json:"extraParsed"`
	Entities []struct{ In, Out string } `json:"entities"`
	Headers  []struct {
		Name  string  `json:"name"`
		Value *string `json:"value"`
	} `json:"headers"`
	AuthURL  string `json:"authUrl"`
	Requests []struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    *string           `json:"body"`
	} `json:"requests"`
	Outcomes map[string]struct {
		OK    json.RawMessage `json:"ok"`
		Error *struct {
			Name, Message, Code string
			Status              int
		} `json:"error"`
	} `json:"outcomes"`
}

type fixtures struct {
	Messages []connectors.GmailMessage `json:"messages"`
	Worst    []connectors.GmailMessage `json:"worst"`
}

func loadGmail(t *testing.T) (gmailGolden, fixtures) {
	var g gmailGolden
	testutil.LoadJSON(t, "gmail.json", &g)
	var f fixtures
	b, err := os.ReadFile("fixtures/gmail.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return g, f
}

// sameJSON compares a Go value with the golden's JSON, parsed on both sides.
func sameJSON(t *testing.T, label string, got any, want json.RawMessage) {
	t.Helper()
	b, err := jsonx.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var x, y any
	json.Unmarshal(b, &x)
	json.Unmarshal(want, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("%s\n got %s\nwant %s", label, b, want)
	}
}

func TestParseMessageMatchesTS(t *testing.T) {
	g, f := loadGmail(t)
	all := append(append([]connectors.GmailMessage{}, f.Messages...), f.Worst...)
	if len(all) != len(g.Parsed) || len(g.Parsed) < 9 {
		t.Fatalf("%d messages, %d parsed", len(all), len(g.Parsed))
	}
	for i, m := range all {
		got, err := connectors.ParseMessage(m)
		want := g.Parsed[i]
		if want.Error != "" {
			if err == nil || jserr.Format(err) != want.Error {
				t.Errorf("%s: %v, want %s", m.ID, err, want.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", m.ID, err)
			continue
		}
		sameJSON(t, m.ID, got, want.OK)
	}
	for _, c := range g.ExtraParsed {
		var m connectors.GmailMessage
		if err := json.Unmarshal(c.Message, &m); err != nil {
			t.Fatal(err)
		}
		got, err := connectors.ParseMessage(m)
		if c.Error != "" {
			if err == nil || "Error: "+err.Error() != c.Error {
				t.Errorf("%s: %v, want %s", c.ID, err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.ID, err)
			continue
		}
		sameJSON(t, c.ID, got, c.OK)
	}
}

func TestEntitiesAndHeadersMatchTS(t *testing.T) {
	g, _ := loadGmail(t)
	for _, c := range g.Entities {
		if got := connectors.DecodeEntities(c.In); got != c.Out {
			t.Errorf("%q: %q, want %q", c.In, got, c.Out)
		}
	}
	part := &connectors.GmailPart{Headers: []connectors.GmailHeader{{Name: "Subject", Value: "  hello  "}, {Name: "subject", Value: "second"}, {Name: "Empty", Value: "   "}}}
	for _, c := range g.Headers {
		got := connectors.Header(part, c.Name)
		if (got == nil) != (c.Value == nil) || (got != nil && *got != *c.Value) {
			t.Errorf("%s: %v, want %v", c.Name, got, c.Value)
		}
	}
}

func TestGoogleClientMatchesTS(t *testing.T) {
	g, _ := loadGmail(t)
	t.Setenv("RECALL_URL", "http://localhost:4800")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client.apps.googleusercontent.com")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	if got := connectors.AuthorizationURL("test-client.apps.googleusercontent.com", "st~a*te", "ch_al-lenge"); got != g.AuthURL {
		t.Errorf("auth url\n got %s\nwant %s", got, g.AuthURL)
	}
	var reqs []*http.Request
	var bodies []*string
	recorder := func(reply string, status int) connectors.Do {
		return func(r *http.Request) (*http.Response, error) {
			reqs = append(reqs, r)
			if r.Body != nil {
				b, _ := io.ReadAll(r.Body)
				s := string(b)
				bodies = append(bodies, &s)
			} else {
				bodies = append(bodies, nil)
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply))}, nil
		}
	}
	ctx := context.Background()
	type outcome struct {
		v   any
		err error
	}
	got := map[string]outcome{}
	run := func(name string, v any, err error) { got[name] = outcome{v, err} }
	v1, e1 := connectors.ExchangeCode(ctx, recorder(`{"access_token":"a1","refresh_token":"r1","expires_in":3599.5,"scope":"x y"}`, 200), "4/0A b+c", "v~*.", 1_000)
	run("exchange", v1, e1)
	v2, e2 := connectors.ExchangeCode(ctx, recorder(`{"access_token":"a1"}`, 200), "c", "v", 1_000)
	run("exchange-no-refresh", v2, e2)
	v3, e3 := connectors.ExchangeCode(ctx, recorder(`{"error":"invalid_grant"}`, 400), "c", "v", 1_000)
	run("exchange-error", v3, e3)
	v4, e4 := connectors.RefreshAccessToken(ctx, recorder(`{"access_token":"a2"}`, 200), "r1", 2_000)
	run("refresh", v4, e4)
	v5, e5 := connectors.RefreshAccessToken(ctx, recorder(`{"error":"invalid_grant"}`, 400), "r1", 2_000)
	run("refresh-revoked", v5, e5)
	v6, e6 := connectors.RefreshAccessToken(ctx, recorder(`{}`, 401), "r1", 2_000)
	run("refresh-401", v6, e6)
	v7, e7 := connectors.RefreshAccessToken(ctx, recorder(`{}`, 500), "r1", 2_000)
	run("refresh-500", v7, e7)
	run("revoke-ok", connectors.RevokeToken(ctx, recorder("", 200), "tok"), nil)
	run("revoke-dead", connectors.RevokeToken(ctx, recorder("", 400), "tok"), nil)
	run("revoke-500", connectors.RevokeToken(ctx, recorder("", 500), "tok"), nil)
	m, em := connectors.GetMessageMetadata(ctx, recorder(`{"id":"m1"}`, 200), "acc", "id/with space")
	run("metadata", m, em)
	l, el := connectors.ListMessages(ctx, recorder(`{}`, 200), "acc", "from:a b~*", "p 1", 20)
	run("list", l, el)
	_, e404 := connectors.ListMessages(ctx, recorder(`{}`, 404), "acc", "x", "", 20)
	run("list-404", nil, e404)
	_, e429 := connectors.ListMessages(ctx, recorder(`{}`, 429), "acc", "x", "", 20)
	run("list-429", nil, e429)
	_, e401 := connectors.ListMessages(ctx, recorder(`{}`, 401), "acc", "x", "", 20)
	run("list-401", nil, e401)
	att, ea := connectors.GetAttachment(ctx, recorder(`{"data":"aGVsbG8"}`, 200), "acc", "m1", "a/1")
	run("attachment", strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(b64(att)), "="), ea)

	if len(reqs) != len(g.Requests) {
		t.Fatalf("%d requests, want %d", len(reqs), len(g.Requests))
	}
	for i, w := range g.Requests {
		r := reqs[i]
		if r.URL.String() != w.URL || r.Method != w.Method {
			t.Errorf("request %d: %s %s, want %s %s", i, r.Method, r.URL, w.Method, w.URL)
		}
		for k, v := range w.Headers {
			if r.Header.Get(k) != v {
				t.Errorf("request %d header %s: %q, want %q", i, k, r.Header.Get(k), v)
			}
		}
		if (bodies[i] == nil) != (w.Body == nil) || (w.Body != nil && *bodies[i] != *w.Body) {
			t.Errorf("request %d body: %v, want %v", i, deref(bodies[i]), deref(w.Body))
		}
	}
	for name, want := range g.Outcomes {
		o, ok := got[name]
		if !ok {
			t.Errorf("no run for %s", name)
			continue
		}
		if want.Error != nil {
			var ge *connectors.GoogleError
			if !errors.As(o.err, &ge) || ge.JSName() != want.Error.Name || ge.Msg != want.Error.Message || ge.Status != want.Error.Status || ge.Code != want.Error.Code {
				t.Errorf("%s: %+v, want %+v", name, o.err, want.Error)
			}
			continue
		}
		if o.err != nil {
			t.Errorf("%s: %v", name, o.err)
			continue
		}
		sameJSON(t, name, o.v, want.OK)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
