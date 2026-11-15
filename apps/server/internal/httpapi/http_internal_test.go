package httpapi

// http.test.ts: withKey.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

func callWithKey(t *testing.T, h func(c *keyCtx) error) *httptest.ResponseRecorder {
	t.Helper()
	d := testkit.OpenDB(t)
	key, _, err := auth.CreateAPIKey(d, "ws_http", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: d, Now: func() int64 { return 1_800_000_000_000 }}
	req := httptest.NewRequest("GET", "/v1/x", nil)
	req.Header.Set("authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.withKey(h)(rec, req)
	return rec
}

func TestUnexpectedErrorIsGeneric500(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	rec := callWithKey(t, func(*keyCtx) error {
		return errors.New("SQLITE_ERROR: no such table: secret_things at /data/recall.db")
	})
	var body any
	json.Unmarshal(rec.Body.Bytes(), &body)
	want := map[string]any{"error": map[string]any{"type": "internal", "message": "internal error"}}
	if rec.Code != 500 || !reflect.DeepEqual(body, want) || strings.Contains(rec.Body.String(), "secret_things") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !strings.Contains(logged.String(), "[recall] api error: SQLITE_ERROR: no such table: secret_things") {
		t.Fatal(logged.String())
	}
}

func TestHttpErrorPassesThrough(t *testing.T) {
	rec := callWithKey(t, func(*keyCtx) error { return httpx.New(409, "conflict", "already there") })
	if rec.Code != 409 || strings.TrimSpace(rec.Body.String()) != `{"error":{"type":"conflict","message":"already there"}}` {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestHttpErrorHeaders(t *testing.T) {
	rec := callWithKey(t, func(*keyCtx) error {
		return httpx.WithHeaders(429, "rate_limited", "slow down", map[string]string{"retry-after": "30"})
	})
	if rec.Code != 429 || rec.Header().Get("retry-after") != "30" {
		t.Fatal(rec.Code, rec.Header())
	}
}
