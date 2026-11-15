package auth_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/zakiego/recall/apps/server/internal/auth"
)

// testdata/passkeys.json comes from testdata/gen/passkeys.ts: software
// authenticators registered through the real Better Auth passkey endpoints.

type goldenRow struct {
	ID           string  `json:"id"`
	Name         *string `json:"name"`
	PublicKey    string  `json:"public_key"`
	UserID       string  `json:"user_id"`
	CredentialID string  `json:"credential_id"`
	Counter      int64   `json:"counter"`
	DeviceType   string  `json:"device_type"`
	BackedUp     int     `json:"backed_up"`
	Transports   *string `json:"transports"`
	CreatedAt    *int64  `json:"created_at"`
	AAGUID       *string `json:"aaguid"`
}

func (r goldenRow) passkey() auth.Passkey {
	return auth.Passkey{ID: r.ID, Name: r.Name, PublicKey: r.PublicKey, UserID: r.UserID, CredentialID: r.CredentialID, Counter: r.Counter, DeviceType: r.DeviceType, BackedUp: r.BackedUp == 1, Transports: r.Transports, CreatedAt: r.CreatedAt, AAGUID: r.AAGUID}
}

type passkeyGolden struct {
	Origin        string `json:"origin"`
	RPID          string `json:"rpId"`
	Registrations []struct {
		Name      string          `json:"name"`
		Challenge string          `json:"challenge"`
		Response  json.RawMessage `json:"response"`
		Row       goldenRow       `json:"row"`
	} `json:"registrations"`
	TSLogins []struct {
		Name         string `json:"name"`
		CounterAfter int64  `json:"counterAfter"`
	} `json:"tsLogins"`
	Rows       map[string]goldenRow `json:"rows"`
	Assertions []struct {
		Name          string          `json:"name"`
		Case          string          `json:"case"`
		Challenge     string          `json:"challenge"`
		StoredCounter int64           `json:"storedCounter"`
		NewCounter    int64           `json:"newCounter"`
		OK            bool            `json:"ok"`
		Response      json.RawMessage `json:"response"`
	} `json:"assertions"`
}

func loadPasskeyGolden(t *testing.T) passkeyGolden {
	raw, err := os.ReadFile("../../testdata/passkeys.json")
	if err != nil {
		t.Fatal(err)
	}
	var g passkeyGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func params() []protocol.CredentialParameter {
	return []protocol.CredentialParameter{{Type: protocol.PublicKeyCredentialType, Algorithm: -8}, {Type: protocol.PublicKeyCredentialType, Algorithm: -7}, {Type: protocol.PublicKeyCredentialType, Algorithm: -257}}
}

// Registering the same attestation in Go stores the same columns Better Auth did.
func TestGoldenRegistrationWritesBetterAuthEncoding(t *testing.T) {
	g := loadPasskeyGolden(t)
	rp, err := auth.NewRP(g.Origin, g.RPID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Registrations) != 3 {
		t.Fatalf("want 3 registrations, got %d", len(g.Registrations))
	}
	for _, reg := range g.Registrations {
		t.Run(reg.Name, func(t *testing.T) {
			session := webauthn.SessionData{Challenge: reg.Challenge, RelyingPartyID: g.RPID, UserID: []byte("any-random-handle"), UserVerification: protocol.VerificationPreferred, CredParams: params()}
			got, err := rp.VerifyRegistration(session, reg.Response)
			if err != nil {
				t.Fatal(err)
			}
			want := reg.Row
			if got.PublicKey != want.PublicKey || got.CredentialID != want.CredentialID || got.Counter != want.Counter || got.DeviceType != want.DeviceType || got.BackedUp != (want.BackedUp == 1) || *got.Transports != *want.Transports || *got.AAGUID != *want.AAGUID {
				t.Fatalf("row differs:\n got %+v %q %q\nwant %+v", got, *got.Transports, *got.AAGUID, want)
			}
			// A different challenge is refused.
			session.Challenge = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
			if _, err := rp.VerifyRegistration(session, reg.Response); err == nil {
				t.Fatal("verified against the wrong challenge")
			}
		})
	}
}

// Every stored row verifies assertions its authenticator signed, and the
// broken ones fail the way simplewebauthn fails them.
func TestGoldenAssertionsVerifyAgainstBetterAuthRows(t *testing.T) {
	g := loadPasskeyGolden(t)
	rp, err := auth.NewRP(g.Origin, g.RPID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range g.TSLogins {
		if g.Rows[l.Name].Counter != l.CounterAfter {
			t.Fatalf("%s: golden rows out of step", l.Name)
		}
	}
	oks := 0
	for _, a := range g.Assertions {
		t.Run(a.Name+"/"+a.Case, func(t *testing.T) {
			row := g.Rows[a.Name].passkey()
			row.Counter = a.StoredCounter
			session := webauthn.SessionData{Challenge: a.Challenge, RelyingPartyID: g.RPID, UserVerification: protocol.VerificationPreferred}
			counter, err := rp.VerifyAssertion(session, row, nil, a.Response)
			if a.OK {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if counter != a.NewCounter {
					t.Fatalf("counter %d, want %d", counter, a.NewCounter)
				}
				oks++
			} else if err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if oks != 3 {
		t.Fatalf("want 3 good assertions, got %d", oks)
	}
	// With every row of the owner as the allow list, too.
	var all []auth.Passkey
	for _, r := range g.Rows {
		all = append(all, r.passkey())
	}
	a := g.Assertions[0]
	row := g.Rows[a.Name].passkey()
	row.Counter = a.StoredCounter
	var allowed [][]byte
	for _, p := range all {
		c, err := p.Credential()
		if err != nil {
			t.Fatal(err)
		}
		allowed = append(allowed, c.ID)
	}
	session := webauthn.SessionData{Challenge: a.Challenge, RelyingPartyID: g.RPID, UserVerification: protocol.VerificationPreferred, AllowedCredentialIDs: allowed}
	if _, err := rp.VerifyAssertion(session, row, all, a.Response); err != nil {
		t.Fatalf("allow list: %v", err)
	}
}
