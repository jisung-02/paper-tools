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
const docxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const docxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

const docxDocumentRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

// docxStylesXML defines Normal plus Heading1..6 (bold, sizes derived from
// headingSizePt at the 11pt body default, in half-points).
const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="35"/><w:szCs w:val="35"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="29"/><w:szCs w:val="29"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style></w:styles>`

// writeDocx serializes doc to a .docx package. Formatting written per run:
// bold/italic/underline/strike, size (half-points), color; per paragraph:
// alignment and HeadingN paragraph style.
func writeDocx(doc *DocModel) []byte {
	doc = normalizeDoc(doc)
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	writeZipEntry(w, "[Content_Types].xml", docxContentTypesXML)
	writeZipEntry(w, "_rels/.rels", docxRootRels)
	writeZipEntry(w, "word/_rels/document.xml.rels", docxDocumentRels)
	writeZipEntry(w, "word/styles.xml", docxStylesXML)

	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, blk := range doc.Blocks {
		if p, ok := blk.(*Para); ok {
			writeDocxPara(&b, p)
		}
	}
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`)
	writeZipEntry(w, "word/document.xml", b.String())

	// ponytail: ignore Close() error; bytes.Buffer-backed zip.Writer never fails on Close.
	w.Close()
	return buf.Bytes()
}

func writeZipEntry(w *zip.Writer, name, content string) {
	if f, err := w.Create(name); err == nil {
		// ponytail: Write to a bytes.Buffer-backed zip entry never fails; error ignored.
		f.Write([]byte(content))
	}
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
	flush := func() {
		if cur != nil {
			cur.Runs = mergeRuns(cur.Runs)
			doc.Blocks = append(doc.Blocks, cur)
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
			case "tab":
				if !inRPr && !inPPr && cur != nil {
					nr := curRun
					nr.Text = "\t"
					cur.Runs = append(cur.Runs, nr)
				}
			case "br", "cr":
				if cur != nil {
					al, hd := cur.Align, cur.Heading
					flush()
					cur = &Para{Align: al, Heading: hd}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "pPr":
				inPPr = false
			case "rPr":
				inRPr = false
			case "p":
				flush()
			}
		}
	}
	flush()
	return doc, nil
}
