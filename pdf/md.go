package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ponytail: a hand-rolled, line-oriented Markdown subset (headings, lists,
// fenced/indented code, blockquotes, hr, and the handful of inline spans
// listed below) reusing TextToPDF's font embedding and greedy word-wrap.
// Tables and raw HTML are not modeled: they fall through to plain paragraph
// text, which is an intentional simplification, not a bug.

// MarkdownPDFOpts configures MarkdownToPDF.
type MarkdownPDFOpts struct {
	FontSize float64 // base body text size; <=0 defaults to 11
}

const (
	mdMargin       = textMargin
	mdContentWidth = textContentWidth
	mdListIndent   = 20.0 // per nesting level (0 or 1)
	mdQuoteIndent  = 20.0
	mdCodeIndent   = 14.0
	mdMinWrapWidth = 40.0 // floor so deeply nested/prefixed lines still wrap
)

// MarkdownToPDF renders md as a word-wrapped, paginated A4 document,
// embedding fontTTF as the single shared font used for body text, headings,
// list items, blockquotes and code blocks alike (there is no separate
// monospace face). It reuses TextToPDF's TTF subsetting/embedding and
// greedy word-wrap machinery.
func MarkdownToPDF(md []byte, fontTTF []byte, opts MarkdownPDFOpts) ([]byte, error) {
	if len(bytes.TrimSpace(md)) == 0 {
		return nil, errors.New("마크다운 내용이 비어 있습니다")
	}

	fontSize := opts.FontSize
	if fontSize <= 0 {
		fontSize = 11
	}

	f, err := parseTTF(fontTTF)
	if err != nil {
		return nil, err
	}

	src := strings.ReplaceAll(string(md), "\t", "    ")
	blocks := parseMarkdown(src)
	lines := layoutMarkdown(f, blocks, fontSize)
	if len(lines) == 0 {
		return nil, errors.New("마크다운 내용이 비어 있습니다")
	}

	// Collect every distinct rune actually drawn (rules carry no text) and
	// mark it used in the font subset before the font objects are built.
	seen := map[rune]bool{}
	var distinctRunes []rune
	for _, ln := range lines {
		if ln.isRule {
			continue
		}
		for _, r := range ln.text {
			if !seen[r] {
				seen[r] = true
				distinctRunes = append(distinctRunes, r)
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

	const x0 = mdMargin
	startY := a4Height - mdMargin - fontSize

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
	startPage := func() {
		buf.Reset()
		y = startY
	}

	for _, ln := range lines {
		if y < mdMargin {
			flushPage()
			startPage()
		}
		switch {
		case ln.isRule:
			ruleY := y - ln.fontSize*0.35
			fmt.Fprintf(&buf, "0.75 w\n%.2f %.2f m %.2f %.2f l S\n", x0, ruleY, x0+mdContentWidth, ruleY)
		case ln.text != "":
			fmt.Fprintf(&buf, "BT\n/F1 %.2f Tf\n1 0 0 1 %.2f %.2f Tm <%X> Tj\nET\n",
				ln.fontSize, x0+ln.indent, y, f.encode(ln.text))
		}
		y -= ln.leading
	}
	flushPage()

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef), nil
}

// ------------------------------------------------------------- block model ---

type mdBlockKind int

const (
	mdBlockParagraph mdBlockKind = iota
	mdBlockHeading
	mdBlockListItem
	mdBlockCode
	mdBlockQuote
	mdBlockRule
)

// mdBlock is one parsed Markdown block. Which fields are meaningful depends
// on kind: heading uses level+text, listItem uses level/ordered/num/text,
// code uses lines (verbatim, never reflowed), everything else uses text.
type mdBlock struct {
	kind    mdBlockKind
	level   int
	ordered bool
	num     int
	text    string
	lines   []string
}

var (
	reMdHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reMdOrdered  = regexp.MustCompile(`^(\d+)\.\s+(.*)$`)
	reMdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reMdLink     = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)
	reMdCodeSpan = regexp.MustCompile("`([^`]*)`")
	reMdBold     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reMdBoldU    = regexp.MustCompile(`__([^_]+)__`)
	reMdItalic   = regexp.MustCompile(`\*([^*]+)\*`)
	reMdItalicU  = regexp.MustCompile(`_([^_]+)_`)
)

// parseMarkdown splits src into a flat sequence of blocks using a simple
// line-oriented scan (no nested block trees, no reference-style links).
func parseMarkdown(src string) []mdBlock {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")

	var blocks []mdBlock
	var para []string
	var quote []string

	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, mdBlock{kind: mdBlockParagraph, text: mdInline(strings.Join(para, " "))})
			para = nil
		}
	}
	flushQuote := func() {
		if len(quote) > 0 {
			blocks = append(blocks, mdBlock{kind: mdBlockQuote, text: mdInline(strings.Join(quote, " "))})
			quote = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Fenced code block: consume verbatim until a closing ``` or EOF.
		if strings.HasPrefix(trimmed, "```") {
			flushPara()
			flushQuote()
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // skip closing fence
			}
			blocks = append(blocks, mdBlock{kind: mdBlockCode, lines: code})
			continue
		}

		if trimmed == "" {
			flushPara()
			flushQuote()
			i++
			continue
		}

		if isHR(trimmed) {
			flushPara()
			flushQuote()
			blocks = append(blocks, mdBlock{kind: mdBlockRule})
			i++
			continue
		}

		if m := reMdHeading.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			flushQuote()
			text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(m[2]), "#"))
			blocks = append(blocks, mdBlock{kind: mdBlockHeading, level: len(m[1]), text: mdInline(text)})
			i++
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			flushPara()
			q := strings.TrimPrefix(trimmed, ">")
			q = strings.TrimPrefix(q, " ")
			quote = append(quote, q)
			i++
			continue
		}
		flushQuote()

		if lvl, ordered, num, text, ok := parseListItem(line); ok {
			flushPara()
			blocks = append(blocks, mdBlock{kind: mdBlockListItem, level: lvl, ordered: ordered, num: num, text: mdInline(text)})
			i++
			continue
		}

		// 4-space (or tab) indented code block.
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			flushPara()
			var code []string
			for i < len(lines) {
				l := lines[i]
				switch {
				case strings.HasPrefix(l, "    "):
					code = append(code, l[4:])
				case strings.HasPrefix(l, "\t"):
					code = append(code, l[1:])
				case strings.TrimSpace(l) == "":
					code = append(code, "")
				default:
					goto doneIndented
				}
				i++
			}
		doneIndented:
			for len(code) > 0 && code[len(code)-1] == "" {
				code = code[:len(code)-1]
			}
			blocks = append(blocks, mdBlock{kind: mdBlockCode, lines: code})
			continue
		}

		para = append(para, trimmed)
		i++
	}
	flushPara()
	flushQuote()
	return blocks
}

