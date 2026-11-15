package auth_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/auth/authtest"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

const origin = "https://recall.example.com"

// T0 is the tests' clock (ms).
const T0 int64 = 1_800_000_000_000

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// signedIn is a user with a fresh session.
func signedIn(t *testing.T, d *db.DB, at int64) *auth.Viewer {
	t.Helper()
	id := auth.RandomString(32, "")
	_, err := d.Exec(`INSERT INTO user (id, name, email, email_verified, username, created_at, updated_at) VALUES (?, 'zaki', ?, 0, 'zaki', ?, ?)`, id, id+"@users.recall.invalid", at, at)
	must(t, err)
	token, _, err := auth.CreateSession(d, id, "", "", at)
	must(t, err)
	f, _ := auth.GetSession(d, token, false, at)
	return f.Viewer()
}

func challengeOf(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	var o struct {
		Challenge string `json:"challenge"`
	}
	must(t, json.Unmarshal(b, &o))
	return o.Challenge
}

func register(t *testing.T, rp *auth.RP, d *db.DB, v *auth.Viewer, a *authtest.Authenticator, name string, now int64) auth.Passkey {
	t.Helper()
	opts, token, err := rp.RegisterOptions(d, v, now)
	must(t, err)
	pk, err := rp.Register(d, v, token, a.Attestation(challengeOf(t, opts)), name, now)
	must(t, err)
	return pk
}

func TestPasskeyRegisterThenSignIn(t *testing.T) {
	d := testkit.OpenDB(t)
	rp, err := auth.NewRP(origin, "")
	must(t, err)
	v := signedIn(t, d, T0)
	a := authtest.New("recall.example.com", origin)
	a.Counter = 7

	opts, token, err := rp.RegisterOptions(d, v, T0)
	must(t, err)
	b, _ := json.Marshal(opts)
	var o map[string]any
	json.Unmarshal(b, &o)
	rpInfo := o["rp"].(map[string]any)
	sel := o["authenticatorSelection"].(map[string]any)
	if rpInfo["id"] != "recall.example.com" || rpInfo["name"] != "Recall" || o["attestation"] != "none" || sel["residentKey"] != "preferred" || sel["userVerification"] != "preferred" {
		t.Fatalf("options: %s", b)
	}
	if params := o["pubKeyCredParams"].([]any); len(params) != 3 {
		t.Fatalf("params: %v", params)
	}
	pk, err := rp.Register(d, v, token, a.Attestation(challengeOf(t, opts)), "  Laptop  ", T0)
	must(t, err)
	if *pk.Name != "Laptop" || pk.UserID != v.User.ID || pk.Counter != 7 || pk.DeviceType != "singleDevice" || pk.BackedUp || *pk.Transports != "internal" || *pk.AAGUID != "00000000-0000-0000-0000-000000000000" || *pk.CreatedAt != T0 {
		t.Fatalf("%+v", pk)
	}
	// The ceremony is single use.
	if _, err := rp.Register(d, v, token, a.Attestation(challengeOf(t, opts)), "", T0); !errors.Is(err, auth.ErrChallengeNotFound) {
		t.Fatalf("reuse: %v", err)
	}

	// Sign in from a fresh browser: a discoverable login.
	req, token2, err := rp.AuthenticateOptions(d, nil, T0+1000)
	must(t, err)
	b, _ = json.Marshal(req)
	if json.Unmarshal(b, &o); o["allowCredentials"] != nil || o["userVerification"] != "preferred" || o["rpId"] != "recall.example.com" {
		t.Fatalf("request options: %s", b)
	}
	uid, err := rp.Authenticate(d, token2, a.Assertion(challengeOf(t, req)), T0+1000)
	must(t, err)
	if uid != v.User.ID {
		t.Fatal(uid)
	}
	var counter int64
	must(t, d.QueryRow("SELECT counter FROM passkey").Scan(&counter))
	if counter != 8 {
		t.Fatalf("counter %d", counter)
	}
	// A replayed assertion: the ceremony is gone.
	if _, err := rp.Authenticate(d, token2, a.Assertion(challengeOf(t, req)), T0+1000); !errors.Is(err, auth.ErrChallengeNotFound) {
		t.Fatalf("replay: %v", err)
	}
	// A counter that went back is refused (simplewebauthn's rule).
	req, token3, _ := rp.AuthenticateOptions(d, v, T0+2000)
	b, _ = json.Marshal(req)
	if json.Unmarshal(b, &o); len(o["allowCredentials"].([]any)) != 1 {
		t.Fatalf("signed in: no allow list: %s", b)
	}
	a.Counter = 3
	if _, err := rp.Authenticate(d, token3, a.Assertion(challengeOf(t, req)), T0+2000); !errors.Is(err, auth.ErrAuthFailed) {
		t.Fatalf("counter: %v", err)
	}
	// An unknown credential.
	other := authtest.New("recall.example.com", origin)
	req, token4, _ := rp.AuthenticateOptions(d, nil, T0+3000)
	if _, err := rp.Authenticate(d, token4, other.Assertion(challengeOf(t, req)), T0+3000); !errors.Is(err, auth.ErrPasskeyNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// An expired ceremony.
	req, token5, _ := rp.AuthenticateOptions(d, nil, T0)
	a.Counter = 20
	if _, err := rp.Authenticate(d, token5, a.Assertion(challengeOf(t, req)), T0+auth.ChallengeTTLMs+1); !errors.Is(err, auth.ErrChallengeNotFound) {
		t.Fatalf("expired: %v", err)
	}
	// A login ceremony can't finish a registration.
	req, token6, _ := rp.AuthenticateOptions(d, nil, T0)
	if _, err := rp.Register(d, v, token6, a.Attestation(challengeOf(t, req)), "", T0); !errors.Is(err, auth.ErrChallengeNotFound) {
		t.Fatalf("cross ceremony: %v", err)
	}
}

func TestRegistrationNeedsAFreshSessionOfTheSameUser(t *testing.T) {
	d := testkit.OpenDB(t)
	rp, _ := auth.NewRP(origin, "")
	v := signedIn(t, d, T0)
	a := authtest.New("recall.example.com", origin)
	opts, token, _ := rp.RegisterOptions(d, v, T0)
	stale := *v
	if _, err := rp.Register(d, &stale, token, a.Attestation(challengeOf(t, opts)), "", T0+auth.SessionFreshAgeMs); !errors.Is(err, auth.ErrSessionNotFresh) {
		t.Fatalf("stale: %v", err)
	}
	other := *v
	other.User.ID = "someone-else"
	if _, err := rp.Register(d, &other, token, a.Attestation(challengeOf(t, opts)), "", T0); !errors.Is(err, auth.ErrNotAllowedToRegister) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := rp.Register(d, nil, token, nil, "", T0); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("no session: %v", err)
	}
	// A wrong origin in the client data fails verification.
	opts, token, _ = rp.RegisterOptions(d, v, T0)
	evil := authtest.New("recall.example.com", "https://evil.example")
	if _, err := rp.Register(d, v, token, evil.Attestation(challengeOf(t, opts)), "", T0); !errors.Is(err, auth.ErrFailedRegistration) {
		t.Fatalf("origin: %v", err)
	}
	// The second passkey excludes the first.
	register(t, rp, d, v, a, "one", T0)
	opts, _, _ = rp.RegisterOptions(d, v, T0)
	b, _ := json.Marshal(opts)
	var o map[string]any
	json.Unmarshal(b, &o)
	if ex, _ := o["excludeCredentials"].([]any); len(ex) != 1 {
		t.Fatalf("exclusions: %s", b)
	}
}

