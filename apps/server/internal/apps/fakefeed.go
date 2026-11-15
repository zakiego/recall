package apps

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// FakeFeed is fake-feed.ts: the changes contract for tests, paging through
// Items sorted by (updatedAt, id) with an opaque cursor, a Bearer key check,
// and FailNext to fail requests.
type FakeFeed struct {
	mu       sync.Mutex
	key      string
	Items    []ChangeItem
	Calls    []FeedCall
	failWith string // a status number, or "network"
	failLeft int
}

// FeedCall is one request the fake got.
type FeedCall struct {
	URL    string
	Cursor *string
	Limit  int
	Auth   string
}

// NewFakeFeed is createFakeFeed.
func NewFakeFeed(key string, items ...ChangeItem) *FakeFeed {
	return &FakeFeed{key: key, Items: append([]ChangeItem(nil), items...)}
}

// Add appends items.
func (f *FakeFeed) Add(items ...ChangeItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Items = append(f.Items, items...)
}

// FailNext makes the next times requests answer with status, or fail to
// connect with "network".
func (f *FakeFeed) FailNext(status string, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith, f.failLeft = status, times
}

// CallCount is how many requests came in.
func (f *FakeFeed) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

var offsetCursor = regexp.MustCompile(`^o:(\d+)$`)

func encodeOffset(n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(n)))
}

func feedJSON(body any, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(jsonx.Stringify(body)))}
}

// Do is the fake's Do.
func (f *FakeFeed) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	var cursor *string
	if v, ok := q["cursor"]; ok {
		c := v[0]
		cursor = &c
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		limit = int(strx.Number(v))
	}
	auth := r.Header.Get("authorization")
	f.Calls = append(f.Calls, FeedCall{URL: r.URL.String(), Cursor: cursor, Limit: limit, Auth: auth})
	if f.failLeft > 0 {
		status := f.failWith
		f.failLeft--
		if status == "network" {
			return nil, errors.New("fetch failed")
		}
		n, _ := strconv.Atoi(status)
		return feedJSON(jsonx.ObjectOf("error", "boom"), n), nil
	}
	if r.URL.Path != "/api/v1/changes" {
		return feedJSON(jsonx.ObjectOf("error", "not_found"), 404), nil
	}
	if auth != "Bearer "+f.key {
		return feedJSON(jsonx.ObjectOf("error", "unauthorized"), 401), nil
	}
	if limit < 1 || limit > 200 {
		return feedJSON(jsonx.ObjectOf("error", "bad_limit"), 400), nil
	}
	offset := 0
	if cursor != nil && *cursor != "" {
		m := offsetCursor.FindStringSubmatch(string(strx.DecodeBase64Node(*cursor)))
		if m == nil {
			return feedJSON(jsonx.ObjectOf("error", "bad_cursor"), 400), nil
		}
		offset, _ = strconv.Atoi(m[1])
	}
	all := append([]ChangeItem(nil), f.Items...)
	// localeCompare on updatedAt, then id.
	sort.SliceStable(all, func(i, j int) bool {
		if c := strx.LocaleCompare(all[i].UpdatedAt, all[j].UpdatedAt); c != 0 {
			return c < 0
		}
		return strx.LocaleCompare(all[i].ID, all[j].ID) < 0
	})
	end := min(offset+limit, len(all))
	page := []ChangeItem{}
	if offset < end {
		page = all[offset:end]
	}
	stop := offset + len(page)
	var next any
	if len(page) > 0 {
		next = encodeOffset(stop)
	} else if cursor != nil {
		next = *cursor
	}
	return feedJSON(jsonx.ObjectOf("items", page, "nextCursor", next, "hasMore", stop < len(all)), 200), nil
}
