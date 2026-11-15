package connectors

// gmail-message.ts: a Gmail API message (format=full) to the text Recall
// ingests, and its PDF attachments. Pure functions, no I/O.

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/zakiego/recall/apps/server/internal/content"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// PdfAttachment is a PDF found in a message. AttachmentID nil means the
// bytes are inline (InlineData).
type PdfAttachment struct {
	PartID       string  `json:"partId"`
	AttachmentID *string `json:"attachmentId"`
	InlineData   *string `json:"inlineData"`
	Filename     string  `json:"filename"`
	Size         float64 `json:"size"`
}

// ParsedMessage is parseMessage's result.
type ParsedMessage struct {
	ID      string          `json:"id"`
	Subject *string         `json:"subject"`
	From    *string         `json:"from"`
	To      *string         `json:"to"`
	Date    *string         `json:"date"`
	Text    string          `json:"text"`
	Pdfs    []PdfAttachment `json:"pdfs"`
}

// Header is header(): the trimmed value of the first header with that name,
// nil when there is none or it is blank.
func Header(part *GmailPart, name string) *string {
	if part == nil {
		return nil
	}
	want := strx.Lower(name)
	for _, h := range part.Headers {
		if strx.Lower(h.Name) == want {
			v := strx.Trim(h.Value)
			if v == "" {
				return nil
			}
			return &v
		}
	}
	return nil
}

func headerOr(part *GmailPart, name string) string {
	if v := Header(part, name); v != nil {
		return *v
	}
	return ""
}

var charsetRE = jsre.MustCompile(`charset="?([^";\s]+)"?`, "i")

func charsetOf(part *GmailPart) string {
	if m := charsetRE.Find(jsre.ToUnits(headerOr(part, "Content-Type"))); m != nil {
		if g, ok := m.Group(1); ok {
			return strx.Lower(g)
		}
	}
	return "utf-8"
}

// decodeBody is new TextDecoder(charset).decode(bytes), falling back to
// UTF-8 for a label TextDecoder doesn't know.
func decodeBody(part *GmailPart) string {
	data := ""
	if part.Body != nil && part.Body.Data != nil {
		data = *part.Body.Data
	}
	return decodeCharset(charsetOf(part), DecodeBase64URL(data))
}

// decodeCharset is new TextDecoder(label).decode(b), UTF-8 when the label
// is refused.
func decodeCharset(label string, b []byte) string {
	enc, err := htmlindex.Get(label)
	if err != nil || enc == encoding.Replacement {
		return strx.TextDecode(b)
	}
	name, _ := htmlindex.Name(enc)
	if name == "utf-8" {
		return strx.TextDecode(b)
	}
	// Single-byte encodings decode through ICU's tables, as Node does.
	if t, ok := singleByte[name]; ok {
		out := make([]rune, len(b))
		for i, c := range b {
			out[i] = t[c]
		}
		return string(out)
	}
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return strx.TextDecode(b)
	}
	return strings.TrimPrefix(string(out), "\uFEFF")
}

func walk(part *GmailPart, visit func(*GmailPart)) {
	visit(part)
	for i := range part.Parts {
		walk(&part.Parts[i], visit)
	}
}

var attachmentDisposition = regexp.MustCompile(`(?i)^attachment`)

func isAttachment(p *GmailPart) bool {
	return (p.Filename != nil && *p.Filename != "") || attachmentDisposition.MatchString(headerOr(p, "Content-Disposition"))
}

func s(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ParseMessage is parseMessage. An HTML-only body that has no element fails
// the way htmlToText does in the TS build.
func ParseMessage(msg GmailMessage) (ParsedMessage, error) {
	root := msg.Payload
	if root == nil {
		root = &GmailPart{}
	}
	var plain, html []string
	pdfs := []PdfAttachment{}
	walk(root, func(p *GmailPart) {
		mime := strx.Lower(s(p.MimeType))
		if isAttachment(p) {
			body := p.Body
			if body != nil && content.IsPdf(mime, s(p.Filename)) && (s(body.AttachmentID) != "" || s(body.Data) != "") {
				a := PdfAttachment{PartID: s(p.PartID), Filename: s(p.Filename), AttachmentID: copyStr(body.AttachmentID)}
				if a.PartID == "" {
					a.PartID = strconv.Itoa(len(pdfs))
				}
				if a.Filename == "" {
					a.Filename = "attachment.pdf"
				}
				// inlineData: attachmentId ? null : (data ?? null)
				if s(body.AttachmentID) == "" {
					a.InlineData = copyStr(body.Data)
				}
				if body.Size != nil {
					a.Size = *body.Size
				}
				pdfs = append(pdfs, a)
			}
			return
		}
		if p.Body == nil || s(p.Body.Data) == "" {
			return
		}
		switch mime {
		case "text/plain":
			plain = append(plain, decodeBody(p))
		case "text/html":
			html = append(html, decodeBody(p))
		}
	})
	// Plain text when the sender wrote one; otherwise the HTML, read like a page.
	var body string
	if len(plain) > 0 {
		body = strings.Join(plain, "\n\n")
	} else {
		texts := make([]string, len(html))
		for i, h := range html {
			r, err := content.HTMLToText(h, "")
			if err != nil {
				return ParsedMessage{}, err
			}
			texts[i] = r.Text
		}
		body = strings.Join(texts, "\n\n")
	}
	out := ParsedMessage{ID: msg.ID, Subject: Header(root, "Subject"), From: Header(root, "From"), To: Header(root, "To"), Date: Header(root, "Date"), Pdfs: pdfs}
	var head []string
	for _, h := range []struct {
		label string
		v     *string
	}{{"Subject", out.Subject}, {"From", out.From}, {"To", out.To}, {"Date", out.Date}} {
		if h.v != nil {
			head = append(head, h.label+": "+*h.v)
		}
	}
	var parts []string
	if hs := strings.Join(head, "\n"); hs != "" {
		parts = append(parts, hs)
	}
	if n := normalize(body); n != "" {
		parts = append(parts, n)
	}
	out.Text = strings.Join(parts, "\n\n")
	return out, nil
}

var (
	crlf       = regexp.MustCompile(`\r\n?`)
	trailingWS = regexp.MustCompile("[ \t ]+\n")
	manyLines  = regexp.MustCompile(`\n{3,}`)
)

func normalize(text string) string {
	text = crlf.ReplaceAllString(text, "\n")
	text = trailingWS.ReplaceAllString(text, "\n")
	text = manyLines.ReplaceAllString(text, "\n\n")
	return strx.Trim(text)
}

var (
	entityRE = regexp.MustCompile(`(?i)&(#x[0-9a-f]+|#\d+|[a-z]+);`)
	entities = map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " "}
)

// DecodeEntities is decodeEntities: Gmail's snippet comes HTML-escaped.
func DecodeEntities(text string) string {
	return entityRE.ReplaceAllStringFunc(text, func(m string) string {
		e := m[1 : len(m)-1]
		if e[0] == '#' {
			var code int64
			var err error
			if len(e) > 1 && (e[1] == 'x' || e[1] == 'X') {
				code, err = strconv.ParseInt(e[2:], 16, 64)
			} else {
				code, err = strconv.ParseInt(e[1:], 10, 64)
			}
			if err == nil && code > 0 && code < 0x110000 {
				return string(rune(code))
			}
			return m
		}
		if v, ok := entities[strx.Lower(e)]; ok {
			return v
		}
		return m
	})
}

func copyStr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
