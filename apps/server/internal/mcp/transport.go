// Package mcp is mcp.ts: Recall's MCP server, stateless Streamable HTTP with
// JSON responses. The transport rules follow @modelcontextprotocol/sdk
// 1.32.2 (WebStandardStreamableHTTPServerTransport with no session id and
// enableJsonResponse, and the Protocol and McpServer request handling), the
// tools follow mcp.ts.
package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	// LatestProtocolVersion is LATEST_PROTOCOL_VERSION.
	LatestProtocolVersion = "2025-11-25"
	maxBodySize           = 4 * 1024 * 1024
	maxBatchSize          = 100
)

// SupportedProtocolVersions is SUPPORTED_PROTOCOL_VERSIONS.
var SupportedProtocolVersions = []string{LatestProtocolVersion, "2025-06-18", "2025-03-26", "2024-11-05", "2024-10-07"}

func supported(v string) bool {
	for _, s := range SupportedProtocolVersions {
		if s == v {
			return true
		}
	}
	return false
}

func writeRaw(w http.ResponseWriter, status int, body []byte, headers map[string]string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	w.Write(body)
}

// jsonError is createJsonErrorResponse.
func jsonError(w http.ResponseWriter, status, code int, message string) {
	body := jsonx.Stringify(jsonx.ObjectOf("jsonrpc", "2.0", "error", jsonx.ObjectOf("code", code, "message", message), "id", nil))
	writeRaw(w, status, []byte(body), map[string]string{"content-type": "application/json"})
}

// protocolError is validateProtocolVersion: a header naming a version this
// server doesn't support.
func protocolError(w http.ResponseWriter, r *http.Request) bool {
	v := r.Header.Values("Mcp-Protocol-Version")
	if len(v) == 0 {
		return false
	}
	pv := strings.Join(v, ", ")
	if supported(pv) {
		return false
	}
	jsonError(w, 400, -32000, "Bad Request: Unsupported protocol version: "+pv+" (supported versions: "+strings.Join(SupportedProtocolVersions, ", ")+")")
	return true
}

// isJSONContentType is isJsonContentType: the parsed media type is
// application/json.
func isJSONContentType(h string) bool {
	if h == "application/json" {
		return true
	}
	if h == "" {
		return false
	}
	if mt, _, err := mime.ParseMediaType(h); err == nil {
		return mt == "application/json"
	}
	essence := strings.ToLower(strings.TrimSpace(strings.SplitN(h, ";", 2)[0]))
	if essence == "" || strings.Contains(h[len(essence):], ",") {
		return false
	}
	return essence == "application/json"
}

