package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// TextPDFOpts configures TextToPDF.
type TextPDFOpts struct {
	FontSize float64 // <=0 defaults to 11
}

const (
	textMargin       = 56.0
	textContentWidth = a4Width - 2*textMargin // 483.28
)

// TextToPDF renders text as a word-wrapped, paginated A4 document, embedding
// fontTTF as a shared subset font used by every page.
func TextToPDF(text string, fontTTF []byte, opts TextPDFOpts) ([]byte, error) {
	fontSize := opts.FontSize
	if fontSize <= 0 {
		fontSize = 11
	}

	f, err := parseTTF(fontTTF)
	if err != nil {
		return nil, err
	}

	// Preprocess: tabs -> 4 spaces, drop CR entirely, keep LF as an explicit
	// hard line break.
	text = strings.ReplaceAll(text, "\t", "    ")
	text = strings.ReplaceAll(text, "\r", "")

	// Collect every distinct rune actually used (excluding the '\n' line
	// break marker itself) and mark them used in the font subset before the
	// font objects are built.
	seen := map[rune]bool{}
	var distinctRunes []rune
	for _, r := range text {
		if r == '\n' {
			continue
		}
		if !seen[r] {
			seen[r] = true
			distinctRunes = append(distinctRunes, r)
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

	// Word-wrap every logical line (split on '\n') into one or more visual
	// lines, then flatten the whole document into one list of visual lines
	// before paginating.
	var visualLines []string
	for _, logical := range strings.Split(text, "\n") {
		visualLines = append(visualLines, wrapLine(f, logical, fontSize)...)
	}

	leading := fontSize * 1.5
	const x = textMargin
	startY := a4Height - textMargin - fontSize

	fontTf := fmt.Sprintf("/F1 %.2f Tf\n", fontSize)

	var kids Array
	var buf bytes.Buffer
	y := startY
	buf.WriteString("BT\n")
	buf.WriteString(fontTf)

	// flushPage finishes the current page's content buffer into a content
	// stream + page dict, appending the new page to kids.
	flushPage := func() {
		buf.WriteString("ET")
		data := append([]byte(nil), buf.Bytes()...)
		// ponytail: uncompressed content stream, fine for this size
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

	startPage := func() {
		buf.Reset()
		y = startY
		buf.WriteString("BT\n")
		buf.WriteString(fontTf)
	}

	for _, line := range visualLines {
		if y < textMargin {
			flushPage()
			startPage()
		}
		if line != "" {
			fmt.Fprintf(&buf, "1 0 0 1 %.2f %.2f Tm <%X> Tj\n", float64(x), y, f.encode(line))
		}
		y -= leading
	}
	flushPage()

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef), nil
}

// ponytail: greedy per-rune wrap, space-preferred for latin; no real line-breaking/hyphenation.
//
// wrapLine wraps a single logical line (no '\n' inside it) to
// textContentWidth at fontSize, returning one or more visual lines. A
// logical line with zero runes still yields exactly one (empty) visual line,
// so callers can always advance the cursor by one leading per input line.
func wrapLine(f *ttfFont, s string, fontSize float64) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return []string{""}
	}

	var lines []string
	start := 0
	for start < len(runes) {
		end := start
		lastSpace := -1 // index into runes of the last space since start, or -1
		width := 0.0
		for end < len(runes) {
			w := lineWidth(f, runes[end:end+1], fontSize)
			if width+w > textContentWidth && end > start {
				break
			}
			if runes[end] == ' ' {
				lastSpace = end
			}
			width += w
			end++
		}
		if end >= len(runes) {
			lines = append(lines, string(runes[start:end]))
			break
		}
		if lastSpace >= start {
			// Break at the last space: it is dropped, the remainder after
			// it starts the next visual line.
			lines = append(lines, string(runes[start:lastSpace]))
			start = lastSpace + 1
		} else {
			// No space since the start of this visual line (e.g. CJK or a
			// long unbroken token): break right before the overflowing rune.
			lines = append(lines, string(runes[start:end]))
			start = end
		}
	}
	return lines
}

// lineWidth returns the width, in points, of runes rendered at fontSize,
// summing each rune's advance width (falling back to glyph 0's advance for
// runes missing from the font's cmap).
func lineWidth(f *ttfFont, runes []rune, fontSize float64) float64 {
	total := 0.0
	for _, r := range runes {
		gid, ok := f.gid(r)
		if !ok {
			gid = 0
		}
		total += float64(f.advance1000(gid)) * fontSize / 1000
	}
	return total
}
