package connectors_test

// connectors.test.ts "POST /v1/connectors/gmail/import": both cases, over
// real HTTP to the httpapi handler.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
)

func (w *world) importRoute(t *testing.T) func(key string, body map[string]any) (int, map[string]any) {
	srv := httptest.NewServer((&httpapi.Server{DB: w.d}).Handler())
	t.Cleanup(srv.Close)
	return func(key string, body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/v1/connectors/gmail/import", bytes.NewReader(b))
		req.Header.Set("authorization", "Bearer "+key)
		req.Header.Set("content-type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
}

func (w *world) key(t *testing.T, ws, name string, createdBy *string) string {
	k, _, err := auth.CreateAPIKey(w.d, ws, name, createdBy)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestImportRouteUsesKeyCreatorsConnection(t *testing.T) {
	w := setup(t)
	w.connect(t, "zane", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	post := w.importRoute(t)
	zane := w.ids["zane"]
	key := w.key(t, w.a, "zane agent", &zane)
	status, out := post(key, map[string]any{"message_id": ticket, "container": "inbox"})
	if status != 202 || out["status"] != "queued" || out["sourceId"] != "gmail:"+ticket || out["duplicate"] != false {
		t.Fatal(status, out)
	}
	if status, dup := post(key, map[string]any{"message_id": ticket, "container": "inbox"}); status != 200 || dup["duplicate"] != true {
		t.Fatal(status, dup)
	}
}

func TestImportRouteRefusals(t *testing.T) {
	w := setup(t)
	w.connect(t, "zane", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	post := w.importRoute(t)
	ida, mallory := w.ids["ida"], w.ids["mallory"]
	evaKey := w.key(t, w.a, "ida", &ida)
	if status, _ := post(evaKey, map[string]any{"message_id": ticket}); status != 409 {
		t.Fatal(status)
	}
	if status, _ := post(w.key(t, w.a, "legacy", nil), map[string]any{"message_id": ticket}); status != 403 {
		t.Fatal(status)
	}
	if status, _ := post(w.key(t, w.b, "b", &mallory), map[string]any{"message_id": ticket}); status != 409 {
		t.Fatal(status)
	}
	if status, _ := post(evaKey, map[string]any{"message_id": "../x"}); status != 400 {
		t.Fatal(status)
	}
	if n := w.count(t, "documents"); n != 0 {
		t.Fatal(n)
	}
}
