package pdf

import (
	"archive/zip"
	"bytes"
	"fmt"
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
