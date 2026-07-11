package pdf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// ---------------------------------------------------------------- parsing ---
//
// ttfFont holds everything needed to (a) map runes to glyph IDs and advance
// widths for laying out text, and (b) produce a subset .ttf containing only
// the glyphs actually used, for embedding as a PDF CIDFontType2 FontFile2.
//
// Glyph IDs are never renumbered: the subset keeps the original numGlyphs
// and simply zeroes out the glyf data for unused glyphs. This lets the PDF
// side use /CIDToGIDMap /Identity and skip building (and shipping) a
// GID-renumbering table.
type ttfFont struct {
	unitsPerEm             int
	numGlyphs              int
	ascender, descender    int
	xMin, yMin, xMax, yMax int

	headRaw []byte // raw "head" table bytes, copied unmodified from the source font
	hheaRaw []byte // raw "hhea" table bytes
	maxpRaw []byte // raw "maxp" table bytes
	hmtxRaw []byte // raw "hmtx" table bytes

	loca []uint32 // numGlyphs+1 cumulative byte offsets into glyf
	glyf []byte   // raw "glyf" table bytes

	advances []uint16 // per-glyph advance width in font units, length numGlyphs

	runeToGID map[rune]uint16 // from the font's Unicode cmap subtable, if any

	used map[uint16]bool // glyph IDs to keep in subset(); always includes 0 (.notdef)
}

type sfntTable struct{ offset, length uint32 }

// parseTTF parses a TrueType font's sfnt table directory and the handful of
// tables needed for subsetting and metrics (head, hhea, maxp, hmtx, loca,
// glyf, cmap). Tables not needed for embedding (OS/2, post, name, GSUB/GPOS,
// cvt/fpgm/prep, DSIG, ...) are ignored entirely.
func parseTTF(data []byte) (*ttfFont, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("ttf: file too short")
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	if len(data) < 12+numTables*16 {
		return nil, fmt.Errorf("ttf: truncated table directory")
	}
	tables := map[string]sfntTable{}
	for i := 0; i < numTables; i++ {
		e := 12 + i*16
		tag := string(data[e : e+4])
		off := binary.BigEndian.Uint32(data[e+8 : e+12])
		length := binary.BigEndian.Uint32(data[e+12 : e+16])
		tables[tag] = sfntTable{off, length}
	}

	get := func(tag string) ([]byte, error) {
		t, ok := tables[tag]
		if !ok {
			return nil, fmt.Errorf("ttf: missing required table %q", tag)
		}
		if uint64(t.offset)+uint64(t.length) > uint64(len(data)) {
			return nil, fmt.Errorf("ttf: table %q out of bounds", tag)
		}
		return data[t.offset : t.offset+t.length], nil
	}

	head, err := get("head")
	if err != nil {
		return nil, err
	}
	hhea, err := get("hhea")
	if err != nil {
		return nil, err
	}
	maxp, err := get("maxp")
	if err != nil {
		return nil, err
	}
	hmtx, err := get("hmtx")
	if err != nil {
		return nil, err
	}
	locaRaw, err := get("loca")
	if err != nil {
		return nil, err
	}
	glyf, err := get("glyf")
	if err != nil {
		return nil, err
	}
	if len(head) < 54 || len(hhea) < 36 || len(maxp) < 6 {
		return nil, fmt.Errorf("ttf: required table shorter than expected")
	}

	f := &ttfFont{
		unitsPerEm: int(binary.BigEndian.Uint16(head[18:20])),
		xMin:       int(int16(binary.BigEndian.Uint16(head[36:38]))),
		yMin:       int(int16(binary.BigEndian.Uint16(head[38:40]))),
		xMax:       int(int16(binary.BigEndian.Uint16(head[40:42]))),
		yMax:       int(int16(binary.BigEndian.Uint16(head[42:44]))),
		headRaw:    append([]byte(nil), head...),
		hheaRaw:    append([]byte(nil), hhea...),
		maxpRaw:    append([]byte(nil), maxp...),
		hmtxRaw:    append([]byte(nil), hmtx...),
		glyf:       append([]byte(nil), glyf...),
	}
	indexToLocFormat := int16(binary.BigEndian.Uint16(head[50:52]))
	f.numGlyphs = int(binary.BigEndian.Uint16(maxp[4:6]))
	f.ascender = int(int16(binary.BigEndian.Uint16(hhea[4:6])))
	f.descender = int(int16(binary.BigEndian.Uint16(hhea[6:8])))
	numberOfHMetrics := int(binary.BigEndian.Uint16(hhea[34:36]))

	// loca: numGlyphs+1 entries, short format stores offset/2.
	f.loca = make([]uint32, f.numGlyphs+1)
	if indexToLocFormat == 0 {
		if len(locaRaw) < 2*(f.numGlyphs+1) {
			return nil, fmt.Errorf("ttf: loca table too short")
		}
		for i := 0; i <= f.numGlyphs; i++ {
			f.loca[i] = uint32(binary.BigEndian.Uint16(locaRaw[i*2:])) * 2
		}
	} else {
		if len(locaRaw) < 4*(f.numGlyphs+1) {
			return nil, fmt.Errorf("ttf: loca table too short")
		}
		for i := 0; i <= f.numGlyphs; i++ {
			f.loca[i] = binary.BigEndian.Uint32(locaRaw[i*4:])
		}
	}

	// hmtx: numberOfHMetrics {advanceWidth uint16, lsb int16} entries; glyphs
	// beyond that reuse the final entry's advance width.
	f.advances = make([]uint16, f.numGlyphs)
	var last uint16
	for i := 0; i < f.numGlyphs; i++ {
		if i < numberOfHMetrics {
			off := i * 4
			if off+2 > len(hmtx) {
				break
			}
			last = binary.BigEndian.Uint16(hmtx[off : off+2])
		}
		f.advances[i] = last
	}

	if cmapTable, ok := tables["cmap"]; ok {
		if uint64(cmapTable.offset)+uint64(cmapTable.length) <= uint64(len(data)) {
			f.runeToGID = parseCmapTable(data, cmapTable.offset)
		}
	}

	return f, nil
}

// cmapCandidate is a subtable found while scanning the cmap directory,
// ranked by how well-suited it is for full-Unicode lookup.
type cmapCandidate struct {
	priority int
	offset   uint32
}

// parseCmapTable scans a "cmap" table's subtable directory (at cmapOff in
// data) and parses the best available Unicode subtable into a rune->GID map.
// Format 12 (full Unicode, platform 3/10 or platform 0) is preferred over
// format 4 (platform 3/1, BMP only).
//
// ponytail: only formats 4 and 12 are implemented (the two used for Unicode
// cmaps in practice); formats 0/2/6/13/14 are skipped.
func parseCmapTable(data []byte, cmapOff uint32) map[rune]uint16 {
	if uint64(cmapOff)+4 > uint64(len(data)) {
		return nil
	}
	n := int(binary.BigEndian.Uint16(data[cmapOff+2 : cmapOff+4]))
	var candidates []cmapCandidate
	for i := 0; i < n; i++ {
		e := cmapOff + 4 + uint32(i*8)
		if uint64(e)+8 > uint64(len(data)) {
			break
		}
		platformID := binary.BigEndian.Uint16(data[e : e+2])
		encodingID := binary.BigEndian.Uint16(data[e+2 : e+4])
		subOff := cmapOff + binary.BigEndian.Uint32(data[e+4:e+8])
		if uint64(subOff)+2 > uint64(len(data)) {
			continue
		}
		priority := 10
		switch {
		case platformID == 3 && encodingID == 10:
			priority = 100
		case platformID == 0:
			priority = 90
		case platformID == 3 && encodingID == 1:
			priority = 50
		}
		candidates = append(candidates, cmapCandidate{priority, subOff})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].priority > candidates[j].priority })

	for _, c := range candidates {
		format := binary.BigEndian.Uint16(data[c.offset : c.offset+2])
		switch format {
		case 12:
			if m := parseCmapFormat12(data, c.offset); len(m) > 0 {
				return m
			}
		case 4:
			if m := parseCmapFormat4(data, c.offset); len(m) > 0 {
				return m
			}
		}
	}
	return nil
}

