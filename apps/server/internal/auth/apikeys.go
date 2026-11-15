// Package auth is auth.ts (API keys and password hashing) and, from M6, the
// sessions, passkeys, workspaces and the OAuth server that replace Better Auth.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Every key belongs to one workspace and can only reach that workspace's rows.

// KeyPrefix starts every API key.
const KeyPrefix = "rcl_"

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Sha256 is the hex SHA-256 of s.
func Sha256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// RandomToken is n random bytes, each as base62[b % 62] (the modulo bias is
// kept on purpose: the TS build makes keys this way).
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return TokenFromBytes(b)
}

// TokenFromBytes is RandomToken with the bytes given.
func TokenFromBytes(b []byte) string {
	out := make([]byte, len(b))
	for i, x := range b {
		out[i] = base62[x%62]
	}
	return string(out)
}

// GenerateAPIKey is "rcl_" plus 32 base62 characters.
func GenerateAPIKey() string { return KeyPrefix + RandomToken(32) }

// APIKey is a row of api_keys, in schema.ts field order.
type APIKey struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspaceId"`
	Name        string  `json:"name"`
	KeyHash     string  `json:"keyHash"`
	Prefix      string  `json:"prefix"`
	CreatedAt   int64   `json:"createdAt"`
	LastUsedAt  *int64  `json:"lastUsedAt"`
	RevokedAt   *int64  `json:"revokedAt"`
	CreatedBy   *string `json:"createdBy"`
}

const apiKeyCols = "id, workspace_id, name, key_hash, prefix, created_at, last_used_at, revoked_at, created_by"

func scanAPIKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var k APIKey
	var lastUsed, revoked sql.NullInt64
	var createdBy sql.NullString
	if err := row.Scan(&k.ID, &k.WorkspaceID, &k.Name, &k.KeyHash, &k.Prefix, &k.CreatedAt, &lastUsed, &revoked, &createdBy); err != nil {
		return k, err
	}
	if lastUsed.Valid {
		k.LastUsedAt = &lastUsed.Int64
	}
	if revoked.Valid {
		k.RevokedAt = &revoked.Int64
	}
	if createdBy.Valid {
		k.CreatedBy = &createdBy.String
	}
	return k, nil
}

// CreateAPIKey stores a new key for the workspace and returns the plaintext
// key once. Only its hash is stored.
func CreateAPIKey(q db.Q, workspaceID, name string, createdBy *string) (string, APIKey, error) {
	key := GenerateAPIKey()
	n := strx.Trim(name)
	if n == "" {
		n = "key"
	}
	row, err := scanAPIKey(q.WQueryRow(
		`INSERT INTO api_keys (id, workspace_id, name, key_hash, prefix, created_by) VALUES (?, ?, ?, ?, ?, ?) RETURNING `+apiKeyCols,
		ids.New(ids.Key), workspaceID, n, Sha256(key), key[:len(KeyPrefix)+6], createdBy,
	))
	if err != nil {
		return "", APIKey{}, err
	}
	return key, row, nil
}

// APIKeyListItem is one entry of ListAPIKeys.
type APIKeyListItem struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  int64   `json:"createdAt"`
	LastUsedAt *int64  `json:"lastUsedAt"`
	RevokedAt  *int64  `json:"revokedAt"`
	CreatedBy  *string `json:"createdBy"`
}

// ListAPIKeys lists the workspace's keys, newest first, with the creator's
// display username.
func ListAPIKeys(q db.Q, workspaceID string) ([]APIKeyListItem, error) {
	rows, err := q.Query(`SELECT "api_keys"."id", "api_keys"."name", "api_keys"."prefix", "api_keys"."created_at", "api_keys"."last_used_at", "api_keys"."revoked_at", "user"."display_username"
		FROM "api_keys" LEFT JOIN "user" ON "user"."id" = "api_keys"."created_by"
		WHERE "api_keys"."workspace_id" = ? ORDER BY "api_keys"."created_at" DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyListItem{}
	for rows.Next() {
		var k APIKeyListItem
		var lastUsed, revoked sql.NullInt64
		var by sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &lastUsed, &revoked, &by); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			k.LastUsedAt = &lastUsed.Int64
		}
		if revoked.Valid {
			k.RevokedAt = &revoked.Int64
		}
		if by.Valid {
			k.CreatedBy = &by.String
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey revokes a key of the workspace. False when there was none to revoke.
func RevokeAPIKey(q db.Q, workspaceID, id string, now int64) (bool, error) {
	n, err := db.Changes(q.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL`, now, id, workspaceID))
	return n > 0, err
}

// KeyAuth is a verified key. CreatedBy is nil for keys made before users existed.
type KeyAuth struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	WorkspaceID string  `json:"workspaceId"`
	CreatedBy   *string `json:"createdBy"`
}

var (
	lastUsedMu     sync.Mutex
	lastUsedWrites = map[string]int64{}
)

// VerifyAPIKey returns the key's owner, or nil for a missing, unknown or
// revoked key. last_used_at is written at most once a minute per key.
func VerifyAPIKey(q db.Q, key string, now int64) (*KeyAuth, error) {
	if key == "" || !strings.HasPrefix(key, KeyPrefix) {
		return nil, nil
	}
	var k KeyAuth
	var by sql.NullString
	err := q.QueryRow(`SELECT id, name, workspace_id, created_by FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL`, Sha256(key)).
		Scan(&k.ID, &k.Name, &k.WorkspaceID, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if by.Valid {
		k.CreatedBy = &by.String
	}
	lastUsedMu.Lock()
	touch := lastUsedWrites[k.ID] < now-60_000
	if touch {
		lastUsedWrites[k.ID] = now
	}
	lastUsedMu.Unlock()
	if touch {
		if _, err := q.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, k.ID); err != nil {
			return nil, err
		}
	}
	return &k, nil
}

// Bearer reads the key from "Authorization: Bearer ..." or else X-API-Key.
// The second result is false when neither header gives one.
func Bearer(h http.Header) (string, bool) {
	// fetch's Headers.get joins repeated headers with ", ".
	a := strings.Join(h.Values("Authorization"), ", ")
	if len(a) >= 7 && strings.EqualFold(a[:7], "bearer ") {
		return strx.Trim(a[7:]), true
	}
	if v := h.Values("X-Api-Key"); len(v) > 0 {
		return strings.Join(v, ", "), true
	}
	return "", false
}
