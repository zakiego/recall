// Package gateway is the client for Simpang Router's OpenAI-compatible
// /v1/chat/completions (streamed), the prompts and the response schemas.
package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/strx"
	"github.com/zakiego/recall/apps/server/internal/zod"
)

// Error is GatewayError. RetryAfterMs is 0 when the gateway sent none.
type Error struct {
	Msg          string
	Status       int
	Type         string
	RetryAfterMs float64
}

func (e *Error) Error() string  { return e.Msg }
func (e *Error) JSName() string { return "GatewayError" }

// RetryAfterMillis is the Retry-After the gateway sent, 0 for none.
func (e *Error) RetryAfterMillis() float64 { return e.RetryAfterMs }

// InvalidJSONError is InvalidJsonError: the model's reply had no usable JSON.
type InvalidJSONError struct {
	Msg  string
	Text string
}

func (e *InvalidJSONError) Error() string  { return e.Msg }
func (e *InvalidJSONError) JSName() string { return "InvalidJsonError" }

var fencedRE = jsre.MustCompile("```(?:json)?\\s*([\\s\\S]*?)```", "")

// ExtractJSON is extractJson: the fenced block or the whole text, else the
// outermost {...}. Objects keep their key order (*jsonx.Object).
func ExtractJSON(text string) (any, error) {
	body := text
	if m := fencedRE.Find(jsre.ToUnits(text)); m != nil {
		body, _ = m.Group(1)
	}
	body = strx.Trim(body)
	if v, err := jsonx.Parse([]byte(body)); err == nil {
		return v, nil
	}
	start := strings.Index(body, "{")
	end := strings.LastIndex(body, "}")
	if start >= 0 && end > start {
		if v, err := jsonx.Parse([]byte(body[start : end+1])); err == nil {
			return v, nil
		}
	}
	return nil, &InvalidJSONError{Msg: "model did not return JSON", Text: text}
}

// Request is ChatRequest. Model "" means config's extractModel.
type Request struct {
	System string
	Prompt string
	Model  string
}

// Do sends an HTTP request (tests replace it).
type Do func(*http.Request) (*http.Response, error)

// ChatTimeout is the 180 s limit on one call.
const ChatTimeout = 180 * time.Second

var defaultClient = &http.Client{Transport: &http.Transport{}}