// parseCmapFormat4 parses a format-4 (segment mapping to delta values)
// subtable at subOff, per the standard algorithm.
func parseCmapFormat4(data []byte, subOff uint32) map[rune]uint16 {
	if uint64(subOff)+8 > uint64(len(data)) {
		return nil
	}
	segCountX2 := uint32(binary.BigEndian.Uint16(data[subOff+6 : subOff+8]))
	endCodeOff := subOff + 14
	startCodeOff := endCodeOff + segCountX2 + 2 // + reservedPad
	idDeltaOff := startCodeOff + segCountX2
	idRangeOff := idDeltaOff + segCountX2
	if uint64(idRangeOff)+uint64(segCountX2) > uint64(len(data)) {
		return nil
	}

	m := map[rune]uint16{}
	segCount := int(segCountX2 / 2)
	for s := 0; s < segCount; s++ {
		endCode := binary.BigEndian.Uint16(data[endCodeOff+uint32(s*2):])
		startCode := binary.BigEndian.Uint16(data[startCodeOff+uint32(s*2):])
		idDelta := int16(binary.BigEndian.Uint16(data[idDeltaOff+uint32(s*2):]))
		idRangeOffset := binary.BigEndian.Uint16(data[idRangeOff+uint32(s*2):])
		if startCode == 0xFFFF && endCode == 0xFFFF {
			continue
		}
		for c := uint32(startCode); c <= uint32(endCode); c++ {
			var gid uint16
			if idRangeOffset == 0 {
				gid = uint16(uint32(int32(c) + int32(idDelta)))
			} else {
				addr := idRangeOff + uint32(s*2) + uint32(idRangeOffset) + 2*(c-uint32(startCode))
				if uint64(addr)+2 > uint64(len(data)) {
					if c == 0xFFFF {
						break
					}
					continue
				}
				g := binary.BigEndian.Uint16(data[addr:])
				if g != 0 {
					gid = uint16(uint32(g) + uint32(int32(idDelta)))
				}
			}
			if gid != 0 {
				m[rune(c)] = gid
			}
			if c == 0xFFFF {
				break // avoid uint32 wraparound on c++
			}
		}
	}
	return m
}

// parseCmapFormat12 parses a format-12 (segmented coverage) subtable at
// subOff, mapping full-Unicode code points via contiguous groups.
func parseCmapFormat12(data []byte, subOff uint32) map[rune]uint16 {
	if uint64(subOff)+16 > uint64(len(data)) {
		return nil
	}
	nGroups := binary.BigEndian.Uint32(data[subOff+12 : subOff+16])
	if uint64(subOff)+16+uint64(nGroups)*12 > uint64(len(data)) {
		return nil
	}
	const maxMappings = 1_200_000
	var mappings uint64
	m := map[rune]uint16{}
	for i := uint32(0); i < nGroups; i++ {
		off := subOff + 16 + i*12
		if uint64(off)+12 > uint64(len(data)) {
			break
		}
		startChar := binary.BigEndian.Uint32(data[off : off+4])
		endChar := binary.BigEndian.Uint32(data[off+4 : off+8])
		startGlyph := binary.BigEndian.Uint32(data[off+8 : off+12])
		if endChar < startChar || endChar > utf8.MaxRune {
			return nil
		}
		span := uint64(endChar) - uint64(startChar) + 1
		if mappings > maxMappings-span {
			return nil
		}
		mappings += span
		for c := startChar; c <= endChar; c++ {
			gid := startGlyph + (c - startChar)
			if gid <= 0xFFFF {
				m[rune(c)] = uint16(gid)
			}
			if c == 0xFFFFFFFF {
				break // avoid uint32 wraparound on c++
			}
		}
	}
	return m
}

// ------------------------------------------------------------- accessors ---

// gid returns the glyph ID for rune r via the font's Unicode cmap.
func (f *ttfFont) gid(r rune) (uint16, bool) {
	g, ok := f.runeToGID[r]
	return g, ok
}

// advance1000 returns gid's advance width scaled to a 1000-unit em, as used
// by PDF /W arrays and simple-font /Widths arrays.
func (f *ttfFont) advance1000(gid uint16) int {
	if int(gid) >= len(f.advances) || f.unitsPerEm == 0 {
		return 0
	}
	return int(math.Round(float64(f.advances[gid]) * 1000 / float64(f.unitsPerEm)))
}

