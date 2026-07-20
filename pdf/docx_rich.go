package pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Static docx package parts. styles.xml carries the built-in Heading1..6
// definitions so w:pStyle references render correctly in Word/LibreOffice.
const docxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Default Extension="jpeg" ContentType="image/jpeg"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const docxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

// docxDocumentRelsHead/Tail bracket the styles relationship plus the
// per-image relationships appended dynamically by writeDocx.
const docxDocumentRelsHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`
const docxDocumentRelsTail = `</Relationships>`

// docxStylesXML defines Normal plus Heading1..6 (bold, sizes derived from
// headingSizePt at the 11pt body default, in half-points).
const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="35"/><w:szCs w:val="35"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="29"/><w:szCs w:val="29"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style></w:styles>`

// writeDocx serializes doc to a .docx package. Formatting written per run:
// bold/italic/underline/strike, size (half-points), color; per paragraph:
// alignment and HeadingN paragraph style.
func writeDocx(doc *DocModel) []byte {
	doc = normalizeDoc(doc)
	reg := &docxImageReg{ids: map[*Image]int{}}

	// Body is built first, into its own buffer, so reg is fully populated
	// before the rels/media entries (which depend on it) are written.
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>`)
	writeDocxBlocks(&b, doc.Blocks, reg)
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`)

	var rels strings.Builder
	rels.WriteString(docxDocumentRelsHead)
	for n, im := range reg.list {
		fmt.Fprintf(&rels, `<Relationship Id="rIdImg%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image%d.%s"/>`,
			n, n, imageExt(sniffImageMIME(im.Data)))
	}
	rels.WriteString(docxDocumentRelsTail)

	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	writeZipEntry(w, "[Content_Types].xml", docxContentTypesXML)
	writeZipEntry(w, "_rels/.rels", docxRootRels)
	writeZipEntry(w, "word/_rels/document.xml.rels", rels.String())
	writeZipEntry(w, "word/styles.xml", docxStylesXML)
	for n, im := range reg.list {
		name := fmt.Sprintf("word/media/image%d.%s", n, imageExt(sniffImageMIME(im.Data)))
		writeZipEntryBytes(w, name, im.Data)
	}
	writeZipEntry(w, "word/document.xml", b.String())

	// ponytail: ignore Close() error; bytes.Buffer-backed zip.Writer never fails on Close.
	w.Close()
	return buf.Bytes()
}

func writeZipEntry(w *zip.Writer, name, content string) {
	writeZipEntryBytes(w, name, []byte(content))
}

func writeZipEntryBytes(w *zip.Writer, name string, data []byte) {
	if f, err := w.Create(name); err == nil {
		// ponytail: Write to a bytes.Buffer-backed zip entry never fails; error ignored.
		f.Write(data)
	}
}

// docxImageReg assigns each distinct *Image a 0-based id in first-encounter
// order, shared between the body writer (for rIdImgN references) and the
// relationships/media part writers.
type docxImageReg struct {
	ids  map[*Image]int
	list []*Image
}

func (r *docxImageReg) id(im *Image) int {
	if n, ok := r.ids[im]; ok {
		return n
	}
	n := len(r.list)
	r.ids[im] = n
	r.list = append(r.list, im)
	return n
}

// imageExt maps a sniffed image MIME type to its docx/hwpx media part
// extension.
func imageExt(mime string) string {
	if mime == "image/jpeg" {
		return "jpeg"
	}
	return "png"
}

// docxTblPr renders full single-line borders so converted tables are
// visible in Word/LibreOffice with default styling.
const docxTblPr = `<w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/></w:tblBorders></w:tblPr>`

func writeDocxBlocks(b *bytes.Buffer, blocks []Block, reg *docxImageReg) {
	for _, blk := range blocks {
		switch t := blk.(type) {
		case *Para:
			writeDocxPara(b, t)
		case *Table:
			writeDocxTable(b, t, reg)
		case *Image:
			writeDocxImage(b, t, reg)
		}
	}
}

