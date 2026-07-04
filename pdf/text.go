package pdf

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf16"
)

// fontDecoder maps character codes shown by a content stream's Tj/TJ
// operators to unicode text, for one font resource.
type fontDecoder struct {
	codeBytes int               // 1 for simple fonts, 2 for Type0/composite fonts
	toUnicode map[uint32]string // code -> decoded text; nil if no ToUnicode CMap
}

// decode turns the bytes of a shown PDF string into text using this font's
// code width and ToUnicode map, falling back to a WinAnsi/latin1
// approximation for codes it can't map.
func (f *fontDecoder) decode(s []byte) string {
	var out strings.Builder
	if f.codeBytes == 2 {
		for i := 0; i+1 < len(s); i += 2 {
			code := uint32(s[i])<<8 | uint32(s[i+1])
			if f.toUnicode != nil {
				if v, ok := f.toUnicode[code]; ok {
					out.WriteString(v)
				}
			}
		}
		return out.String()
	}
	for _, b := range s {
		code := uint32(b)
		if f.toUnicode != nil {
			if v, ok := f.toUnicode[code]; ok {
				out.WriteString(v)
				continue
			}
		}
		// ponytail: no real WinAnsiEncoding table; printable ASCII maps
		// 1:1 and everything else falls back to a latin1 approximation.
		out.WriteRune(rune(b))
	}
	return out.String()
}

// fallbackFontDecoder is used when a shown string has no current font set.
var fallbackFontDecoder = &fontDecoder{codeBytes: 1}

// buildFontDecoder inspects a resolved font dict and constructs its decoder.
func (d *Doc) buildFontDecoder(fontDict Dict) *fontDecoder {
	fd := &fontDecoder{codeBytes: 1}
	if sub, _ := d.R(fontDict["Subtype"]).(Name); sub == "Type0" {
		fd.codeBytes = 2
	}
	if tuRef, ok := fontDict["ToUnicode"]; ok {
		if st, ok := d.R(tuRef).(*Stream); ok {
			if data, err := d.decodeStream(st); err == nil {
				fd.toUnicode = parseToUnicodeCMap(data)
			}
		}
	}
	return fd
}

// parseToUnicodeCMap extracts a code -> unicode-text map from the decoded
// bytes of a /ToUnicode CMap stream. It's a best-effort scanner over
// beginbfchar/endbfchar and beginbfrange/endbfrange blocks, not a full
// PostScript interpreter.
func parseToUnicodeCMap(data []byte) map[uint32]string {
	m := map[uint32]string{}
	parseCMapBlock(data, []byte("beginbfchar"), []byte("endbfchar"), func(toks [][]byte) {
		for i := 0; i+1 < len(toks); i += 2 {
			code, ok := hexToken(toks[i])
			if !ok {
				continue
			}
			val, ok := utf16Token(toks[i+1])
			if !ok {
				continue
			}
			m[code] = val
		}
	})
	parseCMapBlock(data, []byte("beginbfrange"), []byte("endbfrange"), func(toks [][]byte) {
		i := 0
		for i+2 < len(toks) {
			lo, ok1 := hexToken(toks[i])
			hi, ok2 := hexToken(toks[i+1])
			if !ok1 || !ok2 {
				i++
				continue
			}
			if len(toks[i+2]) > 0 && toks[i+2][0] == '[' {
				// <lo> <hi> [<d0> <d1> ...] — 1:1 mapping.
				arr := parseArrayToken(toks[i+2])
				for j, tok := range arr {
					code := lo + uint32(j)
					if code > hi {
						break
					}
					if val, ok := utf16Token(tok); ok {
						m[code] = val
					}
				}
				i += 3
				continue
			}
			// <lo> <hi> <dst> — increment dst per code.
			dst, ok := utf16RuneToken(toks[i+2])
			if ok {
				for code := lo; code <= hi; code++ {
					m[code] = string(rune(int(dst) + int(code-lo)))
				}
			}
			i += 3
		}
	})
	return m
}

// parseCMapBlock finds every [start ... end] block in data and calls fn with
// the whitespace/token-separated <...> and [...] tokens found inside.
func parseCMapBlock(data, start, end []byte, fn func(toks [][]byte)) {
	pos := 0
	for {
		si := bytes.Index(data[pos:], start)
		if si < 0 {
			return
		}
		si += pos
		ei := bytes.Index(data[si:], end)
		if ei < 0 {
			return
		}
		ei += si
		body := data[si+len(start) : ei]
		fn(tokenizeCMapBody(body))
		pos = ei + len(end)
	}
}