// locaRange returns the [start,end) byte range of gid's glyph data within
// the original (non-subset) glyf table. end==start means an empty glyph.
func (f *ttfFont) locaRange(gid uint16) (uint32, uint32) {
	i := int(gid)
	if i < 0 || i+1 >= len(f.loca) {
		return 0, 0
	}
	return f.loca[i], f.loca[i+1]
}

// markUsed records runes (and, transitively, the glyphs they and any
// composite glyphs reference) as needed in the eventual subset(). GID 0
// (.notdef) is always included. Runes with no cmap entry are silently
// skipped (encode() falls back to GID 0 for them too).
func (f *ttfFont) markUsed(runes ...rune) {
	if f.used == nil {
		f.used = map[uint16]bool{}
	}
	f.addUsedGlyph(0)
	for _, r := range runes {
		if g, ok := f.runeToGID[r]; ok {
			f.addUsedGlyph(g)
		}
	}
}

// addUsedGlyph adds gid to the used set and, if it's a composite glyph,
// recursively adds every component glyph it references.
func (f *ttfFont) addUsedGlyph(gid uint16) {
	if f.used[gid] {
		return
	}
	f.used[gid] = true
	lo, hi := f.locaRange(gid)
	if hi <= lo || hi > uint32(len(f.glyf)) {
		return
	}
	data := f.glyf[lo:hi]
	if len(data) < 10 {
		return
	}
	numberOfContours := int16(binary.BigEndian.Uint16(data[0:2]))
	if numberOfContours >= 0 {
		return // simple glyph, no components
	}
	pos := 10
	for {
		if pos+4 > len(data) {
			break
		}
		flags := binary.BigEndian.Uint16(data[pos : pos+2])
		componentGID := binary.BigEndian.Uint16(data[pos+2 : pos+4])
		pos += 4
		f.addUsedGlyph(componentGID)

		const argWords = 0x0001
		if flags&argWords != 0 {
			pos += 4
		} else {
			pos += 2
		}
		const (
			haveScale   = 0x0008
			haveXYScale = 0x0040
			have2x2     = 0x0080
			moreComps   = 0x0020
		)
		switch {
		case flags&haveScale != 0:
			pos += 2
		case flags&haveXYScale != 0:
			pos += 4
		case flags&have2x2 != 0:
			pos += 8
		}
		if flags&moreComps == 0 {
			break
		}
	}
}

// encode returns the raw big-endian GID bytes for s, two bytes per rune, for
// use as the operand of a content-stream hex-string Tj under
// /Encoding /Identity-H. Runes with no cmap entry encode as GID 0.
func (f *ttfFont) encode(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for _, r := range s {
		var gid uint16
		if g, ok := f.runeToGID[r]; ok {
			gid = g
		}
		out = append(out, byte(gid>>8), byte(gid))
	}
	return out
}

// -------------------------------------------------------------- subsetting ---

// subset builds a new sfnt file containing only head/hhea/maxp/hmtx/loca/glyf,
// with glyf data zeroed out for every glyph not in the current used set (see
// markUsed). Glyph IDs are not renumbered, so it pairs with
// /CIDToGIDMap /Identity in the PDF CIDFont dict.
func (f *ttfFont) subset() []byte {
	if f.used == nil {
		f.used = map[uint16]bool{}
	}
	f.used[0] = true // .notdef is always kept, even if markUsed was never called

	newLoca := make([]uint32, f.numGlyphs+1)
	var newGlyf bytes.Buffer
	for gid := 0; gid < f.numGlyphs; gid++ {
		newLoca[gid] = uint32(newGlyf.Len())
		if f.used[uint16(gid)] {
			lo, hi := f.loca[gid], f.loca[gid+1]
			if hi > lo && hi <= uint32(len(f.glyf)) {
				glyphData := f.glyf[lo:hi]
				newGlyf.Write(glyphData)
				if len(glyphData)%2 == 1 {
					newGlyf.WriteByte(0)
				}
			}
		}
	}
	newLoca[f.numGlyphs] = uint32(newGlyf.Len())

	locaBuf := make([]byte, 4*len(newLoca))
	for i, v := range newLoca {
		binary.BigEndian.PutUint32(locaBuf[i*4:], v)
	}

	headCopy := append([]byte(nil), f.headRaw...)
	// ponytail: skip checksum adjustment; PDF renderers tolerate an unset
	// checkSumAdjustment on embedded subsets.
	binary.BigEndian.PutUint32(headCopy[8:12], 0)
	binary.BigEndian.PutUint16(headCopy[50:52], 1) // force indexToLocFormat = long

	tables := map[string][]byte{
		"glyf": newGlyf.Bytes(),
		"head": headCopy,
		"hhea": append([]byte(nil), f.hheaRaw...),
		"hmtx": append([]byte(nil), f.hmtxRaw...),
		"loca": locaBuf,
		"maxp": append([]byte(nil), f.maxpRaw...),
	}
	return buildSfnt(tables)
}

