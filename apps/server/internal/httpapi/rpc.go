package httpapi

// The dashboard's server functions (apps/app/src/lib/fns.ts,
// connector-fns.ts, oauth-fns.ts) as `GET|POST /app/api/rpc/<name>`.
//
// Input: a GET sends it as ?input=<JSON>, a POST as the JSON body (uploadFile
// as multipart form data); none is undefined. The handler's return value is
// the answer, as JSON (nil is null).
//
// A call runs as TanStack Start ran it: the validator first, then the
// function body, which does its own requireSameOrigin / requireUser /
// requireMember in the TS order (Handler.SameOrigin is the first statement
// of the bodies that have it).
//
// Errors are one shape: {"error": "<message>", "code": "<CODE>", "issues"?: [...]}
//
//	404 NOT_FOUND          no such function
//	405 METHOD_NOT_ALLOWED wrong method
//	400 VALIDATION         the input failed its schema (error is ZodError's message)
//	401 UNAUTHORIZED       no session (error "UNAUTHORIZED", as UnauthorizedError)
//	403 FORBIDDEN          ForbiddenError, with its message
//	400 ERROR              a thrown Error the dashboard shows (rpcFail)
//	500 ERROR              anything else, with its message (TanStack sent it too)

import (
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"

	"github.com/zakiego/recall/apps/server/internal/auth"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/viewer"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// RPC is one server function.
type RPC struct {
	Name   string
	Method string
	// Input is the validator's schema; nil takes no input.
	Input zod.Schema
	// Parse replaces Input for a validator that isn't a zod schema
	// (uploadFile's FormData check).
	Parse func(c *Call) (any, error)
	// SameOrigin: the body starts with requireSameOrigin().
	SameOrigin bool
	Fn         func(c *Call) (any, error)
}

// Call is one invocation.
type Call struct {
	R *http.Request
	W http.ResponseWriter
	S *Server
	// Input is the validator's output (jsonx values) or zod.Undefined.
	Input any
	// cookieDone: the session cookie was renewed or cleared already.
	cookieDone bool
}

func (c *Call) DB() *db.DB  { return c.S.DB }
func (c *Call) Now() int64  { return c.S.Now() }
func (c *Call) URL() string { return config.Load().URL }

// Obj is the input object.
func (c *Call) Obj() *jsonx.Object {
	o, _ := c.Input.(*jsonx.Object)
	return o
}

// Str is a string field of the input ("" when absent).
func (c *Call) Str(k string) string {
	v, _ := c.Obj().Get(k)
	s, _ := v.(string)
	return s
}

// Opt is a field of the input and whether it was there (undefined is not).
func (c *Call) Opt(k string) (any, bool) {
	if c.Obj() == nil {
		return nil, false
	}
	v, ok := c.Obj().Get(k)
	if ok && zod.IsUndefined(v) {
		return nil, false
	}
	return v, ok
}

// RequireUser is requireUser.
func (c *Call) RequireUser() (*viewer.Viewer, error) {
	v, err := c.Viewer()
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, viewer.ErrUnauthorized
	}
	return v, nil
}

// RequireMember is requireMember(min).
func (c *Call) RequireMember(min string) (*viewer.Member, error) {
	if _, err := c.Viewer(); err != nil {
		return nil, err
	}
	return viewer.RequireMember(c.DB(), c.R, min, c.Now())
}

// Viewer is getViewer: nil when signed out. Like Better Auth's getSession,
// a session whose expiry moved forward sends its cookie again, and a
// cookie that names no live session is cleared.
func (c *Call) Viewer() (*viewer.Viewer, error) {
	v, err := viewer.FromRequest(c.DB(), c.R, c.Now())
	if err != nil {
		return nil, err
	}
	if !c.cookieDone {
		c.cookieDone = true
		switch {
		case v != nil && v.Found != nil && v.Found.Refreshed:
			http.SetCookie(c.W, auth.SessionCookie(c.URL(), v.Found.Token))
		case v == nil && auth.TokenFrom(c.R) != "":
			http.SetCookie(c.W, auth.ClearCookie(c.URL(), auth.CookieName))
		}
	}
	return v, nil
}

// rpcError is a thrown Error whose message the dashboard shows.
type rpcError struct{ msg string }

func (e *rpcError) Error() string { return e.msg }

// rpcFail is `throw new Error(msg)`.
func rpcFail(msg string) error { return &rpcError{msg} }

var (
	rpcMu  sync.RWMutex
	rpcFns = map[string]*RPC{}
)

