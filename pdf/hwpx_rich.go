package pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Static hwpx package parts (identical to the previous text-only builder).
const hwpxVersionXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hv:HCFVersion xmlns:hv="http://www.hancom.co.kr/hwpml/2011/version" tagetApplication="WORDPROCESSOR" major="5" minor="0" micro="5" buildNumber="0" os="1" xmlVersion="1.4" application="종이도구" appVersion="1.0"/>`

const hwpxContainerXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><ocf:container xmlns:ocf="urn:oasis:names:tc:opendocument:xmlns:container"><ocf:rootfiles><ocf:rootfile full-path="Contents/content.hpf" media-type="application/hwpml-package+xml"/></ocf:rootfiles></ocf:container>`

const hwpxManifestXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><odf:manifest xmlns:odf="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" version="1.2"><odf:file-entry odf:full-path="/" odf:media-type="application/hwp+zip"/><odf:file-entry odf:full-path="Contents/content.hpf" odf:media-type="application/hwpml-package+xml"/><odf:file-entry odf:full-path="Contents/header.xml" odf:media-type="application/xml"/><odf:file-entry odf:full-path="Contents/section0.xml" odf:media-type="application/xml"/></odf:manifest>`

const hwpxContentHpf = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hpf:HWPML xmlns:hpf="http://www.hancom.co.kr/schema/2011/hpf" xmlns:opf="http://www.idpf.org/2007/opf/" version="1.4"><opf:package version="1.4" unique-identifier="종이도구"><opf:metadata><opf:title/></opf:metadata><opf:manifest><opf:item id="header" href="Contents/header.xml" media-type="application/xml"/><opf:item id="section0" href="Contents/section0.xml" media-type="application/xml"/></opf:manifest><opf:spine><opf:itemref idref="header" linear="yes"/><opf:itemref idref="section0" linear="yes"/></opf:spine></opf:package></hpf:HWPML>`

// hwpxAlignName maps a model alignment to the OWPML horizontal value.
// ponytail: AlignDefault keeps the template's JUSTIFY (hwpx convention).
func hwpxAlignName(a Align) string {
	switch a {
	case AlignLeft:
		return "LEFT"
	case AlignCenter:
		return "CENTER"
	case AlignRight:
		return "RIGHT"
	}
	return "JUSTIFY"
}

// hwpxCharPrXML renders one deduplicated charPr entry. height is 1/100 pt.
func hwpxCharPrXML(id int, r Run) string {
	height := 1100
	if r.SizePt > 0 {
		height = int(r.SizePt*100 + 0.5)
	}
	var deco strings.Builder
	if r.Italic {
		deco.WriteString(`<hh:italic/>`)
	}
	if r.Bold {
		deco.WriteString(`<hh:bold/>`)
	}
	if r.Underline {
		deco.WriteString(`<hh:underline type="BOTTOM" shape="SOLID" color="#000000"/>`)
	}
	if r.Strike {
		deco.WriteString(`<hh:strikeout shape="SOLID" color="#000000"/>`)
	}
	return fmt.Sprintf(`<hh:charPr id="%d" height="%d" textColor="#%06X" shadeColor="none" useFontSpace="0" useKerning="0" symMark="NONE" borderFillIDRef="0"><hh:fontRef hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/><hh:ratio hangul="100" latin="100" hanja="100" japanese="100" other="100" symbol="100" user="100"/><hh:spacing hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/><hh:relSz hangul="100" latin="100" hanja="100" japanese="100" other="100" symbol="100" user="100"/><hh:offset hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/>%s</hh:charPr>`,
		id, height, r.Color&0xFFFFFF, deco.String())
}

// hwpxParaPrXML renders one paraPr entry with the given alignment.
func hwpxParaPrXML(id int, a Align) string {
	return fmt.Sprintf(`<hh:paraPr id="%d" tabPrIDRef="0" condense="0" fontLineHeight="0" snapToGrid="1" suppressLineNumbers="0" checked="0"><hh:align horizontal="%s" vertical="BASELINE"/><hh:heading type="NONE" idRef="0" level="0"/><hh:breakSetting breakLatinWord="KEEP_WORD" breakNonLatinWord="KEEP_WORD" widowOrphan="0" keepWithNext="0" keepLines="0" pageBreakBefore="0" lineWrap="BREAK"/><hh:margin><hc:intent value="0" unit="HWPUNIT"/><hc:left value="0" unit="HWPUNIT"/><hc:right value="0" unit="HWPUNIT"/><hc:prev value="0" unit="HWPUNIT"/><hc:next value="0" unit="HWPUNIT"/></hh:margin><hh:lineSpacing type="PERCENT" value="160" unit="HWPUNIT"/></hh:paraPr>`,
		id, hwpxAlignName(a))
}

