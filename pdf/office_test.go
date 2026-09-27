package pdf

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBuildDocxRoundTrip(t *testing.T) {
	paras := []string{"첫째 문단 한글", "Second English 12345", ""}
	docxBytes := writeDocx(docFromParas(paras))

	// Verify text round-trip
	text, err := DocxText(docxBytes)
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}
	if !strings.Contains(text, "첫째 문단 한글") {
		t.Errorf("text missing Korean paragraph; got: %q", text)
	}
	if !strings.Contains(text, "Second English 12345") {
		t.Errorf("text missing English paragraph; got: %q", text)
	}

	// Verify zip structure
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	requiredFiles := map[string]bool{
		"[Content_Types].xml": false,
		"_rels/.rels":         false,
		"word/document.xml":   false,
		"word/styles.xml":     false,
	}
	for _, f := range zr.File {
		if _, ok := requiredFiles[f.Name]; ok {
			requiredFiles[f.Name] = true
		}
	}
	for name, found := range requiredFiles {
		if !found {
			t.Errorf("zip missing required entry: %s", name)
		}
	}
}

func TestBuildHwpxRoundTrip(t *testing.T) {
	paras := []string{"첫째 문단 한글", "Second English 12345", ""}
	hwpxBytes := writeHwpx(docFromParas(paras))

	// Verify text round-trip
	text, err := HwpxText(hwpxBytes)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	if !strings.Contains(text, "첫째 문단 한글") {
		t.Errorf("text missing Korean paragraph; got: %q", text)
	}
	if !strings.Contains(text, "Second English 12345") {
		t.Errorf("text missing English paragraph; got: %q", text)
	}

	// Verify zip structure and mimetype
	zr, err := zip.NewReader(bytes.NewReader(hwpxBytes), int64(len(hwpxBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	requiredFiles := map[string]bool{
		"mimetype":              false,
		"Contents/header.xml":   false,
		"Contents/section0.xml": false,
	}
	var mimetypeFile *zip.File
	for _, f := range zr.File {
		if _, ok := requiredFiles[f.Name]; ok {
			requiredFiles[f.Name] = true
		}
		if f.Name == "mimetype" {
			mimetypeFile = f
		}
	}
	for name, found := range requiredFiles {
		if !found {
			t.Errorf("zip missing required entry: %s", name)
		}
	}

	// Verify mimetype is stored (uncompressed) and has correct content
	if mimetypeFile == nil {
		t.Fatal("mimetype file not found")
	}
	if mimetypeFile.Method != zip.Store {
		t.Errorf("mimetype Method should be Store, got %d", mimetypeFile.Method)
	}
	rc, err := mimetypeFile.Open()
	if err != nil {
		t.Fatalf("open mimetype: %v", err)
	}
	mimeContent, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read mimetype: %v", err)
	}
	if string(mimeContent) != "application/hwp+zip" {
		t.Errorf("mimetype content should be 'application/hwp+zip', got %q", string(mimeContent))
	}
}

func TestDocxToHwpx(t *testing.T) {
	// Build minimal docx in memory
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	ctFile, _ := w.Create("[Content_Types].xml")
	ctFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))

	docFile, _ := w.Create("word/document.xml")
	docFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>테스트 문서</w:t></w:r></w:p><w:p><w:r><w:t>Second Para</w:t></w:r></w:p></w:body></w:document>`))

	w.Close()
	docxBytes := buf.Bytes()

	// Convert to hwpx
	hwpxBytes, err := DocxToHwpx(docxBytes)
	if err != nil {
		t.Fatalf("DocxToHwpx: %v", err)
	}

	// Verify round-trip
	text, err := HwpxText(hwpxBytes)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	if !strings.Contains(text, "테스트 문서") {
		t.Errorf("text missing expected content; got: %q", text)
	}
	if !strings.Contains(text, "Second Para") {
		t.Errorf("text missing second paragraph; got: %q", text)
	}
}

func TestHwpxToDocx(t *testing.T) {
	// Build minimal hwpx in memory
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// Add required files
	mimeHeader := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	mimeFile, _ := w.CreateHeader(mimeHeader)
	mimeFile.Write([]byte("application/hwp+zip"))

	headerFile, _ := w.Create("Contents/header.xml")
	headerFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hh:head xmlns:hh="http://www.hancom.co.kr/hwpml/2011/head" version="1.4" secCnt="1"><hh:refList></hh:refList></hh:head>`))

	sectionFile, _ := w.Create("Contents/section0.xml")
	sectionFile.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><hs:sec xmlns:hs="http://www.hancom.co.kr/hwpml/2011/section" xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph"><hp:p><hp:run><hp:t>첫 문단</hp:t></hp:run></hp:p><hp:p><hp:run><hp:t>First Para</hp:t></hp:run></hp:p></hs:sec>`))

	w.Close()
	hwpxBytes := buf.Bytes()

	// Convert to docx
	docxBytes, err := HwpxToDocx(hwpxBytes)
	if err != nil {
		t.Fatalf("HwpxToDocx: %v", err)
	}

	// Verify round-trip
	text, err := DocxText(docxBytes)
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}
	if !strings.Contains(text, "첫 문단") {
		t.Errorf("text missing expected content; got: %q", text)
	}
	if !strings.Contains(text, "First Para") {
		t.Errorf("text missing English paragraph; got: %q", text)
	}
}

func TestDocxHwpxCrossRoundTripFormatting(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Para{Align: AlignCenter, Runs: []Run{
			{Text: "굵은", Bold: true},
			{Text: " 빨강 14pt", Color: 0xFF0000, SizePt: 14},
			{Text: " 꾸밈", Italic: true, Underline: true, Strike: true},
		}},
		&Para{Runs: []Run{{Text: "평범한 문단"}}},
	}}
	hwpx, err := DocxToHwpx(writeDocx(orig))
	if err != nil {
		t.Fatalf("DocxToHwpx: %v", err)
	}
	docx, err := HwpxToDocx(hwpx)
	if err != nil {
		t.Fatalf("HwpxToDocx: %v", err)
	}
	final, err := parseDocx(docx)
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	assertDocEqual(t, final, orig)
}

func TestDocxHwpxCrossRoundTripImage(t *testing.T) {
	img := &Image{MIME: "image/png", Data: tinyPNG(t, 16, 16), WPt: 90, HPt: 90}
	orig := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "이미지 문서", Bold: true}}},
		img,
	}}
	hwpx, err := DocxToHwpx(writeDocx(orig))
	if err != nil {
		t.Fatalf("DocxToHwpx: %v", err)
	}
	docx, err := HwpxToDocx(hwpx)
	if err != nil {
		t.Fatalf("HwpxToDocx: %v", err)
	}
	final, err := parseDocx(docx)
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	assertDocEqual(t, final, orig)
}

func TestDocxHwpxCrossRoundTripTable(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "머리", Bold: true}}}}, ColSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "한"}}}}},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "둘", Italic: true}}}}}},
		}},
	}}
	hwpx, err := DocxToHwpx(writeDocx(orig))
	if err != nil {
		t.Fatalf("DocxToHwpx: %v", err)
	}
	docx, err := HwpxToDocx(hwpx)
	if err != nil {
		t.Fatalf("HwpxToDocx: %v", err)
	}
	final, err := parseDocx(docx)
	if err != nil {
		t.Fatalf("parseDocx: %v", err)
	}
	assertDocEqual(t, final, orig)
}