// isHR reports whether trimmed (with internal spaces removed) is 3+ of the
// same rule character: -, * or _ (covers "---", "***", "___", "- - -", ...).
func isHR(trimmed string) bool {
	s := strings.ReplaceAll(trimmed, " ", "")
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// parseListItem checks whether line is an unordered (-, *, +) or ordered
// (N.) list item, returning its nesting level (0, or 1 for >=2 leading
// spaces), and, for ordered items, its display number.
func parseListItem(line string) (level int, ordered bool, num int, text string, ok bool) {
	i := 0
	spaces := 0
	for i < len(line) && line[i] == ' ' {
		spaces++
		i++
	}
	rest := line[i:]
	if spaces >= 2 {
		level = 1
	}
	if len(rest) >= 2 && (rest[0] == '-' || rest[0] == '*' || rest[0] == '+') && rest[1] == ' ' {
		return level, false, 0, strings.TrimSpace(rest[2:]), true
	}
	if m := reMdOrdered.FindStringSubmatch(rest); m != nil {
		n, _ := strconv.Atoi(m[1])
		return level, true, n, strings.TrimSpace(m[2]), true
	}
	return 0, false, 0, "", false
}

// mdInline strips inline Markdown spans down to their plain-text content:
// images are dropped entirely, links keep just their label, and
// **bold**/__bold__/*italic*/_italic_/`code` keep just their inner text.
func mdInline(s string) string {
	s = reMdImage.ReplaceAllString(s, "")
	s = reMdLink.ReplaceAllString(s, "$1")
	s = reMdCodeSpan.ReplaceAllString(s, "$1")
	s = reMdBold.ReplaceAllString(s, "$1")
	s = reMdBoldU.ReplaceAllString(s, "$1")
	s = reMdItalic.ReplaceAllString(s, "$1")
	s = reMdItalicU.ReplaceAllString(s, "$1")
	return s
}

// ------------------------------------------------------------------ layout ---

// mdRenderLine is one drawable unit of output: either a horizontal rule or a
// single visual line of text at a given size/indent, followed by leading
// (vertical space, including any extra inter-block gap) before the next one.
type mdRenderLine struct {
	isRule   bool
	text     string
	fontSize float64
	indent   float64
	leading  float64
}

// headingScale returns the body-size multiplier for an ATX heading level
// (1-6): h1 2.0x, h2 1.6x, h3 1.3x, h4-h6 1.1x.
func headingScale(level int) float64 {
	switch level {
	case 1:
		return 2.0
	case 2:
		return 1.6
	case 3:
		return 1.3
	default:
		return 1.1
	}
}

// layoutMarkdown converts parsed blocks into a flat list of render lines,
// word-wrapping paragraph/heading/quote/list text with wrapLineWidth and
// leaving code lines untouched. Inter-block spacing is folded into the
// leading of the previous block's last line via addGap, so the page-break
// loop in MarkdownToPDF only ever has to look at one field per line.
func layoutMarkdown(f *ttfFont, blocks []mdBlock, bodySize float64) []mdRenderLine {
	var out []mdRenderLine
	addGap := func(extra float64) {
		if len(out) == 0 || extra <= 0 {
			return
		}
		out[len(out)-1].leading += extra
	}

	for bi, blk := range blocks {
		switch blk.kind {
		case mdBlockHeading:
			size := bodySize * headingScale(blk.level)
			if bi > 0 {
				addGap(size * 0.5)
			}
			for _, vis := range wrapLineWidth(f, blk.text, size, mdContentWidth) {
				out = append(out, mdRenderLine{text: vis, fontSize: size, leading: size * 1.4})
			}
			addGap(bodySize * 0.4)

		case mdBlockParagraph:
			if bi > 0 {
				addGap(bodySize * 0.5)
			}
			for _, vis := range wrapLineWidth(f, blk.text, bodySize, mdContentWidth) {
				out = append(out, mdRenderLine{text: vis, fontSize: bodySize, leading: bodySize * 1.5})
			}

		case mdBlockQuote:
			if bi > 0 {
				addGap(bodySize * 0.5)
			}
			indent := mdQuoteIndent
			width := mdWrapWidth(mdContentWidth - indent)
			for _, vis := range wrapLineWidth(f, blk.text, bodySize, width) {
				out = append(out, mdRenderLine{text: vis, fontSize: bodySize, indent: indent, leading: bodySize * 1.5})
			}

		case mdBlockCode:
			if bi > 0 {
				addGap(bodySize * 0.5)
			}
			size := bodySize * 0.92
			for _, l := range blk.lines {
				out = append(out, mdRenderLine{text: l, fontSize: size, indent: mdCodeIndent, leading: size * 1.35})
			}

		case mdBlockListItem:
			if bi > 0 && blocks[bi-1].kind != mdBlockListItem {
				addGap(bodySize * 0.4)
			}
			indent := mdListIndent + float64(blk.level)*mdListIndent
			prefix := "• "
			if blk.ordered {
				prefix = strconv.Itoa(blk.num) + ". "
			}
			prefixWidth := lineWidth(f, []rune(prefix), bodySize)
			width := mdWrapWidth(mdContentWidth - indent - prefixWidth)
			for j, vis := range wrapLineWidth(f, blk.text, bodySize, width) {
				if j == 0 {
					out = append(out, mdRenderLine{text: prefix + vis, fontSize: bodySize, indent: indent, leading: bodySize * 1.5})
				} else {
					out = append(out, mdRenderLine{text: vis, fontSize: bodySize, indent: indent + prefixWidth, leading: bodySize * 1.5})
				}
			}

		case mdBlockRule:
			if bi > 0 {
				addGap(bodySize * 0.5)
			}
			out = append(out, mdRenderLine{isRule: true, fontSize: bodySize, leading: bodySize * 1.2})
		}
	}
	return out
}

// mdWrapWidth clamps a computed content width to a sane minimum so heavily
// indented/prefixed text (deep list nesting, etc.) still wraps instead of
// producing a degenerate near-zero width.
func mdWrapWidth(w float64) float64 {
	if w < mdMinWrapWidth {
		return mdMinWrapWidth
	}
	return w
}
