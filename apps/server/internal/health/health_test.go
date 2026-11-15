package health_test

import (
	"testing"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/health"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type resp struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// health.test.ts: ok on a database this build migrated, 503 when a newer
// build migrated it. Bodies byte for byte.
func TestHealthMatchesTS(t *testing.T) {
	var g struct {
		Health struct {
			Fresh resp `json:"fresh"`
			Newer resp `json:"newer"`
		} `json:"health"`
	}
	testutil.LoadJSON(t, "m1.json", &g)
	d := db.Memory()
	defer d.Close()
	status, body, err := health.Check(d)
	if err != nil || status != g.Health.Fresh.Status || string(body) != g.Health.Fresh.Body {
		t.Fatalf("fresh: %d %s %v", status, body, err)
	}
	if _, err := d.Exec("INSERT INTO _migrations (name, applied_at) VALUES ('0099_from_the_future', 1), ('0098_also', 2)"); err != nil {
		t.Fatal(err)
	}
	status, body, err = health.Check(d)
	if err != nil || status != g.Health.Newer.Status || string(body) != g.Health.Newer.Body {
		t.Fatalf("newer: %d %s %v", status, body, err)
	}
}
