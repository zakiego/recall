// Package email is email.ts: outgoing email through Resend's HTTP API. It
// needs RESEND_API_KEY and RECALL_EMAIL_FROM; without them Configured is
// false and callers say "email not configured" instead of sending.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/nodefetch"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Message is EmailMessage. IdempotencyKey: Resend drops a repeat with the
// same key for 24 hours, so a retried send after a lost response can't
// deliver twice.
type Message struct {
	To             string
	Subject        string
	Text           string
	HTML           string
	IdempotencyKey string
}

// Send is SendEmail: the Resend id of the sent message.
type Send func(ctx context.Context, msg Message) (string, error)

// Error is EmailError.
type Error struct {
	Msg    string
	Status int
}

func (e *Error) Error() string  { return e.Msg }
func (e *Error) JSName() string { return "EmailError" }

// Configured is emailConfigured.
func Configured() bool {
	c := config.Load()
	return c.ResendAPIKey != "" && c.EmailFrom != ""
}

// ResendURL is where messages are posted.
const ResendURL = "https://api.resend.com/emails"

const timeout = 15 * time.Second

// NewResendSender is createResendSender; do nil is the real HTTP client.
func NewResendSender(apiKey, from string, do func(*http.Request) (*http.Response, error)) Send {
	if do == nil {
		do = nodefetch.Do
	}
	return func(ctx context.Context, msg Message) (string, error) {
		body := jsonx.ObjectOf("from", from, "to", []string{msg.To}, "subject", msg.Subject, "text", msg.Text)
		if msg.HTML != "" {
			body.Set("html", msg.HTML)
		}
		b, err := jsonx.Marshal(body)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", ResendURL, bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		if msg.IdempotencyKey != "" {
			req.Header.Set("Idempotency-Key", strx.Slice16(msg.IdempotencyKey, 256))
		}
		res, err := do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		ok := res.StatusCode >= 200 && res.StatusCode <= 299
		// A JSON null body: reading its properties throws in JS.
		var v any
		if json.Unmarshal(raw, &v) == nil && v == nil {
			prop := "id"
			if !ok {
				prop = "message"
			}
			return "", jserr.New("TypeError", "Cannot read properties of null (reading '"+prop+"')")
		}
		var parsed struct {
			ID      any `json:"id"`
			Message any `json:"message"`
			Name    any `json:"name"`
		}
		if json.Unmarshal(raw, &parsed) != nil {
			parsed.ID, parsed.Message, parsed.Name = nil, nil, nil
		}
		id, _ := parsed.ID.(string)
		if !ok || !jsonx.Truthy(parsed.ID) {
			what := "no message"
			switch {
			case parsed.Message != nil:
				what = jsonx.JSString(parsed.Message)
			case parsed.Name != nil:
				what = jsonx.JSString(parsed.Name)
			}
			return "", &Error{Msg: fmt.Sprintf("Resend answered %d: %s", res.StatusCode, what), Status: res.StatusCode}
		}
		if id == "" {
			id = jsonx.JSString(parsed.ID)
		}
		return id, nil
	}
}

// SendEmail is sendEmail: the sender for the configured key, read at call
// time.
func SendEmail(ctx context.Context, msg Message) (string, error) {
	c := config.Load()
	if !Configured() {
		return "", &Error{Msg: "email is not configured (set RESEND_API_KEY and RECALL_EMAIL_FROM)"}
	}
	return NewResendSender(c.ResendAPIKey, c.EmailFrom, nil)(ctx, msg)
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// EscapeHTML is escapeHtml.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }
