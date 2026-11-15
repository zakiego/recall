package mcp

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// Instructions is the server's instructions string.
const Instructions = "Recall is a long-term memory. Call search_memories or get_profile before answering questions about the user, their preferences or past work, or ask_memory for a short cited answer. For someone the user knows (a colleague, client or relative), call get_person with their name. Call add_memory when the user shares something worth remembering. Newer facts automatically supersede older ones. Facts can carry an event_date (when it happened) and a due_date (when it ends or is due); call upcoming to see what is due soon."

// Server is one request's MCP server: the caller's workspace, the key the
// ask limit counts against, and the clock in ms.
type Server struct {
	DB         *db.DB
	WS         string
	KeyID      string
	AskLimiter *ratelimit.Limiter
	Now        func() int64
}

// rpcError is an error a request handler throws (McpError and friends).
type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string { return e.message }

// handle is Protocol._onrequest for one request: the response object.
func (s *Server) handle(r *http.Request, req *jsonx.Object) any {
	id, _ := req.Get("id")
	method, _ := req.Get("method")
	p, _ := req.Get("params")
	params, _ := p.(*jsonx.Object)
	var result any
	var err error
	// Protocol parses each request with its schema; a ZodError becomes
	// -32603 with the issues as the message.
	if schema := requestSchemas[method.(string)]; schema != nil {
		if _, err := zod.Parse(schema, req); err != nil {
			msg := "Internal error"
			var ze *zod.Error
			if errors.As(err, &ze) {
				msg = zod.IssuesJSON(ze.Issues)
			}
			return jsonx.ObjectOf("jsonrpc", "2.0", "id", id, "error", jsonx.ObjectOf("code", -32603, "message", msg))
		}
	}
	switch method {
	case "initialize":
		pv, _ := params.Get("protocolVersion")
		version, _ := pv.(string)
		if !supported(version) {
			version = LatestProtocolVersion
		}
		result = jsonx.ObjectOf(
			"protocolVersion", version,
			"capabilities", jsonx.ObjectOf("tools", jsonx.ObjectOf("listChanged", true)),
			"serverInfo", jsonx.ObjectOf("name", "recall", "version", "1.0.0"),
			"instructions", Instructions,
		)
	case "ping":
		result = jsonx.NewMap[any]()
	case "tools/list":
		result = jsonx.ObjectOf("tools", toolList())
	case "tools/call":
		result = s.callTool(r, params)
	default:
		return jsonx.ObjectOf("jsonrpc", "2.0", "id", id, "error", jsonx.ObjectOf("code", -32601, "message", "Method not found"))
	}
	if err != nil {
		var re *rpcError
		code, msg := -32603, "Internal error"
		if errors.As(err, &re) {
			code, msg = re.code, re.message
		}
		return jsonx.ObjectOf("jsonrpc", "2.0", "id", id, "error", jsonx.ObjectOf("code", code, "message", msg))
	}
	return jsonx.ObjectOf("result", result, "jsonrpc", "2.0", "id", id)
}

// textResult is text(value): a string as is, anything else as
// JSON.stringify(value, null, 2).
func textResult(v any) *jsonx.Object {
	t, ok := v.(string)
	if !ok {
		t = pretty(v)
	}
	return jsonx.ObjectOf("content", []any{jsonx.ObjectOf("type", "text", "text", t)})
}

func errorResult(text string) *jsonx.Object {
	o := textResult(text)
	o.Set("isError", true)
	return o
}

// callTool is McpServer's tools/call handler: the tool, its input checked
// against the tool's zod shape, then the tool through guardTool.
func (s *Server) callTool(r *http.Request, params *jsonx.Object) (out any) {
	n, _ := params.Get("name")
	name, _ := n.(string)
	t := toolByName(name)
	if t == nil {
		// _registeredTools is a plain object: Object.prototype's members are
		// "tools" that aren't enabled.
		if objectPrototype[name] {
			return errorResult("MCP error -32602: Tool " + name + " disabled")
		}
		return errorResult("MCP error -32602: Tool " + name + " not found")
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[recall] mcp %s error: %v", name, p)
			out = errorResult("internal error")
		}
	}()
	args, present := params.Get("arguments")
	if !present || args == nil {
		args = jsonx.NewMap[any]()
	}
	out, err := zod.Parse(t.input, args)
	if err != nil {
		var ze *zod.Error
		if errors.As(err, &ze) {
			return errorResult("MCP error -32602: Input validation error: Invalid arguments for tool " + name + ": " + parseErrorMessage(ze.Issues))
		}
		return errorResult(err.Error())
	}
	res, err := t.run(s, r, out.(*jsonx.Object))
	if err != nil {
		// guardTool: an HttpError's message is written for callers; anything
		// else stays in the log.
		var he *httpx.Error
		if errors.As(err, &he) {
			return errorResult(he.Message)
		}
		log.Printf("[recall] mcp %s error: %v", name, err)
		return errorResult("internal error")
	}
	return res
}

var objectPrototype = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true, "valueOf": true,
	"__proto__": true, "toLocaleString": true,
}

// requestSchemas are the SDK's request schemas for the methods served
// (only what can fail; unknown keys are stripped).
var requestSchemas = map[string]zod.Schema{
	"initialize": zod.Object(
		zod.F("method", zod.Literal("initialize")),
		zod.F("params", zod.Object(
			zod.F("protocolVersion", zod.String()),
			zod.F("capabilities", zod.Record(zod.Unknown()).Keys(zod.String())),
			zod.F("clientInfo", zod.Object(zod.F("name", zod.String()), zod.F("version", zod.String()))),
		)),
	),
	"tools/call": zod.Object(
		zod.F("method", zod.Literal("tools/call")),
		zod.F("params", zod.Object(
			zod.F("name", zod.String()),
			zod.F("arguments", zod.Optional(zod.Record(zod.Unknown()).Keys(zod.String()))),
		)),
	),
}

// parseErrorMessage is the SDK's getParseErrorMessage for zod issues.
func parseErrorMessage(issues []zod.Issue) string {
	lines := make([]string, len(issues))
	for i, is := range issues {
		if len(is.Path) == 0 {
			lines[i] = is.Message
			continue
		}
		lines[i] = is.Message + " at " + dotPath(is.Path)
	}
	return strings.Join(lines, "\n")
}

// dotPath is the SDK's getDotPath.
func dotPath(path []any) string {
	var b strings.Builder
	for i, seg := range path {
		switch x := seg.(type) {
		case int:
			if i == 0 {
				b.WriteString(jsonx.JSString(float64(x)))
			} else {
				b.WriteString("[" + jsonx.JSString(float64(x)) + "]")
			}
		default:
			if i > 0 {
				b.WriteString(".")
			}
			b.WriteString(jsonx.JSString(seg))
		}
	}
	return b.String()
}
