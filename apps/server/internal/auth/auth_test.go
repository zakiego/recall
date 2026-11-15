package auth_test

import (
	"bufio"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type pwCase struct {
	Password string `json:"password"`
	Hash     string `json:"hash"`
}

type m1 struct {
	Keys []struct {
		Key    string `json:"key"`
		Hash   string `json:"hash"`
		Prefix string `json:"prefix"`
	} `json:"keys"`
	Tokens      []string `json:"tokens"`
	Scrypt      []pwCase `json:"scrypt"`
	Legacy      []pwCase `json:"legacy"`
	VerifyCases []struct {
		Hash     string `json:"hash"`
		Password string `json:"password"`
		OK       bool   `json:"ok"`
	} `json:"verifyCases"`
	Bearers []struct {
		Headers [][2]string `json:"headers"`
		Key     *string     `json:"key"`
	} `json:"bearers"`
}

func load(t *testing.T) m1 {
	var g m1
	testutil.LoadJSON(t, "m1.json", &g)
	return g
}

var keyPattern = regexp.MustCompile(`^rcl_[0-9A-Za-z]{32}$`)

// Keys the TS build made hash and prefix the same way here, and verify against
// rows stored with the TS hash.
func TestTSKeysVerify(t *testing.T) {
	g := load(t)
	d := db.Memory()
	defer d.Close()
	for i, k := range g.Keys {
		if !keyPattern.MatchString(k.Key) {
			t.Fatalf("TS key %q", k.Key)
		}
		if auth.Sha256(k.Key) != k.Hash || k.Key[:10] != k.Prefix {
			t.Fatalf("key %d: hash or prefix differ", i)
		}
		if _, err := d.Exec(`INSERT INTO api_keys (id, workspace_id, name, key_hash, prefix) VALUES (?, 'ws_a', 'ts', ?, ?)`, "key_"+k.Prefix+string(rune('a'+i)), k.Hash, k.Prefix); err != nil {
			t.Fatal(err)
		}
		got, err := auth.VerifyAPIKey(d, k.Key, 1_000)
		if err != nil || got == nil || got.WorkspaceID != "ws_a" {
			t.Fatalf("verify %d: %+v %v", i, got, err)
		}
	}
	for _, tok := range g.Tokens {
		if !regexp.MustCompile(`^[0-9A-Za-z]+$`).MatchString(tok) {
			t.Fatalf("token %q", tok)
		}
	}
}

func TestTokenFromBytesKeepsTheModuloRule(t *testing.T) {
	if got := auth.TokenFromBytes([]byte{0, 61, 62, 63, 123, 124, 255}); got != "0z01z07" {
		t.Fatalf("%s", got)
	}
}

// auth.test.ts "verifies a created key until it's revoked, and carries its workspace".
func TestCreatedKeyVerifiesUntilRevoked(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	key, row, err := auth.CreateAPIKey(d, "ws_a", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !keyPattern.MatchString(key) || strings.Contains(row.KeyHash, key) || row.Prefix != key[:10] || row.Name != "test" {
		t.Fatalf("%s %+v", key, row)
	}
	now := time.Now().UnixMilli()
	if k, _ := auth.VerifyAPIKey(d, key, now); k == nil || k.ID != row.ID || k.WorkspaceID != "ws_a" {
		t.Fatalf("%+v", k)
	}
	if k, _ := auth.VerifyAPIKey(d, key+"x", now); k != nil {
		t.Fatal("suffix verified")
	}
	if k, _ := auth.VerifyAPIKey(d, "", now); k != nil {
		t.Fatal("empty verified")
	}
	if ok, err := auth.RevokeAPIKey(d, "ws_a", row.ID, now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if k, _ := auth.VerifyAPIKey(d, key, now); k != nil {
		t.Fatal("revoked key verified")
	}
	if _, row, _ := auth.CreateAPIKey(d, "ws_a", "  \t ", nil); row.Name != "key" {
		t.Fatalf("blank name stored as %q", row.Name)
	}
}

// auth.test.ts "lists and revokes only inside the key's workspace".
func TestKeysStayInTheirWorkspace(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	a, arow, _ := auth.CreateAPIKey(d, "ws_a", "a", nil)
	if _, _, err := auth.CreateAPIKey(d, "ws_b", "b", nil); err != nil {
		t.Fatal(err)
	}
	la, _ := auth.ListAPIKeys(d, "ws_a")
	lb, _ := auth.ListAPIKeys(d, "ws_b")
	if len(la) != 1 || la[0].Name != "a" || len(lb) != 1 || lb[0].Name != "b" {
		t.Fatalf("%+v %+v", la, lb)
	}
	if ok, _ := auth.RevokeAPIKey(d, "ws_b", arow.ID, 1); ok {
		t.Fatal("revoked across workspaces")
	}
	if k, _ := auth.VerifyAPIKey(d, a, 1); k == nil {
		t.Fatal("key gone")
	}
	if got := jsonx.Stringify(la[0]); !strings.Contains(got, `"lastUsedAt":null,"revokedAt":null,"createdBy":null}`) {
		t.Fatalf("list item JSON %s", got)
	}
}

// last_used_at is written at most once a minute per key.
func TestLastUsedIsThrottled(t *testing.T) {
	d := db.Memory()
	defer d.Close()
	key, row, _ := auth.CreateAPIKey(d, "ws_a", "a", nil)
	read := func() int64 {
		var v int64
		if err := d.QueryRow("SELECT last_used_at FROM api_keys WHERE id = ?", row.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	base := int64(10_000_000)
	auth.VerifyAPIKey(d, key, base)
	auth.VerifyAPIKey(d, key, base+59_999)
	if read() != base {
		t.Fatalf("%d", read())
	}
	auth.VerifyAPIKey(d, key, base+60_000)
	if read() != base {
		t.Fatalf("written at exactly 60 s: %d", read())
	}
	auth.VerifyAPIKey(d, key, base+60_001)
	if read() != base+60_001 {
		t.Fatalf("%d", read())
	}
}

// auth.test.ts "reads bearer and x-api-key headers", plus the TS edge cases.
func TestBearer(t *testing.T) {
	g := load(t)
	for _, c := range g.Bearers {
		// Through net/http's own parser, which trims values like fetch does.
		raw := "GET / HTTP/1.1\r\nHost: x\r\n"
		for _, kv := range c.Headers {
			raw += kv[0] + ": " + kv[1] + "\r\n"
		}
		req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw + "\r\n")))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := auth.Bearer(req.Header)
		if (c.Key == nil) == ok || (c.Key != nil && *c.Key != got) {
			t.Errorf("%v: got %q %v, want %v", c.Headers, got, ok, c.Key)
		}
	}
}

// Hashes made by the TS build verify here, with every edge case answered the
// same way.
func TestTSPasswordHashesVerify(t *testing.T) {
	g := load(t)
	if len(g.Scrypt) < 8 || len(g.Legacy) < 8 || len(g.VerifyCases) < 20 {
		t.Fatal("golden incomplete")
	}
	for _, c := range g.Scrypt {
		if !auth.VerifyPassword(c.Hash, c.Password) {
			t.Errorf("scrypt %q does not verify", c.Password)
		}
		if auth.VerifyPassword(c.Hash, c.Password+"x") {
			t.Errorf("scrypt %q verifies a wrong password", c.Password)
		}
	}
	for _, c := range g.Legacy {
		if !auth.VerifyPassword(c.Hash, c.Password) || !auth.VerifyLegacyPassword(c.Password, c.Hash) {
			t.Errorf("pbkdf2 %q does not verify", c.Password)
		}
	}
	for _, c := range g.VerifyCases {
		if got := auth.VerifyPassword(c.Hash, c.Password); got != c.OK {
			t.Errorf("verify(%q, %q) = %v, want %v", c.Hash, c.Password, got, c.OK)
		}
	}
}

// auth.test.ts "hashes new passwords with scrypt" and "still verifies the old
// pbkdf2 admin hash". With RECALL_M1_OUT set, the Go hashes are written there
// for testdata/gen/verify-go-m1.ts (the rollback direction).
func TestGoPasswordHashes(t *testing.T) {
	g := load(t)
	type out struct {
		Password string `json:"password"`
		Scrypt   string `json:"scrypt"`
		Legacy   string `json:"legacy"`
	}
	var outs []out
	for _, c := range g.Scrypt {
		h, err := auth.HashPassword(c.Password)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(h, "pbkdf2") || !regexp.MustCompile(`^[0-9a-f]{32}:[0-9a-f]{128}$`).MatchString(h) {
			t.Fatalf("scrypt hash %q", h)
		}
		if !auth.VerifyPassword(h, c.Password) || auth.VerifyPassword(h, "wrong") {
			t.Fatalf("scrypt round trip for %q", c.Password)
		}
		l, err := auth.HashLegacyPassword(c.Password)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(l, "pbkdf2$sha256$100000$") || !auth.VerifyPassword(l, c.Password) || auth.VerifyPassword(l, "wrong") {
			t.Fatalf("pbkdf2 round trip for %q: %s", c.Password, l)
		}
		outs = append(outs, out{c.Password, h, l})
	}
	if auth.VerifyPassword("garbage", "x") {
		t.Fatal("garbage verified")
	}
	if p := os.Getenv("RECALL_M1_OUT"); p != "" {
		if err := os.WriteFile(p, []byte(jsonx.Stringify(outs)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