// tokenizeCMapBody splits a bfchar/bfrange block body into its <hex> and
// [array] tokens, ignoring everything else (comments, bare numbers, etc).
func tokenizeCMapBody(body []byte) [][]byte {
	var toks [][]byte
	i := 0
	for i < len(body) {
		c := body[i]
		switch {
		case c == '<':
			j := bytes.IndexByte(body[i:], '>')
			if j < 0 {
				return toks
			}
			toks = append(toks, body[i:i+j+1])
			i += j + 1
		case c == '[':
			j := bytes.IndexByte(body[i:], ']')
			if j < 0 {
				return toks
			}
			toks = append(toks, body[i:i+j+1])
			i += j + 1
		default:
			i++
		}
	}
	return toks
}

// hexToken parses a "<XX...>" token as a big-endian integer code.
func hexToken(tok []byte) (uint32, bool) {
	if len(tok) < 2 || tok[0] != '<' || tok[len(tok)-1] != '>' {
		return 0, false
	}
	hexDigits := tok[1 : len(tok)-1]
	var v uint32
	n := 0
	for _, c := range hexDigits {
		if isWS(c) {
			continue
		}
		if !isHex(c) {
			return 0, false
		}
		v = v<<4 | uint32(hexVal(c))
		n++
	}
	if n == 0 {
		return 0, false
	}
	return v, true
}

// utf16Token decodes a "<...>" token as UTF-16BE bytes into a Go string.
func utf16Token(tok []byte) (string, bool) {
	if len(tok) < 2 || tok[0] != '<' || tok[len(tok)-1] != '>' {
		return "", false
	}
	hexDigits := make([]byte, 0, len(tok)-2)
	for _, c := range tok[1 : len(tok)-1] {
		if isWS(c) {
			continue
		}
		hexDigits = append(hexDigits, c)
	}
	if len(hexDigits)%2 == 1 {
		hexDigits = append(hexDigits, '0')
	}
	raw := make([]byte, len(hexDigits)/2)
	for i := range raw {
		if !isHex(hexDigits[2*i]) || !isHex(hexDigits[2*i+1]) {
			return "", false
		}
		raw[i] = hexVal(hexDigits[2*i])<<4 | hexVal(hexDigits[2*i+1])
	}
	if len(raw)%2 == 1 {
		return "", false
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i])<<8 | uint16(raw[2*i+1])
	}
	// ponytail: utf16.Decode already handles surrogate pairs for us, so no
	// bespoke surrogate-pair handling is needed here.
	return string(utf16.Decode(units)), true
}

// utf16RuneToken decodes a "<...>" token as UTF-16BE and returns its first
// rune, for use as the base of a bfrange increment.
func utf16RuneToken(tok []byte) (rune, bool) {
	s, ok := utf16Token(tok)
	if !ok || len(s) == 0 {
		return 0, false
	}
	for _, r := range s {
		return r, true
	}
	return 0, false
}

// parseArrayToken splits a "[<a> <b> ...]" token into its inner <...> tokens.
func parseArrayToken(tok []byte) [][]byte {
	if len(tok) < 2 || tok[0] != '[' || tok[len(tok)-1] != ']' {
		return nil
	}
	return tokenizeCMapBody(tok[1 : len(tok)-1])
}

// ------------------------------------------------------ content-stream lexer

// ctTokKind identifies the syntactic category of a content-stream token.
type ctTokKind int

const (
	ctNumber ctTokKind = iota
	ctName
	ctString
	ctArray
	ctOperator
)

type ctTok struct {
	kind ctTokKind
	num  float64
	str  []byte // ctName (bare name text), ctString (decoded bytes), ctOperator (keyword)
	arr  []ctTok
}

// ctLexer tokenizes a page content stream. It's a small, self-contained
// scanner distinct from the object lexer in pdf.go, since content streams
// have different token semantics (bare operator keywords, etc).
type ctLexer struct {
	d   []byte
	pos int
}

