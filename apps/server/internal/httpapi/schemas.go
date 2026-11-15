package httpapi

import (
	"regexp"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// The zod schemas of the /v1 routes (http.ts, api.ts, ask.ts, the import
// route), field for field.

// containerSchema: an empty result falls back to the default container.
var containerSchema = zod.Transformed(zod.Optional(zod.String().Trim().Min(1).Max(memories.MaxContainerLen)), func(v any) any {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return config.Load().DefaultContainer
})

// flag is 1/0/true/false, read as a boolean.
var flag = zod.Transformed(zod.Optional(zod.Enum("1", "0", "true", "false")), func(v any) any { return v == "1" || v == "true" })

func optInt(lo, hi float64) zod.Schema {
	n := zod.CoerceNumber().Int().Min(lo)
	if hi >= 0 {
		n = n.Max(hi)
	}
	return zod.Optional(n)
}

var listInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("cursor", zod.Optional(zod.String())),
	zod.F("history", flag),
	zod.F("review", flag),
	zod.F("limit", optInt(1, 200)),
	zod.F("kind", zod.Optional(zod.Enum("static", "dynamic"))),
)

var searchInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("q", zod.String().Trim().Min(1).Max(2000)),
	zod.F("limit", optInt(1, 100)),
	zod.F("include", zod.Optional(zod.Array(zod.Enum("memories", "chunks")))),
)

var profileInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("q", zod.Optional(zod.String().Trim().Max(2000))),
)

// dateInput: "2026-11-05", "2026-11" or "2026"; null clears it.
var dateInput = zod.Optional(zod.Refined(zod.Optional(zod.Union(zod.String(), zod.Null())), func(v any) bool {
	s, ok := v.(string)
	return !ok || dates.Parse(s) != nil
}, "use YYYY-MM-DD, YYYY-MM or YYYY"))

var patchInput = zod.Object(
	zod.F("fact", zod.Optional(zod.String().Trim().Min(1).Max(500))),
	zod.F("keywords", zod.Optional(zod.String().Trim().Max(500))),
	zod.F("kind", zod.Optional(zod.Enum("static", "dynamic"))),
	zod.F("event_date", dateInput),
	zod.F("due_date", dateInput),
	zod.F("resolve", zod.Optional(zod.Literal(true))),
).Refine(func(o *jsonx.Object) bool {
	for _, k := range []string{"fact", "keywords", "kind", "event_date", "due_date"} {
		if v, ok := o.Get(k); ok && !zod.IsUndefined(v) {
			return true
		}
	}
	v, _ := o.Get("resolve")
	return v == true
}, "nothing to change")

var upcomingInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("all", flag),
	zod.F("days", zod.Default(zod.CoerceNumber().Int().Min(1).Max(366), 30.0)),
	zod.F("past_days", zod.Default(zod.CoerceNumber().Int().Min(0).Max(366), 0.0)),
	zod.F("tz", zod.Default(zod.String().Max(64).Refine(dates.IsTimezone, "unknown time zone"), "UTC")),
	zod.F("limit", optInt(1, 500)),
)

var listPeopleInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("q", zod.Optional(zod.String().Trim().Max(200))),
	zod.F("sort", zod.Optional(zod.Enum("recent", "name"))),
	zod.F("limit", optInt(1, 200)),
	zod.F("offset", optInt(0, -1)),
)

var getPersonInput = zod.Object(zod.F("history", flag))

var askInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("question", zod.String().Trim().Min(1).Max(2000)),
	zod.F("include_chunks", zod.Optional(zod.Bool())),
)

var importBody = zod.Object(
	zod.F("message_id", zod.String().Trim().Regex(connectors.MessageID, `^[A-Za-z0-9_-]{6,64}$`, "not a Gmail message id")),
	zod.F("container", containerSchema),
)

// documentDateInput: a day, a time part ignored.
var documentDateInput = zod.String().Refine(func(s string) bool {
	d := dates.Parse(s)
	return d != nil && d.Precision == "day"
}, "use YYYY-MM-DD (a time part is ignored)")

var chatMessage = zod.Object(
	zod.F("role", zod.String().Trim().Min(1).Max(40)),
	zod.F("content", zod.String().Max(1_000_000)),
)

var httpURL = regexp.MustCompile(`^https?$`)

var addInput = zod.Object(
	zod.F("container", containerSchema),
	zod.F("content", zod.Optional(zod.String().Max(1_000_000))),
	zod.F("messages", zod.Optional(zod.Array(chatMessage).Min(1).Max(2000))),
	zod.F("url", zod.Optional(zod.String().URL(httpURL))),
	zod.F("type", zod.Optional(zod.Enum("text", "chat", "url"))),
	zod.F("title", zod.Optional(zod.String().Trim().Max(300))),
	zod.F("metadata", zod.Optional(zod.Record(zod.Unknown()).Keys(zod.String()))),
	zod.F("source_id", zod.Optional(zod.String().Trim().Min(1).Max(200))),
	zod.F("document_date", zod.Optional(documentDateInput)),
	zod.F("wait", zod.Optional(zod.Bool())),
	zod.F("wait_timeout", zod.Optional(zod.Number().Min(0).Max(120))),
).Refine(func(o *jsonx.Object) bool {
	n := 0
	if c, _ := o.Get("content"); c != nil {
		if s, ok := c.(string); ok && strx.Trim(s) != "" {
			n++
		}
	}
	if m, ok := o.Get("messages"); ok && m != nil && !zod.IsUndefined(m) {
		n++
	}
	if u, _ := o.Get("url"); u != nil {
		if s, ok := u.(string); ok && s != "" {
			n++
		}
	}
	return n == 1
}, "send exactly one of content, messages or url")

// waitFields is add.ts's waitFields, for the query and multipart fields.
var waitFields = zod.Object(
	zod.F("wait", zod.Transformed(zod.Optional(zod.Enum("1", "0", "true", "false")), func(v any) any {
		if zod.IsUndefined(v) {
			return zod.Undefined
		}
		return v == "1" || v == "true"
	})),
	zod.F("wait_timeout", zod.Optional(zod.CoerceNumber().Min(0).Max(120))),
)
