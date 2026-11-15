package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/zakiego/recall/apps/server/internal/api"
	"github.com/zakiego/recall/apps/server/internal/ask"
	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/users"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// keyCtx is what withKey hands a /v1 handler.
type keyCtx struct {
	w   http.ResponseWriter
	r   *http.Request
	d   *db.DB
	ws  string
	key *auth.KeyAuth
	now func() int64
}

// withKey is withKey: the API key, then the handler, with HttpError and
// unexpected errors turned into JSON.
func (s *Server) withKey(h func(c *keyCtx) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("panic: %v", p)
				}
			}()
			key, err := auth.VerifyAPIKey(s.DB, bearer(r), s.Now())
			if err != nil {
				return err
			}
			if key == nil {
				apiError(w, 401, "unauthorized", "missing or invalid API key", nil)
				return nil
			}
			return h(&keyCtx{w: w, r: r, d: s.DB, ws: key.WorkspaceID, key: key, now: s.Now})
		}()
		if err == nil {
			return
		}
		var he *httpx.Error
		if errors.As(err, &he) {
			apiError(w, he.Status, he.Type, he.Message, he.Headers)
			return
		}
		// The message can name tables, file paths or internal hosts, so it stays in the log.
		log.Printf("[recall] api error: %v", err)
		apiError(w, 500, "internal", "internal error", nil)
	}
}