func (l *ctLexer) skipWS() {
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if isWS(c) {
			l.pos++
			continue
		}
		if c == '%' {
			for l.pos < len(l.d) && l.d[l.pos] != '\n' && l.d[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		break
	}
}

// next returns the next token, or ok=false at end of input.
func (l *ctLexer) next() (ctTok, bool) {
	l.skipWS()
	if l.pos >= len(l.d) {
		return ctTok{}, false
	}
	c := l.d[l.pos]
	switch {
	case c == '/':
		return l.ctName()
	case c == '(':
		return l.ctLitString()
	case c == '<' && l.pos+1 < len(l.d) && l.d[l.pos+1] == '<':
		// Inline dict (e.g. BDC/DP operands) — skip it; we don't need it.
		l.skipDict()
		return l.next()
	case c == '<':
		return l.ctHexString()
	case c == '[':
		return l.ctArray()
	case c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.':
		return l.ctNumber()
	default:
		return l.ctOperator()
	}
}

func (l *ctLexer) ctNumber() (ctTok, bool) {
	start := l.pos
	if l.pos < len(l.d) && (l.d[l.pos] == '+' || l.d[l.pos] == '-') {
		l.pos++
	}
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if c >= '0' && c <= '9' || c == '.' {
			l.pos++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(string(l.d[start:l.pos]), 64)
	if err != nil {
		return ctTok{}, false
	}
	return ctTok{kind: ctNumber, num: v}, true
}

func (l *ctLexer) ctName() (ctTok, bool) {
	l.pos++ // consume '/'
	start := l.pos
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if isWS(c) || isDelim(c) {
			break
		}
		l.pos++
	}
	return ctTok{kind: ctName, str: l.d[start:l.pos]}, true
}

func (l *ctLexer) ctOperator() (ctTok, bool) {
	start := l.pos
	for l.pos < len(l.d) && !isWS(l.d[l.pos]) && !isDelim(l.d[l.pos]) {
		l.pos++
	}
	if l.pos == start {
		// Unrecognized delimiter (e.g. stray '{', '}', '>') — skip it.
		l.pos++
		return l.next()
	}
	return ctTok{kind: ctOperator, str: l.d[start:l.pos]}, true
}

func (l *ctLexer) ctLitString() (ctTok, bool) {
	l.pos++ // consume '('
	depth := 1
	var buf []byte
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		switch c {
		case '(':
			depth++
			buf = append(buf, c)
			l.pos++
		case ')':
			depth--
			l.pos++
			if depth == 0 {
				return ctTok{kind: ctString, str: buf}, true
			}
			buf = append(buf, c)
		case '\\':
			l.pos++
			if l.pos >= len(l.d) {
				return ctTok{kind: ctString, str: buf}, true
			}
			e := l.d[l.pos]
			switch e {
			case 'n':
				buf = append(buf, '\n')
				l.pos++
			case 'r':
				buf = append(buf, '\r')
				l.pos++
			case 't':
				buf = append(buf, '\t')
				l.pos++
			case 'b':
				buf = append(buf, '\b')
				l.pos++
			case 'f':
				buf = append(buf, '\f')
				l.pos++
			case '(':
				buf = append(buf, '(')
				l.pos++
			case ')':
				buf = append(buf, ')')
				l.pos++
			case '\\':
				buf = append(buf, '\\')
				l.pos++
			case '\r':
				l.pos++
				if l.pos < len(l.d) && l.d[l.pos] == '\n' {
					l.pos++
				}
			case '\n':
				l.pos++
			default:
				if e >= '0' && e <= '7' {
					v := 0
					n := 0
					for n < 3 && l.pos < len(l.d) && l.d[l.pos] >= '0' && l.d[l.pos] <= '7' {
						v = v*8 + int(l.d[l.pos]-'0')
						l.pos++
						n++
					}
					buf = append(buf, byte(v))
				} else {
					buf = append(buf, e)
					l.pos++
				}
			}
		default:
			buf = append(buf, c)
			l.pos++
		}
	}
	return ctTok{kind: ctString, str: buf}, true
}

func (l *ctLexer) ctHexString() (ctTok, bool) {
	l.pos++ // consume '<'
	var digits []byte
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if c == '>' {
			l.pos++
			break
		}
		if isWS(c) {
			l.pos++
			continue
		}
		if !isHex(c) {
			l.pos++
			continue
		}
		digits = append(digits, c)
		l.pos++
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		out[i] = hexVal(digits[2*i])<<4 | hexVal(digits[2*i+1])
	}
	return ctTok{kind: ctString, str: out}, true
}

func (l *ctLexer) ctArray() (ctTok, bool) {
	l.pos++ // consume '['
	var elems []ctTok
	for {
		l.skipWS()
		if l.pos >= len(l.d) {
			break
		}
		if l.d[l.pos] == ']' {
			l.pos++
			break
		}
		tok, ok := l.next()
		if !ok {
			break
		}
		elems = append(elems, tok)
	}
	return ctTok{kind: ctArray, arr: elems}, true
}

