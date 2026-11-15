package content

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// PDFToText is pdfToText: the text of every page, pages joined by a blank
// line, and the Title from the document info. The TS build uses pdf.js
// (unpdf); github.com/ledongthuc/pdf has no layout engine, so lines and the
// spaces inside them are rebuilt here from glyph positions the way pdf.js
// decides them: a new line when the baseline moves, a space when the gap to
// the next glyph is wider than a fraction of the font size (Known gaps, M3).
func PDFToText(b []byte) (r Resolved, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("Invalid PDF structure: %v", p)
		}
	}()
	rd, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return Resolved{}, err
	}
	r.Mime = "application/pdf"
	if info := rd.Trailer().Key("Info"); !info.IsNull() {
		if t := info.Key("Title"); t.Kind() == pdf.String {
			if s := strx.Trim(t.Text()); s != "" {
				r.Title = &s
			}
		}
	}
	pages := make([]string, 0, rd.NumPage())
	for i := 1; i <= rd.NumPage(); i++ {
		pages = append(pages, pageText(rd.Page(i)))
	}
	r.Text = strx.Trim(strings.Join(pages, "\n\n"))
	return r, nil
}

func pageText(p pdf.Page) string {
	if p.V.IsNull() {
		return ""
	}
	glyphs := p.Content().Text
	var b strings.Builder
	var prev *pdf.Text
	newline := func() {
		if s := b.String(); s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	for i := range glyphs {
		g := &glyphs[i]
		// The library marks T* and friends with a "\n" glyph of its own.
		if g.S == "\n" {
			newline()
			prev = nil
			continue
		}
		if prev != nil {
			size := math.Max(prev.FontSize, 1)
			switch {
			case math.Abs(g.Y-prev.Y) > size*0.5:
				newline()
			case g.X-(prev.X+prev.W) > size*0.15 && !strings.HasSuffix(prev.S, " ") && !strings.HasPrefix(g.S, " "):
				b.WriteByte(' ')
			}
		}
		b.WriteString(g.S)
		prev = g
	}
	return strings.TrimSuffix(b.String(), "\n")
}
