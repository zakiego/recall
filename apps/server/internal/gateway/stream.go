package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

// Simpang answers /v1/chat/completions with "stream": true as an OpenAI
// chat-completions stream (SSE): each event is a chat.completion.chunk whose
// choices[].delta.content carries the next piece of the reply, and
// "data: [DONE]" ends it. Reasoning deltas (reasoning_content) and usage-only
// chunks carry no reply text. An upstream failure after the 200 arrives as a
// chunk with an "error" field instead of choices.

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

// streamChunk holds the chunk fields the reader uses.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content *string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

var errDone = errors.New("[DONE]")

// ReadStreamText is the reply text of an SSE body: every choice's
// delta.content in order. It stops at [DONE] (later events are ignored) or
// at an error chunk, which becomes a stream_error Error.
func ReadStreamText(body io.Reader) (string, error) {
	var b strings.Builder
	err := sseEvents(body, func(data string) error {
		if data == "[DONE]" {
			return errDone
		}
		var c streamChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return &Error{Msg: "bad stream chunk: " + err.Error() + ": " + data, Status: 502, Type: "bad_stream"}
		}
		if len(c.Error) > 0 && string(c.Error) != "null" {
			return &Error{Msg: chunkErrorMessage(c.Error), Status: 502, Type: "stream_error"}
		}
		for _, ch := range c.Choices {
			if ch.Delta.Content != nil {
				b.WriteString(*ch.Delta.Content)
			}
		}
		return nil
	})
	if err != nil && err != errDone {
		var ge *Error
		if errors.As(err, &ge) {
			return "", ge
		}
		return "", &Error{Msg: err.Error(), Status: 502, Type: "stream_error"}
	}
	return b.String(), nil
}

// chunkErrorMessage is an error chunk's error.message, or the error itself
// when it's a bare string, or its JSON text otherwise.
func chunkErrorMessage(raw json.RawMessage) string {
	v, err := jsonx.Parse(raw)
	if err != nil {
		return string(raw)
	}
	if s, ok := v.(string); ok {
		return s
	}
	if o, ok := v.(*jsonx.Object); ok {
		if m, ok := o.Get("message"); ok && m != nil {
			return jsonx.JSString(m)
		}
	}
	return jsonx.Stringify(v)
}