// buildSfnt assembles a minimal sfnt file from a set of already-encoded
// tables, writing the standard header, a directory sorted by tag, and the
// table data padded to 4-byte boundaries.
func buildSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	numTables := len(tags)
	entrySelector := 0
	for (1 << uint(entrySelector+1)) <= numTables {
		entrySelector++
	}
	searchRange := (1 << uint(entrySelector)) * 16
	rangeShift := numTables*16 - searchRange

	header := make([]byte, 12)
	binary.BigEndian.PutUint32(header[0:4], 0x00010000)
	binary.BigEndian.PutUint16(header[4:6], uint16(numTables))
	binary.BigEndian.PutUint16(header[6:8], uint16(searchRange))
	binary.BigEndian.PutUint16(header[8:10], uint16(entrySelector))
	binary.BigEndian.PutUint16(header[10:12], uint16(rangeShift))

	offset := uint32(12 + 16*numTables)
	var dir, body bytes.Buffer
	for _, tag := range tags {
		data := tables[tag]
		padded := padTo4(data)

		entry := make([]byte, 16)
		copy(entry[0:4], tag)
		binary.BigEndian.PutUint32(entry[4:8], tableChecksum(padded))
		binary.BigEndian.PutUint32(entry[8:12], offset)
		binary.BigEndian.PutUint32(entry[12:16], uint32(len(data)))
		dir.Write(entry)

		body.Write(padded)
		offset += uint32(len(padded))
	}

	out := make([]byte, 0, len(header)+dir.Len()+body.Len())
	out = append(out, header...)
	out = append(out, dir.Bytes()...)
	out = append(out, body.Bytes()...)
	return out
}

func padTo4(data []byte) []byte {
	pad := (4 - len(data)%4) % 4
	if pad == 0 {
		return data
	}
	out := make([]byte, len(data)+pad)
	copy(out, data)
	return out
}

func tableChecksum(padded []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(padded); i += 4 {
		sum += binary.BigEndian.Uint32(padded[i : i+4])
	}
	return sum
}

// ------------------------------------------------------------- PDF embedding ---

