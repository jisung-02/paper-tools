package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// pdfSpan is one identically-styled fragment of a visual line.
type pdfSpan struct {
	text                            string
	size                            float64
	bold, italic, underline, strike bool
	color                           uint32
}

// styleEq reports whether two spans share every style field.
func (s pdfSpan) styleEq(o pdfSpan) bool {
	s.text, o.text = "", ""
	return s == o
}

// pdfRichLine is one laid-out visual line plus its vertical advance.
type pdfRichLine struct {
	spans   []pdfSpan
	leading float64
	align   Align
}

// wrapSpans greedily wraps styled spans into visual lines of at most width
// points, breaking at the last space where possible (the span-aware
// counterpart of wrapLineWidth; CJK/unbroken tokens break at the
// overflowing rune).
func wrapSpans(f *ttfFont, spans []pdfSpan, width float64) [][]pdfSpan {
	type srune struct {
		r rune
		s int
	}
	var runes []srune
	for si, sp := range spans {
		for _, r := range sp.text {
			runes = append(runes, srune{r, si})
		}
	}
	if len(runes) == 0 {
		return [][]pdfSpan{nil}
	}
	build := func(seg []srune) []pdfSpan {
		var out []pdfSpan
		for _, sr := range seg {
			st := spans[sr.s]
			st.text = string(sr.r)
			if n := len(out); n > 0 && out[n-1].styleEq(st) {
				out[n-1].text += st.text
			} else {
				out = append(out, st)
			}
		}
		return out
	}
	var lines [][]pdfSpan
	start := 0
	for start < len(runes) {
		end, lastSpace, w := start, -1, 0.0
		for end < len(runes) {
			cw := lineWidth(f, []rune{runes[end].r}, spans[runes[end].s].size)
			if w+cw > width && end > start {
				break
			}
			if runes[end].r == ' ' {
				lastSpace = end
			}
			w += cw
			end++
		}
		if end >= len(runes) {
			lines = append(lines, build(runes[start:end]))
			break
		}
		if lastSpace >= start {
			lines = append(lines, build(runes[start:lastSpace]))
			start = lastSpace + 1
		} else {
			lines = append(lines, build(runes[start:end]))
			start = end
		}
	}
	return lines
}

// spanLineWidth measures a laid-out line.
func spanLineWidth(f *ttfFont, spans []pdfSpan) float64 {
	w := 0.0
	for _, sp := range spans {
		w += lineWidth(f, []rune(sp.text), sp.size)
	}
	return w
}

