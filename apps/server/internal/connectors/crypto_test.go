package connectors_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type sealGolden struct {
	Seal struct {
		Secret string `json:"secret"`
		Cases  []struct {
			Plaintext string `json:"plaintext"`
			Context   string `json:"context"`
			Sealed    string `json:"sealed"`
		} `json:"cases"`
		Unseal []struct {
			Secret  string `json:"secret"`
			Sealed  string `json:"sealed"`
			Context string `json:"context"`
			Result  struct {
				OK    *string `json:"ok"`
				Error string  `json:"error"`
				Name  string  `json:"name"`
			} `json:"result"`
		} `json:"unseal"`
		Short struct {
			Error string `json:"error"`
		} `json:"short"`
	} `json:"seal"`
}

// Values the TS build sealed open here, and every failure gives the TS
// error text.
func TestUnsealTSValues(t *testing.T) {
	var g sealGolden
	testutil.LoadJSON(t, "m1.json", &g)
	if len(g.Seal.Unseal) < 10 {
		t.Fatal("golden incomplete")
	}
	for i, c := range g.Seal.Cases {
		got, err := connectors.Unseal(g.Seal.Secret, c.Sealed, c.Context)
		if c.Plaintext == "" {
			// An empty plaintext seals to an empty body, which unseal refuses.
			var se *connectors.SealError
			if !errors.As(err, &se) || se.Msg != "unreadable sealed value" {
				t.Errorf("case %d: %v", i, err)
			}
			continue
		}
		if err != nil || got != c.Plaintext {
			t.Errorf("case %d: %q %v", i, got, err)
		}
	}
	for i, c := range g.Seal.Unseal {
		got, err := connectors.Unseal(c.Secret, c.Sealed, c.Context)
		parts := strings.Split(c.Sealed, ".")
		if c.Result.OK != nil && len(parts) == 4 && len(parts[3]) < 22 {
			// A truncated GCM tag opens in Node (with a deprecation warning).
			// Go refuses it on purpose (Known gaps, M1).
			var se *connectors.SealError
			if !errors.As(err, &se) {
				t.Errorf("unseal %d: a truncated tag opened: %q", i, got)
			}
			continue
		}
		if c.Result.OK != nil {
			if err != nil || got != *c.Result.OK {
				t.Errorf("unseal %d: %q %v, want %q", i, got, err, *c.Result.OK)
			}
			continue
		}
		var se *connectors.SealError
		if !errors.As(err, &se) || se.Msg != c.Result.Error || c.Result.Name != "SealError" {
			t.Errorf("unseal %d: %v, want %q", i, err, c.Result.Error)
		}
	}
	_, err := connectors.Seal("short", "x", "y")
	if err == nil || err.Error() != g.Seal.Short.Error {
		t.Fatalf("short secret: %v", err)
	}
}

// Go seals values the TS build opens (testdata/gen/verify-go-m1.ts reads the
// file RECALL_M1_SEAL_OUT names), and they round-trip here.
func TestSealRoundTrip(t *testing.T) {
	var g sealGolden
	testutil.LoadJSON(t, "m1.json", &g)
	type out struct {
		Plaintext string `json:"plaintext"`
		Context   string `json:"context"`
		Sealed    string `json:"sealed"`
	}
	var outs []out
	for _, c := range g.Seal.Cases {
		s, err := connectors.Seal(g.Seal.Secret, c.Plaintext, c.Context)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(s, ".")
		if len(parts) != 4 || parts[0] != "v1" || len(parts[1]) != 16 || len(parts[3]) != 22 {
			t.Fatalf("form %q", s)
		}
		if c.Plaintext != "" {
			if got, err := connectors.Unseal(g.Seal.Secret, s, c.Context); err != nil || got != c.Plaintext {
				t.Fatalf("round trip %q: %q %v", c.Plaintext, got, err)
			}
			if _, err := connectors.Unseal(g.Seal.Secret, s, c.Context+"x"); err == nil {
				t.Fatal("opened with another context")
			}
		}
		outs = append(outs, out{c.Plaintext, c.Context, s})
	}
	if p := os.Getenv("RECALL_M1_SEAL_OUT"); p != "" {
		if err := os.WriteFile(p, []byte(jsonx.Stringify(map[string]any{"secret": g.Seal.Secret, "cases": outs})), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
