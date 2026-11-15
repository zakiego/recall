// Package content is content.ts: text out of HTML, PDF and text files, and
// fetching a URL without ever reaching a private address.
package content

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"

	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/jsre"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	MaxFetchBytes = 15 * 1024 * 1024
	MaxTextChars  = 2_000_000
)

// Resolved is a document's text: Title nil when there is none, Mime "" when
// the TS code left it undefined.
type Resolved struct {
	Title *string
	Text  string
	Mime  string
}

// FormatChat is formatChat: "role: content" blocks, run on the parsed
// metadata.messages as they are (a caller may store anything there). A
// message that isn't an object with string content fails with the
// TypeError the TS code throws; the role is printed with JS String().
func FormatChat(messages []any) (string, error) {
	parts := make([]string, len(messages))
	for i, m := range messages {
		if m == nil {
			return "", jserr.New("TypeError", "Cannot read properties of null (reading 'role')")
		}
		var role, c any
		var hasRole, hasContent bool
		if o, ok := m.(*jsonx.Object); ok {
			role, hasRole = o.Get("role")
			c, hasContent = o.Get("content")
		}
		text, isStr := c.(string)
		switch {
		case !hasContent:
			return "", jserr.New("TypeError", "Cannot read properties of undefined (reading 'trim')")
		case c == nil:
			return "", jserr.New("TypeError", "Cannot read properties of null (reading 'trim')")
		case !isStr:
			return "", jserr.New("TypeError", "m.content.trim is not a function")
		}
		r := "undefined"
		if hasRole {
			r = jsonx.JSString(role)
		}
		parts[i] = r + ": " + strx.Trim(text)
	}
	return strings.Join(parts, "\n\n"), nil
}

// textContent is the DOM's textContent: every text node, in order.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.CommentNode {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func find(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, tag); f != nil {
			return f
		}
	}
	return nil
}

func removeAll(n *html.Node, tags ...string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && slices.Contains(tags, c.Data) {
			n.RemoveChild(c)
		} else {
			removeAll(c, tags...)
		}
		c = next
	}
}

var (
	hspace     = regexp.MustCompile("[ \t ]+")
	blankLines = jsre.MustCompile(`\n\s*\n\s*`, "g")
)

func ptr(s string) *string { return &s }

// htmlparser2 10.1.0's tree rules, which linkedom builds its DOM with.
var (
	pTag             = []string{"p"}
	formTags         = []string{"input", "option", "optgroup", "select", "button", "datalist", "textarea"}
	openImpliesClose = map[string][]string{
		"tr": {"tr", "th", "td"}, "th": {"th"}, "td": {"thead", "th", "td"}, "body": {"head", "link", "script"}, "li": {"li"},
		"p": pTag, "h1": pTag, "h2": pTag, "h3": pTag, "h4": pTag, "h5": pTag, "h6": pTag,
		"select": formTags, "input": formTags, "output": formTags, "button": formTags, "datalist": formTags, "textarea": formTags,
		"option": {"option"}, "optgroup": {"optgroup", "option"}, "dd": {"dd", "dt"}, "dt": {"dd", "dt"},
		"address": pTag, "article": pTag, "aside": pTag, "blockquote": pTag, "details": pTag, "div": pTag, "dl": pTag,
		"fieldset": pTag, "figcaption": pTag, "figure": pTag, "footer": pTag, "form": pTag, "header": pTag, "hr": pTag,
		"main": pTag, "nav": pTag, "ol": pTag, "pre": pTag, "section": pTag, "table": pTag, "ul": pTag,
		"rt": {"rt", "rp"}, "rp": {"rt", "rp"}, "tbody": {"thead", "tbody"}, "tfoot": {"thead", "tbody"},
	}
	voidElements = map[string]bool{"area": true, "base": true, "basefont": true, "br": true, "col": true, "command": true, "embed": true,
		"frame": true, "hr": true, "img": true, "input": true, "isindex": true, "keygen": true, "link": true, "meta": true, "param": true,
		"source": true, "track": true, "wbr": true}
	// Raw text in x/net's tokenizer but markup in htmlparser2's.
	notRawText = map[string]bool{"iframe": true, "noembed": true, "noframes": true, "noscript": true, "plaintext": true}
)

// linkedomShape is what the TS build's DOM makes of the markup. linkedom
// builds the tree the way htmlparser2 parses it (no implied <html>, <head> or
// <body>), its document element is the first element, and document.body is
// the element right after the document element's first child when that child
// is a <head>, or its first child itself otherwise (linkedom prepends a new
// <head> then), provided that element is a <body>; else body is a new empty
// element. ok is false when there is no element at all, which makes
// Readability throw in the TS build.
func linkedomShape(page string) (hasBody, ok bool) {
	t := tree{}
	z := html.NewTokenizer(strings.NewReader(page))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if !t.seenRoot {
				return false, false
			}
			switch {
			case len(t.children) == 0:
				return false, true
			case t.children[0] == "head":
				return len(t.children) > 1 && t.children[1] == "body", true
			default:
				return t.children[0] == "body", true
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			b, _ := z.TagName()
			name := string(b)
			t.open(name, true, tt == html.SelfClosingTagToken)
			if notRawText[name] {
				z.NextIsNotRawText()
			}
		case html.EndTagToken:
			b, _ := z.TagName()
			t.close(string(b))
		}
	}
}

// tree follows htmlparser2's element stack far enough to know the document
// element's first two element children.
type tree struct {
	stack    []string // innermost last
	foreign  []bool
	seenRoot bool
	rootOpen bool
	children []string
}

func (t *tree) pop(n int) {
	t.stack = t.stack[:len(t.stack)-n]
	if len(t.stack) == 0 {
		t.rootOpen = false
	}
}

