package pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"strings"
)

// ponytail: text + paragraph breaks only; no formatting/bold/images/tables (best-effort reflow, not a faithful converter).

// splitParas splits extracted text on newlines, trimming ONE trailing empty paragraph
// if the text ends with "\n" (which split produces a trailing "" element).
// Keeps any other internal empty paragraphs (blank lines) as-is.
// Returns at least one paragraph (empty string if input is empty).
func splitParas(text string) []string {
	paras := strings.Split(text, "\n")
	// If the split has at least one element and the last is empty (text ended with "\n"),
	// drop that one trailing empty element only.
	if len(paras) > 0 && paras[len(paras)-1] == "" {
		paras = paras[:len(paras)-1]
	}
	// Ensure we always return at least one paragraph.
	if len(paras) == 0 {
		paras = []string{""}
	}
	return paras
}

// escapeXMLText safely escapes a string for use in XML text content.
// xml.EscapeText only escapes & < > ' " — it does not remove control
// characters that are outright illegal in XML 1.0 (e.g. PDF text extraction
// can surface raw 0x01 bytes from malformed fonts), which would otherwise
// produce a document.xml/section0.xml that strict parsers (Word, Hancom)
// reject. Strip those first; the common clean-text path allocates nothing extra.
func escapeXMLText(s string) string {
	if strings.IndexFunc(s, isIllegalXMLRune) != -1 {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			if !isIllegalXMLRune(r) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// isIllegalXMLRune reports whether r is not a valid XML 1.0 character.
// Legal: U+0009, U+000A, U+000D, U+0020-U+D7FF, U+E000-U+FFFD, U+10000-U+10FFFF.
func isIllegalXMLRune(r rune) bool {
	switch {
	case r == 0x09 || r == 0x0A || r == 0x0D:
		return false
	case r >= 0x20 && r <= 0xD7FF:
		return false
	case r >= 0xE000 && r <= 0xFFFD:
		return false
	case r >= 0x10000 && r <= 0x10FFFF:
		return false
	default:
		return true
	}
}

// buildDocx builds a minimal valid .docx (zip) in memory from a list of paragraphs.
func buildDocx(paras []string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// [Content_Types].xml
	if ctFile, err := w.Create("[Content_Types].xml"); err == nil {
		ctFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))
	}

	// _rels/.rels
	if relsFile, err := w.Create("_rels/.rels"); err == nil {
		relsFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`))
	}

	// word/document.xml with paragraph body
	if docFile, err := w.Create("word/document.xml"); err == nil {
		var bodyBuf bytes.Buffer
		bodyBuf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
		for _, p := range paras {
			if p == "" {
				bodyBuf.WriteString(`<w:p/>`)
			} else {
				bodyBuf.WriteString(`<w:p><w:r><w:t xml:space="preserve">`)
				bodyBuf.WriteString(escapeXMLText(p))
				bodyBuf.WriteString(`</w:t></w:r></w:p>`)
			}
		}
		bodyBuf.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`)
		docFile.Write(bodyBuf.Bytes())
	}

	// ponytail: ignore Close() error; bytes.Buffer-backed zip.Writer never fails on Close.
	w.Close()

	return buf.Bytes()
}

