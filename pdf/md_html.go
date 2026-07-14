package pdf

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// ponytail: renders the exact same Markdown subset md.go's PDF path
// supports (see parseMarkdown) as an HTML body fragment, for live preview
// via innerHTML. Reuses md.go's block-detection helpers (isHR,
// parseListItem) and inline-span regexes (reMdImage, reMdLink,
// reMdCodeSpan, reMdBold*, reMdItalic*) directly instead of re-declaring
// them. No <html>/<head>/<body> wrapper is emitted.
//
// SECURITY: every byte of source text reaches the output only through
// html.EscapeString (see mdInlineHTML/renderBlocksHTML below), so raw HTML
// in the input can never become live markup. Link hrefs are restricted to
// an allowlist (see sanitizeHref); anything else renders as plain text.

// MarkdownToHTML renders md as a self-contained HTML fragment matching the
// block/inline coverage of MarkdownToPDF exactly (no tables, no raw HTML).
func MarkdownToHTML(md []byte) []byte {
	src := strings.ReplaceAll(string(md), "\t", "    ")
	blocks := parseMarkdownHTML(src)
	var b strings.Builder
	renderBlocksHTML(&b, blocks)
	return []byte(b.String())
}

// parseMarkdownHTML mirrors parseMarkdown's block splitting exactly (same
// heading/list/quote/code/hr detection, via the same helpers/regexes) but
// keeps each block's text as raw, unprocessed Markdown instead of running
// it through mdInline. mdInline collapses **bold**/[links](...)/`code`
// down to plain text for the PDF path, which would throw away the
// information mdInlineHTML needs to render real <strong>/<a>/<code> tags.
func parseMarkdownHTML(src string) []mdBlock {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")

	var blocks []mdBlock
	var para []string
	var quote []string

	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, mdBlock{kind: mdBlockParagraph, text: strings.Join(para, " ")})
			para = nil
		}
	}
	flushQuote := func() {
		if len(quote) > 0 {
			blocks = append(blocks, mdBlock{kind: mdBlockQuote, text: strings.Join(quote, " ")})
			quote = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

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
			blocks = append(blocks, mdBlock{kind: mdBlockHeading, level: len(m[1]), text: text})
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
			blocks = append(blocks, mdBlock{kind: mdBlockListItem, level: lvl, ordered: ordered, num: num, text: text})
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

// ------------------------------------------------------------- block render ---

// renderBlocksHTML walks the flat block sequence, batching consecutive
// mdBlockListItem blocks into a single (possibly nested) <ul>/<ol> via
// renderList.
func renderBlocksHTML(b *strings.Builder, blocks []mdBlock) {
	i := 0
	for i < len(blocks) {
		if blocks[i].kind == mdBlockListItem {
			j := i
			for j < len(blocks) && blocks[j].kind == mdBlockListItem {
				j++
			}
			renderList(b, blocks[i:j])
			i = j
			continue
		}

		blk := blocks[i]
		switch blk.kind {
		case mdBlockHeading:
			level := blk.level
			if level < 1 {
				level = 1
			} else if level > 6 {
				level = 6
			}
			tag := "h" + strconv.Itoa(level)
			b.WriteString("<" + tag + ">")
			b.WriteString(mdInlineHTML(blk.text))
			b.WriteString("</" + tag + ">")

		case mdBlockParagraph:
			b.WriteString("<p>")
			b.WriteString(mdInlineHTML(blk.text))
			b.WriteString("</p>")

		case mdBlockQuote:
			b.WriteString("<blockquote><p>")
			b.WriteString(mdInlineHTML(blk.text))
			b.WriteString("</p></blockquote>")

		case mdBlockCode:
			b.WriteString("<pre><code>")
			b.WriteString(html.EscapeString(strings.Join(blk.lines, "\n")))
			b.WriteString("</code></pre>")

		case mdBlockRule:
			b.WriteString("<hr>")
		}
		i++
	}
}

// openList writes the opening <ul> or <ol> tag for a new list, an <ol>
// carrying a start="N" attribute when the list's first item didn't start
// at 1 (e.g. "5. five"), matching the literal numbers md.go's PDF path
// prints. num is an int, so no escaping is needed.
func openList(b *strings.Builder, ordered bool, num int) {
	if !ordered {
		b.WriteString("<ul>")
		return
	}
	if num > 1 {
		b.WriteString(`<ol start="` + strconv.Itoa(num) + `">`)
	} else {
		b.WriteString("<ol>")
	}
}

// renderList renders a run of consecutive list-item blocks as one or more
// nested <ul>/<ol> trees. md.go's parseListItem only ever reports level 0
// or level 1 (2+ leading spaces), so nesting never goes deeper than two
// levels; a level-1 item is nested inside the still-open <li> of the
// preceding level-0 item.
func renderList(b *strings.Builder, items []mdBlock) {
	outerOpen, outerOrdered := false, false
	innerOpen, innerOrdered := false, false

	closeInner := func() {
		if innerOpen {
			if innerOrdered {
				b.WriteString("</ol>")
			} else {
				b.WriteString("</ul>")
			}
			innerOpen = false
		}
	}
	closeOuter := func() {
		closeInner()
		if outerOpen {
			b.WriteString("</li>")
			if outerOrdered {
				b.WriteString("</ol>")
			} else {
				b.WriteString("</ul>")
			}
			outerOpen = false
		}
	}

	for _, item := range items {
		level := item.level
		// A run that starts at level 1 (no open outer list yet) has no
		// parent <li> to nest inside; promote it to level 0 instead of
		// fabricating an empty outer <li> around it. The PDF path has no
		// such phantom-bullet artifact, so this keeps them visually
		// consistent.
		if level == 1 && !outerOpen {
			level = 0
		}

		if level == 0 {
			closeInner()
			if outerOpen && outerOrdered != item.ordered {
				closeOuter()
			}
			if outerOpen {
				b.WriteString("</li>")
			} else {
				openList(b, item.ordered, item.num)
				outerOpen, outerOrdered = true, item.ordered
			}
			b.WriteString("<li>")
			b.WriteString(mdInlineHTML(item.text))
			continue
		}

		// level 1: nest inside the currently open outer <li>.
		if innerOpen && innerOrdered != item.ordered {
			closeInner()
		}
		if !innerOpen {
			openList(b, item.ordered, item.num)
			innerOpen, innerOrdered = true, item.ordered
		}
		b.WriteString("<li>")
		b.WriteString(mdInlineHTML(item.text))
		b.WriteString("</li>")
	}
	closeOuter()
}

// ------------------------------------------------------------ inline render ---

// inlineSeg is one piece of text produced while rendering an inline span
// run: raw markdown pending further passes and final HTML-escaping, or
// html, a finished/opaque HTML fragment that must not be reprocessed or
// re-escaped (its text content has already been escaped by the renderer
// that produced it).
type inlineSeg struct {
	raw    string
	html   string
	isHTML bool
}

// mdInlineHTML renders the same inline spans mdInline recognizes—images
// (dropped entirely, matching the PDF path), links, **bold**/__bold__,
// *italic*/_italic_ and `code`—as HTML, applied in the same left-to-right
// precedence order mdInline uses. Anything not part of a recognized span
// is emitted only after html.EscapeString, so unsupported raw HTML in the
// input (e.g. a literal <script> tag) always ends up as inert text.
func mdInlineHTML(s string) string {
	segs := []inlineSeg{{raw: s}}
	segs = inlinePass(segs, reMdImage, func([]string) []inlineSeg { return nil })
	segs = inlinePass(segs, reMdLink, renderLinkHTML)
	segs = inlinePass(segs, reMdCodeSpan, renderCodeHTML)
	segs = inlinePass(segs, reMdBold, renderStrongHTML)
	segs = inlinePass(segs, reMdBoldU, renderStrongHTML)
	segs = inlinePass(segs, reMdItalic, renderEmHTML)
	segs = inlinePass(segs, reMdItalicU, renderEmHTML)

	var b strings.Builder
	for _, seg := range segs {
		if seg.isHTML {
			b.WriteString(seg.html)
		} else {
			b.WriteString(html.EscapeString(seg.raw))
		}
	}
	return b.String()
}

// inlinePass scans every still-raw segment for non-overlapping matches of
// re, replacing each match with render's output; already-rendered (isHTML)
// segments pass through untouched so a later pass never reprocesses HTML
// produced by an earlier one.
func inlinePass(segs []inlineSeg, re *regexp.Regexp, render func(groups []string) []inlineSeg) []inlineSeg {
	var out []inlineSeg
	for _, seg := range segs {
		if seg.isHTML {
			out = append(out, seg)
			continue
		}
		s := seg.raw
		last := 0
		for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
			if loc[0] < last {
				continue
			}
			if loc[0] > last {
				out = append(out, inlineSeg{raw: s[last:loc[0]]})
			}
			groups := make([]string, len(loc)/2)
			for gi := range groups {
				if loc[2*gi] >= 0 {
					groups[gi] = s[loc[2*gi]:loc[2*gi+1]]
				}
			}
			out = append(out, render(groups)...)
			last = loc[1]
		}
		if last < len(s) {
			out = append(out, inlineSeg{raw: s[last:]})
		}
	}
	return out
}

// renderLinkHTML renders [label](href) as <a>, or as plain (still further
// processed) label text if href fails sanitizeHref's allowlist.
func renderLinkHTML(groups []string) []inlineSeg {
	label, href := groups[1], sanitizeHref(groups[2])
	if href == "" {
		return []inlineSeg{{raw: label}}
	}
	attrs := ""
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		attrs = ` target="_blank" rel="noopener"`
	}
	open := `<a href="` + html.EscapeString(href) + `"` + attrs + `>`
	return []inlineSeg{{html: open, isHTML: true}, {raw: label}, {html: "</a>", isHTML: true}}
}

// renderCodeHTML renders `code` content verbatim and escaped, protected
// from any later bold/italic pass (unlike mdInline's plain-string
// replacement, which would let those passes reprocess it).
func renderCodeHTML(groups []string) []inlineSeg {
	return []inlineSeg{{html: "<code>" + html.EscapeString(groups[1]) + "</code>", isHTML: true}}
}

func renderStrongHTML(groups []string) []inlineSeg {
	return []inlineSeg{{html: "<strong>", isHTML: true}, {raw: groups[1]}, {html: "</strong>", isHTML: true}}
}

func renderEmHTML(groups []string) []inlineSeg {
	return []inlineSeg{{html: "<em>", isHTML: true}, {raw: groups[1]}, {html: "</em>", isHTML: true}}
}

// sanitizeHref allows only http://, https://, mailto:, and scheme-less
// hrefs (fragments, relative/absolute paths). Any other scheme—javascript:,
// data:, vbscript:, or one hidden behind whitespace/case tricks—is
// rejected by returning "". Embedded tab/newline/CR are stripped first
// since browsers ignore them when sniffing a URL's scheme.
func sanitizeHref(raw string) string {
	h := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, raw)
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}

	i := strings.IndexByte(h, ':')
	if i <= 0 {
		return h // no scheme: relative path or "#fragment"
	}
	scheme := strings.ToLower(h[:i])
	for _, r := range scheme {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
			return h // not a valid scheme token: treat as a relative path
		}
	}
	switch scheme {
	case "http", "https", "mailto":
		return h
	default:
		return ""
	}
}
