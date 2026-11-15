package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// New passwords use Better Auth's scrypt (@better-auth/utils/password on Node:
// N 16384, r 16, p 1, 64-byte key, the password NFKC-normalized, the salt the
// 32-character hex string itself, stored as "<salt>:<key hex>"). The pbkdf2
// form is what `cli hash-password` wrote for ADMIN_PASSWORD_HASH before users
// existed, and it still verifies.

const (
	pbkdf2Iterations = 100_000
	pbkdf2KeyLen     = 32
	// MinPasswordLength and MaxPasswordLength bound new passwords.
	MinPasswordLength = 12
	MaxPasswordLength = 256
)

// HashLegacyPassword is "pbkdf2$sha256$100000$<salt b64>$<hash b64>".
func HashLegacyPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return "pbkdf2$sha256$" + strconv.Itoa(pbkdf2Iterations) + "$" + base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(key), nil
}

// VerifyLegacyPassword checks a pbkdf2 hash.
func VerifyLegacyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iterations := strx.Number(parts[2])
	if math.IsNaN(iterations) || iterations != math.Trunc(iterations) || iterations < 1 || iterations > math.MaxInt32 {
		return false
	}
	salt := strx.DecodeBase64Node(parts[3])
	expected := strx.DecodeBase64Node(parts[4])
	if len(salt) == 0 || len(expected) == 0 {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, password, salt, int(iterations), len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// IsLegacyHash reports a pbkdf2 hash.
func IsLegacyHash(stored string) bool { return strings.HasPrefix(stored, "pbkdf2$") }

func scryptKey(password, salt string) ([]byte, error) {
	return scrypt.Key([]byte(strx.NFKC(password)), []byte(salt), 16384, 16, 1, 64)
}

// HashPassword is Better Auth's hashPassword.
func HashPassword(password string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(b)
	key, err := scryptKey(password, salt)
	if err != nil {
		return "", err
	}
	return salt + ":" + hex.EncodeToString(key), nil
}

// VerifyPassword checks either form. Anything unreadable is false.
func VerifyPassword(hash, password string) bool {
	if IsLegacyHash(hash) {
		return VerifyLegacyPassword(password, hash)
	}
	parts := strings.Split(hash, ":")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	key, err := scryptKey(password, parts[0])
	if err != nil {
		return false
	}
	return hex.EncodeToString(key) == parts[1]
}
