package apps_test

// testdata/apps.json from testdata/gen/apps-goldens.ts (generated with TZ=UTC).
// It still holds the removed Finansial and Oslo vectors; only the feed, date
// and day ones are read.

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
	Days []struct {
		Zone string  `json:"zone"`
		At   string  `json:"at"`
		Day  *string `json:"day"`
	} `json:"days"`
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

// The day boundary matches V8's Intl reading of each time zone.
func TestLocalDayMatchesTS(t *testing.T) {
	saved := time.Local
	time.Local = time.UTC
	defer func() { time.Local = saved }()
	for _, c := range load(t).Days {
		day, ok := apps.LocalDay(c.At, c.Zone)
		if ok != (c.Day != nil) || (ok && day != *c.Day) {
			t.Errorf("%s in %s: %q %v, want %v", c.At, c.Zone, day, ok, c.Day)
		}
	}
}

func TestMain(m *testing.M) { os.Exit(runMain(m)) }
