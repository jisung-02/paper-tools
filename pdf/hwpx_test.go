package pdf

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHwpxText(t *testing.T) {
	hwpxData := buildTestHwpx()

	txt, err := HwpxText(hwpxData)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	if !strings.Contains(txt, "한글 문서입니다") {
		t.Errorf("extracted text missing expected Korean text; got: %q", txt)
	}
}

func TestHwpxToPDF(t *testing.T) {
	hwpxData := buildTestHwpx()
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}

	pdfBytes, err := HwpxToPDF(hwpxData, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("HwpxToPDF: %v", err)
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
	if !strings.Contains(text, "한글 문서입니다") {
		t.Errorf("extracted PDF text missing expected Korean text; got: %q", text)
	}
}

// buildTestHwpx creates a minimal in-memory .hwpx file for testing.
func buildTestHwpx() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Create section0.xml with minimal HWPML structure.
	xmlContent := `<hp:sec xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph">
<hp:p><hp:run><hp:t>한글 문서입니다</hp:t></hp:run></hp:p>
</hp:sec>`

	w, err := zw.Create("Contents/section0.xml")
	if err != nil {
		panic(err)
	}
	if _, err := w.Write([]byte(xmlContent)); err != nil {
		panic(err)
	}

	if err := zw.Close(); err != nil {
		panic(err)
	}

	return buf.Bytes()
}
