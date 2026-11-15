// Package connectors holds the Gmail and app connectors (src/server/connectors).
package connectors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Connector secrets (OAuth tokens, the OAuth state cookie) are sealed with
// AES-256-GCM under a key derived from CONNECTOR_SECRET with HKDF-SHA256. Each
// value is bound to a context string (the row it belongs to) as AAD, so a
// ciphertext copied into another row won't open. Text form
// "v1.<iv>.<ciphertext>.<tag>", base64url without padding (crypto.ts).

// MinSecretLength is the shortest CONNECTOR_SECRET accepted, in UTF-16 units.
const MinSecretLength = 32

const sealVersion = "v1"

// SealError is crypto.ts's SealError.
type SealError struct{ Msg string }

func (e *SealError) Error() string { return e.Msg }

var (
	keyMu sync.Mutex
	keys  = map[string][]byte{}
)

func deriveKey(secret string) []byte {
	keyMu.Lock()
	defer keyMu.Unlock()
	if k, ok := keys[secret]; ok {
		return k
	}
	k, err := hkdf.Key(sha256.New, []byte(secret), []byte("recall-connectors"), "connector-tokens-v1", 32)
	if err != nil {
		panic(err)
	}
	keys[secret] = k
	return k
}

func gcm(key []byte, nonceSize int) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, nonceSize)
}

// Seal encrypts plaintext for context.
func Seal(secret, plaintext, context string) (string, error) {
	if strx.Len16(secret) < MinSecretLength {
		return "", &SealError{"CONNECTOR_SECRET is too short"}
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	aead, err := gcm(deriveKey(secret), 12)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, iv, []byte(plaintext), []byte(context))
	body, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	enc := base64.RawURLEncoding
	return strings.Join([]string{sealVersion, enc.EncodeToString(iv), enc.EncodeToString(body), enc.EncodeToString(tag)}, "."), nil
}

// Unseal decrypts a sealed value. It returns a *SealError on a wrong key, a
// wrong context or a tampered value.
func Unseal(secret, sealed, context string) (string, error) {
	parts := strings.Split(sealed, ".")
	get := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	version, iv, body, tag := get(0), get(1), get(2), get(3)
	if version != sealVersion || iv == "" || body == "" || tag == "" {
		return "", &SealError{"unreadable sealed value"}
	}
	fail := &SealError{"sealed value didn't open (wrong CONNECTOR_SECRET or tampered)"}
	ivb := strx.DecodeBase64Node(iv)
	tagb := strx.DecodeBase64Node(tag)
	if len(ivb) == 0 || len(tagb) != 16 {
		return "", fail
	}
	aead, err := gcm(deriveKey(secret), len(ivb))
	if err != nil {
		return "", fail
	}
	ct := append(strx.DecodeBase64Node(body), tagb...)
	pt, err := aead.Open(nil, ivb, ct, []byte(context))
	if err != nil {
		return "", fail
	}
	return strx.DecodeUTF8(pt), nil
}