// registerRPC adds a server function; a duplicate panics at startup.
func registerRPC(fns ...RPC) {
	rpcMu.Lock()
	defer rpcMu.Unlock()
	for _, f := range fns {
		if f.Method != http.MethodGet && f.Method != http.MethodPost || f.Fn == nil {
			panic("httpapi: bad server function " + f.Name)
		}
		if _, dup := rpcFns[f.Name]; dup {
			panic("httpapi: duplicate server function " + f.Name)
		}
		f := f
		rpcFns[f.Name] = &f
	}
}

// RPCNames lists the registered server functions.
func RPCNames() []string {
	rpcMu.RLock()
	defer rpcMu.RUnlock()
	out := make([]string, 0, len(rpcFns))
	for n := range rpcFns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func rpcWrite(w http.ResponseWriter, status int, code, msg string, issues []*jsonx.Object) {
	body := jsonx.ObjectOf("error", msg, "code", code)
	if issues != nil {
		body.Set("issues", issues)
	}
	writeJSON(w, status, body, map[string]string{"cache-control": "no-store"})
}

// maxRPCBody is the REST cap (MAX_JSON_BYTES): addText takes 1M characters.
const maxRPCBody = MaxJSONBytes

func rpcInput(r *http.Request) (any, error) {
	var raw []byte
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		if !q.Has("input") {
			return zod.Undefined, nil
		}
		raw = []byte(q.Get("input"))
	} else {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBody+1))
		if err != nil {
			return nil, rpcFail("Couldn't read the request.")
		}
		if len(b) > maxRPCBody {
			return nil, &rpcStatus{413, "VALIDATION", "Request body is too large."}
		}
		raw = b
	}
	if len(raw) == 0 {
		return zod.Undefined, nil
	}
	v, err := jsonx.Parse(raw)
	if err != nil {
		return nil, &rpcStatus{400, "VALIDATION", "Input isn't valid JSON."}
	}
	return v, nil
}

type rpcStatus struct {
	status int
	code   string
	msg    string
}

func (e *rpcStatus) Error() string { return e.msg }

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rpcMu.RLock()
	f := rpcFns[name]
	rpcMu.RUnlock()
	if f == nil {
		rpcWrite(w, 404, "NOT_FOUND", "No server function "+name, nil)
		return
	}
	if r.Method != f.Method {
		w.Header().Set("allow", f.Method)
		rpcWrite(w, 405, "METHOD_NOT_ALLOWED", name+" takes "+f.Method, nil)
		return
	}
	c := &Call{R: r, W: w, S: s}
	var err error
	if f.Parse != nil {
		c.Input, err = f.Parse(c)
	} else {
		c.Input, err = rpcInput(r)
		if err == nil && f.Input != nil {
			c.Input, err = zod.Parse(f.Input, c.Input)
		}
	}
	if err == nil && f.SameOrigin {
		err = viewer.RequireSameOrigin(r, config.Load().URL)
	}
	var out any
	if err == nil {
		out, err = s.runRPC(f, c)
	}
	if err != nil {
		s.rpcError(w, name, err)
		return
	}
	writeJSON(w, 200, out, map[string]string{"cache-control": "no-store"})
}

func (s *Server) runRPC(f *RPC, c *Call) (out any, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[recall] server function %s panicked: %v", f.Name, p)
			out, err = nil, errors.New("internal error")
		}
	}()
	return f.Fn(c)
}

func (s *Server) rpcError(w http.ResponseWriter, name string, err error) {
	var ze *zod.Error
	var fe *viewer.Forbidden
	var ue *rpcError
	var st *rpcStatus
	var he *httpx.Error
	switch {
	case errors.As(err, &ze):
		issues := make([]*jsonx.Object, len(ze.Issues))
		for i, is := range ze.Issues {
			issues[i] = is.JSON()
		}
		rpcWrite(w, 400, "VALIDATION", ze.Error(), issues)
	case errors.Is(err, viewer.ErrUnauthorized):
		rpcWrite(w, 401, "UNAUTHORIZED", "UNAUTHORIZED", nil)
	case errors.As(err, &fe):
		rpcWrite(w, 403, "FORBIDDEN", fe.Msg, nil)
	case errors.As(err, &ue):
		rpcWrite(w, 400, "ERROR", ue.msg, nil)
	case errors.As(err, &st):
		rpcWrite(w, st.status, st.code, st.msg, nil)
	case errors.As(err, &he):
		// An HttpError (ask, add) keeps its status and text.
		rpcWrite(w, he.Status, "ERROR", he.Message, nil)
	default:
		log.Printf("[recall] server function %s failed: %v", name, err)
		rpcWrite(w, 500, "ERROR", err.Error(), nil)
	}
}