// buildHwpx builds a minimal HWPX (OWPML) package in memory from a list of paragraphs.
// ponytail: minimal single-style OWPML; real Hancom documents carry far more head metadata (bin data, tab defs, numbering, bullet, border fills). This is text-only and UNVALIDATED against real Hancom Office.
func buildHwpx(paras []string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// mimetype MUST be stored as the FIRST entry with Store method (uncompressed).
	mimeHeader := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	if mimeFile, err := w.CreateHeader(mimeHeader); err == nil {
		mimeFile.Write([]byte("application/hwp+zip"))
	}

	// version.xml
	if vFile, err := w.Create("version.xml"); err == nil {
		vFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hv:HCFVersion xmlns:hv="http://www.hancom.co.kr/hwpml/2011/version" tagetApplication="WORDPROCESSOR" major="5" minor="0" micro="5" buildNumber="0" os="1" xmlVersion="1.4" application="종이도구" appVersion="1.0"/>`))
	}

	// META-INF/container.xml
	if cFile, err := w.Create("META-INF/container.xml"); err == nil {
		cFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><ocf:container xmlns:ocf="urn:oasis:names:tc:opendocument:xmlns:container"><ocf:rootfiles><ocf:rootfile full-path="Contents/content.hpf" media-type="application/hwpml-package+xml"/></ocf:rootfiles></ocf:container>`))
	}

	// META-INF/manifest.xml
	if mFile, err := w.Create("META-INF/manifest.xml"); err == nil {
		mFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><odf:manifest xmlns:odf="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" version="1.2"><odf:file-entry odf:full-path="/" odf:media-type="application/hwp+zip"/><odf:file-entry odf:full-path="Contents/content.hpf" odf:media-type="application/hwpml-package+xml"/><odf:file-entry odf:full-path="Contents/header.xml" odf:media-type="application/xml"/><odf:file-entry odf:full-path="Contents/section0.xml" odf:media-type="application/xml"/></odf:manifest>`))
	}

	// Contents/content.hpf
	if hpfFile, err := w.Create("Contents/content.hpf"); err == nil {
		hpfFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hpf:HWPML xmlns:hpf="http://www.hancom.co.kr/schema/2011/hpf" xmlns:opf="http://www.idpf.org/2007/opf/" version="1.4"><opf:package version="1.4" unique-identifier="종이도구"><opf:metadata><opf:title/></opf:metadata><opf:manifest><opf:item id="header" href="Contents/header.xml" media-type="application/xml"/><opf:item id="section0" href="Contents/section0.xml" media-type="application/xml"/></opf:manifest><opf:spine><opf:itemref idref="header" linear="yes"/><opf:itemref idref="section0" linear="yes"/></opf:spine></opf:package></hpf:HWPML>`))
	}

	// Contents/header.xml
	if hFile, err := w.Create("Contents/header.xml"); err == nil {
		hFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hh:head xmlns:hh="http://www.hancom.co.kr/hwpml/2011/head" xmlns:hc="http://www.hancom.co.kr/hwpml/2011/core" version="1.4" secCnt="1">
<hh:refList>
<hh:fontfaces itemCnt="1"><hh:fontface lang="HANGUL" fontCnt="1"><hh:font id="0" face="함초롬바탕" type="TTF" isEmbedded="0"><hh:typeInfo familyType="FCF_UNKNOWN" weight="0" proportion="0" contrast="0" strokeVariation="0" armStyle="0" letterform="0" midline="0" xHeight="0"/></hh:font></hh:fontface></hh:fontfaces>
<hh:charProperties itemCnt="1"><hh:charPr id="0" height="1000" textColor="#000000" shadeColor="none" useFontSpace="0" useKerning="0" symMark="NONE" borderFillIDRef="0"><hh:fontRef hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/><hh:ratio hangul="100" latin="100" hanja="100" japanese="100" other="100" symbol="100" user="100"/><hh:spacing hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/><hh:relSz hangul="100" latin="100" hanja="100" japanese="100" other="100" symbol="100" user="100"/><hh:offset hangul="0" latin="0" hanja="0" japanese="0" other="0" symbol="0" user="0"/></hh:charPr></hh:charProperties>
<hh:paraProperties itemCnt="1"><hh:paraPr id="0" tabPrIDRef="0" condense="0" fontLineHeight="0" snapToGrid="1" suppressLineNumbers="0" checked="0"><hh:align horizontal="JUSTIFY" vertical="BASELINE"/><hh:heading type="NONE" idRef="0" level="0"/><hh:breakSetting breakLatinWord="KEEP_WORD" breakNonLatinWord="KEEP_WORD" widowOrphan="0" keepWithNext="0" keepLines="0" pageBreakBefore="0" lineWrap="BREAK"/><hh:margin><hc:intent value="0" unit="HWPUNIT"/><hc:left value="0" unit="HWPUNIT"/><hc:right value="0" unit="HWPUNIT"/><hc:prev value="0" unit="HWPUNIT"/><hc:next value="0" unit="HWPUNIT"/></hh:margin><hh:lineSpacing type="PERCENT" value="160" unit="HWPUNIT"/></hh:paraPr></hh:paraProperties>
<hh:styles itemCnt="1"><hh:style id="0" type="PARA" name="바탕글" engName="Normal" paraPrIDRef="0" charPrIDRef="0" nextStyleIDRef="0" langID="1042" lockForm="0"/></hh:styles>
</hh:refList></hh:head>`))
	}

	// Contents/section0.xml with paragraph body
	if sFile, err := w.Create("Contents/section0.xml"); err == nil {
		var secBuf bytes.Buffer
		secBuf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hs:sec xmlns:hs="http://www.hancom.co.kr/hwpml/2011/section" xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph">`)
		for _, p := range paras {
			if p == "" {
				secBuf.WriteString(`<hp:p paraPrIDRef="0" styleIDRef="0"><hp:run charPrIDRef="0"><hp:t></hp:t></hp:run></hp:p>`)
			} else {
				secBuf.WriteString(`<hp:p paraPrIDRef="0" styleIDRef="0"><hp:run charPrIDRef="0"><hp:t>`)
				secBuf.WriteString(escapeXMLText(p))
				secBuf.WriteString(`</hp:t></hp:run></hp:p>`)
			}
		}
		secBuf.WriteString(`</hs:sec>`)
		sFile.Write(secBuf.Bytes())
	}

	// ponytail: ignore Close() error; bytes.Buffer-backed zip.Writer cannot fail on Close.
	w.Close()

	return buf.Bytes()
}

// DocxToHwpx converts a .docx file to .hwpx by extracting text and rebuilding.
func DocxToHwpx(data []byte) ([]byte, error) {
	t, err := DocxText(data)
	if err != nil {
		return nil, err
	}
	return buildHwpx(splitParas(t)), nil
}

// HwpxToDocx converts a .hwpx file to .docx by extracting text and rebuilding.
func HwpxToDocx(data []byte) ([]byte, error) {
	t, err := HwpxText(data)
	if err != nil {
		return nil, err
	}
	return buildDocx(splitParas(t)), nil
}
