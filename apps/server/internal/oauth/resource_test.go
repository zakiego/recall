package oauth_test

// oauth.test.ts: the discovery cases and "ignores garbage and
// API-key-shaped tokens" (the authorization-server flow comes with M6).

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/oauth"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

const origin = "http://localhost:4800"

func TestProtectedResourceMetadata(t *testing.T) {
	got := jsonx.Stringify(oauth.ProtectedResourceMetadata(origin))
	if !strings.HasPrefix(got, `{"resource":"`+origin+`/mcp","authorization_servers":["`+origin+`/api/auth"]`) {
		t.Fatal(got)
	}
}

func TestUnauthorizedChallenge(t *testing.T) {
	t.Setenv("RECALL_URL", origin)
	_, headers := oauth.Unauthorized("")
	if !strings.Contains(headers["www-authenticate"], `resource_metadata="`+origin+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatal(headers)
	}
}

func TestIgnoresGarbageAndKeyShapedTokens(t *testing.T) {
	d := testkit.OpenDB(t)
	now := time.Now().UnixMilli()
	for _, tok := range []string{"", "rco_nope", "rcl_abc"} {
		if a, err := oauth.VerifyAccessToken(d, tok, now); a != nil || err != nil {
			t.Fatal(tok, a, err)
		}
	}
	if c, err := oauth.MCPCaller(d, "rco_nope", now); c != nil || err != nil {
		t.Fatal(c, err)
	}
}