// Serve answers one /mcp request for workspace ws: a fresh server per
// request, as handleMcp does.
func (s *Server) Serve(w http.ResponseWriter, r *http.Request) {
	accept := strings.Join(r.Header.Values("Accept"), ", ")
	switch r.Method {
	case http.MethodGet:
		if !strings.Contains(accept, "text/event-stream") {
			jsonError(w, 406, -32000, "Not Acceptable: Client must accept text/event-stream")
			return
		}
		if protocolError(w, r) {
			return
		}
		// The standalone SSE stream closes at once: the per-request server
		// closes after answering.
		writeRaw(w, 200, nil, map[string]string{"content-type": "text/event-stream", "cache-control": "no-cache, no-transform", "connection": "keep-alive", "x-accel-buffering": "no"})
		return
	case http.MethodDelete:
		if protocolError(w, r) {
			return
		}
		w.WriteHeader(200)
		return
	case http.MethodPost:
	default:
		writeRaw(w, 405, []byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"Method not allowed."},"id":null}`), map[string]string{"allow": "GET, POST, DELETE", "content-type": "application/json"})
		return
	}
	if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
		jsonError(w, 406, -32000, "Not Acceptable: Client must accept both application/json and text/event-stream")
		return
	}
	if !isJSONContentType(strings.Join(r.Header.Values("Content-Type"), ", ")) {
		jsonError(w, 415, -32000, "Unsupported Media Type: Content-Type must be application/json")
		return
	}
	if r.ContentLength > maxBodySize {
		jsonError(w, 413, -32000, "Payload Too Large: Request body must not exceed "+strconv.Itoa(maxBodySize)+" bytes")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	if err == nil && len(raw) > maxBodySize {
		jsonError(w, 413, -32000, "Payload Too Large: Request body must not exceed "+strconv.Itoa(maxBodySize)+" bytes")
		return
	}
	var parsed any
	if err == nil {
		parsed, err = jsonx.Parse([]byte(strx.TextDecode(raw)))
	}
	if err != nil {
		jsonError(w, 400, -32700, "Parse error: Invalid JSON")
		return
	}
	list, isBatch := parsed.([]any)
	if isBatch && len(list) > maxBatchSize {
		jsonError(w, 400, -32600, "Invalid Request: Batch must not exceed "+strconv.Itoa(maxBatchSize)+" messages")
		return
	}
	if !isBatch {
		list = []any{parsed}
	}
	msgs := make([]*jsonx.Object, len(list))
	for i, m := range list {
		o, ok := m.(*jsonx.Object)
		if !ok || !validMessage(o) {
			jsonError(w, 400, -32700, "Parse error: Invalid JSON-RPC message")
			return
		}
		msgs[i] = o
	}
	isInit := false
	for _, m := range msgs {
		isInit = isInit || isInitializeRequest(m)
	}
	if isInit && len(msgs) > 1 {
		jsonError(w, 400, -32600, "Invalid Request: Only one initialization request is allowed")
		return
	}
	if !isInit && protocolError(w, r) {
		return
	}
	var requests []*jsonx.Object
	for _, m := range msgs {
		if isRequest(m) {
			requests = append(requests, m)
		}
	}
	if len(requests) == 0 {
		w.WriteHeader(202)
		return
	}
	// Responses are kept per request id: a repeated id gets one answer, the
	// first request's.
	var responses []any
	seen := map[string]bool{}
	for _, m := range requests {
		id, _ := m.Get("id")
		key := jsonx.Stringify(id)
		if seen[key] {
			s.handle(r, m)
			continue
		}
		seen[key] = true
		responses = append(responses, s.handle(r, m))
	}
	var body any = responses
	if len(responses) == 1 {
		body = responses[0]
	}
	writeRaw(w, 200, []byte(jsonx.Stringify(body)), map[string]string{"content-type": "application/json"})
}

// --- JSONRPCMessageSchema ----------------------------------------------------------------

func hasOnly(o *jsonx.Object, keys ...string) bool {
	for _, k := range o.Keys() {
		ok := false
		for _, want := range keys {
			ok = ok || k == want
		}
		if !ok {
			return false
		}
	}
	return true
}

func validID(v any, present bool) bool {
	if !present {
		return false
	}
	switch x := v.(type) {
	case string:
		return true
	case float64:
		return x == float64(int64(x)) && x <= 9007199254740991 && x >= -9007199254740991
	}
	return false
}

// validParams is BaseRequestParamsSchema.loose().optional(): an object
// whose _meta, when there, is an object with a string or integer
// progressToken.
func validParams(o *jsonx.Object) bool {
	params, has := o.Get("params")
	if !has {
		return true
	}
	p, ok := params.(*jsonx.Object)
	if !ok {
		return false
	}
	meta, has := p.Get("_meta")
	if !has {
		return true
	}
	m, ok := meta.(*jsonx.Object)
	if !ok {
		return false
	}
	tok, has := m.Get("progressToken")
	return !has || validID(tok, true)
}

func isRequest(o *jsonx.Object) bool {
	v, _ := o.Get("jsonrpc")
	id, hasID := o.Get("id")
	method, _ := o.Get("method")
	_, isStr := method.(string)
	return hasOnly(o, "jsonrpc", "id", "method", "params") && v == "2.0" && validID(id, hasID) && isStr && validParams(o)
}

func isNotification(o *jsonx.Object) bool {
	v, _ := o.Get("jsonrpc")
	method, _ := o.Get("method")
	_, isStr := method.(string)
	return hasOnly(o, "jsonrpc", "method", "params") && v == "2.0" && isStr && validParams(o)
}

func isResult(o *jsonx.Object) bool {
	v, _ := o.Get("jsonrpc")
	id, hasID := o.Get("id")
	res, _ := o.Get("result")
	_, resObj := res.(*jsonx.Object)
	return hasOnly(o, "jsonrpc", "id", "result") && v == "2.0" && validID(id, hasID) && resObj
}

func isErrorResponse(o *jsonx.Object) bool {
	v, _ := o.Get("jsonrpc")
	id, hasID := o.Get("id")
	e, _ := o.Get("error")
	eo, ok := e.(*jsonx.Object)
	if !ok || !hasOnly(o, "jsonrpc", "id", "error") || v != "2.0" || (hasID && !validID(id, true)) {
		return false
	}
	code, _ := eo.Get("code")
	msg, _ := eo.Get("message")
	c, isNum := code.(float64)
	_, isStr := msg.(string)
	return isNum && c == float64(int64(c)) && isStr
}

func validMessage(o *jsonx.Object) bool {
	return isRequest(o) || isNotification(o) || isResult(o) || isErrorResponse(o)
}

// isInitializeRequest: method initialize with protocolVersion, capabilities
// and clientInfo { name, version }.
func isInitializeRequest(o *jsonx.Object) bool {
	if !isRequest(o) {
		return false
	}
	if m, _ := o.Get("method"); m != "initialize" {
		return false
	}
	p, _ := o.Get("params")
	params, ok := p.(*jsonx.Object)
	if !ok {
		return false
	}
	pv, _ := params.Get("protocolVersion")
	caps, _ := params.Get("capabilities")
	ci, _ := params.Get("clientInfo")
	_, pvOK := pv.(string)
	_, capsOK := caps.(*jsonx.Object)
	info, infoOK := ci.(*jsonx.Object)
	if !pvOK || !capsOK || !infoOK {
		return false
	}
	name, _ := info.Get("name")
	version, _ := info.Get("version")
	_, nameOK := name.(string)
	_, versionOK := version.(string)
	return nameOK && versionOK
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
