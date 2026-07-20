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
	size    float64 // max span size on the line
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

// layoutPara resolves p's runs into styled spans (applying heading bold and
// size defaults), wraps them to width, and returns the resulting visual
// lines with their leading and max span size. Inter-block gap handling
// (addGap) is the caller's job — cells don't get inter-block gaps beyond
// line leading (ponytail).
func layoutPara(f *ttfFont, p *Para, bodySize, width float64) []pdfRichLine {
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
	lineFactor := 1.5
	if p.Heading > 0 {
		lineFactor = 1.4
	}
	var out []pdfRichLine
	for _, ln := range wrapSpans(f, spans, width) {
		maxSize := base
		for _, sp := range ln {
			if sp.size > maxSize {
				maxSize = sp.size
			}
		}
		out = append(out, pdfRichLine{spans: ln, leading: maxSize * lineFactor, align: p.Align, size: maxSize})
	}
	return out
}

// drawRichLine draws one laid-out line's spans (fill/stroke color, bold and
// italic synthesis, underline/strike) into buf at baseline y, honoring
// ln.align within [x0, x0+width).
func drawRichLine(buf *bytes.Buffer, f *ttfFont, ln pdfRichLine, x0, width, y float64) {
	lw := spanLineWidth(f, ln.spans)
	x := x0
	switch ln.align {
	case AlignCenter:
		x += (width - lw) / 2
	case AlignRight:
		x += width - lw
	}
	for _, sp := range ln.spans {
		if sp.text == "" {
			continue
		}
		cr := float64((sp.color>>16)&0xFF) / 255
		cg := float64((sp.color>>8)&0xFF) / 255
		cb := float64(sp.color&0xFF) / 255
		fmt.Fprintf(buf, "%.3f %.3f %.3f rg\n%.3f %.3f %.3f RG\n", cr, cg, cb, cr, cg, cb)
		mode := 0
		if sp.bold {
			mode = 2
			fmt.Fprintf(buf, "%.2f w\n", sp.size*0.03)
		}
		skew := 0.0
		if sp.italic {
			skew = 0.21
		}
		fmt.Fprintf(buf, "BT\n/F1 %.2f Tf\n%d Tr\n1 0 %.2f 1 %.2f %.2f Tm <%X> Tj\nET\n",
			sp.size, mode, skew, x, y, f.encode(sp.text))
		wsp := lineWidth(f, []rune(sp.text), sp.size)
		if sp.underline {
			fmt.Fprintf(buf, "%.2f w\n%.2f %.2f m %.2f %.2f l S\n",
				sp.size*0.05, x, y-sp.size*0.12, x+wsp, y-sp.size*0.12)
		}
		if sp.strike {
			fmt.Fprintf(buf, "%.2f w\n%.2f %.2f m %.2f %.2f l S\n",
				sp.size*0.05, x, y+sp.size*0.28, x+wsp, y+sp.size*0.28)
		}
		x += wsp
	}
}

// pdfRenderItem is one top-level renderable: a laid-out paragraph line, or a
// laid-out table. Exactly one of the two fields is set.
type pdfRenderItem struct {
	line  *pdfRichLine
	table *pdfTableBox
}

// pdfTableBox is a laid-out table: uniform column width, per-row height, and
// every cell's laid-out lines.
type pdfTableBox struct {
	cols  int
	colW  float64
	rowH  []float64
	rows  int
	cells []pdfCellBox
}

// pdfCellBox is one laid-out cell placement (grid coordinates plus span, as
// reported by tableGrid) and its laid-out paragraph lines.
type pdfCellBox struct {
	row, col, colSpan, rowSpan int
	lines                      []pdfRichLine
}

const tblPad = 3.0

// flattenCellParas returns the cell's paragraphs; nested tables are
// flattened into their cells' paragraphs in order.
// ponytail: depth-1 rendering — nested table structure is not drawn.
func flattenCellParas(blocks []Block) []*Para {
	var out []*Para
	for _, blk := range blocks {
		switch b := blk.(type) {
		case *Para:
			out = append(out, b)
		case *Table:
			for _, row := range b.Rows {
				for _, c := range row {
					out = append(out, flattenCellParas(c.Blocks)...)
				}
			}
		}
	}
	return out
}

// layoutTable lays out t's grid: a uniform column width (textContentWidth
// split evenly), each row sized to its tallest cell (rowspan cells grow
// their last spanned row if needed), and every cell's paragraphs laid out to
// its inner width.
func layoutTable(f *ttfFont, t *Table, bodySize float64) *pdfTableBox {
	cols, items := tableGrid(t)
	if cols == 0 {
		return nil
	}
	box := &pdfTableBox{cols: cols, colW: textContentWidth / float64(cols), rows: len(t.Rows), rowH: make([]float64, len(t.Rows))}
	minH := bodySize*1.5 + 2*tblPad
	for i := range box.rowH {
		box.rowH[i] = minH
	}
	type spanned struct {
		idx int
		h   float64
	}
	var spans []spanned
	for _, it := range items {
		if it.Cell == nil {
			continue
		}
		inner := box.colW*float64(it.W) - 2*tblPad
		if inner < mdMinWrapWidth {
			inner = mdMinWrapWidth
		}
		var lines []pdfRichLine
		for _, p := range flattenCellParas(it.Cell.Blocks) {
			lines = append(lines, layoutPara(f, p, bodySize, inner)...)
		}
		h := 2 * tblPad
		for _, ln := range lines {
			h += ln.leading
		}
		cb := pdfCellBox{row: it.Row, col: it.Col, colSpan: it.W, rowSpan: it.Cell.rowSpan(), lines: lines}
		box.cells = append(box.cells, cb)
		if cb.rowSpan <= 1 {
			if h > box.rowH[it.Row] {
				box.rowH[it.Row] = h
			}
		} else {
			spans = append(spans, spanned{len(box.cells) - 1, h})
		}
	}
	// a rowspan cell taller than its rows grows the last spanned row
	for _, s := range spans {
		cb := box.cells[s.idx]
		end := cb.row + cb.rowSpan
		if end > len(box.rowH) {
			end = len(box.rowH)
		}
		sum := 0.0
		for r := cb.row; r < end; r++ {
			sum += box.rowH[r]
		}
		if s.h > sum && end > cb.row {
			box.rowH[end-1] += s.h - sum
		}
	}
	return box
}

