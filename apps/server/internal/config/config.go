// Package config reads the environment the way src/server/config.ts does.
// Load reads it on every call, so tests can change the environment first.
package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/whatwg"
)

// Config is config() in the TS build, field for field and in its key order.
type Config struct {
	DataDir   string `json:"dataDir"`
	DBPath    string `json:"dbPath"`
	ModelsDir string `json:"modelsDir"`
	// Where the embedding model runs: "child" frees its memory between bursts
	// of work (production), "inline" keeps it in this process.
	EmbedMode string `json:"embedMode"`
	// How long the embedding child may sit idle before it's stopped.
	EmbedIdleMs        jsonx.Num `json:"embedIdleMs"`
	GatewayURL         string    `json:"gatewayUrl"`
	GatewayKey         string    `json:"gatewayKey"`
	ExtractModel       string    `json:"extractModel"`
	FallbackModel      string    `json:"fallbackModel"`
	DefaultContainer   string    `json:"defaultContainer"`
	AdminPasswordHash  string    `json:"adminPasswordHash"`
	SessionSecret      string    `json:"sessionSecret"`
	URL                string    `json:"url"`
	PasskeyRpID        string    `json:"passkeyRpId"`
	AuthRateLimit      bool      `json:"authRateLimit"`
	JobsEnabled        bool      `json:"jobsEnabled"`
	GoogleClientID     string    `json:"googleClientId"`
	GoogleClientSecret string    `json:"googleClientSecret"`
	ConnectorSecret    string    `json:"connectorSecret"`
	GmailFake          bool      `json:"gmailFake"`
	ResendAPIKey       string    `json:"resendApiKey"`
	EmailFrom          string    `json:"emailFrom"`
}

// DevMode is true only in `recall dev serve` (the TS build's
// import.meta.env.DEV, which is false in production builds and under tsx).
var DevMode bool

func env(k string) (string, bool) { return os.LookupEnv(k) }

// or is JS `a || b` for strings: an unset or empty value takes b.
func or(k, fallback string) string {
	if v, ok := env(k); ok && v != "" {
		return v
	}
	return fallback
}

// nullish is JS `a ?? b`: only an unset value takes b.
func nullish(k, fallback string) string {
	if v, ok := env(k); ok {
		return v
	}
	return fallback
}

func trimRightSlashes(s string) string { return strings.TrimRight(s, "/") }

// Load returns the configuration from the current environment.
func Load() Config {
	u := trimRightSlashes(or("RECALL_URL", "http://localhost:4800"))
	dataDir := nullish("DATA_DIR", "")
	if _, ok := env("DATA_DIR"); !ok {
		dataDir = resolve("data")
	}
	c := Config{
		DataDir: dataDir,
	}
	if v, ok := env("RECALL_DB_PATH"); ok {
		c.DBPath = v
	} else {
		c.DBPath = filepath.Join(dataDir, "recall.db")
	}
	if v, ok := env("MODELS_DIR"); ok {
		c.ModelsDir = v
	} else {
		c.ModelsDir = filepath.Join(dataDir, "models")
	}
	switch e, _ := env("RECALL_EMBED"); e {
	case "child", "inline":
		c.EmbedMode = e
	default:
		if v, _ := env("NODE_ENV"); v == "production" {
			c.EmbedMode = "child"
		} else {
			c.EmbedMode = "inline"
		}
	}
	idle, _ := env("RECALL_EMBED_IDLE_MS")
	idleMs := strx.Number(idle)
	if idleMs == 0 || math.IsNaN(idleMs) {
		idleMs = 10 * 60_000
	}
	c.EmbedIdleMs = jsonx.Num(idleMs)
	c.GatewayURL = trimRightSlashes(nullish("GATEWAY_URL", ""))
	c.GatewayKey = nullish("GATEWAY_KEY", "")
	c.ExtractModel = or("EXTRACT_MODEL", "claude-haiku-5-5")
	c.FallbackModel = or("FALLBACK_MODEL", "claude-haiku-5-5")
	c.DefaultContainer = or("RECALL_DEFAULT_CONTAINER", "default")
	c.AdminPasswordHash = nullish("ADMIN_PASSWORD_HASH", "")
	c.SessionSecret = nullish("SESSION_SECRET", "")
	c.URL = u
	if v := or("PASSKEY_RP_ID", ""); v != "" {
		c.PasskeyRpID = v
	} else {
		c.PasskeyRpID = Hostname(u)
	}
	authRL, _ := env("RECALL_AUTH_RATE_LIMIT")
	c.AuthRateLimit = authRL != "off"
	jobs, _ := env("RECALL_JOBS")
	c.JobsEnabled = jobs != "off"
	c.GoogleClientID = trimmed("GOOGLE_CLIENT_ID")
	c.GoogleClientSecret = trimmed("GOOGLE_CLIENT_SECRET")
	c.ConnectorSecret = trimmed("CONNECTOR_SECRET")
	fake, _ := env("RECALL_GMAIL_FAKE")
	c.GmailFake = DevMode && fake == "1"
	c.ResendAPIKey = nullish("RESEND_API_KEY", "")
	c.EmailFrom = nullish("RECALL_EMAIL_FROM", "")
	return c
}

// process.env.X?.trim() ?? ""
func trimmed(k string) string {
	v, _ := env(k)
	return strx.Trim(v)
}

// resolve is path.resolve(p) against the working directory.
func resolve(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	return filepath.Join(wd, p)
}

// Hostname is new URL(u).hostname (WHATWG: IDNA, numeric IPv4 forms, IPv6
// compressed in brackets). A URL new URL() refuses gives "" (the TS build
// throws there, which fails every request).
func Hostname(u string) string {
	p, err := whatwg.ParseURL(u)
	if err != nil {
		return ""
	}
	return p.Host
}
