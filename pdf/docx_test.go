package pdf

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDocxText(t *testing.T) {
	// Build a minimal valid .docx in memory
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// Write [Content_Types].xml
	ctFile, err := w.Create("[Content_Types].xml")
	if err != nil {
		t.Fatalf("create [Content_Types].xml: %v", err)
	}
	ctContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	if _, err := ctFile.Write([]byte(ctContent)); err != nil {
		t.Fatalf("write [Content_Types].xml: %v", err)
	}

	// Write word/document.xml
	docFile, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create word/document.xml: %v", err)
	}
	docContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>안녕하세요 문서 변환</w:t></w:r></w:p><w:p><w:r><w:t>Hello Document Conversion</w:t></w:r></w:p></w:body></w:document>`
	if _, err := docFile.Write([]byte(docContent)); err != nil {
		t.Fatalf("write word/document.xml: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	docxBytes := buf.Bytes()

	// Test DocxText
	text, err := DocxText(docxBytes)
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}

	// Verify text contains both Korean and Latin paragraphs
	if !strings.Contains(text, "안녕하세요 문서 변환") {
		t.Errorf("DocxText missing Korean text; got: %q", text)
	}
	if !strings.Contains(text, "Hello Document Conversion") {
		t.Errorf("DocxText missing Latin text; got: %q", text)
	}
	if !strings.Contains(text, "\n") {
		t.Errorf("DocxText missing paragraph break; got: %q", text)
	}
}

func TestDocxToPDF(t *testing.T) {
	// Build a minimal valid .docx in memory
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// Write [Content_Types].xml
	ctFile, err := w.Create("[Content_Types].xml")
	if err != nil {
		t.Fatalf("create [Content_Types].xml: %v", err)
	}
	ctContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	if _, err := ctFile.Write([]byte(ctContent)); err != nil {
		t.Fatalf("write [Content_Types].xml: %v", err)
	}

	// Write word/document.xml
	docFile, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create word/document.xml: %v", err)
	}
	docContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>안녕하세요 문서 변환</w:t></w:r></w:p><w:p><w:r><w:t>Hello Document Conversion</w:t></w:r></w:p></w:body></w:document>`
	if _, err := docFile.Write([]byte(docContent)); err != nil {
		t.Fatalf("write word/document.xml: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	docxBytes := buf.Bytes()

	// Load the font
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}

	// Test DocxToPDF
	pdfBytes, err := DocxToPDF(docxBytes, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("DocxToPDF: %v", err)
	}

	// Verify the PDF starts with %PDF
	if !strings.HasPrefix(string(pdfBytes), "%PDF") {
		t.Fatalf("output does not start with %%PDF")
	}

	// Parse the PDF to confirm it is structurally valid.
	if _, err := Parse(pdfBytes); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Extract text and verify round-trip.
	text, err := ExtractText(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}

	// Verify the extracted text contains the Korean paragraph
	if !strings.Contains(text, "안녕하세요 문서 변환") {
		t.Errorf("extracted text missing Korean text; got: %q", text)
	}
}

// FuzzDocxToPDF exercises DOCX parsing/conversion with arbitrary bytes; the
// only failure mode under test is a panic (errors are expected and
// ignored). The font is fixed to the app's real bundled font, matching
// wasm/docx2pdf, which only lets the file bytes vary.
func FuzzDocxToPDF(f *testing.F) {
	font := testFont(f)
	f.Add([]byte(""))
	f.Add([]byte("PK\x03\x04"))
	f.Add(writeDocx(docFromParas([]string{"첫째 문단 한글", "Second English 12345", ""})))
	f.Fuzz(func(t *testing.T, data []byte) {
		DocxToPDF(data, font, TextPDFOpts{})
	})
}
