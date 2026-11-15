// Package authtest is a software passkey authenticator for tests (ES256,
// attestation "none"), the Go twin of testdata/gen/passkeys.ts.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
)

// Authenticator holds one credential.
type Authenticator struct {
	Key        *ecdsa.PrivateKey
	CredID     []byte
	Flags      byte
	Counter    uint32
	AAGUID     [16]byte
	Transports []string
	RPID       string
	Origin     string
}

// New is a single-device ES256 authenticator (flags UP|UV|AT) for rpID and origin.
func New(rpID, origin string) *Authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Authenticator{Key: key, CredID: id, Flags: 0x45, Transports: []string{"internal"}, RPID: rpID, Origin: origin}
}

// ID is the credential id as the browser sends it.
func (a *Authenticator) ID() string { return b64(a.CredID) }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func head(major byte, n int) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 0x100:
		return []byte{major<<5 | 24, byte(n)}
	default:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
}

func cborInt(n int) []byte {
	if n >= 0 {
		return head(0, n)
	}
	return head(1, -1-n)
}

func cborBytes(b []byte) []byte { return append(head(2, len(b)), b...) }
func cborText(s string) []byte  { return append(head(3, len(s)), s...) }
func cborMap(n int) []byte      { return head(5, n) }
func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func (a *Authenticator) cose() []byte {
	x := a.Key.PublicKey.X.FillBytes(make([]byte, 32))
	y := a.Key.PublicKey.Y.FillBytes(make([]byte, 32))
	return cat(cborMap(5), cborInt(1), cborInt(2), cborInt(3), cborInt(-7), cborInt(-1), cborInt(1), cborInt(-2), cborBytes(x), cborInt(-3), cborBytes(y))
}

func counter(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

func (a *Authenticator) rpHash() []byte {
	h := sha256.Sum256([]byte(a.RPID))
	return h[:]
}

// Attestation is a registration response for challenge (base64url).
func (a *Authenticator) Attestation(challenge string) json.RawMessage {
	client, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": challenge, "origin": a.Origin, "crossOrigin": false})
	idLen := make([]byte, 2)
	binary.BigEndian.PutUint16(idLen, uint16(len(a.CredID)))
	authData := cat(a.rpHash(), []byte{a.Flags}, counter(a.Counter), a.AAGUID[:], idLen, a.CredID, a.cose())
	att := cat(cborMap(3), cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(0), cborText("authData"), cborBytes(authData))
	transports := a.Transports
	if transports == nil {
		transports = []string{}
	}
	out, _ := json.Marshal(map[string]any{
		"id": a.ID(), "rawId": a.ID(), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(client), "attestationObject": b64(att), "transports": transports},
		"clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform",
	})
	return out
}

// Assertion is a login response for challenge; a non-zero counter goes up first.
func (a *Authenticator) Assertion(challenge string) json.RawMessage {
	if a.Counter > 0 {
		a.Counter++
	}
	client, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": a.Origin, "crossOrigin": false})
	authData := cat(a.rpHash(), []byte{a.Flags &^ 0x40}, counter(a.Counter))
	sum := sha256.Sum256(client)
	digest := sha256.Sum256(cat(authData, sum[:]))
	sig, err := ecdsa.SignASN1(rand.Reader, a.Key, digest[:])
	if err != nil {
		panic(err)
	}
	out, _ := json.Marshal(map[string]any{
		"id": a.ID(), "rawId": a.ID(), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(client), "authenticatorData": b64(authData), "signature": b64(sig), "userHandle": b64([]byte("handle"))},
		"clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform",
	})
	return out
}