// writeHwpx serializes doc to an HWPX (OWPML) package, deduplicating run
// styles into charPr entries and paragraph formats into paraPr/style
// entries. Heading paragraphs fold bold + headingSizePt into their runs'
// charPr and get a "개요 N" style entry.
// ponytail: minimal OWPML head, single section, no real outline numbering;
// UNVALIDATED against real Hancom Office (same standing limitation as the
// previous text-only builder).
func writeHwpx(doc *DocModel) []byte {
	doc = normalizeDoc(doc)

	// Deduplicated tables. charPr id 0 is the plain default so empty
	// paragraphs and untouched readers keep working.
	charIDs := map[Run]int{{}: 0}
	charList := []Run{{}}
	charOf := func(r Run, heading int) int {
		s := r.style()
		if heading > 0 {
			s.Bold = true
			if s.SizePt == 0 {
				s.SizePt = headingSizePt(heading)
			}
		}
		if id, ok := charIDs[s]; ok {
			return id
		}
		// ponytail: cap the dedup table; pathological inputs with thousands
		// of distinct styles fall back to the plain default charPr.
		if len(charList) >= maxHwpxCharPrs {
			return 0
		}
		id := len(charList)
		charIDs[s] = id
		charList = append(charList, s)
		return id
	}
	paraIDs := map[Align]int{AlignDefault: 0}
	paraList := []Align{AlignDefault}
	paraOf := func(a Align) int {
		if id, ok := paraIDs[a]; ok {
			return id
		}
		id := len(paraList)
		paraIDs[a] = id
		paraList = append(paraList, a)
		return id
	}
	// style id 0 = 바탕글(Normal); one extra style per heading level used.
	styleOf := map[int]int{} // heading level -> style id
	type styleEnt struct{ level, paraPr, charPr int }
	var styleList []styleEnt

	// First pass: build the section body while filling the tables.
	var sec bytes.Buffer
	sec.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hs:sec xmlns:hs="http://www.hancom.co.kr/hwpml/2011/section" xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph">`)
	for _, blk := range doc.Blocks {
		p, ok := blk.(*Para)
		if !ok {
			continue
		}
		pid := paraOf(p.Align)
		sid := 0
		if p.Heading >= 1 && p.Heading <= 6 {
			if _, ok := styleOf[p.Heading]; !ok {
				styleOf[p.Heading] = len(styleList) + 1
				styleList = append(styleList, styleEnt{p.Heading, pid, charOf(Run{}, p.Heading)})
			}
			sid = styleOf[p.Heading]
		}
		fmt.Fprintf(&sec, `<hp:p paraPrIDRef="%d" styleIDRef="%d">`, pid, sid)
		if len(p.Runs) == 0 {
			sec.WriteString(`<hp:run charPrIDRef="0"><hp:t></hp:t></hp:run>`)
		}
		for _, r := range p.Runs {
			fmt.Fprintf(&sec, `<hp:run charPrIDRef="%d">`, charOf(r, p.Heading))
			for i, seg := range strings.Split(r.Text, "\t") {
				if i > 0 {
					sec.WriteString(`<hp:tab/>`)
				}
				sec.WriteString(`<hp:t>` + escapeXMLText(seg) + `</hp:t>`)
			}
			sec.WriteString(`</hp:run>`)
		}
		sec.WriteString(`</hp:p>`)
	}
	sec.WriteString(`</hs:sec>`)

	// Header with the collected tables.
	var head bytes.Buffer
	head.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hh:head xmlns:hh="http://www.hancom.co.kr/hwpml/2011/head" xmlns:hc="http://www.hancom.co.kr/hwpml/2011/core" version="1.4" secCnt="1"><hh:refList>`)
	head.WriteString(`<hh:fontfaces itemCnt="1"><hh:fontface lang="HANGUL" fontCnt="1"><hh:font id="0" face="함초롬바탕" type="TTF" isEmbedded="0"><hh:typeInfo familyType="FCF_UNKNOWN" weight="0" proportion="0" contrast="0" strokeVariation="0" armStyle="0" letterform="0" midline="0" xHeight="0"/></hh:font></hh:fontface></hh:fontfaces>`)
	fmt.Fprintf(&head, `<hh:charProperties itemCnt="%d">`, len(charList))
	for id, r := range charList {
		head.WriteString(hwpxCharPrXML(id, r))
	}
	head.WriteString(`</hh:charProperties>`)
	fmt.Fprintf(&head, `<hh:paraProperties itemCnt="%d">`, len(paraList))
	for id, a := range paraList {
		head.WriteString(hwpxParaPrXML(id, a))
	}
	head.WriteString(`</hh:paraProperties>`)
	fmt.Fprintf(&head, `<hh:styles itemCnt="%d">`, 1+len(styleList))
	head.WriteString(`<hh:style id="0" type="PARA" name="바탕글" engName="Normal" paraPrIDRef="0" charPrIDRef="0" nextStyleIDRef="0" langID="1042" lockForm="0"/>`)
	for i, s := range styleList {
		fmt.Fprintf(&head, `<hh:style id="%d" type="PARA" name="개요 %d" engName="Heading %d" paraPrIDRef="%d" charPrIDRef="%d" nextStyleIDRef="0" langID="1042" lockForm="0"/>`,
			i+1, s.level, s.level, s.paraPr, s.charPr)
	}
	head.WriteString(`</hh:styles></hh:refList></hh:head>`)

	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	// mimetype MUST be the first entry, stored uncompressed.
	if mf, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store}); err == nil {
		mf.Write([]byte("application/hwp+zip"))
	}
	writeZipEntry(w, "version.xml", hwpxVersionXML)
	writeZipEntry(w, "META-INF/container.xml", hwpxContainerXML)
	writeZipEntry(w, "META-INF/manifest.xml", hwpxManifestXML)
	writeZipEntry(w, "Contents/content.hpf", hwpxContentHpf)
	writeZipEntry(w, "Contents/header.xml", head.String())
	writeZipEntry(w, "Contents/section0.xml", sec.String())
	// ponytail: ignore Close() error; bytes.Buffer-backed zip.Writer never fails on Close.
	w.Close()
	return buf.Bytes()
}

// parseHwpx reads an HWPX package into the shared DocModel by first
// building id→format maps from Contents/header.xml, then walking the
// sorted section files.
// ponytail: charPr/paraPr/style resolution only — tables (stage 2), images
// (stage 3), numbering and per-language fonts are not modeled; their text
// still comes through as plain paragraphs.
func parseHwpx(data []byte) (*DocModel, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("유효한 hwpx 파일이 아닙니다")
	}
	if err := validateOfficeZIP(zr, "hwpx"); err != nil {
		return nil, err
	}

	charStyles := map[string]Run{}
	paraAligns := map[string]Align{}
	styleHeading := map[string]int{}
	var sections []*zip.File
	for _, f := range zr.File {
		if f.Name == "Contents/header.xml" {
			hb, err := readOfficeEntry(f, "hwpx")
			if err != nil {
				return nil, err
			}
			parseHwpxHeader(hb, charStyles, paraAligns, styleHeading)
		}
		if strings.HasPrefix(f.Name, "Contents/section") && strings.HasSuffix(f.Name, ".xml") {
			sections = append(sections, f)
		}
	}
	if len(sections) == 0 {
		return nil, errors.New("유효한 hwpx 파일이 아닙니다")
	}
	sort.Slice(sections, func(i, j int) bool {
		return hwpxSectionNumber(sections[i].Name) < hwpxSectionNumber(sections[j].Name)
	})

	doc := &DocModel{}
	textBytes := 0
	blocks, runs := 0, 0
	for _, f := range sections {
		sb, err := readOfficeEntry(f, "hwpx")
		if err != nil {
			return nil, err
		}
		if err := parseHwpxSection(sb, charStyles, paraAligns, styleHeading, doc, &textBytes, &blocks, &runs); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// parseHwpxHeader fills the id→format maps from header.xml.
func parseHwpxHeader(data []byte, charStyles map[string]Run, paraAligns map[string]Align, styleHeading map[string]int) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var curChar, curParaPr string // charPr/paraPr id being parsed, "" when outside
	for {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		t, ok := tok.(xml.StartElement)
		if !ok {
			if e, ok := tok.(xml.EndElement); ok {
				switch e.Name.Local {
				case "charPr":
					curChar = ""
				case "paraPr":
					curParaPr = ""
				}
			}
			continue
		}
		switch t.Name.Local {
		case "charPr":
			curChar = xmlAttr(t, "id")
			r := Run{}
			// ponytail: height 1100 is exactly what the writer emits for
			// SizePt 0 ("default 11pt"), so map it back to 0 — defaults stay
			// defaults across round-trips. An external explicit 11pt is
			// indistinguishable and also becomes default; visually identical
			// in this pipeline.
			if n, err := strconv.Atoi(xmlAttr(t, "height")); err == nil && n > 0 && n != 1100 {
				r.SizePt = float64(n) / 100
			}
			if c := xmlAttr(t, "textColor"); strings.HasPrefix(c, "#") {
				if v, err := strconv.ParseUint(c[1:], 16, 32); err == nil {
					r.Color = uint32(v) & 0xFFFFFF
				}
			}
			charStyles[curChar] = r
		case "bold":
			if curChar != "" {
				r := charStyles[curChar]
				r.Bold = true
				charStyles[curChar] = r
			}
		case "italic":
			if curChar != "" {
				r := charStyles[curChar]
				r.Italic = true
				charStyles[curChar] = r
			}
		case "underline":
			if curChar != "" && xmlAttr(t, "type") != "NONE" {
				r := charStyles[curChar]
				r.Underline = true
				charStyles[curChar] = r
			}
		case "strikeout":
			if curChar != "" && xmlAttr(t, "shape") != "NONE" {
				r := charStyles[curChar]
				r.Strike = true
				charStyles[curChar] = r
			}
		case "paraPr":
			curChar = ""
			id := xmlAttr(t, "id")
			// alignment arrives via the child <hh:align> below; record id now
			paraAligns[id] = AlignDefault
			// remember which paraPr the next align belongs to
			curParaPr = id
		case "align":
			if curParaPr != "" {
				switch xmlAttr(t, "horizontal") {
				case "LEFT":
					paraAligns[curParaPr] = AlignLeft
				case "CENTER":
					paraAligns[curParaPr] = AlignCenter
				case "RIGHT":
					paraAligns[curParaPr] = AlignRight
				}
			}
		case "style":
			name := xmlAttr(t, "engName")
			if lvl, ok := strings.CutPrefix(name, "Heading "); ok {
				if n, err := strconv.Atoi(lvl); err == nil && n >= 1 && n <= 6 {
					styleHeading[xmlAttr(t, "id")] = n
				}
			}
		}
	}
}

// parseHwpxSection appends paragraphs from one section XML to doc.
func parseHwpxSection(data []byte, charStyles map[string]Run, paraAligns map[string]Align, styleHeading map[string]int, doc *DocModel, textBytes, blocks, runs *int) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur *Para
	var curRun Run
	flush := func() {
		if cur != nil {
			cur.Runs = mergeRuns(cur.Runs)
			doc.Blocks = append(doc.Blocks, cur)
			*blocks++
			cur = nil
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("유효한 hwpx 파일이 아닙니다")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				flush()
				cur = &Para{
					Align:   paraAligns[xmlAttr(t, "paraPrIDRef")],
					Heading: styleHeading[xmlAttr(t, "styleIDRef")],
				}
			case "run":
				curRun = charStyles[xmlAttr(t, "charPrIDRef")]
			case "t":
				var sb strings.Builder
			readText:
				for {
					tok2, err := dec.Token()
					if err != nil {
						break readText
					}
					switch t2 := tok2.(type) {
					case xml.CharData:
						sb.Write(t2)
					case xml.EndElement:
						if t2.Name.Local == "t" {
							break readText
						}
					}
				}
				*textBytes += sb.Len()
				if *textBytes > int(officeParseLimits.maxTextBytes) {
					return errors.New("hwpx: extracted text too large")
				}
				if cur == nil {
					cur = &Para{}
				}
				nr := curRun
				nr.Text = sb.String()
				cur.Runs = append(cur.Runs, nr)
				*runs++
			case "tab":
				if cur != nil {
					nr := curRun
					nr.Text = "\t"
					cur.Runs = append(cur.Runs, nr)
					*runs++
				}
			case "lineBreak":
				if cur != nil {
					al, hd := cur.Align, cur.Heading
					flush()
					cur = &Para{Align: al, Heading: hd}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "run":
				curRun = Run{}
			case "p":
				curRun = Run{}
				flush()
			}
		}
		if *blocks > maxModelBlocks || *runs > maxModelRuns {
			return errors.New("hwpx: document too complex")
		}
	}
	flush()
	return nil
}
