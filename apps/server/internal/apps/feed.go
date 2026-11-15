// Package apps is the pull connectors for Zaki's own apps, Oslo (todo) and
// Finansial (money): the changes-feed client (changes-feed.ts), the
// Finansial and Oslo mappings (finansial.ts, oslo.ts), the stored
// connectors and their sync (apps.ts), and the fake feed (fake-feed.ts).
package apps

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

const (
	PageLimit      = 100
	MaxPagesPerRun = 20
	FeedTimeoutMs  = 20_000
)

// ChangeItem is changeItem after parsing; Data is {} when the feed sent none.
type ChangeItem struct {
	Kind      string        `json:"kind"`
	ID        string        `json:"id"`
	UpdatedAt string        `json:"updatedAt"`
	URL       string        `json:"url"`
	Deleted   bool          `json:"deleted"`
	Data      *jsonx.Object `json:"data"`
}

// FeedPage is one page.
type FeedPage struct {
	Items      []ChangeItem
	Skipped    int
	NextCursor *string
	HasMore    bool
}

// FeedError is FeedError; Status 0 is null. Code is "auth", "bad_cursor",
// "upstream", "bad_response", "timeout" or "network".
type FeedError struct {
	Msg    string
	Status int
	Code   string
}

func (e *FeedError) Error() string  { return e.Msg }
func (e *FeedError) JSName() string { return "FeedError" }

// FeedURL is feedUrl: /api/v1/changes on the base's origin, with the cursor
// and limit. ok false when the base isn't a URL.
func FeedURL(baseURL string, cursor *string, limit int) (string, bool) {
	base, err := whatwg.ParseURL(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return "", false
	}
	u, err := base.Resolve("/api/v1/changes")
	if err != nil {
		return "", false
	}
	var q [][2]string
	if cursor != nil && *cursor != "" {
		q = append(q, [2]string{"cursor", *cursor})
	}
	q = append(q, [2]string{"limit", fmt.Sprint(limit)})
	u.RawQuery, u.Fragment = "", ""
	return u.Href() + "?" + connectors.FormEncode(q), true
}

var reasonRE = regexp.MustCompile(`^[A-Za-z0-9_ .,:'-]+$`)

// upstreamReason is the app's own short error code, for the error line.
func upstreamReason(body []byte) string {
	v, err := jsonx.Parse(body)
	if err != nil {
		return ""
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return ""
	}
	reason := ""
	if e, ok := o.Get("error"); ok {
		if s, isStr := e.(string); isStr {
			reason = s
		}
	}
	if reason == "" {
		if e, ok := o.Get("error"); !ok || !isString(e) {
			if c, ok := o.Get("code"); ok {
				if s, isStr := c.(string); isStr {
					reason = s
				}
			}
		}
	}
	if reason != "" && strx.Len16(reason) <= 80 && reasonRE.MatchString(reason) {
		return ": " + reason
	}
	return ""
}

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

// FetchOptions are fetchPage's options; Limit and TimeoutMs 0 are the
// defaults.
type FetchOptions struct {
	BaseURL   string
	APIKey    string
	Cursor    *string
	Limit     int
	TimeoutMs int
}