// skipDict skips a "<< ... >>" inline dict operand without parsing it.
func (l *ctLexer) skipDict() {
	l.pos += 2
	depth := 1
	for l.pos < len(l.d) && depth > 0 {
		if l.pos+1 < len(l.d) && l.d[l.pos] == '<' && l.d[l.pos+1] == '<' {
			depth++
			l.pos += 2
			continue
		}
		if l.pos+1 < len(l.d) && l.d[l.pos] == '>' && l.d[l.pos+1] == '>' {
			depth--
			l.pos += 2
			continue
		}
		l.pos++
	}
}

// ------------------------------------------------------------ ExtractText

// ExtractText pulls a best-effort plain-text rendering of file's selectable
// text out of its content streams, page by page.
func ExtractText(file []byte) (string, error) {
	d, err := Parse(file)
	if err != nil {
		return "", err
	}
	pages, err := d.Pages()
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for i, page := range pages {
		text := extractPageText(d, page)
		out.WriteString(text)
		if i != len(pages)-1 {
			out.WriteString("\n\n")
		}
	}
	return out.String(), nil
}

// extractPageText is best-effort: any failure to resolve content/fonts for
// this page just yields an empty string for it, never an error.
func extractPageText(d *Doc, page Page) string {
	content := pageContentBytes(d, page)
	if content == nil {
		return ""
	}
	fonts := pageFontDecoders(d, page)

	var out strings.Builder
	var cur *fontDecoder
	var pending []ctTok // operands seen since the last operator

	showString := func(s []byte) {
		fd := cur
		if fd == nil {
			fd = fallbackFontDecoder
		}
		out.WriteString(fd.decode(s))
	}

	lx := &ctLexer{d: content}
	for {
		tok, ok := lx.next()
		if !ok {
			break
		}
		if tok.kind != ctOperator {
			pending = append(pending, tok)
			continue
		}
		switch string(tok.str) {
		case "Tf":
			if len(pending) >= 2 && pending[len(pending)-2].kind == ctName {
				name := Name(pending[len(pending)-2].str)
				cur = fonts[name] // nil if not found, that's fine
			}
		case "Tj":
			if len(pending) >= 1 && pending[len(pending)-1].kind == ctString {
				showString(pending[len(pending)-1].str)
			}
		case "TJ":
			if len(pending) >= 1 && pending[len(pending)-1].kind == ctArray {
				for _, el := range pending[len(pending)-1].arr {
					switch el.kind {
					case ctString:
						showString(el.str)
					case ctNumber:
						if el.num < -100 {
							out.WriteString(" ")
						}
					}
				}
			}
		case "'":
			if len(pending) >= 1 && pending[len(pending)-1].kind == ctString {
				out.WriteString("\n")
				showString(pending[len(pending)-1].str)
			}
		case "\"":
			if len(pending) >= 1 && pending[len(pending)-1].kind == ctString {
				out.WriteString("\n")
				showString(pending[len(pending)-1].str)
			}
		case "Td", "TD", "T*":
			out.WriteString("\n")
		}
		pending = pending[:0]
	}
	return out.String()
}

// pageContentBytes resolves and concatenates a page's /Contents stream(s).
// /Contents lives on the page's own dict (it's not one of the inheritable
// attributes Pages() copies into page.Attrs), so it's fetched via d.Get.
func pageContentBytes(d *Doc, page Page) []byte {
	pageDict, ok := d.Get(page.Num).(Dict)
	if !ok {
		return nil
	}
	contents, ok := pageDict["Contents"]
	if !ok {
		return nil
	}
	switch v := d.R(contents).(type) {
	case *Stream:
		data, err := d.decodeStream(v)
		if err != nil {
			return nil
		}
		return data
	case Array:
		var parts [][]byte
		for _, item := range v {
			st, ok := d.R(item).(*Stream)
			if !ok {
				continue
			}
			data, err := d.decodeStream(st)
			if err != nil {
				continue
			}
			parts = append(parts, data)
		}
		return bytes.Join(parts, []byte(" "))
	}
	return nil
}

// pageFontDecoders resolves a page's /Resources /Font dict into a map of
// font resource name -> decoder.
func pageFontDecoders(d *Doc, page Page) map[Name]*fontDecoder {
	out := map[Name]*fontDecoder{}
	res, ok := d.R(page.Attrs["Resources"]).(Dict)
	if !ok {
		return out
	}
	fontDict, ok := d.R(res["Font"]).(Dict)
	if !ok {
		return out
	}
	for name, v := range fontDict {
		fd, ok := d.R(v).(Dict)
		if !ok {
			continue
		}
		out[name] = d.buildFontDecoder(fd)
	}
	return out
}
