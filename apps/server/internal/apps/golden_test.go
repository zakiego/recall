package apps_test

// testdata/apps.json from testdata/gen/apps-goldens.ts (generated with TZ=UTC).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type appsGolden struct {
	URLs []struct {
		Base   string  `json:"base"`
		Cursor *string `json:"cursor"`
		Limit  int     `json:"limit"`
		URL    string  `json:"url"`
	} `json:"urls"`
	Pages []struct {
		Reply struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"reply"`
		OK    json.RawMessage `json:"ok"`
		Error *struct {
			Message, Code string
			Status        *int
		} `json:"error"`
	} `json:"pages"`
	Settings []struct {
		In  json.RawMessage `json:"in"`
		Out json.RawMessage `json:"out"`
	} `json:"settings"`
	Money []struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
		Digits   float64 `json:"digits"`
		Out      string  `json:"out"`
	} `json:"money"`
	Hashes []struct {
		In    json.RawMessage `json:"in"`
		Out   string          `json:"out"`
		Short string          `json:"short"`
	} `json:"hashes"`
	Docs []struct {
		Item        json.RawMessage `json:"item"`
		Docs        []json.RawMessage
		EventDate   *string `json:"eventDate"`
		RuleVersion *string `json:"ruleVersion"`
		LoanVersion *string `json:"loanVersion"`
	} `json:"docs"`
	Days []struct {
		Zone string  `json:"zone"`
		At   string  `json:"at"`
		Day  *string `json:"day"`
	} `json:"days"`
	Digests []struct {
		Day    string            `json:"day"`
		Events []apps.OsloEvent  `json:"events"`
		Zone   string            `json:"zone"`
		Out    map[string]string `json:"out"`
	} `json:"digests"`
	DateStrings []struct {
		S string   `json:"s"`
		T *float64 `json:"t"`
	} `json:"dateStrings"`
}

func load(t *testing.T) appsGolden {
	var g appsGolden
	testutil.LoadJSON(t, "apps.json", &g)
	return g
}

func TestDateParseMatchesV8(t *testing.T) {
	saved := time.Local
	time.Local = time.UTC
	defer func() { time.Local = saved }()
	for _, c := range load(t).DateStrings {
		got, ok := dates.JSParse(c.S)
		if ok != (c.T != nil) || (ok && float64(got) != *c.T) {
			want := "NaN"
			if c.T != nil {
				want = fmt.Sprint(int64(*c.T))
			}
			t.Errorf("%q: %d %v, want %s", c.S, got, ok, want)
		}
	}
}

func TestFeedURLsAndPagesMatchTS(t *testing.T) {
	g := load(t)
	for _, c := range g.URLs {
		if got, _ := apps.FeedURL(c.Base, c.Cursor, c.Limit); got != c.URL {
			t.Errorf("%s: %s, want %s", c.Base, got, c.URL)
		}
	}
	for i, c := range g.Pages {
		do := func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: c.Reply.Status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(c.Reply.Body))}, nil
		}
		page, err := apps.FetchPage(context.Background(), do, apps.FetchOptions{BaseURL: "https://x.test", APIKey: "k"})
		if c.Error != nil {
			fe, ok := err.(*apps.FeedError)
			status := 0
			if c.Error.Status != nil {
				status = *c.Error.Status
			}
			if !ok || fe.Msg != c.Error.Message || fe.Code != c.Error.Code || fe.Status != status {
				t.Errorf("page %d: %v, want %+v", i, err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("page %d: %v", i, err)
			continue
		}
		got := jsonx.ObjectOf("items", page.Items, "skipped", page.Skipped, "nextCursor", page.NextCursor, "hasMore", page.HasMore)
		same(t, "page", got, c.OK)
	}
}

func same(t *testing.T, label string, got any, want json.RawMessage) {
	t.Helper()
	b, _ := jsonx.Marshal(got)
	var x, y any
	json.Unmarshal(b, &x)
	json.Unmarshal(want, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("%s\n got %s\nwant %s", label, b, want)
	}
}

func TestFinansialMatchesTS(t *testing.T) {
	g := load(t)
	for _, c := range g.Settings {
		var raw *jsonx.Object
		if s := string(c.In); s != `"undefined"` && s != "null" {
			raw, _ = jsonx.ParseObject(c.In)
		}
		got := apps.FinansialSettings(raw)
		// Byte-compare too: the thresholds' key order is what the page shows.
		b, _ := jsonx.Marshal(got)
		w, _ := jsonx.Parse(c.Out)
		if string(b) != jsonx.Stringify(w) {
			t.Errorf("settings %s: %s, want %s", c.In, b, c.Out)
		}
	}
	for _, c := range g.Money {
		if got := apps.FormatMoney(c.Amount, c.Currency, c.Digits); got != c.Out {
			t.Errorf("money %v %s %v: %q, want %q", c.Amount, c.Currency, c.Digits, got, c.Out)
		}
	}
	for _, c := range g.Hashes {
		o, _ := jsonx.ParseObject(c.In)
		if got := apps.HashObject(o, 64); got != c.Out {
			t.Errorf("hash %s: %s, want %s", c.In, got, c.Out)
		}
		if got := apps.HashObject(o, 16); got != c.Short {
			t.Errorf("hash16 %s", c.In)
		}
	}
	settings := []apps.Settings{apps.FinansialSettings(nil), apps.FinansialSettings(jsonx.ObjectOf("thresholds", jsonx.ObjectOf("IDR", 0.5, "USD", 1.0)))}
	for _, c := range g.Docs {
		var it apps.ChangeItem
		if err := json.Unmarshal(c.Item, &it); err != nil {
			t.Fatal(err)
		}
		it.Data, _ = jsonx.ParseObject(mustField(c.Item, "data"))
		for i, s := range settings {
			doc := apps.FinansialDocument(it, s)
			if string(c.Docs[i]) == "null" {
				if doc != nil {
					t.Errorf("%s/%d: %+v, want none", it.ID, i, doc)
				}
				continue
			}
			if doc == nil {
				t.Errorf("%s/%d: none, want %s", it.ID, i, c.Docs[i])
				continue
			}
			got := jsonx.ObjectOf("sourceId", doc.SourceID, "title", doc.Title, "content", doc.Content, "documentDate", doc.DocumentDate)
			if doc.Fingerprint != "" {
				got.Set("fingerprint", doc.Fingerprint)
			}
			same(t, it.ID, got, c.Docs[i])
		}
		if d, ok := apps.FinansialEventDate(it); (c.EventDate == nil) == ok || (ok && d != *c.EventDate) {
			t.Errorf("%s event date %q %v", it.ID, d, ok)
		}
		if c.RuleVersion != nil && apps.RuleVersion(it) != *c.RuleVersion {
			t.Errorf("%s rule version", it.ID)
		}
		if c.LoanVersion != nil && apps.LoanVersion(it) != *c.LoanVersion {
			t.Errorf("%s loan version", it.ID)
		}
	}
}

func mustField(raw json.RawMessage, k string) []byte {
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	return m[k]
}

func TestOsloMatchesTS(t *testing.T) {
	saved := time.Local
	time.Local = time.UTC
	defer func() { time.Local = saved }()
	g := load(t)
	for _, c := range g.Days {
		day, ok := apps.LocalDay(c.At, c.Zone)
		if ok != (c.Day != nil) || (ok && day != *c.Day) {
			t.Errorf("%s in %s: %q %v, want %v", c.At, c.Zone, day, ok, c.Day)
		}
	}
	for _, c := range g.Digests {
		title, content := apps.OsloDigest(c.Day, c.Events, c.Zone)
		if title != c.Out["title"] || content != c.Out["content"] {
			t.Errorf("digest %s %s\n got %q\n%q\nwant %q\n%q", c.Day, c.Zone, title, content, c.Out["title"], c.Out["content"])
		}
	}
}

func TestMain(m *testing.M) { os.Exit(runMain(m)) }