func str(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func boolOf(o *jsonx.Object, k string) bool {
	v, _ := o.Get(k)
	return v == true
}

func intPtr(o *jsonx.Object, k string) *int {
	v, ok := o.Get(k)
	f, isNum := v.(float64)
	if !ok || !isNum {
		return nil
	}
	n := int(f)
	return &n
}

func (c *keyCtx) json(status int, body any) error {
	jsonNoStore(c.w, status, body, nil)
	return nil
}

func (s *Server) routesV1(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/containers", s.withKey(func(c *keyCtx) error {
		list, err := memories.ListContainers(c.d, c.ws)
		if err != nil {
			return err
		}
		if list == nil {
			list = []memories.ContainerInfo{}
		}
		return c.json(200, jsonx.ObjectOf("containers", list))
	}))

	mux.HandleFunc("GET /v1/documents/{id}", s.withKey(func(c *keyCtx) error {
		doc, ok, err := memories.GetDocument(c.d, c.ws, c.r.PathValue("id"))
		if err != nil {
			return err
		}
		if !ok {
			return httpErr(404, "not_found", "no such document")
		}
		v, err := api.DocumentView(doc)
		if err != nil {
			return err
		}
		return c.json(200, jsonx.ObjectOf("document", v))
	}))
	mux.HandleFunc("DELETE /v1/documents/{id}", s.withKey(func(c *keyCtx) error {
		ok, err := memories.DeleteDocument(c.d, c.ws, c.r.PathValue("id"))
		if err != nil {
			return err
		}
		if !ok {
			return httpErr(404, "not_found", "no such document")
		}
		return c.json(200, jsonx.ObjectOf("deleted", true))
	}))

	mux.HandleFunc("GET /v1/memories", s.withKey(func(c *keyCtx) error {
		in, err := parseQuery(c.r, listInput)
		if err != nil {
			return err
		}
		out, err := api.List(c.d, c.ws, api.ListInput{Container: str(in, "container"), Cursor: str(in, "cursor"), History: boolOf(in, "history"), Review: boolOf(in, "review"), Limit: intPtr(in, "limit"), Kind: str(in, "kind")})
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))
	mux.HandleFunc("POST /v1/memories", s.withKey(s.handleAdd))

	mux.HandleFunc("GET /v1/memories/{id}", s.withKey(func(c *keyCtx) error {
		m, ok, err := memories.GetMemory(c.d, c.ws, c.r.PathValue("id"))
		if err != nil {
			return err
		}
		if !ok {
			return httpErr(404, "not_found", "no such memory")
		}
		history, err := memories.Chain(c.d, c.ws, m.ID, 500)
		if err != nil {
			return err
		}
		return c.json(200, jsonx.ObjectOf("memory", m, "history", history))
	}))
	mux.HandleFunc("PATCH /v1/memories/{id}", s.withKey(func(c *keyCtx) error {
		in, err := readJSON(c.r, patchInput)
		if err != nil {
			return err
		}
		out, err := api.PatchMemory(c.d, c.ws, c.r.PathValue("id"), patchOf(in), c.now)
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))
	mux.HandleFunc("DELETE /v1/memories/{id}", s.withKey(func(c *keyCtx) error {
		m, ok, err := memories.ForgetMemory(c.d, c.ws, c.r.PathValue("id"), c.now())
		if err != nil {
			return err
		}
		if !ok {
			return httpErr(404, "not_found", "no such memory")
		}
		return c.json(200, jsonx.ObjectOf("memory", m))
	}))

	mux.HandleFunc("GET /v1/people", s.withKey(func(c *keyCtx) error {
		in, err := parseQuery(c.r, listPeopleInput)
		if err != nil {
			return err
		}
		out, err := api.People(c.d, c.ws, api.PeopleInput{Container: str(in, "container"), Q: str(in, "q"), Sort: str(in, "sort"), Limit: intPtr(in, "limit"), Offset: intPtr(in, "offset")})
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))
	mux.HandleFunc("GET /v1/people/{id}", s.withKey(func(c *keyCtx) error {
		in, err := parseQuery(c.r, getPersonInput)
		if err != nil {
			return err
		}
		out, err := api.Person(c.d, c.ws, c.r.PathValue("id"), boolOf(in, "history"))
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))

	mux.HandleFunc("GET /v1/profile", s.withKey(func(c *keyCtx) error {
		in, err := parseQuery(c.r, profileInput)
		if err != nil {
			return err
		}
		out, err := api.Profile(c.d, c.ws, api.ProfileInput{Container: str(in, "container"), Q: str(in, "q")})
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))

	mux.HandleFunc("POST /v1/search", s.withKey(func(c *keyCtx) error {
		in, err := readJSON(c.r, searchInput)
		if err != nil {
			return err
		}
		si := api.SearchInput{Container: str(in, "container"), Q: str(in, "q"), Limit: intPtr(in, "limit")}
		if inc, ok := in.Get("include"); ok {
			if list, ok := inc.([]any); ok {
				si.Include = []string{}
				for _, x := range list {
					si.Include = append(si.Include, x.(string))
				}
			}
		}
		out, err := api.Search(c.d, c.ws, si)
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))

	mux.HandleFunc("GET /v1/upcoming", s.withKey(func(c *keyCtx) error {
		in, err := parseQuery(c.r, upcomingInput)
		if err != nil {
			return err
		}
		days, _ := in.Get("days")
		past, _ := in.Get("past_days")
		out, err := api.Upcoming(c.d, c.ws, api.UpcomingInput{Container: str(in, "container"), All: boolOf(in, "all"), Days: int(days.(float64)), PastDays: int(past.(float64)), TZ: str(in, "tz"), Limit: intPtr(in, "limit")}, c.now())
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))

	mux.HandleFunc("POST /v1/ask", s.withKey(func(c *keyCtx) error {
		if err := ratelimit.LimitAsk(c.key.ID, s.AskLimiter); err != nil {
			return err
		}
		in, err := readJSON(c.r, askInput)
		if err != nil {
			return err
		}
		ai := ask.Input{Container: str(in, "container"), Question: str(in, "question")}
		if v, ok := in.Get("include_chunks"); ok {
			b, _ := v.(bool)
			ai.IncludeChunks = &b
		}
		out, err := ask.Ask(c.r.Context(), c.d, c.ws, ai, 0)
		if err != nil {
			return err
		}
		return c.json(200, out)
	}))

	mux.HandleFunc("POST /v1/connectors/gmail/import", s.withKey(func(c *keyCtx) error {
		in, err := readJSON(c.r, importBody)
		if err != nil {
			return err
		}
		// A connection is personal: only keys its owner created (and who is still a member) may use it.
		owner := ""
		if c.key.CreatedBy != nil {
			owner = *c.key.CreatedBy
		}
		role := ""
		if owner != "" {
			if role, err = users.GetRole(c.d, owner, c.ws); err != nil {
				return err
			}
		}
		if owner == "" || role == "" {
			return httpErr(403, "forbidden", "this key has no owner who can use a Gmail connection; create a key in the dashboard as the user who connected Gmail")
		}
		res, err := connectors.Import(c.d, connectors.ImportInput{WorkspaceID: c.ws, UserID: owner, Container: str(in, "container"), Messages: []connectors.ImportMessage{{ID: str(in, "message_id")}}})
		var ce *connectors.ConnectorError
		if errors.As(err, &ce) {
			status := map[string]int{"not_configured": 503, "not_connected": 409, "reauth": 409, "invalid": 400}[ce.Code]
			return httpErr(status, ce.Code, ce.Msg)
		}
		if err != nil {
			return err
		}
		d := res.Documents[0]
		doc, _, err := memories.GetDocument(c.d, c.ws, d.ID)
		if err != nil {
			return err
		}
		status := 202
		if d.Duplicate {
			status = 200
		}
		return c.json(status, jsonx.ObjectOf("id", doc.ID, "container", doc.Container, "status", doc.Status, "sourceId", doc.SourceID, "duplicate", d.Duplicate))
	}))
}

// patchOf reads patchInput's output into api.PatchInput.
func patchOf(in *jsonx.Object) api.PatchInput {
	var p api.PatchInput
	opt := func(k string) *string {
		if v, ok := in.Get(k); ok {
			if s, ok := v.(string); ok {
				return &s
			}
		}
		return nil
	}
	p.Fact, p.Keywords, p.Kind = opt("fact"), opt("keywords"), opt("kind")
	date := func(k string) memories.DateValue {
		v, ok := in.Get(k)
		if !ok || zod.IsUndefined(v) {
			return memories.DateValue{}
		}
		if s, ok := v.(string); ok {
			return memories.Value(dates.Parse(s))
		}
		return memories.Value(nil)
	}
	p.Event, p.Due = date("event_date"), date("due_date")
	p.Resolve = boolOf(in, "resolve")
	return p
}

// --- POST /v1/memories (add.ts) ----------------------------------------------------------

// MaxMultipartBytes is MAX_MULTIPART_BYTES: the file plus room for the rest.
const MaxMultipartBytes = api.MaxFileBytes + 1024*1024

type waitOpts struct {
	wait    *bool
	timeout *float64
}

func parseWait(o *jsonx.Object, where string) (waitOpts, error) {
	out, err := zod.Parse(waitFields, o)
	if err != nil {
		return waitOpts{}, httpErr(400, "invalid_request", where+": wait is 1/0/true/false, wait_timeout is 0 to 120 seconds")
	}
	p := out.(*jsonx.Object)
	var w waitOpts
	if v, ok := p.Get("wait"); ok {
		if b, ok := v.(bool); ok {
			w.wait = &b
		}
	}
	if v, ok := p.Get("wait_timeout"); ok {
		if f, ok := v.(float64); ok {
			w.timeout = &f
		}
	}
	return w, nil
}

func (s *Server) handleAdd(c *keyCtx) error {
	query, err := parseWait(queryObject(c.r), "query")
	if err != nil {
		return err
	}
	var res api.AddResult
	var body waitOpts
	if strings.HasPrefix(c.r.Header.Get("Content-Type"), "multipart/form-data") {
		if res, body, err = s.addMultipart(c); err != nil {
			return err
		}
	} else {
		in, err := readJSON(c.r, addInput)
		if err != nil {
			return err
		}
		if res, err = api.AddContent(c.d, c.ws, addInputOf(in), c.now()); err != nil {
			return err
		}
		if v, ok := in.Get("wait"); ok {
			if b, ok := v.(bool); ok {
				body.wait = &b
			}
		}
		if v, ok := in.Get("wait_timeout"); ok {
			if f, ok := v.(float64); ok {
				body.timeout = &f
			}
		}
	}
	wait := false
	switch {
	case body.wait != nil:
		wait = *body.wait
	case query.wait != nil:
		wait = *query.wait
	}
	// A duplicate queued nothing new, so it's 200 even while still processing.
	if !wait {
		if res.Duplicate {
			return c.json(200, res)
		}
		return c.json(202, res)
	}
	timeoutMs := float64(api.DefaultWaitMs)
	if body.timeout != nil {
		timeoutMs = *body.timeout * 1000
	} else if query.timeout != nil {
		timeoutMs = *query.timeout * 1000
	}
	out, err := api.WaitForAdd(c.r.Context(), c.d, c.ws, res, timeoutMs)
	if err != nil {
		return err
	}
	if out.Settled || res.Duplicate {
		return c.json(200, out)
	}
	return c.json(202, out)
}

// addInputOf reads addInput's output into api.AddInput.
func addInputOf(in *jsonx.Object) api.AddInput {
	var a api.AddInput
	a.Container = str(in, "container")
	opt := func(k string) *string {
		if v, ok := in.Get(k); ok {
			if s, ok := v.(string); ok {
				return &s
			}
		}
		return nil
	}
	a.Content, a.URL, a.Title, a.SourceID, a.DocumentDate = opt("content"), opt("url"), opt("title"), opt("source_id"), opt("document_date")
	a.Type = str(in, "type")
	if v, ok := in.Get("messages"); ok {
		if list, ok := v.([]any); ok {
			for _, m := range list {
				o := m.(*jsonx.Object)
				a.Messages = append(a.Messages, api.ChatMessage{Role: str(o, "role"), Content: str(o, "content")})
			}
		}
	}
	if v, ok := in.Get("metadata"); ok {
		if o, ok := v.(*jsonx.Object); ok {
			a.Metadata = o
		}
	}
	return a
}

// addMultipart is addMultipart: the form read under the cap, then the
// file and the fields the way FormData.get gives them.
func (s *Server) addMultipart(c *keyCtx) (api.AddResult, waitOpts, error) {
	raw, err := readBodyCapped(c.r, MaxMultipartBytes)
	if err != nil {
		return api.AddResult{}, waitOpts{}, err
	}
	form, err := parseForm(c.r.Header.Get("Content-Type"), raw)
	if err != nil {
		return api.AddResult{}, waitOpts{}, httpErr(400, "invalid_request", "couldn't read the multipart body")
	}
	file := form.file("file")
	if file == nil {
		return api.AddResult{}, waitOpts{}, httpErr(400, "invalid_request", "multipart body needs a file field")
	}
	// containerSchema.parse: a ZodError here is not an HttpError, so the
	// route answers 500 (as the TS route does).
	cv, err := zod.Parse(containerSchema, form.value("container"))
	if err != nil {
		return api.AddResult{}, waitOpts{}, err
	}
	// Metadata that parses to something other than an object is dropped
	// (Known gaps).
	var metadata *jsonx.Object
	var metaAny any
	if raw := form.value("metadata"); raw != zod.Undefined && raw != "" {
		v, err := jsonx.Parse([]byte(raw.(string)))
		if err != nil {
			return api.AddResult{}, waitOpts{}, httpErr(400, "invalid_request", "metadata must be a JSON object")
		}
		metaAny = v
		metadata, _ = v.(*jsonx.Object)
	}
	var title *string
	if t, ok := form.value("title").(string); ok {
		if t = strx.Trim(t); t != "" {
			title = &t
		}
	}
	rawDate := ""
	if t, ok := form.value("document_date").(string); ok {
		rawDate = strx.Trim(t)
	}
	if rawDate != "" {
		dd := dates.Parse(rawDate)
		if dd == nil || dd.Precision != "day" {
			return api.AddResult{}, waitOpts{}, httpErr(400, "invalid_request", "document_date must be YYYY-MM-DD")
		}
		// { ...metadata, documentDate }: an array or string spreads its
		// indexes; other values spread to nothing.
		m := jsonx.NewMap[any]()
		switch mv := metaAny.(type) {
		case *jsonx.Object:
			m = mv.Clone()
		case []any:
			for i, x := range mv {
				m.Set(strconv.Itoa(i), x)
			}
		case string:
			for i, u := range utf16.Encode([]rune(mv)) {
				m.Set(strconv.Itoa(i), string(utf16.Decode([]uint16{u})))
			}
		}
		m.Set("documentDate", dd.Date)
		metadata = m
	}
	var sourceID *string
	if rs, ok := form.value("source_id").(string); ok && strx.Trim(rs) != "" {
		v, err := zod.Parse(zod.String().Trim().Min(1).Max(200), rs)
		if err != nil {
			return api.AddResult{}, waitOpts{}, httpErr(400, "invalid_request", "source_id must be 1 to 200 characters")
		}
		sid := v.(string)
		sourceID = &sid
	}
	fields := jsonx.NewMap[any]()
	if v, ok := form.value("wait").(string); ok && v != "" {
		fields.Set("wait", v)
	}
	if v, ok := form.value("wait_timeout").(string); ok && v != "" {
		fields.Set("wait_timeout", v)
	}
	body, err := parseWait(fields, "form")
	if err != nil {
		return api.AddResult{}, waitOpts{}, err
	}
	name := file.name
	if name == "" {
		name = "upload"
	}
	mimeType := file.mime
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	t := file.name
	if title != nil {
		t = *title
	}
	res, err := api.AddFile(c.d, c.ws, api.FileInput{Container: cv.(string), Title: &t, Metadata: metadata, SourceID: sourceID, Name: name, Mime: mimeType, Bytes: file.bytes}, c.now())
	return res, body, err
}

type formFile struct {
	name, mime string
	bytes      []byte
}

type formPart struct {
	name  string
	value string
	file  *formFile
}

type formData []formPart

// file is form.get(name) when it is a File.
func (f formData) file(name string) *formFile {
	for _, p := range f {
		if p.name == name {
			return p.file
		}
	}
	return nil
}

// value is form.get(name)?.toString(): Undefined when missing.
func (f formData) value(name string) any {
	for _, p := range f {
		if p.name == name {
			if p.file != nil {
				return "[object File]"
			}
			return p.value
		}
	}
	return zod.Undefined
}

// parseForm reads multipart/form-data like Response.formData(): a part
// with a non-empty filename is a File, every other part a string.
func parseForm(contentType string, body []byte) (formData, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, errors.New("no boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out formData
	for {
		p, err := mr.NextRawPart()
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return nil, err
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(p); err != nil {
			return nil, err
		}
		// undici's parser: a filename parameter (even "") makes a File, kept
		// as sent; its type is the part's Content-Type lowercased, text/plain
		// when there is none.
		_, disp, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		part := formPart{name: disp["name"]}
		if fn, ok := disp["filename"]; ok {
			typ := strings.ToLower(p.Header.Get("Content-Type"))
			if _, has := p.Header["Content-Type"]; !has {
				typ = "text/plain"
			}
			part.file = &formFile{name: fn, mime: typ, bytes: buf.Bytes()}
		} else {
			part.value = strx.DecodeUTF8(buf.Bytes())
		}
		out = append(out, part)
	}
}
