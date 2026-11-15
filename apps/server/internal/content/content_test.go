package content_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/content"
	"github.com/zakiego/recall/apps/server/internal/jserr"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/testutil"
)

type resolvedJSON struct {
	Title *string `json:"title"`
	Text  string  `json:"text"`
	Mime  *string `json:"mime"`
}

type outcome struct {
	OK    *resolvedJSON `json:"ok"`
	Error string        `json:"error"`
}

type golden struct {
	HTML      map[string]struct{ NoURL, WithURL resolvedJSON }
	HTMLCases []struct {
		HTML   string  `json:"html"`
		Out    outcome `json:"out"`
		OutURL outcome `json:"outUrl"`
	} `json:"htmlCases"`
	Bytes []struct {
		Mime  *string       `json:"mime"`
		Name  *string       `json:"name"`
		Text  string        `json:"text"`
		OK    *resolvedJSON `json:"ok"`
		Error string        `json:"error"`
	} `json:"bytes"`
	PDFs       map[string]json.RawMessage `json:"pdfs"`
	Predicates []struct {
		Mime *string `json:"mime"`
		Name *string `json:"name"`
		PDF  bool    `json:"pdf"`
		Text bool    `json:"text"`
	} `json:"predicates"`
	Addresses []struct {
		A       string `json:"a"`
		Private bool   `json:"private"`
	} `json:"addresses"`
	Chat  string `json:"chat"`
	Chats []struct {
		Messages json.RawMessage `json:"messages"`
		OK       *string         `json:"ok"`
		Error    string          `json:"error"`
	} `json:"chats"`
}

