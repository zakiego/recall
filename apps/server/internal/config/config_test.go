package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

var envKeys = []string{
	"RECALL_URL", "DATA_DIR", "RECALL_DB_PATH", "MODELS_DIR", "RECALL_EMBED", "NODE_ENV", "RECALL_EMBED_IDLE_MS",
	"GATEWAY_URL", "GATEWAY_KEY", "EXTRACT_MODEL", "FALLBACK_MODEL", "RECALL_DEFAULT_CONTAINER", "ADMIN_PASSWORD_HASH",
	"SESSION_SECRET", "PASSKEY_RP_ID", "RECALL_AUTH_RATE_LIMIT", "RECALL_JOBS", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	"CONNECTOR_SECRET", "RECALL_GMAIL_FAKE", "RESEND_API_KEY", "RECALL_EMAIL_FROM",
}

// config() of the TS build for each environment in the golden. Paths that
// depend on the working directory are compared after swapping the TS cwd for
// this test's.
func TestLoadMatchesTheTSConfig(t *testing.T) {
	var g struct {
		Config []struct {
			Env    map[string]string `json:"env"`
			Cwd    string            `json:"cwd"`
			Config json.RawMessage   `json:"config"`
		} `json:"config"`
	}
	testutil.LoadJSON(t, "m1.json", &g)
	if len(g.Config) < 10 {
		t.Fatalf("%d config cases", len(g.Config))
	}
	wd, _ := os.Getwd()
	for i, c := range g.Config {
		for _, k := range envKeys {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
		for k, v := range c.Env {
			t.Setenv(k, v)
		}
		got := jsonx.Stringify(config.Load())
		want, err := jsonx.Parse(c.Config)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.ReplaceAll(jsonx.Stringify(want), c.Cwd, wd)
		if got != w {
			t.Errorf("case %d %v:\n got %s\nwant %s", i, c.Env, got, w)
		}
	}
}

func TestGmailFakeNeedsDevMode(t *testing.T) {
	t.Setenv("RECALL_GMAIL_FAKE", "1")
	if config.Load().GmailFake {
		t.Fatal("gmailFake on outside dev mode")
	}
	config.DevMode = true
	defer func() { config.DevMode = false }()
	if !config.Load().GmailFake {
		t.Fatal("gmailFake off in dev mode")
	}
}
