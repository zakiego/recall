// Package health is GET /health (health.ts): no auth. The compose healthcheck
// and deploy.sh read its status, so anything that makes the app unsafe to
// serve must turn it non-200.
package health

import (
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

type okBody struct {
	OK  bool   `json:"ok"`
	Vec string `json:"vec"`
}

type newerBody struct {
	OK                bool     `json:"ok"`
	Error             string   `json:"error"`
	Message           string   `json:"message"`
	UnknownMigrations []string `json:"unknownMigrations"`
}

// Check returns the status code and the JSON body. A database error is
// returned as an error (the TS handler throws, and the server answers 500).
func Check(d *db.DB) (int, []byte, error) {
	unknown, err := db.UnknownMigrations(d, db.Migrations())
	if err != nil {
		return 0, nil, err
	}
	if len(unknown) > 0 {
		b, err := jsonx.Marshal(newerBody{
			OK:    false,
			Error: "db_newer_than_app",
			Message: "The database has migrations this build doesn't know (" + strings.Join(unknown, ", ") +
				"). It was migrated by a newer version; deploy that version again or restore a backup taken before it.",
			UnknownMigrations: unknown,
		})
		return 503, b, err
	}
	v, err := d.VecVersion()
	if err != nil {
		return 0, nil, err
	}
	b, err := jsonx.Marshal(okBody{OK: true, Vec: v})
	return 200, b, err
}