// open is emitOpenTag (implied closes when implied is true), then
// onselfclosingtag, which closes the element only in a foreign context.
func (t *tree) open(name string, implied, selfClosing bool) {
	if closes, ok := openImpliesClose[name]; ok && implied {
		for len(t.stack) > 0 && slices.Contains(closes, t.stack[len(t.stack)-1]) {
			t.pop(1)
		}
	}
	isRoot := false
	switch {
	case !t.seenRoot && len(t.stack) == 0:
		t.seenRoot, isRoot = true, true
	case len(t.stack) == 1 && t.rootOpen && len(t.children) < 2:
		t.children = append(t.children, name)
	}
	if voidElements[name] {
		return
	}
	t.stack = append(t.stack, name)
	if isRoot {
		t.rootOpen = true
	}
	switch name {
	case "math", "svg":
		t.foreign = append(t.foreign, true)
	case "mi", "mo", "mn", "ms", "mtext", "annotation-xml", "foreignobject", "desc", "title":
		t.foreign = append(t.foreign, false)
	}
	// closeCurrentTag pops the element but leaves its foreign context.
	if selfClosing && len(t.foreign) > 0 && t.foreign[len(t.foreign)-1] {
		t.pop(1)
	}
}

// close is onclosetag.
func (t *tree) close(name string) {
	switch name {
	case "math", "svg", "mi", "mo", "mn", "ms", "mtext", "annotation-xml", "foreignobject", "desc", "title":
		if len(t.foreign) > 0 {
			t.foreign = t.foreign[:len(t.foreign)-1]
		}
	}
	if voidElements[name] {
		if name == "br" {
			// </br> is a <br>, without implied closes.
			t.open("br", false, false)
		}
		return
	}
	for i := len(t.stack) - 1; i >= 0; i-- {
		if t.stack[i] == name {
			t.pop(len(t.stack) - i)
			return
		}
	}
	if name == "p" {
		// </p> without an open <p> is an empty <p>.
		t.open("p", true, false)
		t.pop(1)
	}
}

// ErrNoDocument is the TS build's Readability error for markup without an
// element.
var ErrNoDocument = errors.New("First argument to Readability constructor should be a document object.")

// HTMLToText is htmlToText: Readability's article text, or the body text
// without script, style, noscript, nav, footer and header when Readability
// finds nothing; whitespace folded. The title is Readability's, the
// document's <title>, the URL, or nil.
func HTMLToText(page string, pageURL string) (Resolved, error) {
	hasBody, ok := linkedomShape(page)
	if !ok {
		return Resolved{}, ErrNoDocument
	}
	var title string
	doc, err := html.Parse(strings.NewReader(page))
	if err == nil {
		if t := find(doc, "title"); t != nil {
			title = strx.Trim(textContent(t))
		}
	}
	var articleTitle, text string
	if hasBody {
		func() {
			defer func() { _ = recover() }()
			fresh, err := html.Parse(strings.NewReader(page))
			if err != nil {
				return
			}
			p := readability.NewParser()
			p.KeepClasses = false
			base, _ := url.Parse("about:blank")
			a, err := p.ParseDocument(fresh, base)
			if err != nil || a.Node == nil {
				return
			}
			articleTitle = a.Title()
			text = textContent(a.Node)
		}()
	}
	if strx.Trim(text) == "" && doc != nil {
		removeAll(doc, "script", "style", "noscript", "nav", "footer", "header")
		text = ""
		if body := find(doc, "body"); body != nil && hasBody {
			text = textContent(body)
		}
	}
	text = hspace.ReplaceAllString(text, " ")
	text = blankLines.ReplaceAll(text, "\n\n")
	text = strx.Trim(text)
	r := Resolved{Text: text}
	switch {
	case strx.Trim(articleTitle) != "":
		r.Title = ptr(strx.Trim(articleTitle))
	case title != "":
		r.Title = ptr(title)
	case pageURL != "":
		r.Title = ptr(pageURL)
	}
	return r, nil
}

var (
	textMime = regexp.MustCompile(`json|xml|yaml|markdown|csv`)
	textName = jsre.MustCompile(`\.(txt|md|markdown|csv|json|ya?ml|log|html?)$`, "i")
	htmlName = jsre.MustCompile(`\.html?$`, "i")
)

// IsPdf is isPdf: the MIME type mentions pdf or the name ends in .pdf.
func IsPdf(mime, name string) bool {
	return strings.Contains(mime, "pdf") || strings.HasSuffix(strx.Lower(name), ".pdf")
}

// IsTextLike is isTextLike.
func IsTextLike(mime, name string) bool {
	if mime != "" && (strings.HasPrefix(mime, "text/") || textMime.MatchString(mime)) {
		return true
	}
	return textName.Test(name)
}

// ErrUnsupported is "unsupported file type: ...".
type ErrUnsupported struct{ What string }

func (e *ErrUnsupported) Error() string { return "unsupported file type: " + e.What }

// BytesToText is bytesToText: a PDF, HTML or text file to text. mime and name
// may be "".
func BytesToText(b []byte, mimeOrNil, nameOrNil *string) (Resolved, error) {
	mime, name := deref(mimeOrNil), deref(nameOrNil)
	if IsPdf(mime, name) {
		return PDFToText(b)
	}
	raw := strx.TextDecode(b)
	if strings.Contains(mime, "html") || htmlName.Test(name) {
		return HTMLToText(raw, "")
	}
	if IsTextLike(mime, name) {
		return Resolved{Text: strx.Trim(raw), Mime: mime}, nil
	}
	// mime ?? name ?? "unknown"
	what := "unknown"
	if mimeOrNil != nil {
		what = mime
	} else if nameOrNil != nil {
		what = name
	}
	return Resolved{}, &ErrUnsupported{what}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
