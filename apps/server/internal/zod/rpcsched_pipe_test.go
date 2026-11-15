package zod_test

import (
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// z.object({ s: z.string().transform((v) => v.trim()).pipe(z.string().max(3)) })
func TestTransformPipe(t *testing.T) {
	s := zod.Object(zod.F("s", zod.TransformPipe(zod.String(), func(v any) any { return strings.TrimSpace(v.(string)) }, zod.String().Max(3))))
	parse := func(in string) string {
		out, err := zod.ParseJSON(s, []byte(in))
		if err != nil {
			return err.Error()
		}
		return jsonx.Stringify(out)
	}
	if got := parse(`{"s":"  abc  "}`); got != `{"s":"abc"}` {
		t.Fatal(got)
	}
	// The pipe checks the transformed value, with the field's path.
	if got := parse(`{"s":" abcd "}`); !strings.Contains(got, `"too_big"`) || !strings.Contains(got, `"s"`) {
		t.Fatal(got)
	}
	// A type error stops before the transform runs.
	if got := parse(`{"s":5}`); !strings.Contains(got, "expected string, received number") {
		t.Fatal(got)
	}
	if got := parse(`{}`); !strings.Contains(got, "expected string, received undefined") {
		t.Fatal(got)
	}
}
