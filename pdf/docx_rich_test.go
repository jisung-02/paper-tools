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
	if !strings.Contains(styles, `w:styleId="Heading1"`) {
		t.Errorf("styles.xml missing Heading1 style")
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