// drawTableChunk strokes the grid and draws cell text for rows [r0, rEnd)
// with the chunk's top edge at yTop. Rowspan cells crossing the chunk
// boundary draw their text in the first chunk only (the chunk's row range
// clamps the drawn height and text to what's on this page).
func drawTableChunk(buf *bytes.Buffer, f *ttfFont, tb *pdfTableBox, r0, rEnd int, yTop float64) {
	rowTop := make([]float64, rEnd-r0+1)
	rowTop[0] = yTop
	for r := r0; r < rEnd; r++ {
		rowTop[r-r0+1] = rowTop[r-r0] - tb.rowH[r]
	}
	buf.WriteString("0 0 0 RG\n0.75 w\n")
	for _, cb := range tb.cells {
		if cb.row < r0 || cb.row >= rEnd {
			continue
		}
		x := textMargin + float64(cb.col)*tb.colW
		w := float64(cb.colSpan) * tb.colW
		end := cb.row + cb.rowSpan
		if end > rEnd {
			end = rEnd
		}
		h := 0.0
		for r := cb.row; r < end; r++ {
			h += tb.rowH[r]
		}
		top := rowTop[cb.row-r0]
		fmt.Fprintf(buf, "%.2f %.2f %.2f %.2f re S\n", x, top-h, w, h)
		yLine := top - tblPad
		for _, ln := range cb.lines {
			yLine -= ln.size
			drawRichLine(buf, f, ln, x+tblPad, w-2*tblPad, yLine)
			yLine -= ln.leading - ln.size
		}
	}
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

	var items []pdfRenderItem
	// addGap folds extra leading into the previous line item (paragraph
	// inter-block spacing, mirroring md.go). A table item is left alone: its
	// own post-draw gap is applied directly against y in the page loop below,
	// and a paragraph following a table gets no separate pre-gap on top of
	// that (simplest — ponytail).
	addGap := func(extra float64) {
		if extra <= 0 || len(items) == 0 {
			return
		}
		if last := items[len(items)-1].line; last != nil {
			last.leading += extra
		}
	}
	for bi, blk := range doc.Blocks {
		switch b := blk.(type) {
		case *Para:
			base := bodySize
			if b.Heading > 0 {
				base = headingSizePt(b.Heading)
			}
			if bi > 0 {
				// Mirror md.go: headings get a lead-in gap scaled to their
				// own size, body blocks to the body size.
				pre := bodySize * 0.5
				if b.Heading > 0 {
					pre = base * 0.5
				}
				addGap(pre)
			}
			paraLines := layoutPara(f, b, bodySize, textContentWidth)
			for i := range paraLines {
				items = append(items, pdfRenderItem{line: &paraLines[i]})
			}
			if b.Heading > 0 {
				addGap(bodySize * 0.4)
			}
		case *Table:
			if bi > 0 {
				addGap(bodySize * 0.5)
			}
			if tb := layoutTable(f, b, bodySize); tb != nil {
				items = append(items, pdfRenderItem{table: tb})
			}
		}
	}

	// Font subset: every rune actually drawn, across both line items and
	// every table cell's laid-out lines.
	seen := map[rune]bool{}
	var distinctRunes []rune
	addRunes := func(spans []pdfSpan) {
		for _, sp := range spans {
			for _, r := range sp.text {
				if !seen[r] {
					seen[r] = true
					distinctRunes = append(distinctRunes, r)
				}
			}
		}
	}
	for _, it := range items {
		if it.line != nil {
			addRunes(it.line.spans)
		}
		if it.table != nil {
			for _, cb := range it.table.cells {
				for _, ln := range cb.lines {
					addRunes(ln.spans)
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
	for _, it := range items {
		if it.line != nil {
			if y < textMargin {
				flushPage()
				buf.Reset()
				y = startY
			}
			drawRichLine(&buf, f, *it.line, textMargin, textContentWidth, y)
			y -= it.line.leading
			continue
		}
		// table: draw row-atomically, splitting across pages
		tb := it.table
		r0 := 0
		for r0 < tb.rows {
			avail := y + bodySize - textMargin // y is the next text baseline; top edge ≈ y + bodySize
			// how many rows fit
			rEnd := r0
			h := 0.0
			for rEnd < tb.rows && h+tb.rowH[rEnd] <= avail {
				h += tb.rowH[rEnd]
				rEnd++
			}
			if rEnd == r0 { // nothing fits
				if h == 0 && y >= startY { // taller than a whole page: force one row
					rEnd = r0 + 1
					h = tb.rowH[r0]
				} else {
					flushPage()
					buf.Reset()
					y = startY
					continue
				}
			}
			drawTableChunk(&buf, f, tb, r0, rEnd, y+bodySize)
			y -= h + bodySize*0.5
			r0 = rEnd
		}
	}
	flushPage()

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef)
}