// FetchPage is fetchPage.
func FetchPage(ctx context.Context, do connectors.Do, o FetchOptions) (FeedPage, error) {
	limit, timeoutMs := o.Limit, o.TimeoutMs
	if limit == 0 {
		limit = PageLimit
	}
	if timeoutMs == 0 {
		timeoutMs = FeedTimeoutMs
	}
	network := &FeedError{"couldn't reach the app", 0, "network"}
	u, ok := FeedURL(o.BaseURL, o.Cursor, limit)
	if !ok {
		return FeedPage{}, network
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return FeedPage{}, network
	}
	req.Header.Set("authorization", "Bearer "+o.APIKey)
	req.Header.Set("accept", "application/json")
	res, err := do(req)
	if err != nil {
		if ctx.Err() != nil {
			return FeedPage{}, &FeedError{fmt.Sprintf("the feed didn't answer within %d s", int64(math.Floor(float64(timeoutMs)/1000+0.5))), 0, "timeout"}
		}
		return FeedPage{}, network
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		reason := ""
		if readErr == nil {
			reason = upstreamReason(body)
		}
		switch res.StatusCode {
		case 401, 403:
			return FeedPage{}, &FeedError{fmt.Sprintf("the app refused the API key (HTTP %d%s)", res.StatusCode, reason), res.StatusCode, "auth"}
		case 400:
			return FeedPage{}, &FeedError{"the app rejected the cursor (HTTP 400" + reason + ")", 400, "bad_cursor"}
		}
		return FeedPage{}, &FeedError{fmt.Sprintf("the app answered HTTP %d%s", res.StatusCode, reason), res.StatusCode, "upstream"}
	}
	var parsed any
	if readErr == nil {
		parsed, readErr = jsonx.Parse(body)
	}
	if readErr != nil {
		return FeedPage{}, &FeedError{"the feed didn't return JSON", res.StatusCode, "bad_response"}
	}
	bad := &FeedError{"the feed's response doesn't match the changes contract", res.StatusCode, "bad_response"}
	page, ok := parsed.(*jsonx.Object)
	if !ok {
		return FeedPage{}, bad
	}
	itemsV, _ := page.Get("items")
	items, ok := itemsV.([]any)
	hasMoreV, _ := page.Get("hasMore")
	hasMore, okH := hasMoreV.(bool)
	nextV, hasNext := page.Get("nextCursor")
	var next *string
	okN := true
	if hasNext && nextV != nil {
		s, isStr := nextV.(string)
		okN = isStr
		next = &s
	}
	if !ok || !okH || !okN {
		return FeedPage{}, bad
	}
	out := FeedPage{Items: []ChangeItem{}, NextCursor: next, HasMore: hasMore}
	for _, raw := range items {
		it, ok := parseItem(raw)
		if _, valid := dates.JSParse(it.UpdatedAt); ok && valid {
			out.Items = append(out.Items, it)
		} else {
			out.Skipped++
		}
	}
	return out, nil
}

// parseItem is changeItem.safeParse.
func parseItem(raw any) (ChangeItem, bool) {
	o, ok := raw.(*jsonx.Object)
	if !ok {
		return ChangeItem{}, false
	}
	str := func(k string, minLen, maxLen int, def *string) (string, bool) {
		v, present := o.Get(k)
		if !present && def != nil {
			return *def, true
		}
		s, isStr := v.(string)
		n := strx.Len16(s)
		return s, isStr && n >= minLen && n <= maxLen
	}
	var it ChangeItem
	var ok1, ok2, ok3, ok4 bool
	it.Kind, ok1 = str("kind", 1, 64, nil)
	it.ID, ok2 = str("id", 1, 200, nil)
	it.UpdatedAt, ok3 = str("updatedAt", 1, 64, nil)
	empty := ""
	it.URL, ok4 = str("url", 0, 2000, &empty)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return ChangeItem{}, false
	}
	if v, present := o.Get("deleted"); present {
		b, isBool := v.(bool)
		if !isBool {
			return ChangeItem{}, false
		}
		it.Deleted = b
	}
	it.Data = jsonx.NewMap[any]()
	if v, present := o.Get("data"); present && v != nil {
		d, isObj := v.(*jsonx.Object)
		if !isObj {
			return ChangeItem{}, false
		}
		it.Data = d
	}
	return it, true
}

// PullResult is pullChanges' result.
type PullResult struct {
	Pages    int
	Items    int
	Skipped  int
	Cursor   *string
	CaughtUp bool
}

// PullOptions are pullChanges' options; MaxPages 0 is MaxPagesPerRun.
type PullOptions struct {
	FetchOptions
	MaxPages int
}

// PullChanges is pullChanges: pages from the cursor until hasMore is false
// or the page cap. onPage stores a page and the cursor after it together; a
// FeedError stops the loop with the cursor where the last stored page left
// it.
func PullChanges(ctx context.Context, do connectors.Do, o PullOptions, onPage func(FeedPage, *string) error) (PullResult, error) {
	maxPages := o.MaxPages
	if maxPages == 0 {
		maxPages = MaxPagesPerRun
	}
	cursor := o.Cursor
	out := PullResult{Cursor: cursor}
	for out.Pages < maxPages {
		fo := o.FetchOptions
		fo.Cursor = cursor
		p, err := FetchPage(ctx, do, fo)
		if err != nil {
			return out, err
		}
		// An empty page echoes the cursor; a missing nextCursor keeps ours.
		next := p.NextCursor
		if next == nil {
			next = cursor
		}
		if err := onPage(p, next); err != nil {
			return out, err
		}
		out.Pages++
		out.Items += len(p.Items)
		out.Skipped += p.Skipped
		moved := !sameCursor(next, cursor)
		cursor = next
		out.Cursor = cursor
		if !p.HasMore {
			out.CaughtUp = true
			break
		}
		// A feed that says "more" but doesn't move would loop forever.
		if !moved {
			break
		}
	}
	return out, nil
}

func sameCursor(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
