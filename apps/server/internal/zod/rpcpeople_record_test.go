package zod_test

import (
	"errors"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// z.record(z.string().max(3), z.enum([...]).nullable()), as zod 4.6.7 prints
// it (checked in node against the app's zod).
func TestRecordKeys(t *testing.T) {
	s := zod.Object(zod.F("tools", zod.Optional(zod.Record(zod.Nullable(zod.Enum("allow", "ask", "deny"))).Keys(zod.String().Max(3)))))
	in, _ := jsonx.Parse([]byte(`{"tools":{"abcdef":"allow","ok":"nope","x":null}}`))
	_, err := zod.Parse(s, in)
	var ze *zod.Error
	if !errors.As(err, &ze) {
		t.Fatal(err)
	}
	want := `[{"code":"invalid_key","origin":"record","issues":[{"origin":"string","code":"too_big","maximum":3,"inclusive":true,"path":[],"message":"Too big: expected string to have <=3 characters"}],"path":["tools","abcdef"],"message":"Invalid key in record"},{"code":"invalid_value","values":["allow","ask","deny"],"path":["tools","ok"],"message":"Invalid option: expected one of \"allow\"|\"ask\"|\"deny\""}]`
	list := make([]any, len(ze.Issues))
	for i, is := range ze.Issues {
		list[i] = is.JSON()
	}
	if got := jsonx.Stringify(list); got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	in, _ = jsonx.Parse([]byte(`{"tools":{"ok":"allow","x":null}}`))
	out, err := zod.Parse(s, in)
	if err != nil || jsonx.Stringify(out) != `{"tools":{"ok":"allow","x":null}}` {
		t.Fatal(jsonx.Stringify(out), err)
	}
}
