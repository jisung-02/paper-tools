// Package pdf implements a minimal, dependency-free PDF reader and writer
// sufficient for merging and splitting documents.
package pdf

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// ErrEncrypted is returned by Parse when the document has an /Encrypt entry.
var ErrEncrypted = errors.New("encrypted files are not supported")

const maxInflateBytes = 64 << 20

var errInflateTooLarge = errors.New("inflated stream too large")

type Ref struct{ Num, Gen int }
type Name string
type Dict map[Name]any
type Array []any
type String []byte

type Stream struct {
	Dict Dict
	Data []byte // raw bytes as stored in file, still encoded
}

type xrefEntry struct {
	typ  int // 0 free, 1 = byte offset, 2 = inside object stream
	a, b int // typ1: offset, gen; typ2: container objnum, index
}

type Doc struct {
	data    []byte
	xref    map[int]xrefEntry
	trailer Dict
	objs    map[int]any  // cache
	loading map[int]bool // re-entrancy guard

	fileKey  []byte // non-nil once an encrypted doc's password has checked out
	cryptAES bool   // true for AESV2, false for RC4 (V2)
}

// ---------------------------------------------------------------- lexer ---

type lexer struct {
	d   []byte
	pos int
}

func isWS(c byte) bool {
	switch c {
	case 0x00, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func (l *lexer) skipWS() {
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

func (l *lexer) keyword() string {
	l.skipWS()
	start := l.pos
	for l.pos < len(l.d) && !isWS(l.d[l.pos]) && !isDelim(l.d[l.pos]) {
		l.pos++
	}
	return string(l.d[start:l.pos])
}

func (l *lexer) number() (float64, bool, error) {
	l.skipWS()
	start := l.pos
	isInt := true
	if l.pos < len(l.d) && (l.d[l.pos] == '+' || l.d[l.pos] == '-') {
		l.pos++
	}
	sawDigit := false
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if c >= '0' && c <= '9' {
			sawDigit = true
			l.pos++
			continue
		}
		if c == '.' {
			isInt = false
			l.pos++
			continue
		}
		break
	}
	if !sawDigit {
		l.pos = start
		return 0, false, fmt.Errorf("not a number at offset %d", start)
	}
	v, err := strconv.ParseFloat(string(l.d[start:l.pos]), 64)
	if err != nil {
		return 0, false, err
	}
	return v, isInt, nil
}

func (l *lexer) int() (int, error) {
	v, isInt, err := l.number()
	if err != nil {
		return 0, err
	}
	if !isInt {
		return 0, fmt.Errorf("expected integer at offset %d", l.pos)
	}
	return int(v), nil
}

func (l *lexer) obj() (any, error) {
	l.skipWS()
	if l.pos >= len(l.d) {
		return nil, io.ErrUnexpectedEOF
	}
	c := l.d[l.pos]
	switch {
	case c == '<' && l.pos+1 < len(l.d) && l.d[l.pos+1] == '<':
		return l.dict()
	case c == '<':
		return l.hexString()
	case c == '(':
		return l.litString()
	case c == '/':
		return l.name()
	case c == '[':
		return l.array()
	case c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.':
		return l.numberOrRef()
	default:
		kw := l.keyword()
		switch kw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected token %q at offset %d", kw, l.pos)
	}
}

func (l *lexer) dict() (Dict, error) {
	l.pos += 2 // consume "<<"
	d := Dict{}
	for {
		l.skipWS()
		if l.pos+1 < len(l.d) && l.d[l.pos] == '>' && l.d[l.pos+1] == '>' {
			l.pos += 2
			break
		}
		if l.pos >= len(l.d) {
			return nil, fmt.Errorf("unterminated dict")
		}
		if l.d[l.pos] != '/' {
			return nil, fmt.Errorf("dict key must start with / at offset %d", l.pos)
		}
		key, err := l.name()
		if err != nil {
			return nil, err
		}
		v, err := l.obj()
		if err != nil {
			return nil, err
		}
		if v == nil {
			// PDF spec: a key whose value is null is treated as absent.
			continue
		}
		d[key] = v
	}
	return d, nil
}

func (l *lexer) array() (any, error) {
	l.pos++ // consume '['
	var arr Array
	for {
		l.skipWS()
		if l.pos >= len(l.d) {
			return nil, fmt.Errorf("unterminated array")
		}
		if l.d[l.pos] == ']' {
			l.pos++
			break
		}
		v, err := l.obj()
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	return arr, nil
}

func (l *lexer) name() (Name, error) {
	l.pos++ // consume '/'
	var buf []byte
	for l.pos < len(l.d) {
		c := l.d[l.pos]
		if isWS(c) || isDelim(c) {
			break
		}
		if c == '#' && l.pos+2 < len(l.d) && isHex(l.d[l.pos+1]) && isHex(l.d[l.pos+2]) {
			buf = append(buf, hexVal(l.d[l.pos+1])<<4|hexVal(l.d[l.pos+2]))
			l.pos += 3
			continue
		}
		buf = append(buf, c)
		l.pos++
	}
	return Name(buf), nil
}

func (l *lexer) litString() (any, error) {
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
				return String(buf), nil
			}
			buf = append(buf, c)
		case '\\':
			l.pos++
			if l.pos >= len(l.d) {
				return String(buf), nil
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
	return String(buf), nil
}

func (l *lexer) hexString() (any, error) {
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
			return nil, fmt.Errorf("bad hex string char %q at offset %d", c, l.pos)
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
	return String(out), nil
}

func (l *lexer) numberOrRef() (any, error) {
	v, isInt, err := l.number()
	if err != nil {
		return nil, err
	}
	if isInt && v >= 0 {
		save := l.pos
		g, err2 := l.int()
		if err2 == nil && g >= 0 {
			kw := l.keyword()
			if kw == "R" {
				return Ref{Num: int(v), Gen: g}, nil
			}
		}
		l.pos = save
	}
	if isInt {
		return int(v), nil
	}
	return v, nil
}

// -------------------------------------------------------------- parsing ---

// findStartXref locates the startxref keyword near the end of the file and
// returns the byte offset that follows it.
func findStartXref(data []byte) (int, error) {
	tail := data
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("startxref not found: not a PDF?")
	}
	off := len(data) - len(tail) + idx + len("startxref")
	lx := &lexer{d: data, pos: off}
	v, err := lx.int()
	if err != nil {
		return 0, fmt.Errorf("bad startxref value: %w", err)
	}
	return v, nil
}

// Parse reads a PDF document, following the xref chain to build an object
// index. It does not decode object contents eagerly.
func Parse(data []byte) (*Doc, error) {
	return parse(data, nil)
}

// parse is the shared implementation behind Parse and ParseWithPassword. A
// nil pw preserves today's behavior of surfacing ErrEncrypted on any
// /Encrypt entry; a non-nil pw instead tries to unlock the document with it.
func parse(data []byte, pw *string) (*Doc, error) {
	off, err := findStartXref(data)
	if err != nil {
		return nil, err
	}
	d := &Doc{
		data:    data,
		xref:    map[int]xrefEntry{},
		trailer: Dict{},
		objs:    map[int]any{},
		loading: map[int]bool{},
	}
	seen := map[int]bool{}
	for off != 0 && !seen[off] {
		seen[off] = true
		prev, err := d.readXrefSection(off)
		if err != nil {
			return nil, err
		}
		off = prev
	}
	if _, ok := d.trailer["Encrypt"]; ok {
		if pw == nil {
			return nil, ErrEncrypted
		}
		if err := d.setupCrypt(*pw); err != nil {
			return nil, err
		}
	}
	if _, ok := d.trailer["Root"]; !ok {
		return nil, fmt.Errorf("missing trailer /Root")
	}
	return d, nil
}

func (d *Doc) addEntry(num int, e xrefEntry) {
	if _, ok := d.xref[num]; ok {
		return
	}
	d.xref[num] = e
}

func (d *Doc) mergeTrailer(t Dict) {
	for k, v := range t {
		if _, ok := d.trailer[k]; !ok {
			d.trailer[k] = v
		}
	}
}

func (d *Doc) readXrefSection(off int) (int, error) {
	if off < 0 || off >= len(d.data) {
		return 0, fmt.Errorf("xref offset %d out of range", off)
	}
	lx := &lexer{d: d.data, pos: off}
	save := lx.pos
	kw := lx.keyword()
	if kw == "xref" {
		return d.readClassicXref(lx)
	}
	lx.pos = save
	return d.readXrefStream(lx)
}

type collectedEntry struct {
	num int
	e   xrefEntry
}

func (d *Doc) readClassicXref(lx *lexer) (int, error) {
	var collected []collectedEntry
	for {
		lx.skipWS()
		save := lx.pos
		kw := lx.keyword()
		if kw == "trailer" {
			break
		}
		lx.pos = save
		start, err := lx.int()
		if err != nil {
			return 0, fmt.Errorf("bad xref subsection header: %w", err)
		}
		count, err := lx.int()
		if err != nil {
			return 0, fmt.Errorf("bad xref subsection header: %w", err)
		}
		for i := 0; i < count; i++ {
			f1, err := lx.int()
			if err != nil {
				return 0, fmt.Errorf("bad xref entry: %w", err)
			}
			f2, err := lx.int()
			if err != nil {
				return 0, fmt.Errorf("bad xref entry: %w", err)
			}
			typKw := lx.keyword()
			num := start + i
			switch typKw {
			case "n":
				collected = append(collected, collectedEntry{num, xrefEntry{typ: 1, a: f1, b: f2}})
			case "f":
				collected = append(collected, collectedEntry{num, xrefEntry{typ: 0}})
			default:
				return 0, fmt.Errorf("bad xref entry type %q", typKw)
			}
		}
	}
	tv, err := lx.obj()
	if err != nil {
		return 0, fmt.Errorf("bad trailer: %w", err)
	}
	trailer, ok := tv.(Dict)
	if !ok {
		return 0, fmt.Errorf("trailer is not a dict")
	}
	// Hybrid-file rule: entries from XRefStm take precedence over this
	// classic section's own entries.
	if xs, ok := trailer["XRefStm"]; ok {
		if n, ok := xs.(int); ok {
			if _, err := d.readXrefStream(&lexer{d: d.data, pos: n}); err != nil {
				return 0, err
			}
		}
	}
	for _, c := range collected {
		d.addEntry(c.num, c.e)
	}
	d.mergeTrailer(trailer)
	prev := 0
	if p, ok := trailer["Prev"].(int); ok {
		prev = p
	}
	return prev, nil
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func (d *Doc) readXrefStream(lx *lexer) (int, error) {
	_, v, err := d.parseObjectAt(lx.pos)
	if err != nil {
		return 0, err
	}
	st, ok := v.(*Stream)
	if !ok {
		return 0, fmt.Errorf("xref entry is not a stream")
	}
	data, err := d.decodeStream(st)
	if err != nil {
		return 0, err
	}
	wArr, ok := st.Dict["W"].(Array)
	if !ok || len(wArr) != 3 {
		return 0, fmt.Errorf("bad xref stream /W")
	}
	var w [3]int
	for i := 0; i < 3; i++ {
		n, ok := wArr[i].(int)
		if !ok {
			return 0, fmt.Errorf("bad /W entry")
		}
		if n < 0 {
			return 0, fmt.Errorf("bad /W entry")
		}
		w[i] = n
	}
	size, _ := st.Dict["Size"].(int)
	var index []int
	if idxArr, ok := st.Dict["Index"].(Array); ok {
		for _, v := range idxArr {
			n, ok := v.(int)
			if !ok {
				return 0, fmt.Errorf("bad /Index entry")
			}
			index = append(index, n)
		}
	} else {
		index = []int{0, size}
	}
	rowLen := w[0] + w[1] + w[2]
	pos := 0
	for i := 0; i+1 < len(index); i += 2 {
		start := index[i]
		count := index[i+1]
		for j := 0; j < count; j++ {
			if pos+rowLen > len(data) {
				return 0, fmt.Errorf("xref stream truncated")
			}
			row := data[pos : pos+rowLen]
			pos += rowLen
			off := 0
			typ := 1
			if w[0] > 0 {
				typ = int(beUint(row[0:w[0]]))
				off = w[0]
			}
			f1 := int(beUint(row[off : off+w[1]]))
			off += w[1]
			f2 := int(beUint(row[off : off+w[2]]))
			num := start + j
			switch typ {
			case 0:
				d.addEntry(num, xrefEntry{typ: 0})
			case 1:
				d.addEntry(num, xrefEntry{typ: 1, a: f1, b: f2})
			case 2:
				d.addEntry(num, xrefEntry{typ: 2, a: f1, b: f2})
			}
		}
	}
	d.mergeTrailer(st.Dict)
	prev := 0
	if p, ok := st.Dict["Prev"].(int); ok {
		prev = p
	}
	return prev, nil
}

// parseObjectAt lexes "num gen obj ... endobj" starting at off, returning
// the object number and its value (Dict, Array, *Stream, or scalar).
func (d *Doc) parseObjectAt(off int) (int, any, error) {
	lx := &lexer{d: d.data, pos: off}
	num, err := lx.int()
	if err != nil {
		return 0, nil, fmt.Errorf("bad object header: %w", err)
	}
	if _, err := lx.int(); err != nil { // gen
		return 0, nil, fmt.Errorf("bad object header: %w", err)
	}
	if kw := lx.keyword(); kw != "obj" {
		return 0, nil, fmt.Errorf("expected 'obj' keyword, got %q", kw)
	}
	v, err := lx.obj()
	if err != nil {
		return 0, nil, err
	}
	dict, ok := v.(Dict)
	if !ok {
		return num, v, nil
	}
	save := lx.pos
	lx.skipWS()
	kw := lx.keyword()
	if kw != "stream" {
		lx.pos = save
		return num, dict, nil
	}
	p := lx.pos
	if p < len(lx.d) && lx.d[p] == '\r' {
		p++
	}
	if p < len(lx.d) && lx.d[p] == '\n' {
		p++
	}
	start := p
	length := -1
	if lv, ok := dict["Length"]; ok {
		switch t := lv.(type) {
		case int:
			length = t
		case Ref:
			if iv, ok := d.Get(t.Num).(int); ok {
				length = iv
			}
		}
	}
	var sdata []byte
	if length >= 0 && start+length <= len(lx.d) {
		sdata = lx.d[start : start+length]
	} else {
		// ponytail: endstream scan fallback for broken /Length
		idx := bytes.Index(lx.d[start:], []byte("endstream"))
		if idx < 0 {
			return 0, nil, fmt.Errorf("stream without endstream")
		}
		end := start + idx
		sdata = bytes.TrimRight(lx.d[start:end], "\r\n")
	}
	return num, &Stream{Dict: dict, Data: sdata}, nil
}

// ------------------------------------------------------------- objects ---

// Get resolves an indirect object by number, caching the result.
func (d *Doc) Get(num int) any {
	if v, ok := d.objs[num]; ok {
		return v
	}
	if d.loading[num] {
		return nil
	}
	e, ok := d.xref[num]
	if !ok || e.typ == 0 {
		return nil
	}
	switch e.typ {
	case 1:
		d.loading[num] = true
		defer delete(d.loading, num)
		_, v, err := d.parseObjectAt(e.a)
		if err != nil {
			return nil
		}
		if d.fileKey != nil {
			v = d.decryptValue(num, e.b, v)
		}
		d.objs[num] = v
		return v
	case 2:
		d.loading[num] = true
		defer delete(d.loading, num)
		d.loadObjStm(e.a)
		return d.objs[num]
	}
	return nil
}

// R follows indirect references until a direct value is reached.
func (d *Doc) R(v any) any {
	hops := 0
	for {
		r, ok := v.(Ref)
		if !ok {
			return v
		}
		if hops >= 64 {
			return nil
		}
		hops++
		v = d.Get(r.Num)
	}
}

func (d *Doc) loadObjStm(container int) {
	sv := d.Get(container)
	st, ok := sv.(*Stream)
	if !ok {
		return
	}
	data, err := d.decodeStream(st)
	if err != nil {
		return
	}
	n, _ := d.R(st.Dict["N"]).(int)
	first, _ := d.R(st.Dict["First"]).(int)

	lx := &lexer{d: data}
	type pair struct{ num, off int }
	pairs := make([]pair, 0, n)
	for i := 0; i < n; i++ {
		onum, err := lx.int()
		if err != nil {
			return
		}
		ooff, err := lx.int()
		if err != nil {
			return
		}
		pairs = append(pairs, pair{onum, ooff})
	}
	for _, p := range pairs {
		e, ok := d.xref[p.num]
		if !ok || e.typ != 2 || e.a != container {
			continue
		}
		if _, ok := d.objs[p.num]; ok {
			continue
		}
		olx := &lexer{d: data, pos: first + p.off}
		v, err := olx.obj()
		if err != nil {
			continue
		}
		d.objs[p.num] = v
	}
}

// ------------------------------------------------------------- streams ---

// decodeStreamWith decodes s using resolve to follow indirect references,
// letting callers other than Doc (e.g. the builder, via b.rv) decode too.
func decodeStreamWith(resolve func(any) any, s *Stream) ([]byte, error) {
	filter := resolve(s.Dict["Filter"])
	if filter == nil {
		return s.Data, nil
	}
	var name Name
	switch f := filter.(type) {
	case Name:
		name = f
	case Array:
		if len(f) != 1 {
			return nil, fmt.Errorf("unsupported filter")
		}
		nm, ok := f[0].(Name)
		if !ok {
			return nil, fmt.Errorf("unsupported filter")
		}
		name = nm
	default:
		return nil, fmt.Errorf("unsupported filter")
	}
	if name != "FlateDecode" {
		return nil, fmt.Errorf("unsupported filter")
	}
	raw, err := inflate(s.Data)
	if err != nil {
		return nil, err
	}

	dp := resolve(s.Dict["DecodeParms"])
	if dp == nil {
		dp = resolve(s.Dict["DP"])
	}
	if arr, ok := dp.(Array); ok {
		if len(arr) > 0 {
			dp = resolve(arr[0])
		} else {
			dp = nil
		}
	}
	parms, ok := dp.(Dict)
	if !ok {
		return raw, nil
	}
	predictor := 1
	columns := 1
	colors := 1
	bpc := 8
	if v, ok := resolve(parms["Predictor"]).(int); ok {
		predictor = v
	}
	if v, ok := resolve(parms["Columns"]).(int); ok {
		columns = v
	}
	if v, ok := resolve(parms["Colors"]).(int); ok {
		colors = v
	}
	if v, ok := resolve(parms["BitsPerComponent"]).(int); ok {
		bpc = v
	}
	return unpredict(raw, predictor, columns, colors, bpc)
}

func (d *Doc) decodeStream(s *Stream) ([]byte, error) { return decodeStreamWith(d.R, s) }

func inflate(data []byte) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		out, err2 := readAllInflateLimited(zr)
		zr.Close()
		if err2 == nil {
			return out, nil
		}
		if errors.Is(err2, errInflateTooLarge) {
			return nil, err2
		}
	}
	// Some files omit the zlib header; fall back to raw deflate.
	fr := flate.NewReader(bytes.NewReader(data))
	out, err := readAllInflateLimited(fr)
	fr.Close()
	if err != nil {
		return nil, fmt.Errorf("inflate: %w", err)
	}
	return out, nil
}

func readAllInflateLimited(r io.Reader) ([]byte, error) {
	lr := &io.LimitedReader{R: r, N: maxInflateBytes + 1}
	out, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(out) > maxInflateBytes {
		return nil, fmt.Errorf("%w: limit %d bytes", errInflateTooLarge, maxInflateBytes)
	}
	return out, nil
}

func unpredict(data []byte, predictor, columns, colors, bpc int) ([]byte, error) {
	if predictor <= 1 {
		return data, nil
	}
	if columns <= 0 || colors <= 0 || bpc <= 0 {
		return nil, fmt.Errorf("invalid predictor parameters")
	}
	if predictor == 2 {
		if bpc != 8 {
			return nil, fmt.Errorf("tiff predictor requires 8 bits per component")
		}
		bpp := colors
		rowLen := colors * columns
		out := make([]byte, len(data))
		copy(out, data)
		for r := 0; r+rowLen <= len(out); r += rowLen {
			row := out[r : r+rowLen]
			for i := bpp; i < len(row); i++ {
				row[i] += row[i-bpp]
			}
		}
		return out, nil
	}
	// PNG predictors (>= 10).
	bpp := (colors*bpc + 7) / 8
	rowLen := (colors*bpc*columns + 7) / 8
	stride := rowLen + 1
	nrows := len(data) / stride
	out := make([]byte, nrows*rowLen)
	prev := make([]byte, rowLen)
	for r := 0; r < nrows; r++ {
		row := data[r*stride : r*stride+stride]
		ft := row[0]
		cur := row[1:]
		dst := out[r*rowLen : (r+1)*rowLen]
		for i := 0; i < rowLen; i++ {
			var left, up, upleft byte
			if i >= bpp {
				left = dst[i-bpp]
				upleft = prev[i-bpp]
			}
			up = prev[i]
			switch ft {
			case 0:
				dst[i] = cur[i]
			case 1:
				dst[i] = cur[i] + left
			case 2:
				dst[i] = cur[i] + up
			case 3:
				dst[i] = cur[i] + byte((int(left)+int(up))/2)
			case 4:
				dst[i] = cur[i] + paeth(left, up, upleft)
			default:
				return nil, fmt.Errorf("unsupported png filter type %d", ft)
			}
		}
		prev = dst
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa := abs(p - int(a))
	pb := abs(p - int(b))
	pc := abs(p - int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ------------------------------------------------------------ page tree ---

type Page struct {
	Num   int  // object number in source doc
	Attrs Dict // inherited attributes to stamp onto the page (fill gaps only)
	Force Dict // attributes to force-set on the output page dict (overwrites)
}

var inheritable = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

// Pages walks the page tree rooted at trailer["Root"]["Pages"] and returns
// the leaf pages in document order, along with attributes inherited from
// ancestor nodes.
func (d *Doc) Pages() ([]Page, error) {
	root, ok := d.R(d.trailer["Root"]).(Dict)
	if !ok {
		return nil, fmt.Errorf("missing or invalid /Root")
	}
	rootRef, ok := root["Pages"].(Ref)
	if !ok {
		return nil, fmt.Errorf("missing /Pages in /Root")
	}

	visited := map[int]bool{}
	var pages []Page
	var walk func(ref Ref, inherited Dict) error
	walk = func(ref Ref, inherited Dict) error {
		if visited[ref.Num] {
			return nil
		}
		visited[ref.Num] = true
		node, ok := d.Get(ref.Num).(Dict)
		if !ok {
			return nil
		}
		newInherited := Dict{}
		for k, v := range inherited {
			newInherited[k] = v
		}
		for _, k := range inheritable {
			if v, ok := node[k]; ok {
				newInherited[k] = v
			}
		}
		kids, hasKids := d.R(node["Kids"]).(Array)
		isPage := node["Type"] == Name("Page")
		if hasKids && !isPage {
			for _, kv := range kids {
				kref, ok := kv.(Ref)
				if !ok {
					continue
				}
				if err := walk(kref, newInherited); err != nil {
					return err
				}
			}
			return nil
		}
		pages = append(pages, Page{Num: ref.Num, Attrs: newInherited})
		return nil
	}
	if err := walk(rootRef, Dict{}); err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("document has zero pages")
	}
	return pages, nil
}
