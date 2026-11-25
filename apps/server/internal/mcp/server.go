// Package mcp is Recall's MCP server: stateless Streamable HTTP with JSON
// responses, served by the official Go SDK. The SDK speaks both protocol
// eras on one endpoint: 2025 clients go through initialize, 2026-07-28
// clients through server/discover and a per-request _meta envelope. The
// tools follow mcp.ts.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/httpx"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/ratelimit"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// maxBodySize caps a request body; a bigger one gets 413.
const maxBodySize = 4 * 1024 * 1024

// Server is one request's MCP server: the caller's workspace, the key the
// ask limit counts against, and the clock in ms.
type Server struct {
	DB         *db.DB
	WS         string
	KeyID      string
	AskLimiter *ratelimit.Limiter
	Now        func() int64
}

var handlerOptions = &sdk.StreamableHTTPOptions{
	Stateless:    true,
	JSONResponse: true,
	// Every request carries an API key or OAuth token a DNS-rebinding page
	// can't have, and behind the tunnel the Host is the public name.
	DisableLocalhostProtection: true,
	MaxRequestBodyBytes:        maxBodySize,
}

// Serve answers one /mcp request for the caller s was built for.
func (s *Server) Serve(w http.ResponseWriter, r *http.Request) {
	sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.sdkServer() }, handlerOptions).ServeHTTP(w, r)
}

func (s *Server) sdkServer() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "recall", Version: "1.0.0"}, &sdk.ServerOptions{
		Instructions: Instructions,
		// Tools only: no logging capability, as before the SDK.
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{ListChanged: true}},
	})
	defs := sdkTools()
	for i, t := range tools {
		srv.AddTool(defs[i], func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return s.callTool(ctx, t, req.Params.Arguments), nil
		})
	}
	srv.AddReceivingMiddleware(unknownTools, toolOrder)
	return srv
}

// sdkTools are the tools as tools/list shows them, built once.
var sdkTools = sync.OnceValue(func() []*sdk.Tool {
	out := make([]*sdk.Tool, len(tools))
	for i, t := range tools {
		out[i] = t.sdkTool()
	}
	return out
})

// unknownTools answers a tools/call for a tool that doesn't exist with an
// isError result, as McpServer does, instead of the SDK's JSON-RPC error.
func unknownTools(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		if method == "tools/call" {
			if call, ok := req.(*sdk.CallToolRequest); ok && call.Params != nil && toolByName(call.Params.Name) == nil {
				name := call.Params.Name
				// _registeredTools is a plain object: Object.prototype's members
				// are "tools" that aren't enabled.
				if objectPrototype[name] {
					return errorResult("MCP error -32602: Tool " + name + " disabled"), nil
				}
				return errorResult("MCP error -32602: Tool " + name + " not found"), nil
			}
		}
		return next(ctx, method, req)
	}
}

// toolOrder lists the tools in the order they are declared, as McpServer
// does: get_playbook first, so a client that reads in order meets it before
// the rest. The SDK sorts them by name.
func toolOrder(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		res, err := next(ctx, method, req)
		if list, ok := res.(*sdk.ListToolsResult); ok && err == nil {
			slices.SortStableFunc(list.Tools, func(a, b *sdk.Tool) int { return toolIndex(a.Name) - toolIndex(b.Name) })
		}
		return res, err
	}
}

// textResult is text(value): a string as is, anything else as
// JSON.stringify(value, null, 2).
func textResult(v any) *sdk.CallToolResult {
	t, ok := v.(string)
	if !ok {
		t = pretty(v)
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: t}}}
}

func errorResult(text string) *sdk.CallToolResult {
	o := textResult(text)
	o.IsError = true
	return o
}

// callTool is McpServer's tools/call handler: the tool's input checked
// against its zod shape, then the tool through guardTool.
func (s *Server) callTool(ctx context.Context, t *tool, raw json.RawMessage) (out *sdk.CallToolResult) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[recall] mcp %s error: %v", t.name, p)
			out = errorResult("internal error")
		}
	}()
	var args any = jsonx.NewMap[any]()
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && string(trimmed) != "null" {
		v, err := jsonx.Parse([]byte(strx.TextDecode(trimmed)))
		if err != nil {
			return errorResult("MCP error -32602: Input validation error: Invalid arguments for tool " + t.name + ": " + err.Error())
		}
		args = v
	}
	parsed, err := zod.Parse(t.input, args)
	if err != nil {
		var ze *zod.Error
		if errors.As(err, &ze) {
			return errorResult("MCP error -32602: Input validation error: Invalid arguments for tool " + t.name + ": " + parseErrorMessage(ze.Issues))
		}
		return errorResult(err.Error())
	}
	res, err := t.run(s, ctx, parsed.(*jsonx.Object))
	if err != nil {
		// guardTool: an HttpError's message is written for callers; anything
		// else stays in the log.
		var he *httpx.Error
		if errors.As(err, &he) {
			return errorResult(he.Message)
		}
		log.Printf("[recall] mcp %s error: %v", t.name, err)
		return errorResult("internal error")
	}
	return res
}

var objectPrototype = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true, "valueOf": true,
	"__proto__": true, "toLocaleString": true,
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

// pretty is JSON.stringify(v, null, 2).
func pretty(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "null"
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return string(b)
	}
	return out.String()
}