type chatBody struct {
	Model           string    `json:"model"`
	ReasoningEffort string    `json:"reasoning_effort"`
	Stream          bool      `json:"stream"`
	Messages        []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOnce is chatOnce: one call, the reply text. ctx cancels it early; the
// 180 s timeout applies either way.
func ChatOnce(ctx context.Context, req Request, do Do) (string, error) {
	cfg := config.Load()
	if cfg.GatewayKey == "" {
		return "", &Error{Msg: "GATEWAY_KEY is not set", Status: 0, Type: "config"}
	}
	if cfg.GatewayURL == "" {
		return "", &Error{Msg: "GATEWAY_URL is not set", Status: 0, Type: "config"}
	}
	model := req.Model
	if model == "" {
		model = cfg.ExtractModel
	}
	body, err := jsonx.Marshal(chatBody{
		Model:           model,
		ReasoningEffort: "low",
		Stream:          true,
		Messages:        []message{{Role: "system", Content: req.System}, {Role: "user", Content: req.Prompt}},
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, ChatTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(ctx, "POST", cfg.GatewayURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", jserr.New("TypeError", "Invalid URL")
	}
	hr.Header.Set("authorization", "Bearer "+cfg.GatewayKey)
	hr.Header.Set("content-type", "application/json")
	if do == nil {
		do = defaultClient.Do
	}
	res, err := do(hr)
	if err != nil {
		return "", fetchError(ctx, err)
	}
	defer res.Body.Close()
	// !res.ok || !res.body: fetch gives these statuses no body.
	nullBody := res.StatusCode == 101 || res.StatusCode == 103 || res.StatusCode == 204 || res.StatusCode == 205 || res.StatusCode == 304
	if res.StatusCode < 200 || res.StatusCode > 299 || nullBody {
		typ := "http_error"
		msg := fmt.Sprintf("gateway returned %d", res.StatusCode)
		// error.code ?? error.type ?? type, error.message ?? message, each on
		// its own. Simpang puts its own reason (model_not_allowed,
		// budget_exceeded) in code and the OpenAI category in type.
		if b, err := io.ReadAll(res.Body); err == nil && !nullBody {
			if v, err := jsonx.Parse(b); err == nil {
				if o, ok := v.(*jsonx.Object); ok {
					if eo, ok := o.Get("error"); ok {
						if e, ok := eo.(*jsonx.Object); ok {
							if c, ok := e.Get("code"); ok && c != nil {
								typ = jsonx.JSString(c)
							} else if t, ok := e.Get("type"); ok && t != nil {
								typ = jsonx.JSString(t)
							}
							if m, ok := e.Get("message"); ok && m != nil {
								msg = jsonx.JSString(m)
							}
						}
					}
				}
			}
		}
		ra := strx.Number(res.Header.Get("retry-after"))
		if res.Header.Get("retry-after") == "" {
			ra = 0
		}
		e := &Error{Msg: msg, Status: res.StatusCode, Type: typ}
		if !math.IsNaN(ra) && !math.IsInf(ra, 0) && ra > 0 {
			e.RetryAfterMs = ra * 1000
		}
		return "", e
	}
	// A failed body read after an abort carries the abort's message, which
	// readStreamText turns into a stream_error GatewayError.
	return ReadStreamText(&abortReader{r: res.Body, ctx: ctx})
}

// abortReader gives a body read that failed because ctx ended the error
// message undici gives it: the abort reason's message.
type abortReader struct {
	r   io.Reader
	ctx context.Context
}

func (a *abortReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if err != nil && err != io.EOF && a.ctx.Err() != nil {
		return n, errors.New(abortMessage(a.ctx))
	}
	return n, err
}

func abortMessage(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "The operation was aborted due to timeout"
	}
	if c := context.Cause(ctx); c != nil && c != context.Canceled {
		var je *jserr.Error
		if errors.As(c, &je) {
			return je.Message
		}
		return c.Error()
	}
	return "This operation was aborted"
}

// fetchError gives a failed fetch the name and text Node's fetch gives it.
func fetchError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return jserr.New("TimeoutError", "The operation was aborted due to timeout")
	}
	if ctx.Err() != nil {
		if c := context.Cause(ctx); c != nil && c != context.Canceled {
			return c
		}
		return jserr.New("AbortError", "This operation was aborted")
	}
	return &jserr.Error{Name: "TypeError", Message: "fetch failed", Cause: err}
}

var modelWord = regexp.MustCompile(`(?i)model`)

func rejected(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	return ge.Type == "model_not_allowed" || (ge.Status == 400 && modelWord.MatchString(ge.Msg))
}

// Schema validates a parsed reply: zod's parse for one response schema.
type Schema[T any] func(v any) (T, error)

// ChatJSON is chatJsonWithModel: one structured call, the reply validated by
// schema. Invalid JSON gets one retry with a reminder; a rejected model falls
// back to FALLBACK_MODEL. It returns the data and the model that produced it.
func ChatJSON[T any](ctx context.Context, req Request, schema Schema[T], do Do) (T, string, error) {
	var zero T
	cfg := config.Load()
	model := req.Model
	if model == "" {
		model = cfg.ExtractModel
	}
	prompt := req.Prompt
	for attempt := 0; attempt < 2; attempt++ {
		text, err := ChatOnce(ctx, Request{System: req.System, Prompt: prompt, Model: model}, do)
		if err != nil {
			if rejected(err) && model != cfg.FallbackModel {
				logx.Error("[recall] gateway rejected %s, falling back to %s", model, cfg.FallbackModel)
				model = cfg.FallbackModel
				if text, err = ChatOnce(ctx, Request{System: req.System, Prompt: prompt, Model: model}, do); err != nil {
					return zero, "", err
				}
			} else {
				return zero, "", err
			}
		}
		v, perr := ExtractJSON(text)
		var data T
		if perr == nil {
			data, perr = schema(v)
		}
		if perr == nil {
			return data, model, nil
		}
		if attempt == 1 || ctx.Err() != nil {
			var ie *InvalidJSONError
			if errors.As(perr, &ie) {
				return zero, "", ie
			}
			// String(err): a ZodError prints its issues only.
			shape := jserr.Format(perr)
			var ze *zod.Error
			if errors.As(perr, &ze) {
				shape = ze.Error()
			}
			return zero, "", &InvalidJSONError{Msg: "invalid shape: " + shape, Text: text}
		}
		prompt = req.Prompt + "\n\nYour last reply was not valid JSON for the required shape. Reply again with only the JSON object."
	}
	return zero, "", errors.New("unreachable")
}