func load(t *testing.T) golden {
	var g golden
	raw, err := os.ReadFile(testutil.Testdata("content.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tmp struct {
		HTML map[string]struct {
			NoURL   resolvedJSON `json:"noUrl"`
			WithURL resolvedJSON `json:"withUrl"`
		} `json:"html"`
	}
	if err := json.Unmarshal(raw, &tmp); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	g.HTML = map[string]struct{ NoURL, WithURL resolvedJSON }{}
	for k, v := range tmp.HTML {
		g.HTML[k] = struct{ NoURL, WithURL resolvedJSON }{v.NoURL, v.WithURL}
	}
	return g
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func same(t *testing.T, name string, got content.Resolved, want resolvedJSON) {
	t.Helper()
	if str(got.Title) != str(want.Title) || (got.Title == nil) != (want.Title == nil) || got.Text != want.Text || got.Mime != str(want.Mime) {
		t.Errorf("%s:\n got title %q text %q mime %q\nwant title %q text %q mime %q", name, str(got.Title), got.Text, got.Mime, str(want.Title), want.Text, str(want.Mime))
	}
}

func TestHTMLToTextMatchesTS(t *testing.T) {
	g := load(t)
	if len(g.HTML) < 5 || len(g.HTMLCases) < 8 {
		t.Fatal("golden incomplete")
	}
	names := make([]string, 0, len(g.HTML))
	for k := range g.HTML {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		page, err := os.ReadFile(testutil.Testdata("content", "html", name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := content.HTMLToText(string(page), "")
		if err != nil {
			t.Fatal(err)
		}
		same(t, name, r, g.HTML[name].NoURL)
		r, _ = content.HTMLToText(string(page), "https://example.com/page")
		same(t, name+" with url", r, g.HTML[name].WithURL)
	}
	for _, c := range g.HTMLCases {
		for _, v := range []struct {
			u   string
			out outcome
		}{{"", c.Out}, {"https://x.test/", c.OutURL}} {
			r, err := content.HTMLToText(c.HTML, v.u)
			if v.out.OK == nil {
				if err == nil || err.Error() != v.out.Error {
					t.Errorf("%q: %v, want error %q", c.HTML, err, v.out.Error)
				}
				continue
			}
			if err != nil {
				t.Errorf("%q: %v", c.HTML, err)
				continue
			}
			same(t, c.HTML, r, *v.out.OK)
		}
	}
}

func TestBytesToTextMatchesTS(t *testing.T) {
	g := load(t)
	for _, c := range g.Bytes {
		r, err := content.BytesToText([]byte(c.Text), c.Mime, c.Name)
		if c.OK == nil {
			if err == nil || err.Error() != c.Error {
				t.Errorf("%v %v: %v, want %q", str(c.Mime), str(c.Name), err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v %v: %v", str(c.Mime), str(c.Name), err)
			continue
		}
		same(t, str(c.Mime)+" "+str(c.Name), r, *c.OK)
	}
	for _, c := range g.Predicates {
		if content.IsPdf(str(c.Mime), str(c.Name)) != c.PDF || content.IsTextLike(str(c.Mime), str(c.Name)) != c.Text {
			t.Errorf("predicates %v %v", str(c.Mime), str(c.Name))
		}
	}
	if got, err := content.FormatChat([]any{jsonx.ObjectOf("role", "user", "content", "  hi \n"), jsonx.ObjectOf("role", "assistant", "content", "hello")}); err != nil || got != g.Chat {
		t.Errorf("formatChat %q %v", got, err)
	}
	if len(g.Chats) < 15 {
		t.Fatal("golden incomplete")
	}
	for _, c := range g.Chats {
		v, err := jsonx.Parse(c.Messages)
		if err != nil {
			t.Fatal(err)
		}
		got, err := content.FormatChat(v.([]any))
		if c.OK == nil {
			if err == nil || jserr.Format(err) != c.Error {
				t.Errorf("formatChat %s: %q %v, want %s", c.Messages, got, err, c.Error)
			}
			continue
		}
		if err != nil || got != *c.OK {
			t.Errorf("formatChat %s: %q %v, want %q", c.Messages, got, err, *c.OK)
		}
	}
}

// content.test.ts "isPrivateAddress", plus more ranges.
func TestIsPrivateAddressMatchesTS(t *testing.T) {
	g := load(t)
	if len(g.Addresses) < 40 {
		t.Fatal("golden incomplete")
	}
	for _, c := range g.Addresses {
		if got := content.IsPrivateAddress(c.A); got != c.Private {
			t.Errorf("%q: %v, want %v", c.A, got, c.Private)
		}
	}
}

// PDF text: pdf.js has no Go port (Known gaps, M3). The title and the words
// must be the same; line breaks and spaces inside lines may differ on real
// documents, so the words are compared after folding whitespace. The PDFs
// written by the generator must match exactly.
func TestPDFToTextAgainstPDFJS(t *testing.T) {
	g := load(t)
	exact := map[string]bool{"made-simple.pdf": true, "made-kerning.pdf": true, "made-empty.pdf": true, "w3c-dummy.pdf": true}
	files, _ := filepath.Glob(testutil.Testdata("content", "pdf", "*.pdf"))
	if len(files) < 6 {
		t.Fatalf("%d PDFs", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		var want resolvedJSON
		if err := json.Unmarshal(g.PDFs[name], &want); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(f)
		got, err := content.PDFToText(b)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if str(got.Title) != str(want.Title) || got.Mime != "application/pdf" {
			t.Errorf("%s: title %q, want %q", name, str(got.Title), str(want.Title))
		}
		if exact[name] {
			if got.Text != want.Text {
				t.Errorf("%s text:\n got %q\nwant %q", name, got.Text, want.Text)
			}
			continue
		}
		gw, ww := strings.Fields(got.Text), strings.Fields(want.Text)
		match := wordOverlap(gw, ww)
		t.Logf("%s: %d words here, %d in pdf.js, %.1f%% of pdf.js words in order", name, len(gw), len(ww), match*100)
		if match < 0.9 {
			t.Errorf("%s: only %.1f%% of the words match", name, match*100)
		}
	}
}

// wordOverlap is the longest common subsequence of words over the length of b.
func wordOverlap(a, b []string) float64 {
	if len(b) == 0 {
		if len(a) == 0 {
			return 1
		}
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(b)]) / float64(len(b))
}

var _ = jsonx.Stringify
