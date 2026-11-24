package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/people"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/retry"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

type tool struct {
	name, title, description string
	// input is z.object(shape): unknown keys dropped.
	input *zod.ObjectS
	// props is the tools/list JSON schema of the shape, in order; required
	// lists the keys that aren't optional.
	props       []prop
	required    []string
	annotations *sdk.ToolAnnotations
	run         func(s *Server, ctx context.Context, args *jsonx.Object) (*sdk.CallToolResult, error)
}

type prop struct {
	key    string
	schema *jsonx.Object
}

// jsonProp is one property's JSON schema the way the SDK prints it:
// description first, then type, then the constraints.
func jsonProp(desc, typ string, kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	if desc != "" {
		o.Set("description", desc)
	}
	o.Set("type", typ)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

const containerDesc = "Memory namespace: a user id, project or team. Defaults to the server's default container."

var containerSchema = zod.Optional(zod.String().Trim().Min(1).Max(memories.MaxContainerLen))

var containerProp = prop{"container", jsonProp(containerDesc, "string", "minLength", 1, "maxLength", memories.MaxContainerLen)}

var httpURL = regexp.MustCompile(`^https?$`)

var tools = []*tool{
	{
		// First, so a client that reads tools/list in order meets it before
		// the rest. Any caller may read it: it describes the tools, not
		// what is in the workspace. The text goes out as is, so the markdown
		// reaches the client as markdown rather than a JSON string.
		name:        "get_playbook",
		title:       "Get playbook",
		description: "The Recall operating rules: when to search before answering, what is worth saving and what never is, containers, how newer facts supersede older ones, event_date versus due_date, people, the limits and the errors. Call this before your first other tool call in a conversation; once per conversation is enough. Returns a version string that matches the one in the server instructions.",
		input:       zod.Object(),
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolp(false)},
		run: func(*Server, context.Context, *jsonx.Object) (*sdk.CallToolResult, error) {
			return textResult(playbookDocument()), nil
		},
	},
	{
		name:        "add_memory",
		title:       "Add memory",
		description: "Save text, a conversation excerpt or a URL. Facts are extracted in the background (a few seconds) and contradicting older facts are superseded. Returns the document id, or with wait=true the extracted facts.",
		input: zod.Object(
			zod.F("content", zod.Optional(zod.String().Max(1_000_000))),
			zod.F("url", zod.Optional(zod.String().URL(httpURL))),
			zod.F("title", zod.Optional(zod.String().Max(300))),
			zod.F("source_id", zod.Optional(zod.String().Trim().Min(1).Max(api.MaxSourceIDLen))),
			zod.F("wait", zod.Optional(zod.Bool())),
			zod.F("document_date", zod.Optional(zod.String().Regex(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`), `^\d{4}-\d{2}-\d{2}`))),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"content", jsonProp("Text to remember. Send this or url.", "string", "maxLength", 1_000_000)},
			{"url", jsonProp("A web page or PDF to fetch and remember.", "string", "format", "uri")},
			{"title", jsonProp("", "string", "maxLength", 300)},
			{"source_id", jsonProp("Your own id for the source, e.g. gmail:<message id>. A repeat in the same container returns the existing document instead of adding it again.", "string", "minLength", 1, "maxLength", api.MaxSourceIDLen)},
			{"wait", jsonProp("Wait (up to 60 s) for extraction to finish and return the facts it added or updated.", "boolean")},
			{"document_date", jsonProp("The day the content was written (YYYY-MM-DD), e.g. an email's sent date. Relative dates in it (\"next Friday\") resolve against this. Defaults to today.", "string", "pattern", `^\d{4}-\d{2}-\d{2}`)},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false), OpenWorldHint: boolp(true)},
		run:         addMemory,
	},
	{
		name:        "search_memories",
		title:       "Search memories",
		description: "Hybrid keyword + semantic search over current memories (English or Indonesian). Optionally include raw document chunks.",
		input: zod.Object(
			zod.F("query", zod.String().Min(1).Max(2000)),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(50))),
			zod.F("include_chunks", zod.Optional(zod.Bool())),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"query", jsonProp("", "string", "minLength", 1, "maxLength", 2000)},
			{"limit", jsonProp("", "integer", "minimum", 1, "maximum", 50)},
			{"include_chunks", jsonProp("Also search the raw text of saved documents.", "boolean")},
			containerProp,
		},
		required:    []string{"query"},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         searchMemories,
	},
	{
		name:        "ask_memory",
		title:       "Ask memory",
		description: "Ask a question in English or Indonesian and get a short answer written only from the stored facts, with citations ([1], [2]) that map to memory or document ids. Says plainly when memory doesn't cover it. Takes a few seconds (one LLM call).",
		input: zod.Object(
			zod.F("question", zod.String().Trim().Min(1).Max(2000)),
			zod.F("include_chunks", zod.Optional(zod.Bool())),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"question", jsonProp("", "string", "minLength", 1, "maxLength", 2000)},
			{"include_chunks", jsonProp("Also use the raw text of saved documents.", "boolean")},
			containerProp,
		},
		required:    []string{"question"},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolp(false)},
		run:         askMemory,
	},
	{
		name:        "get_profile",
		title:       "Get profile",
		description: "A short summary of who the subject is (static) and what is going on now (dynamic), plus the facts behind it. Pass query to also get matching memories.",
		input:       zod.Object(zod.F("query", zod.Optional(zod.String().Max(2000))), zod.F("container", containerSchema)),
		props:       []prop{{"query", jsonProp("", "string", "maxLength", 2000)}, containerProp},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         getProfile,
	},
	{
		name:        "list_memories",
		title:       "List memories",
		description: "List current memories, newest first. Pass cursor from the previous page to continue. history=true includes superseded versions. needs_review=true lists only current facts flagged as conflicting with another fact; reviewNote says why.",
		input: zod.Object(
			zod.F("cursor", zod.Optional(zod.String())),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(200))),
			zod.F("history", zod.Optional(zod.Bool())),
			zod.F("needs_review", zod.Optional(zod.Bool())),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"cursor", jsonProp("", "string")},
			{"limit", jsonProp("", "integer", "minimum", 1, "maximum", 200)},
			{"history", jsonProp("", "boolean")},
			{"needs_review", jsonProp("Only facts flagged for human review because they conflict with another fact.", "boolean")},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         listMemories,
	},
	{
		name:        "upcoming",
		title:       "Upcoming",
		description: "Current facts with a due date in the next N days (contracts ending, renewals, deadlines), soonest first, with days_left. Use it for \"what's due soon\" or before planning. past_days also includes facts that came due recently.",
		input: zod.Object(
			zod.F("days", zod.Optional(zod.Int().Min(1).Max(api.MaxUpcomingDays))),
			zod.F("past_days", zod.Optional(zod.Int().Min(0).Max(api.MaxUpcomingDays))),
			zod.F("all_containers", zod.Optional(zod.Bool())),
			zod.F("timezone", zod.Optional(zod.String().Max(64))),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"days", jsonProp("How far ahead to look. Default 30.", "integer", "minimum", 1, "maximum", api.MaxUpcomingDays)},
			{"past_days", jsonProp("Also include facts due in the last N days. Default 0.", "integer", "minimum", 0, "maximum", api.MaxUpcomingDays)},
			{"all_containers", jsonProp("Every container in the workspace instead of one.", "boolean")},
			{"timezone", jsonProp("IANA time zone for \"today\", e.g. Asia/Makassar. Default UTC.", "string", "maxLength", 64)},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         upcoming,
	},
	{
		name:        "list_people",
		title:       "List people",
		description: "The people named in this container's facts (colleagues, clients, family), most recently mentioned first, with role, organization, contact details and how many current facts mention them. Pass query to filter by name, alias, role, organization, email or handle.",
		input: zod.Object(
			zod.F("query", zod.Optional(zod.String().Trim().Max(200))),
			zod.F("sort", zod.Optional(zod.Enum("recent", "name"))),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(200))),
			zod.F("offset", zod.Optional(zod.Int().Min(0))),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"query", jsonProp("", "string", "maxLength", 200)},
			{"sort", jsonProp("recent (default): last mentioned first. name: A to Z.", "string", "enum", []string{"recent", "name"})},
			{"limit", jsonProp("", "integer", "minimum", 1, "maximum", 200)},
			{"offset", jsonProp("nextOffset from the previous page.", "integer", "minimum", 0, "maximum", float64(zod.MaxSafeInt))},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         listPeople,
	},
	{
		name:        "get_person",
		title:       "Get person",
		description: "Everything stored about one person the user knows: name, aliases, role, organization, contact details, a short summary and the current facts that mention them, newest first, with their sources. Pass id from list_people, or name (matches names and aliases, ignoring case).",
		input: zod.Object(
			zod.F("id", zod.Optional(zod.String().Trim().Min(1).Max(64))),
			zod.F("name", zod.Optional(zod.String().Trim().Min(1).Max(200))),
			zod.F("history", zod.Optional(zod.Bool())),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"id", jsonProp("", "string", "minLength", 1, "maxLength", 64)},
			{"name", jsonProp("", "string", "minLength", 1, "maxLength", 200)},
			{"history", jsonProp("Also include older versions of those facts.", "boolean")},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         getPerson,
	},
	{
		name:        "list_documents",
		title:       "List documents",
		description: "List saved documents (texts, URLs, files, emails), newest first, with their processing status. status=failed lists only documents whose extraction gave up; error says why, and retry_document queues one again. Pass cursor from the previous page to continue.",
		input: zod.Object(
			zod.F("status", zod.Optional(zod.Enum(documentStatuses...))),
			zod.F("cursor", zod.Optional(zod.String())),
			zod.F("limit", zod.Optional(zod.Int().Min(1).Max(200))),
			zod.F("container", containerSchema),
		),
		props: []prop{
			{"status", jsonProp("Only documents with this status.", "string", "enum", documentStatuses)},
			{"cursor", jsonProp("", "string")},
			{"limit", jsonProp("", "integer", "minimum", 1, "maximum", 200)},
			containerProp,
		},
		annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		run:         listDocuments,
	},
	{
		name:        "retry_document",
		title:       "Retry document",
		description: "Queue a failed document for extraction again, e.g. after the LLM gateway was down. Pass id from list_documents with status=failed. Only failed documents can be retried.",
		input:       zod.Object(zod.F("id", zod.String().Trim().Min(1).Max(64))),
		props:       []prop{{"id", jsonProp("", "string", "minLength", 1, "maxLength", 64)}},
		required:    []string{"id"},
		annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false), OpenWorldHint: boolp(true)},
		run:         retryDocument,
	},
}

// documentStatuses are the values documents.status takes.
var documentStatuses = []string{"queued", "processing", "done", "failed"}

// toolIndex is the tool's place in tools, len(tools) for an unknown one.
func toolIndex(name string) int {
	for i, t := range tools {
		if t.name == name {
			return i
		}
	}
	return len(tools)
}

func toolByName(name string) *tool {
	for _, t := range tools {
		if t.name == name {
			return t
		}
	}
	return nil
}

// sdkTool is the tool as tools/list shows it, as McpServer builds it. The
// input schema goes out as raw JSON so its keys keep their order.
func (t *tool) sdkTool() *sdk.Tool {
	props := jsonx.NewMap[any]()
	for _, p := range t.props {
		props.Set(p.key, p.schema)
	}
	schema := jsonx.ObjectOf("$schema", "http://json-schema.org/draft-07/schema#", "type", "object", "properties", props)
	if len(t.required) > 0 {
		schema.Set("required", t.required)
	}
	return &sdk.Tool{Name: t.name, Title: t.title, Description: t.description, InputSchema: json.RawMessage(jsonx.Stringify(schema)), Annotations: t.annotations}
}

func boolp(b bool) *bool { return &b }

// --- arguments -----------------------------------------------------------------------------

func argStr(a *jsonx.Object, k string) (string, bool) {
	v, ok := a.Get(k)
	s, isStr := v.(string)
	return s, ok && isStr
}

func argBool(a *jsonx.Object, k string) (bool, bool) {
	v, ok := a.Get(k)
	b, isBool := v.(bool)
	return b, ok && isBool
}

func argInt(a *jsonx.Object, k string) (int, bool) {
	v, ok := a.Get(k)
	f, isNum := v.(float64)
	return int(f), ok && isNum
}

// container is args.container || the default container.
func container(a *jsonx.Object) string {
	if c, ok := argStr(a, "container"); ok && c != "" {
		return c
	}
	return config.Load().DefaultContainer
}

// dateFields is dates(m): only the dates a fact has, in short form.
func dateFields(o *jsonx.Object, eventDate, eventPrecision, dueDate, duePrecision *string) {
	if e := dates.FromColumns(eventDate, eventPrecision); e != nil {
		o.Set("event_date", e.String())
	}
	if d := dates.FromColumns(dueDate, duePrecision); d != nil {
		o.Set("due_date", d.String())
	}
}

func iso(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

// --- the tools ---------------------------------------------------------------------------

func addMemory(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	content, hasContent := argStr(a, "content")
	url, hasURL := argStr(a, "url")
	if (!hasContent || strx.Trim(content) == "") && (!hasURL || url == "") {
		return errorResult("Send content or url."), nil
	}
	in := api.AddInput{Container: container(a)}
	if hasURL && url != "" {
		in.URL = &url
	} else if hasContent {
		in.Content = &content
	}
	if t, ok := argStr(a, "title"); ok {
		in.Title = &t
	}
	if sid, ok := argStr(a, "source_id"); ok {
		in.SourceID = &sid
	}
	if dd, ok := argStr(a, "document_date"); ok {
		in.DocumentDate = &dd
	}
	res, err := api.AddContent(s.DB, s.WS, in, s.Now())
	if err != nil {
		return nil, err
	}
	if wait, _ := argBool(a, "wait"); !wait {
		note := "Queued. Facts appear within a few seconds."
		if res.Duplicate {
			note = "Already saved under this source_id."
		}
		return textResult(jsonx.ObjectOf("id", res.ID, "container", res.Container, "status", res.Status, "sourceId", res.SourceID, "duplicate", res.Duplicate, "note", note)), nil
	}
	out, err := api.WaitForAdd(ctx, s.DB, s.WS, res, api.DefaultWaitMs)
	if err != nil {
		return nil, err
	}
	if !out.Settled {
		return textResult(jsonx.ObjectOf("id", res.ID, "container", res.Container, "status", out.Status, "sourceId", res.SourceID, "duplicate", res.Duplicate, "note", "Still processing after 60 s. Facts will appear when it finishes.")), nil
	}
	o := jsonx.ObjectOf("id", out.ID, "container", out.Container, "status", out.Status, "duplicate", out.Duplicate)
	if out.Error != nil && *out.Error != "" {
		o.Set("error", *out.Error)
	}
	fact := func(m api.FactView, replaces bool) *jsonx.Object {
		f := jsonx.ObjectOf("id", m.ID, "fact", m.Fact, "kind", m.Kind)
		if replaces {
			f.Set("replaces", m.SupersedesID)
		}
		dateFields(f, m.EventDate, m.EventPrecision, m.DueDate, m.DuePrecision)
		if m.ReviewNote != nil && *m.ReviewNote != "" {
			f.Set("reviewNote", *m.ReviewNote)
		}
		return f
	}
	added, updated := []any{}, []any{}
	for _, m := range out.Added {
		added = append(added, fact(m, false))
	}
	for _, m := range out.Updated {
		updated = append(updated, fact(m, true))
	}
	o.Set("added", added)
	o.Set("updated", updated)
	o.Set("invalidated", out.Invalidated)
	return textResult(o), nil
}

func searchMemories(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	q, _ := argStr(a, "query")
	limit := 10
	if n, ok := argInt(a, "limit"); ok {
		limit = n
	}
	include := []string{"memories"}
	if c, _ := argBool(a, "include_chunks"); c {
		include = []string{"memories", "chunks"}
	}
	res, err := api.Search(s.DB, s.WS, api.SearchInput{Container: container(a), Q: q, Limit: &limit, Include: include})
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, h := range res.Results {
		score, _ := strconv.ParseFloat(strx.ToFixed(h.Score, 4), 64)
		o := jsonx.ObjectOf("type", h.Type, "id", h.ID, "text", h.Text, "score", score)
		if h.Type == "memory" {
			o.Set("kind", h.Kind)
			dateFields(o, h.EventDate, h.EventPrecision, h.DueDate, h.DuePrecision)
			if len(h.People) > 0 {
				names := make([]string, len(h.People))
				for i, p := range h.People {
					names[i] = p.Name
				}
				o.Set("people", names)
			}
		} else {
			o.Set("document", h.DocumentTitle)
		}
		list = append(list, o)
	}
	return textResult(list), nil
}

func askMemory(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	keyID := s.KeyID
	if keyID == "" {
		keyID = s.WS
	}
	if err := ratelimit.LimitAsk(keyID, s.AskLimiter); err != nil {
		return nil, err
	}
	q, _ := argStr(a, "question")
	in := ask.Input{Container: container(a), Question: q}
	if c, ok := argBool(a, "include_chunks"); ok {
		in.IncludeChunks = &c
	}
	res, err := ask.Ask(ctx, s.DB, s.WS, in, 0)
	if err != nil {
		return nil, err
	}
	citations := []any{}
	for _, c := range res.Answer.Citations {
		citations = append(citations, jsonx.ObjectOf("ref", c.Ref, "type", c.Type, "id", c.ID, "text", c.Text))
	}
	return textResult(jsonx.ObjectOf("answer", res.Answer.Answer, "covered", res.Answer.Covered, "citations", citations, "model", res.Answer.Model)), nil
}

func getProfile(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	c := container(a)
	p, err := memories.GetProfile(s.DB, s.WS, c)
	if err != nil {
		return nil, err
	}
	facts := func(list []model.Memory) []string {
		out := []string{}
		for _, m := range list {
			out = append(out, m.Fact)
		}
		return out
	}
	var updated any
	if p.UpdatedAt != nil && *p.UpdatedAt != 0 {
		updated = iso(*p.UpdatedAt)
	}
	o := jsonx.ObjectOf("container", p.Container, "static", p.Static, "dynamic", p.Dynamic, "updatedAt", updated, "staticFacts", facts(p.StaticMemories), "dynamicFacts", facts(p.DynamicMemories))
	if q, ok := argStr(a, "query"); ok && q != "" {
		limit := 10
		res, err := api.Search(s.DB, s.WS, api.SearchInput{Container: c, Q: q, Limit: &limit})
		if err != nil {
			return nil, err
		}
		texts := []string{}
		for _, h := range res.Results {
			texts = append(texts, h.Text)
		}
		o.Set("searchResults", texts)
	}
	return textResult(o), nil
}

func listMemories(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	limit := 50
	if n, ok := argInt(a, "limit"); ok {
		limit = n
	}
	cursor, hasCursor := argStr(a, "cursor")
	history, _ := argBool(a, "history")
	review, _ := argBool(a, "needs_review")
	c := container(a)
	page, next, err := memories.ListMemories(s.DB, memories.ListOptions{WorkspaceID: s.WS, Container: c, Cursor: cursor, History: history, Limit: &limit, Review: review})
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, m := range page {
		o := jsonx.ObjectOf("id", m.ID, "fact", m.Fact, "kind", m.Kind, "current", m.ValidTo == nil)
		dateFields(o, m.EventDate, m.EventPrecision, m.DueDate, m.DuePrecision)
		if m.ReviewNote != nil && *m.ReviewNote != "" {
			o.Set("reviewNote", *m.ReviewNote)
		}
		o.Set("createdAt", iso(m.CreatedAt))
		list = append(list, o)
	}
	o := jsonx.ObjectOf("memories", list, "nextCursor", next)
	// containers: args.cursor ? undefined : ... (an empty cursor counts as none).
	if !hasCursor || cursor == "" {
		cs, err := memories.ListContainers(s.DB, s.WS)
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, ci := range cs {
			names = append(names, ci.Container)
		}
		o.Set("containers", names)
	}
	return textResult(o), nil
}

func upcoming(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	tz, hasTZ := argStr(a, "timezone")
	if hasTZ && tz != "" && !dates.IsTimezone(tz) {
		return errorResult("Unknown time zone: " + tz), nil
	}
	if !hasTZ || tz == "" {
		tz = "UTC"
	}
	in := api.UpcomingInput{Container: container(a), Days: 30, TZ: tz}
	in.All, _ = argBool(a, "all_containers")
	if n, ok := argInt(a, "days"); ok {
		in.Days = n
	}
	if n, ok := argInt(a, "past_days"); ok {
		in.PastDays = n
	}
	res, err := api.Upcoming(s.DB, s.WS, in, s.Now())
	if err != nil {
		return nil, err
	}
	list := []any{}
	ms, _ := res.Get("memories")
	for _, x := range ms.([]any) {
		m := x.(*jsonx.Object)
		get := func(k string) any { v, _ := m.Get(k); return v }
		o := jsonx.ObjectOf("id", get("id"), "fact", get("fact"), "container", get("container"))
		sp := func(k string) *string {
			if v, ok := get(k).(string); ok {
				return &v
			}
			return nil
		}
		dateFields(o, sp("eventDate"), sp("eventPrecision"), sp("dueDate"), sp("duePrecision"))
		o.Set("days_left", get("daysLeft"))
		list = append(list, o)
	}
	today, _ := res.Get("today")
	cont, _ := res.Get("container")
	return textResult(jsonx.ObjectOf("today", today, "container", cont, "memories", list)), nil
}

func listPeople(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	in := api.PeopleInput{Container: container(a)}
	in.Q, _ = argStr(a, "query")
	in.Sort, _ = argStr(a, "sort")
	limit := 50
	if n, ok := argInt(a, "limit"); ok {
		limit = n
	}
	in.Limit = &limit
	if n, ok := argInt(a, "offset"); ok {
		in.Offset = &n
	}
	res, err := api.People(s.DB, s.WS, in)
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, p := range res.People {
		o := jsonx.ObjectOf("id", p.ID, "name", p.Name)
		if len(p.Aliases) > 0 {
			o.Set("aliases", p.Aliases)
		}
		if p.Role != nil && *p.Role != "" {
			o.Set("role", *p.Role)
		}
		if p.Organization != nil && *p.Organization != "" {
			o.Set("organization", *p.Organization)
		}
		if len(p.Emails) > 0 {
			o.Set("emails", p.Emails)
		}
		if len(p.Handles) > 0 {
			o.Set("handles", p.Handles)
		}
		o.Set("facts", p.FactCount)
		var last any
		if p.LastMentionedAt != nil && *p.LastMentionedAt != 0 {
			last = iso(*p.LastMentionedAt)
		}
		o.Set("lastMentionedAt", last)
		list = append(list, o)
	}
	return textResult(jsonx.ObjectOf("people", list, "total", res.Total, "nextOffset", res.NextOffset)), nil
}

func getPerson(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	c := container(a)
	argID, hasID := argStr(a, "id")
	name, hasName := argStr(a, "name")
	id := ""
	switch {
	case hasID:
		id = argID
	case hasName && name != "":
		named, err := people.NamedIn(s.DB, search.Scope{WorkspaceID: s.WS, Container: c}, name)
		if err != nil {
			return nil, err
		}
		if len(named) > 0 {
			id = named[0].ID
		}
	}
	found := false
	if id != "" {
		_, ok, err := people.GetRow(s.DB, s.WS, id)
		if err != nil {
			return nil, err
		}
		found = ok
	}
	if !found {
		if (hasID && argID != "") || (hasName && name != "") {
			which := name
			if hasID {
				which = argID
			}
			return errorResult("No person matches " + jsonx.Stringify(which) + " in " + c + "."), nil
		}
		return errorResult("Send id or name."), nil
	}
	history, _ := argBool(a, "history")
	res, err := api.Person(s.DB, s.WS, id, history)
	if err != nil {
		return nil, err
	}
	p := res.Person
	o, err := jsonx.ParseObject([]byte(jsonx.Stringify(p)))
	if err != nil {
		return nil, err
	}
	var last any
	if p.LastMentionedAt != nil && *p.LastMentionedAt != 0 {
		last = iso(*p.LastMentionedAt)
	}
	o.Set("lastMentionedAt", last)
	o.Set("createdAt", iso(p.CreatedAt))
	o.Set("updatedAt", iso(p.UpdatedAt))
	facts := []any{}
	for _, f := range res.Facts {
		ff := f.Clone()
		if v, ok := ff.Get("createdAt"); ok {
			if n, ok := v.(int64); ok {
				ff.Set("createdAt", iso(n))
			}
		}
		src, _ := ff.Get("source")
		var label any
		if ps, ok := src.(*people.Source); ok && ps != nil {
			if ps.Title != nil {
				label = *ps.Title
			} else if ps.URL != nil {
				label = *ps.URL
			}
		}
		ff.Set("source", label)
		facts = append(facts, ff)
	}
	o.Set("facts", facts)
	return textResult(o), nil
}

func listDocuments(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	limit := 50
	if n, ok := argInt(a, "limit"); ok {
		limit = n
	}
	status, _ := argStr(a, "status")
	cursor, _ := argStr(a, "cursor")
	page, next, err := memories.ListDocuments(s.DB, s.WS, container(a), status, cursor, &limit)
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, d := range page {
		o := jsonx.ObjectOf("id", d.ID, "title", d.Title, "sourceType", d.SourceType, "status", d.Status)
		if d.URL != nil {
			o.Set("url", *d.URL)
		}
		if d.Error != nil && *d.Error != "" {
			o.Set("error", *d.Error)
		}
		o.Set("createdAt", iso(d.CreatedAt))
		list = append(list, o)
	}
	return textResult(jsonx.ObjectOf("documents", list, "nextCursor", next)), nil
}

func retryDocument(s *Server, ctx context.Context, a *jsonx.Object) (*sdk.CallToolResult, error) {
	id, _ := argStr(a, "id")
	// An API key or OAuth token carries no user, so emails that need the
	// importer's Gmail access are refused here; the dashboard can retry them.
	err := retry.Document(s.DB, s.WS, id, retry.Options{FailedOnly: true}, s.Now())
	var notFailed *retry.NotFailedError
	switch {
	case errors.Is(err, retry.ErrNoDocument):
		return errorResult("No document " + jsonx.Stringify(id) + " in this workspace."), nil
	case errors.Is(err, retry.ErrNotImporter):
		return errorResult(err.Error() + " Retry it from the dashboard."), nil
	case errors.As(err, &notFailed):
		return errorResult(err.Error()), nil
	case err != nil:
		return nil, err
	}
	return textResult(jsonx.ObjectOf("id", id, "status", "queued", "note", "Queued again. Facts appear within a few seconds.")), nil
}
