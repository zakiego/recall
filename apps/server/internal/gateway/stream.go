package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// The gateway answers /v1/chat with an AI SDK UI message stream (SSE). The
// TS build reads it with the AI SDK's own parseJsonEventStream and
// readUIMessageStream; this is the same walk for the chunk types that change
// the text: every chunk is validated against uiMessageChunkSchema, text and
// reasoning parts are opened, extended and closed by id, a "start" with a new
// message id and "reset-step" drop parts, and an "error" chunk ends the read.

// sseEvents yields the data of each event (eventsource-parser rules: lines
// end in \r\n, \r or \n; "data:" lines are joined by "\n"; a blank line
// dispatches; comments and other fields are ignored; an unfinished last
// event is dropped).
func sseEvents(r io.Reader, fn func(data string) error) error {
	var buf []byte
	var data []string
	has := false
	// process handles every complete line in buf. A trailing "\r" waits for
	// the next read unless the body ended, since a "\n" may follow it.
	process := func(ended bool) error {
		for {
			i := bytes.IndexAny(buf, "\r\n")
			if i < 0 || (buf[i] == '\r' && i+1 == len(buf) && !ended) {
				return nil // no terminator yet (at the end: an incomplete line)
			}
			line := string(buf[:i])
			if buf[i] == '\r' && i+1 < len(buf) && buf[i+1] == '\n' {
				buf = buf[i+2:]
			} else {
				buf = buf[i+1:]
			}
			if line == "" {
				if has {
					if err := fn(strings.Join(data, "\n")); err != nil {
						return err
					}
				}
				data, has = nil, false
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			field, value, found := strings.Cut(line, ":")
			if found {
				value = strings.TrimPrefix(value, " ")
			}
			if field == "data" {
				data = append(data, value)
				has = true
			}
		}
	}
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if perr := process(err == io.EOF); perr != nil {
			return perr
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type part struct {
	kind string // "text", "reasoning", "step-start", "other"
	text string
}

type streamState struct {
	messageID string
	parts     []*part
	text      map[string]*part
	reasoning map[string]*part
}

func (s *streamState) currentStepStart() int {
	i := len(s.parts) - 1
	for i >= 0 && s.parts[i].kind != "step-start" {
		i--
	}
	return i + 1
}

// chunk fields the processor reads.
type chunk struct {
	Type      string  `json:"type"`
	ID        *string `json:"id"`
	Delta     *string `json:"delta"`
	ErrorText *string `json:"errorText"`
	MessageID *string `json:"messageId"`
}

func uiStreamError(kind, id, what, before string) error {
	return fmt.Errorf(`Received %s for missing %s part with ID "%s". Ensure a "%s" chunk is sent before any "%s" chunks.`, kind, what, id, before, kind)
}

// ReadStreamText is readStreamText: the final assistant text of an SSE body.
func ReadStreamText(body io.Reader) (string, error) {
	st := &streamState{text: map[string]*part{}, reasoning: map[string]*part{}}
	var streamError *string
	err := sseEvents(body, func(data string) error {
		if data == "[DONE]" {
			return nil
		}
		raw := []byte(data)
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return &Error{Msg: "bad stream chunk: AI_JSONParseError: JSON parsing failed: Text: " + data + ".\nError message: " + err.Error(), Status: 502, Type: "bad_stream"}
		}
		if msg := validateChunk(v); msg != "" {
			return &Error{Msg: "bad stream chunk: AI_TypeValidationError: Type validation failed: Value: " + jsonx.Stringify(v) + ".\nError message: " + msg, Status: 502, Type: "bad_stream"}
		}
		var c chunk
		_ = json.Unmarshal(raw, &c)
		if c.Type == "error" {
			streamError = c.ErrorText
		}
		return st.apply(c)
	})
	if err != nil {
		if ge, ok := err.(*Error); ok {
			return "", ge
		}
		msg := err.Error()
		if streamError != nil {
			msg = *streamError
		}
		return "", &Error{Msg: msg, Status: 502, Type: "stream_error"}
	}
	if streamError != nil {
		return "", &Error{Msg: *streamError, Status: 502, Type: "stream_error"}
	}
	var b strings.Builder
	for _, p := range st.parts {
		if p.kind == "text" {
			b.WriteString(p.text)
		}
	}
	return b.String(), nil
}

// errTerminate is an "error" chunk: readUIMessageStream with terminateOnError
// stops there and throws its text.
type errTerminate struct{ text string }

func (e *errTerminate) Error() string { return e.text }

func (s *streamState) apply(c chunk) error {
	id := ""
	if c.ID != nil {
		id = *c.ID
	}
	switch c.Type {
	case "text-start":
		p := &part{kind: "text"}
		s.text[id] = p
		s.parts = append(s.parts, p)
	case "text-delta":
		p := s.text[id]
		if p == nil {
			return uiStreamError("text-delta", id, "text", "text-start")
		}
		p.text += *c.Delta
	case "text-end":
		if s.text[id] == nil {
			return uiStreamError("text-end", id, "text", "text-start")
		}
		delete(s.text, id)
	case "reasoning-start":
		p := &part{kind: "reasoning"}
		s.reasoning[id] = p
		s.parts = append(s.parts, p)
	case "reasoning-delta":
		p := s.reasoning[id]
		if p == nil {
			return uiStreamError("reasoning-delta", id, "reasoning", "reasoning-start")
		}
		p.text += *c.Delta
	case "reasoning-end":
		if s.reasoning[id] == nil {
			return uiStreamError("reasoning-end", id, "reasoning", "reasoning-start")
		}
		delete(s.reasoning, id)
	case "start-step":
		s.parts = append(s.parts, &part{kind: "step-start"})
	case "reset-step":
		s.text = map[string]*part{}
		s.reasoning = map[string]*part{}
		s.parts = s.parts[:s.currentStepStart()]
	case "start":
		if c.MessageID != nil && *c.MessageID != s.messageID {
			s.parts = nil
			s.text = map[string]*part{}
			s.reasoning = map[string]*part{}
		}
		if c.MessageID != nil {
			s.messageID = *c.MessageID
		}
	case "error":
		return &errTerminate{text: *c.ErrorText}
	case "file", "reasoning-file", "source-url", "source-document", "custom":
		s.parts = append(s.parts, &part{kind: "other"})
	}
	return nil
}

func isString(o map[string]any, k string) bool { _, ok := o[k].(string); return ok }

// optString is z.string().optional(): absent or a string (null fails).
func optString(o map[string]any, k string) bool {
	v, ok := o[k]
	if !ok {
		return true
	}
	_, isStr := v.(string)
	return isStr
}

func optBool(o map[string]any, k string) bool {
	v, ok := o[k]
	if !ok {
		return true
	}
	_, isB := v.(bool)
	return isB
}

func has(o map[string]any, k string) bool { _, ok := o[k]; return ok }

// validateChunk checks a chunk against uiMessageChunkSchema's required fields
// and returns "" when it passes.
func validateChunk(v any) string {
	o, ok := v.(map[string]any)
	if !ok {
		return "expected object"
	}
	t, ok := o["type"].(string)
	if !ok {
		return "invalid type"
	}
	need := func(conds ...bool) string {
		for _, c := range conds {
			if !c {
				return "invalid " + t + " chunk"
			}
		}
		return ""
	}
	switch t {
	case "text-start", "reasoning-start", "reasoning-end":
		return need(isString(o, "id"))
	case "text-delta", "reasoning-delta":
		return need(isString(o, "id"), isString(o, "delta"))
	case "text-end":
		_, arr := o["citations"].([]any)
		return need(isString(o, "id"), !has(o, "citations") || arr)
	case "error":
		return need(isString(o, "errorText"))
	case "tool-input-start":
		return need(isString(o, "toolCallId"), isString(o, "toolName"), optBool(o, "providerExecuted"), optBool(o, "dynamic"), optString(o, "title"))
	case "tool-input-delta":
		return need(isString(o, "toolCallId"), isString(o, "inputTextDelta"))
	case "tool-input-available":
		return need(isString(o, "toolCallId"), isString(o, "toolName"), optBool(o, "providerExecuted"), optBool(o, "dynamic"), optString(o, "title"))
	case "tool-input-error":
		return need(isString(o, "toolCallId"), isString(o, "toolName"), isString(o, "errorText"))
	case "tool-approval-request":
		return need(isString(o, "approvalId"), isString(o, "toolCallId"))
	case "tool-approval-response":
		return need(isString(o, "approvalId"), has(o, "approved") && optBool(o, "approved"))
	case "tool-output-available":
		return need(isString(o, "toolCallId"), optBool(o, "preliminary"))
	case "tool-output-error":
		return need(isString(o, "toolCallId"), isString(o, "errorText"))
	case "tool-output-denied":
		return need(isString(o, "toolCallId"))
	case "custom":
		return need(isString(o, "kind"))
	case "source-url":
		return need(isString(o, "sourceId"), isString(o, "url"))
	case "source-document":
		return need(isString(o, "sourceId"), isString(o, "mediaType"), isString(o, "title"), optString(o, "filename"))
	case "file", "reasoning-file":
		return need(isString(o, "url"), isString(o, "mediaType"))
	case "steering-message":
		_, arr := o["parts"].([]any)
		return need(isString(o, "id"), arr)
	case "start-step", "finish-step", "reset-step", "message-metadata":
		if t == "message-metadata" {
			return need(has(o, "messageMetadata"))
		}
		return ""
	case "start":
		return need(optString(o, "messageId"))
	case "finish":
		fr, ok := o["finishReason"]
		if !ok {
			return ""
		}
		switch fr {
		case "stop", "length", "content-filter", "tool-calls", "error", "interrupted", "other":
			return ""
		}
		return "invalid finishReason"
	case "abort":
		return need(optString(o, "reason"))
	}
	if strings.HasPrefix(t, "data-") {
		return need(optString(o, "id"), optBool(o, "transient"))
	}
	return "invalid chunk type " + t
}