// embedTTF writes f's current subset (see subset()/markUsed()) into b as a
// Type0/CIDFontType2 font using /Encoding /Identity-H and
// /CIDToGIDMap /Identity, plus a ToUnicode CMap covering usedRunesInOrder,
// and returns the Ref of the top-level Type0 font dict.
//
// Content streams built elsewhere should show text for this font via
// f.encode(s) as a hex-string Tj/TJ operand, e.g.:
//
//	fmt.Fprintf(&buf, "<%X> Tj\n", f.encode(line))
func embedTTF(b *builder, f *ttfFont, usedRunesInOrder []rune) (Ref, error) {
	subsetBytes := f.subset()
	length1 := len(subsetBytes)
	compressed := zlibDefault(subsetBytes)

	fontFileRef := b.alloc()
	b.objs[fontFileRef.Num-1] = &Stream{
		Dict: Dict{
			"Length1": length1,
			"Filter":  Name("FlateDecode"),
			"Length":  len(compressed),
		},
		Data: compressed,
	}

	scale := 1000.0 / float64(f.unitsPerEm)
	sc := func(v int) int { return int(math.Round(float64(v) * scale)) }
	ascent := sc(f.ascender)

	fdRef := b.alloc()
	b.objs[fdRef.Num-1] = Dict{
		"Type":        Name("FontDescriptor"),
		"FontName":    Name("NanumGothic"),
		"Flags":       4,
		"FontBBox":    Array{sc(f.xMin), sc(f.yMin), sc(f.xMax), sc(f.yMax)},
		"ItalicAngle": 0,
		"Ascent":      ascent,
		"Descent":     sc(f.descender),
		"CapHeight":   ascent,
		"StemV":       80,
		"FontFile2":   fontFileRef,
	}

	gids := make([]int, 0, len(f.used))
	for g := range f.used {
		gids = append(gids, int(g))
	}
	sort.Ints(gids)
	widths := make(Array, 0, 2*len(gids))
	for _, g := range gids {
		widths = append(widths, g, Array{f.advance1000(uint16(g))})
	}

	cidFontRef := b.alloc()
	b.objs[cidFontRef.Num-1] = Dict{
		"Type":     Name("Font"),
		"Subtype":  Name("CIDFontType2"),
		"BaseFont": Name("NanumGothic"),
		"CIDSystemInfo": Dict{
			"Registry":   String("Adobe"),
			"Ordering":   String("Identity"),
			"Supplement": 0,
		},
		"FontDescriptor": fdRef,
		"CIDToGIDMap":    Name("Identity"),
		"DW":             1000,
		"W":              widths,
	}

	tuRef, err := embedToUnicode(b, f, usedRunesInOrder)
	if err != nil {
		return Ref{}, err
	}

	type0Ref := b.alloc()
	b.objs[type0Ref.Num-1] = Dict{
		"Type":            Name("Font"),
		"Subtype":         Name("Type0"),
		"BaseFont":        Name("NanumGothic"),
		"Encoding":        Name("Identity-H"),
		"DescendantFonts": Array{cidFontRef},
		"ToUnicode":       tuRef,
	}
	return type0Ref, nil
}

// embedToUnicode writes a /ToUnicode CMap stream mapping each used rune's
// 2-byte GID code to its UTF-16BE text, in the shape parseToUnicodeCMap (see
// text.go) expects: beginbfchar/endbfchar blocks of "<code> <utf16be>" pairs.
func embedToUnicode(b *builder, f *ttfFont, usedRunesInOrder []rune) (Ref, error) {
	seen := map[rune]bool{}
	seenGlyphs := map[uint16]rune{}
	var lines []string
	for _, r := range usedRunesInOrder {
		if seen[r] {
			continue
		}
		seen[r] = true
		gid, ok := f.gid(r)
		if !ok {
			continue
		}
		if previous, exists := seenGlyphs[gid]; exists && previous != r {
			return Ref{}, fmt.Errorf("ambiguous ToUnicode mapping: glyph %d maps both %U and %U", gid, previous, r)
		}
		seenGlyphs[gid] = r
		units := utf16.Encode([]rune{r})
		var hexVal bytes.Buffer
		for _, u := range units {
			fmt.Fprintf(&hexVal, "%04X", u)
		}
		lines = append(lines, fmt.Sprintf("<%04X> <%s>", gid, hexVal.String()))
	}

	var body bytes.Buffer
	body.WriteString("/CIDInit /ProcSet findresource begin\n")
	body.WriteString("12 dict begin\n")
	body.WriteString("begincmap\n")
	body.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	body.WriteString("/CMapName /Adobe-Identity-UCS def\n")
	body.WriteString("/CMapType 2 def\n")
	body.WriteString("1 begincodespacerange\n")
	body.WriteString("<0000> <FFFF>\n")
	body.WriteString("endcodespacerange\n")
	for i := 0; i < len(lines); i += 100 {
		end := i + 100
		if end > len(lines) {
			end = len(lines)
		}
		chunk := lines[i:end]
		fmt.Fprintf(&body, "%d beginbfchar\n", len(chunk))
		for _, l := range chunk {
			body.WriteString(l)
			body.WriteByte('\n')
		}
		body.WriteString("endbfchar\n")
	}
	body.WriteString("endcmap\n")
	body.WriteString("CMapName currentdict /CMap defineresource pop\n")
	body.WriteString("end\n")
	body.WriteString("end\n")

	compressed := zlibDefault(body.Bytes())
	ref := b.alloc()
	b.objs[ref.Num-1] = &Stream{
		Dict: Dict{
			"Filter": Name("FlateDecode"),
			"Length": len(compressed),
		},
		Data: compressed,
	}
	return ref, nil
}
