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

func TestParseDocxTooComplex(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for i := 0; i < maxModelBlocks+2; i++ {
		sb.WriteString(`<w:p/>`)
	}
	sb.WriteString(`</w:body></w:document>`)
	if _, err := parseDocx(makeTestDocx(t, sb.String())); err == nil || err.Error() != "docx: document too complex" {
		t.Fatalf("want too-complex error, got %v", err)
	}
}

func TestParseDocxStaleRunStyleReset(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>a</w:t></w:r><w:t>b</w:t></w:p>` +
		`</w:body></w:document>`
	d, err := parseDocx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	runs := d.Blocks[0].(*Para).Runs
	if len(runs) != 2 || !runs[0].Bold || runs[1].Bold {
		t.Fatalf("stale style leaked past </w:r>: %+v", runs)
	}
}

// assertDocEqual compares two stage-1 docs paragraph by paragraph.
func assertDocEqual(t *testing.T, got, want *DocModel) {
	t.Helper()
	if len(got.Blocks) != len(want.Blocks) {
		t.Fatalf("got %d blocks, want %d", len(got.Blocks), len(want.Blocks))
	}
	for i := range want.Blocks {
		switch wb := want.Blocks[i].(type) {
		case *Para:
			gp, ok := got.Blocks[i].(*Para)
			if !ok {
				t.Errorf("block %d: got %T want *Para", i, got.Blocks[i])
				continue
			}
			if gp.Align != wb.Align || gp.Heading != wb.Heading || len(gp.Runs) != len(wb.Runs) {
				t.Errorf("para %d: got %+v want %+v", i, gp, wb)
				continue
			}
			for j := range wb.Runs {
				if gp.Runs[j] != wb.Runs[j] {
					t.Errorf("para %d run %d: got %+v want %+v", i, j, gp.Runs[j], wb.Runs[j])
				}
			}
		case *Table:
			gt, ok := got.Blocks[i].(*Table)
			if !ok {
				t.Errorf("block %d: got %T want *Table", i, got.Blocks[i])
				continue
			}
			if len(gt.Rows) != len(wb.Rows) {
				t.Errorf("table %d: got %d rows want %d", i, len(gt.Rows), len(wb.Rows))
				continue
			}
			for r := range wb.Rows {
				if len(gt.Rows[r]) != len(wb.Rows[r]) {
					t.Errorf("table %d row %d: got %d cells want %d", i, r, len(gt.Rows[r]), len(wb.Rows[r]))
					continue
				}
				for ci := range wb.Rows[r] {
					gc, wc := gt.Rows[r][ci], wb.Rows[r][ci]
					if gc.colSpan() != wc.colSpan() || gc.rowSpan() != wc.rowSpan() {
						t.Errorf("table %d cell %d/%d spans: got %d,%d want %d,%d",
							i, r, ci, gc.colSpan(), gc.rowSpan(), wc.colSpan(), wc.rowSpan())
					}
					assertDocEqual(t, &DocModel{Blocks: gc.Blocks}, &DocModel{Blocks: wc.Blocks})
				}
			}
		}
	}
}

func TestWriteDocxTable(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "머리", Bold: true}}}}, ColSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "한"}}}}},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "둘"}}}}}},
		}},
	}}
	xmlDoc := docxEntry(t, writeDocx(doc), "word/document.xml")
	for _, want := range []string{
		`<w:tbl>`, `<w:tblBorders>`, `<w:gridCol/><w:gridCol/><w:gridCol/>`,
		`<w:gridSpan w:val="2"/>`, `<w:vMerge w:val="restart"/>`, `<w:vMerge/>`,
		`머리`, `세로`,
	} {
		if !strings.Contains(xmlDoc, want) {
			t.Errorf("document.xml missing %s", want)
		}
	}
	// every cell carries at least one paragraph; text still extractable
	txt, err := DocxText(writeDocx(doc))
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}
	for _, want := range []string{"머리", "세로", "한", "둘"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

func TestParseDocxTable(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>앞 문단</w:t></w:r></w:p>` +
		`<w:tbl><w:tblGrid><w:gridCol/><w:gridCol/><w:gridCol/></w:tblGrid>` +
		`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>머리</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>세로</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>한</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>둘</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr>` +
		`</w:tbl>` +
		`<w:p><w:r><w:t>뒤 문단</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	d, err := parseDocx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	if len(d.Blocks) != 3 {
		t.Fatalf("want [para, table, para], got %d blocks", len(d.Blocks))
	}
	tbl := d.Blocks[1].(*Table)
	if len(tbl.Rows) != 2 || len(tbl.Rows[0]) != 2 || len(tbl.Rows[1]) != 2 {
		t.Fatalf("rows/cells wrong: %+v", tbl.Rows)
	}
	if tbl.Rows[0][0].ColSpan != 2 {
		t.Errorf("colspan: %+v", tbl.Rows[0][0])
	}
	if tbl.Rows[0][1].RowSpan != 2 {
		t.Errorf("rowspan (restart+1 continue = 2): %+v", tbl.Rows[0][1])
	}
	head := tbl.Rows[0][0].Blocks[0].(*Para)
	if !head.Runs[0].Bold || head.Runs[0].Text != "머리" {
		t.Errorf("cell formatting lost: %+v", head.Runs)
	}
	if d.Blocks[2].(*Para).Runs[0].Text != "뒤 문단" {
		t.Errorf("paragraph after table lost")
	}
}

func TestDocxTableWriteParseRoundTrip(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "표 앞"}}},
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "굵게", Bold: true}}}}, ColSpan: 2, RowSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로칸"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "둘", Italic: true}}}}}},
		}},
		&Para{Runs: []Run{{Text: "표 뒤"}}},
	}}
	parsed, err := parseDocx(writeDocx(orig))
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	assertDocEqual(t, parsed, orig)
}

func TestParseDocxNestedTableDoesNotCorruptOuterSpans(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:tbl><w:tr>` +
		`<w:tc><w:p><w:r><w:t>바깥</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="3"/><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>안</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p/></w:tc>` +
		`<w:tc><w:p><w:r><w:t>이웃</w:t></w:r></w:p></w:tc>` +
		`</w:tr></w:tbl>` +
		`</w:body></w:document>`
	d, err := parseDocx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	tbl := d.Blocks[0].(*Table)
	if len(tbl.Rows) != 1 || len(tbl.Rows[0]) != 2 {
		t.Fatalf("outer table shape wrong: %+v", tbl.Rows)
	}
	if c := tbl.Rows[0][0]; c.colSpan() != 1 || c.rowSpan() != 1 {
		t.Errorf("outer cell spans corrupted by nested table's tcPr: %+v", c)
	}
}

func TestParseDocxMalformedNestedTcNoPanic(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:tbl><w:tr><w:tc><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tc></w:tr></w:tbl>` +
		`</w:body></w:document>`
	if _, err := parseDocx(makeTestDocx(t, docXML)); err != nil {
		t.Logf("clean error is acceptable: %v", err)
	}
}

func TestParseDocxHugeGridSpanClamped(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="1000000000"/></w:tcPr><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`</w:body></w:document>`
	d, err := parseDocx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	if c := d.Blocks[0].(*Table).Rows[0][0]; c.ColSpan > maxTableSpan {
		t.Fatalf("span not clamped: %d", c.ColSpan)
	}
	// the full conversion must stay small
	out, err := DocxToHwpx(makeTestDocx(t, docXML))
	if err != nil {
		t.Fatalf("DocxToHwpx: %v", err)
	}
	if len(out) > 1<<20 {
		t.Fatalf("amplified output: %d bytes", len(out))
	}
}