// renderDocPDF renders doc as a paginated A4 PDF with per-run styling.
// Bold and italic are synthesized from the single embedded regular font
// (stroke render mode / shear matrix); underline and strike are drawn.
func renderDocPDF(doc *DocModel, fontTTF []byte, opts TextPDFOpts) ([]byte, error) {
	bodySize := opts.FontSize
	if bodySize <= 0 {
		bodySize = 11
	}
	f, err := parseTTF(fontTTF)
	if err != nil {
		return nil, err
	}
	doc = normalizeDoc(doc)
	if len(doc.Blocks) == 0 {
		doc.Blocks = []Block{&Para{}}
	}

	var lines []pdfRichLine
	addGap := func(extra float64) {
		if len(lines) > 0 && extra > 0 {
			lines[len(lines)-1].leading += extra
		}
	}
	for bi, blk := range doc.Blocks {
		p, ok := blk.(*Para)
		if !ok {
			continue
		}
		base := bodySize
		if p.Heading > 0 {
			// Pinned heading sizes (headingSizePt), matching writeDocx's
			// styles.xml and writeHwpx's folded charPr — headings must not
			// scale with a custom body FontSize.
			base = headingSizePt(p.Heading)
		}
		var spans []pdfSpan
		for _, r := range p.Runs {
			sp := pdfSpan{
				text:      strings.ReplaceAll(r.Text, "\t", "    "),
				size:      r.SizePt,
				bold:      r.Bold || p.Heading > 0,
				italic:    r.Italic,
				underline: r.Underline,
				strike:    r.Strike,
				color:     r.Color,
			}
			if sp.size == 0 {
				sp.size = base
			}
			spans = append(spans, sp)
		}
		if bi > 0 {
			// Mirror md.go: headings get a lead-in gap scaled to their own
			// size, body blocks to the body size.
			pre := bodySize * 0.5
			if p.Heading > 0 {
				pre = base * 0.5
			}
			addGap(pre)
		}
		lineFactor := 1.5
		if p.Heading > 0 {
			lineFactor = 1.4
		}
		for _, ln := range wrapSpans(f, spans, textContentWidth) {
			maxSize := base
			for _, sp := range ln {
				if sp.size > maxSize {
					maxSize = sp.size
				}
			}
			lines = append(lines, pdfRichLine{spans: ln, leading: maxSize * lineFactor, align: p.Align})
		}
		if p.Heading > 0 {
			addGap(bodySize * 0.4)
		}
	}

	// Font subset: every rune actually drawn.
	seen := map[rune]bool{}
	var distinctRunes []rune
	for _, ln := range lines {
		for _, sp := range ln.spans {
			for _, r := range sp.text {
				if !seen[r] {
					seen[r] = true
					distinctRunes = append(distinctRunes, r)
				}
			}
		}
	}
	f.markUsed(distinctRunes...)

	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()
	type0Ref, err := embedTTF(b, f, distinctRunes)
	if err != nil {
		return nil, err
	}

	startY := a4Height - textMargin - bodySize
	var kids Array
	var buf bytes.Buffer
	y := startY
	flushPage := func() {
		data := append([]byte(nil), buf.Bytes()...)
		contentRef := b.alloc()
		b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(data)}, Data: data}
		pageRef := b.alloc()
		b.objs[pageRef.Num-1] = Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{0, 0, a4Width, a4Height},
			"Resources": Dict{"Font": Dict{"F1": type0Ref}},
			"Contents":  contentRef,
		}
		kids = append(kids, pageRef)
	}
	for _, ln := range lines {
		if y < textMargin {
			flushPage()
			buf.Reset()
			y = startY
		}
		lw := spanLineWidth(f, ln.spans)
		x := textMargin
		switch ln.align {
		case AlignCenter:
			x += (textContentWidth - lw) / 2
		case AlignRight:
			x += textContentWidth - lw
		}
		for _, sp := range ln.spans {
			if sp.text == "" {
				continue
			}
			cr := float64((sp.color>>16)&0xFF) / 255
			cg := float64((sp.color>>8)&0xFF) / 255
			cb := float64(sp.color&0xFF) / 255
			fmt.Fprintf(&buf, "%.3f %.3f %.3f rg\n%.3f %.3f %.3f RG\n", cr, cg, cb, cr, cg, cb)
			mode := 0
			if sp.bold {
				mode = 2
				fmt.Fprintf(&buf, "%.2f w\n", sp.size*0.03)
			}
			skew := 0.0
			if sp.italic {
				skew = 0.21
			}
			fmt.Fprintf(&buf, "BT\n/F1 %.2f Tf\n%d Tr\n1 0 %.2f 1 %.2f %.2f Tm <%X> Tj\nET\n",
				sp.size, mode, skew, x, y, f.encode(sp.text))
			wsp := lineWidth(f, []rune(sp.text), sp.size)
			if sp.underline {
				fmt.Fprintf(&buf, "%.2f w\n%.2f %.2f m %.2f %.2f l S\n",
					sp.size*0.05, x, y-sp.size*0.12, x+wsp, y-sp.size*0.12)
			}
			if sp.strike {
				fmt.Fprintf(&buf, "%.2f w\n%.2f %.2f m %.2f %.2f l S\n",
					sp.size*0.05, x, y+sp.size*0.28, x+wsp, y+sp.size*0.28)
			}
			x += wsp
		}
		y -= ln.leading
	}
	flushPage()

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef)
}