// An IP address can't be a WebAuthn relying party id: NewRP still succeeds,
// with passkeys off, and every ceremony returns ErrPasskeysOff.
func TestNewRPWithAnIPHostTurnsPasskeysOff(t *testing.T) {
	for _, appURL := range []string{"http://127.0.0.1:4895", "http://[::1]:4895", "https://10.0.0.5"} {
		rp, err := auth.NewRP(appURL, "")
		must(t, err)
		if rp.WA != nil || rp.Off == "" || !strings.Contains(rp.Off, "IP address") {
			t.Fatalf("%s: %+v", appURL, rp)
		}
		d := testkit.OpenDB(t)
		v := signedIn(t, d, T0)
		if _, _, err := rp.RegisterOptions(d, v, T0); !errors.Is(err, auth.ErrPasskeysOff) {
			t.Fatalf("%s RegisterOptions: %v", appURL, err)
		}
		if _, err := rp.Register(d, v, "tok", json.RawMessage(`{}`), "x", T0); !errors.Is(err, auth.ErrPasskeysOff) {
			t.Fatalf("%s Register: %v", appURL, err)
		}
		if _, _, err := rp.AuthenticateOptions(d, nil, T0); !errors.Is(err, auth.ErrPasskeysOff) {
			t.Fatalf("%s AuthenticateOptions: %v", appURL, err)
		}
		if _, err := rp.Authenticate(d, "tok", json.RawMessage(`{}`), T0); !errors.Is(err, auth.ErrPasskeysOff) {
			t.Fatalf("%s Authenticate: %v", appURL, err)
		}
	}
	// Host names keep passkeys on; a broken URL is still an error.
	for _, appURL := range []string{"http://localhost:4890", origin} {
		rp, err := auth.NewRP(appURL, "")
		must(t, err)
		if rp.WA == nil || rp.Off != "" {
			t.Fatalf("%s: %+v", appURL, rp)
		}
	}
	if _, err := auth.NewRP("not a url", ""); err == nil {
		t.Fatal("want an error")
	}
}
