package pdf

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func docxEntry(t *testing.T, docx []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatalf("entry %s not found", name)
	return ""
}

func TestWriteDocxFormatting(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목입니다"}}},
		&Para{Align: AlignCenter, Runs: []Run{
			{Text: "굵은", Bold: true},
			{Text: " 빨강 ", Color: 0xFF0000, SizePt: 14},
			{Text: "밑줄취소", Italic: true, Underline: true, Strike: true},
		}},
		&Para{},
		&Para{Runs: []Run{{Text: "탭\t뒤"}}},
	}}
	b := writeDocx(doc)
	xmlDoc := docxEntry(t, b, "word/document.xml")

	for _, want := range []string{
		`<w:pStyle w:val="Heading1"/>`,
		`<w:jc w:val="center"/>`,
		`<w:b/>`,
		`<w:i/>`,
		`<w:u w:val="single"/>`,
		`<w:strike/>`,
		`<w:sz w:val="28"/>`,
		`<w:color w:val="FF0000"/>`,
		`<w:tab/>`,
		`굵은`,
	} {
		if !strings.Contains(xmlDoc, want) {
			t.Errorf("document.xml missing %s", want)
		}
	}
	styles := docxEntry(t, b, "word/styles.xml")
	for _, want := range []string{
		`w:styleId="Heading1"`,
		`w:styleId="Heading6"`,
		`<w:sz w:val="44"/>`,
		`<w:sz w:val="35"/>`,
		`<w:sz w:val="29"/>`,
		`<w:sz w:val="24"/>`,
	} {
		if !strings.Contains(styles, want) {
			t.Errorf("styles.xml missing %s", want)
		}
	}
	// still extractable via the untouched text path
	txt, err := DocxText(b)
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}
	if !strings.Contains(txt, "제목입니다") || !strings.Contains(txt, "탭\t뒤") {
		t.Errorf("text round-trip broken: %q", txt)
	}
}

// makeTestDocx zips documentXML into a minimal docx package.
func makeTestDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	ct, _ := w.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))
	df, _ := w.Create("word/document.xml")
	df.Write([]byte(documentXML))
	w.Close()
	return buf.Bytes()
}

func TestParseDocxFormatting(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>제목</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:jc w:val="right"/><w:rPr><w:b/></w:rPr></w:pPr>` +
		`<w:r><w:rPr><w:b/><w:sz w:val="28"/><w:color w:val="FF0000"/></w:rPr><w:t>굵고빨강</w:t></w:r>` +
		`<w:r><w:rPr><w:i/><w:u w:val="single"/><w:strike/></w:rPr><w:t xml:space="preserve"> 뒤</w:t></w:r>` +
		`<w:r><w:rPr><w:b w:val="false"/><w:u w:val="none"/></w:rPr><w:t>평문</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>한 줄</w:t><w:br/><w:t>두 줄</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	d, err := parseDocx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	if len(d.Blocks) != 4 {
		t.Fatalf("got %d blocks, want 4 (br splits the last para): %+v", len(d.Blocks), d.Blocks)
	}
	p0 := d.Blocks[0].(*Para)
	if p0.Heading != 2 || p0.Runs[0].Text != "제목" {
		t.Errorf("p0: %+v", p0)
	}
	p1 := d.Blocks[1].(*Para)
	if p1.Align != AlignRight {
		t.Errorf("p1 align: %v", p1.Align)
	}
	if len(p1.Runs) != 3 {
		t.Fatalf("p1 runs: %+v", p1.Runs)
	}
	if r := p1.Runs[0]; !r.Bold || r.SizePt != 14 || r.Color != 0xFF0000 || r.Text != "굵고빨강" {
		t.Errorf("p1.r0: %+v", r)
	}
	if r := p1.Runs[1]; !r.Italic || !r.Underline || !r.Strike || r.Text != " 뒤" {
		t.Errorf("p1.r1: %+v", r)
	}
	if r := p1.Runs[2]; r.Bold || r.Underline || r.Text != "평문" {
		t.Errorf("p1.r2 (explicit off values): %+v", r)
	}
	if d.Blocks[2].(*Para).Runs[0].Text != "한 줄" || d.Blocks[3].(*Para).Runs[0].Text != "두 줄" {
		t.Errorf("br split wrong: %+v %+v", d.Blocks[2], d.Blocks[3])
	}
}

func TestDocxWriteParseRoundTrip(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목 하나"}}},
		&Para{Align: AlignCenter, Runs: []Run{
			{Text: "굵은글씨", Bold: true},
			{Text: "보통 "},
			{Text: "색상", Color: 0x0000FF, SizePt: 9, Italic: true, Underline: true, Strike: true},
		}},
		&Para{},
		&Para{Runs: []Run{{Text: "마지막 문단"}}},
	}}
	parsed, err := parseDocx(writeDocx(orig))
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	assertDocEqual(t, parsed, orig)
}

func TestDocxToPDFPreservesBold(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "굵은 텍스트", Bold: true}}},
	}}
	pdfBytes, err := DocxToPDF(writeDocx(doc), testFont(t), TextPDFOpts{})
	if err != nil {
		t.Fatalf("DocxToPDF: %v", err)
	}
	if !bytes.Contains(pdfBytes, []byte("2 Tr")) {
		t.Error("bold run did not reach the PDF renderer")
	}
}

// assertDocEqual compares two stage-1 docs paragraph by paragraph.
func assertDocEqual(t *testing.T, got, want *DocModel) {
	t.Helper()
	if len(got.Blocks) != len(want.Blocks) {
		t.Fatalf("got %d blocks, want %d", len(got.Blocks), len(want.Blocks))
	}
	for i := range want.Blocks {
		gp, wp := got.Blocks[i].(*Para), want.Blocks[i].(*Para)
		if gp.Align != wp.Align || gp.Heading != wp.Heading || len(gp.Runs) != len(wp.Runs) {
			t.Errorf("para %d: got %+v want %+v", i, gp, wp)
			continue
		}
		for j := range wp.Runs {
			if gp.Runs[j] != wp.Runs[j] {
				t.Errorf("para %d run %d: got %+v want %+v", i, j, gp.Runs[j], wp.Runs[j])
			}
		}
	}
}