// writeDocxImage emits one inline drawing paragraph. Sizes are EMU
// (1pt = 12700).
func writeDocxImage(b *bytes.Buffer, im *Image, reg *docxImageReg) {
	if sniffImageMIME(im.Data) == "" {
		return // ponytail: unsupported/garbage image data is dropped on write
	}
	n := reg.id(im)
	wPt, hPt := im.displaySizePt()
	cx, cy := int(wPt*12700+0.5), int(hPt*12700+0.5)
	fmt.Fprintf(b, `<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Picture %d"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:nvPicPr><pic:cNvPr id="%d" name="Picture %d"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="rIdImg%d"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
		cx, cy, n+1, n+1, n+1, n+1, n, cx, cy)
}

// writeDocxTable serializes a table; covered grid positions become
// vMerge-continue cells (required by wordprocessingml), colspans become
// gridSpan.
func writeDocxTable(b *bytes.Buffer, t *Table, reg *docxImageReg) {
	cols, items := tableGrid(t)
	if cols == 0 {
		return
	}
	b.WriteString(`<w:tbl>` + docxTblPr + `<w:tblGrid>`)
	for i := 0; i < cols; i++ {
		b.WriteString(`<w:gridCol/>`)
	}
	b.WriteString(`</w:tblGrid>`)
	row := -1
	for _, it := range items {
		if it.Row != row {
			if row >= 0 {
				b.WriteString(`</w:tr>`)
			}
			row = it.Row
			b.WriteString(`<w:tr>`)
		}
		var tcPr strings.Builder
		if it.W > 1 {
			fmt.Fprintf(&tcPr, `<w:gridSpan w:val="%d"/>`, it.W)
		}
		switch {
		case it.Cell == nil:
			tcPr.WriteString(`<w:vMerge/>`)
		case it.Cell.rowSpan() > 1:
			tcPr.WriteString(`<w:vMerge w:val="restart"/>`)
		}
		b.WriteString(`<w:tc>`)
		if tcPr.Len() > 0 {
			b.WriteString(`<w:tcPr>` + tcPr.String() + `</w:tcPr>`)
		}
		wrote := false
		if it.Cell != nil && len(it.Cell.Blocks) > 0 {
			writeDocxBlocks(b, it.Cell.Blocks, reg)
			// a tc must END with a paragraph; append one if the cell's last
			// block is a nested table
			if _, isTbl := it.Cell.Blocks[len(it.Cell.Blocks)-1].(*Table); !isTbl {
				wrote = true
			}
		}
		if !wrote {
			b.WriteString(`<w:p/>`)
		}
		b.WriteString(`</w:tc>`)
	}
	b.WriteString(`</w:tr></w:tbl>`)
}

func writeDocxPara(b *bytes.Buffer, p *Para) {
	var pPr strings.Builder
	if p.Heading >= 1 && p.Heading <= 6 {
		fmt.Fprintf(&pPr, `<w:pStyle w:val="Heading%d"/>`, p.Heading)
	}
	switch p.Align {
	case AlignLeft:
		pPr.WriteString(`<w:jc w:val="left"/>`)
	case AlignCenter:
		pPr.WriteString(`<w:jc w:val="center"/>`)
	case AlignRight:
		pPr.WriteString(`<w:jc w:val="right"/>`)
	}
	if pPr.Len() == 0 && len(p.Runs) == 0 {
		b.WriteString(`<w:p/>`)
		return
	}
	b.WriteString(`<w:p>`)
	if pPr.Len() > 0 {
		b.WriteString(`<w:pPr>` + pPr.String() + `</w:pPr>`)
	}
	for _, r := range p.Runs {
		writeDocxRun(b, r)
	}
	b.WriteString(`</w:p>`)
}

func writeDocxRun(b *bytes.Buffer, r Run) {
	var rPr strings.Builder
	if r.Bold {
		rPr.WriteString(`<w:b/>`)
	}
	if r.Italic {
		rPr.WriteString(`<w:i/>`)
	}
	if r.Underline {
		rPr.WriteString(`<w:u w:val="single"/>`)
	}
	if r.Strike {
		rPr.WriteString(`<w:strike/>`)
	}
	if r.SizePt > 0 {
		half := int(r.SizePt*2 + 0.5)
		fmt.Fprintf(&rPr, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, half, half)
	}
	if r.Color != 0 {
		fmt.Fprintf(&rPr, `<w:color w:val="%06X"/>`, r.Color&0xFFFFFF)
	}
	b.WriteString(`<w:r>`)
	if rPr.Len() > 0 {
		b.WriteString(`<w:rPr>` + rPr.String() + `</w:rPr>`)
	}
	for i, seg := range strings.Split(r.Text, "\t") {
		if i > 0 {
			b.WriteString(`<w:tab/>`)
		}
		if seg != "" {
			b.WriteString(`<w:t xml:space="preserve">` + escapeXMLText(seg) + `</w:t>`)
		}
	}
	b.WriteString(`</w:r>`)
}

// xmlAttr returns the named attribute's value (any namespace), or "".
func xmlAttr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// xmlOn interprets a wordprocessingml on/off toggle element: present with no
// val (or a truthy val) means on; val 0/false/none/off means off.
func xmlOn(t xml.StartElement) bool {
	switch xmlAttr(t, "val") {
	case "0", "false", "none", "off":
		return false
	}
	return true
}

// parseDocx reads word/document.xml into the shared DocModel.
// ponytail: direct rPr/pPr formatting plus HeadingN pStyle only — styles.xml
// indirection, numbering, tables (stage 2) and images (stage 3) are not
// resolved; their text still comes through as plain paragraphs.
func parseDocx(data []byte) (*DocModel, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("유효한 docx 파일이 아닙니다")
	}
	if err := validateOfficeZIP(r, "docx"); err != nil {
		return nil, err
	}
	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, errors.New("유효한 docx 파일이 아닙니다")
	}
	docBytes, err := readOfficeEntry(docFile, "docx")
	if err != nil {
		return nil, err
	}

	doc := &DocModel{}
	dec := xml.NewDecoder(bytes.NewReader(docBytes))
	var cur *Para
	var curRun Run
	inRPr, inPPr := false, false
	textBytes := 0
	blocks, runs := 0, 0

	// docxTbl tracks one in-progress <w:tbl> while it is being parsed. Rows
	// are built as [][]*Cell (not [][]Cell) because `open` keeps pointers
	// into cells across rows for vMerge RowSpan++; storing *Cell into a
	// growing []Cell would dangle on reallocation. The pointer slice is
	// deref-copied into Table.Rows only once, at </w:tbl>, after all
	// RowSpan increments are done.
	type docxTbl struct {
		rows       [][]*Cell
		row        []*Cell
		cell       *Cell
		gridCol    int
		pendSpan   int
		pendVMerge string // "", "restart", "continue"
		inTcPr     bool
		open       map[int]*Cell // grid col -> vMerge restart cell
		nested     int           // depth of flattened inner tables
	}
	var tstack []*docxTbl

	// appendBlock routes a finished block to the innermost open table cell,
	// or to doc.Blocks when no table is open.
	appendBlock := func(blk Block) {
		blocks++
		if n := len(tstack); n > 0 && tstack[n-1].cell != nil {
			c := tstack[n-1].cell
			c.Blocks = append(c.Blocks, blk)
			return
		}
		doc.Blocks = append(doc.Blocks, blk)
	}

	flush := func() {
		if cur != nil {
			cur.Runs = mergeRuns(cur.Runs)
			appendBlock(cur)
			cur = nil
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("유효한 docx 파일이 아닙니다")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				flush()
				cur = &Para{}
			case "pPr":
				inPPr = true
			case "rPr":
				inRPr = true
			case "r":
				curRun = Run{}
			case "pStyle":
				if inPPr && cur != nil {
					if lvl, ok := strings.CutPrefix(xmlAttr(t, "val"), "Heading"); ok {
						if n, err := strconv.Atoi(lvl); err == nil && n >= 1 && n <= 6 {
							cur.Heading = n
						}
					}
				}
			case "jc":
				if inPPr && cur != nil {
					switch xmlAttr(t, "val") {
					case "left", "start":
						cur.Align = AlignLeft
					case "center":
						cur.Align = AlignCenter
					case "right", "end":
						cur.Align = AlignRight
					}
				}
			case "b":
				if inRPr && !inPPr {
					curRun.Bold = xmlOn(t)
				}
			case "i":
				if inRPr && !inPPr {
					curRun.Italic = xmlOn(t)
				}
			case "strike":
				if inRPr && !inPPr {
					curRun.Strike = xmlOn(t)
				}
			case "u":
				if inRPr && !inPPr {
					curRun.Underline = xmlAttr(t, "val") != "none"
				}
			case "sz":
				if inRPr && !inPPr {
					if n, err := strconv.Atoi(xmlAttr(t, "val")); err == nil && n > 0 {
						curRun.SizePt = float64(n) / 2
					}
				}
			case "color":
				if inRPr && !inPPr {
					if v := xmlAttr(t, "val"); v != "" && v != "auto" {
						if c, err := strconv.ParseUint(v, 16, 32); err == nil {
							curRun.Color = uint32(c) & 0xFFFFFF
						}
					}
				}
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
				textBytes += sb.Len()
				if textBytes > int(officeParseLimits.maxTextBytes) {
					return nil, errors.New("docx: extracted text too large")
				}
				if cur == nil {
					cur = &Para{}
				}
				nr := curRun
				nr.Text = sb.String()
				cur.Runs = append(cur.Runs, nr)
				runs++
			case "tab":
				if !inRPr && !inPPr && cur != nil {
					nr := curRun
					nr.Text = "\t"
					cur.Runs = append(cur.Runs, nr)
					runs++
				}
			case "br", "cr":
				if cur != nil {
					al, hd := cur.Align, cur.Heading
					flush()
					cur = &Para{Align: al, Heading: hd}
				}
			case "tbl":
				flush()
				if n := len(tstack); n > 0 {
					tstack[n-1].nested++ // ponytail: nested tables flatten into the enclosing cell
				} else {
					tstack = append(tstack, &docxTbl{open: map[int]*Cell{}})
				}
			case "tr":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					top := tstack[n-1]
					top.row = nil
					top.gridCol = 0
				}
			case "tc":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					flush()
					top := tstack[n-1]
					top.cell = &Cell{}
					top.pendSpan = 1
					top.pendVMerge = ""
				}
			case "tcPr":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					tstack[n-1].inTcPr = true
				}
			case "gridSpan":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 && tstack[n-1].inTcPr {
					v, _ := strconv.Atoi(xmlAttr(t, "val"))
					if v < 1 {
						v = 1
					} else if v > maxTableSpan {
						v = maxTableSpan
					}
					tstack[n-1].pendSpan = v
				}
			case "vMerge":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 && tstack[n-1].inTcPr {
					if v := xmlAttr(t, "val"); v == "restart" {
						tstack[n-1].pendVMerge = "restart"
					} else {
						tstack[n-1].pendVMerge = "continue"
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "pPr":
				inPPr = false
			case "rPr":
				inRPr = false
			case "r":
				curRun = Run{}
			case "p":
				flush()
			case "tcPr":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					tstack[n-1].inTcPr = false
				}
			case "tc":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					flush()
					top := tstack[n-1]
					// malformed nesting (e.g. <w:tc><w:tc>...</w:tc></w:tc>) makes
					// the outer close's top.cell already nil (cleared by the inner
					// close); treat it as a phantom close and leave gridCol/rows
					// untouched rather than deref a nil cell below.
					if top.cell == nil {
						break
					}
					w := top.pendSpan
					if w < 1 {
						w = 1
					} else if w > maxTableSpan {
						w = maxTableSpan
					}
					c := top.gridCol
					cell := top.cell
					top.cell = nil
					switch top.pendVMerge {
					case "continue":
						if rc := top.open[c]; rc != nil {
							rc.RowSpan++
						}
						// covered position: the cell content (an empty <w:p/>) is discarded
					case "restart":
						cell.ColSpan = w
						cell.RowSpan = 1
						top.row = append(top.row, cell)
						top.open[c] = cell
					default:
						cell.ColSpan = w
						top.row = append(top.row, cell)
						delete(top.open, c)
					}
					top.gridCol += w
				}
			case "tr":
				if n := len(tstack); n > 0 && tstack[n-1].nested == 0 {
					top := tstack[n-1]
					top.rows = append(top.rows, top.row)
					top.row = nil
				}
			case "tbl":
				if n := len(tstack); n > 0 {
					top := tstack[n-1]
					if top.nested > 0 {
						top.nested--
					} else {
						tstack = tstack[:n-1]
						tbl := &Table{}
						for _, pr := range top.rows {
							var nr []Cell
							for _, pc := range pr {
								nr = append(nr, *pc)
							}
							tbl.Rows = append(tbl.Rows, nr)
						}
						appendBlock(tbl)
					}
				}
			}
		}
		if blocks > maxModelBlocks || runs > maxModelRuns {
			return nil, errors.New("docx: document too complex")
		}
	}
	flush()
	return doc, nil
}
