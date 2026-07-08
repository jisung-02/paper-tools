package pdf

import (
	"os"
	"strings"
	"testing"
)

func TestPdfToDocxRoundTrip(t *testing.T) {
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}

	text := "한글 문단 테스트\n\nSecond paragraph in English."
	pdfBytes, err := TextToPDF(text, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("TextToPDF: %v", err)
	}

	docxBytes, err := PdfToDocx(pdfBytes)
	if err != nil {
		t.Fatalf("PdfToDocx: %v", err)
	}

	got, err := DocxText(docxBytes)
	if err != nil {
		t.Fatalf("DocxText: %v", err)
	}
	if !strings.Contains(got, "한글 문단 테스트") {
		t.Errorf("docx text missing Korean paragraph; got: %q", got)
	}
	if !strings.Contains(got, "Second paragraph in English.") {
		t.Errorf("docx text missing English paragraph; got: %q", got)
	}
}

func TestPdfToHwpxRoundTrip(t *testing.T) {
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}

	text := "한글 문단 테스트\n\nSecond paragraph in English."
	pdfBytes, err := TextToPDF(text, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("TextToPDF: %v", err)
	}

	hwpxBytes, err := PdfToHwpx(pdfBytes)
	if err != nil {
		t.Fatalf("PdfToHwpx: %v", err)
	}

	got, err := HwpxText(hwpxBytes)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	if !strings.Contains(got, "한글 문단 테스트") {
		t.Errorf("hwpx text missing Korean paragraph; got: %q", got)
	}
	if !strings.Contains(got, "Second paragraph in English.") {
		t.Errorf("hwpx text missing English paragraph; got: %q", got)
	}
}

// splitTextParagraphs is exercised directly here because ExtractText's own
// newline behavior (single "\n" per content-stream line, "\n\n" between
// pages) is what determines paragraph boundaries in PdfToDocx/PdfToHwpx.
func TestSplitTextParagraphs(t *testing.T) {
	content := `BT /F1 12 Tf 72 750 Td
(First paragraph line one.) Tj
0 -14 Td
(First paragraph line two.) Tj
0 -28 Td
(Second paragraph.) Tj
ET`
	pdfBytes := textPDF(content, "")
	text, err := ExtractText(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}

	paras := splitTextParagraphs(text)
	if len(paras) != 1 {
		t.Fatalf("expected 1 paragraph (no blank line in source), got %d: %q", len(paras), paras)
	}
	if !strings.Contains(paras[0], "First paragraph line one.") || !strings.Contains(paras[0], "Second paragraph.") {
		t.Errorf("paragraph missing expected text; got: %q", paras[0])
	}

	// Blank-line-separated input does split into distinct paragraphs.
	multi := splitTextParagraphs("Paragraph one line a\nline b\n\nParagraph two\n\n\nParagraph three")
	if len(multi) != 3 {
		t.Fatalf("expected 3 paragraphs, got %d: %q", len(multi), multi)
	}
	if multi[0] != "Paragraph one line a line b" {
		t.Errorf("paragraph 1 = %q, want collapsed single-line text", multi[0])
	}
	if multi[1] != "Paragraph two" {
		t.Errorf("paragraph 2 = %q", multi[1])
	}
	if multi[2] != "Paragraph three" {
		t.Errorf("paragraph 3 = %q", multi[2])
	}
}

func TestPdfToDocxEmptyText(t *testing.T) {
	// A structurally valid PDF with no text content should produce the
	// "no text" error, not a zero-paragraph document.
	pdfBytes := textPDF("", "")
	if _, err := PdfToDocx(pdfBytes); err == nil {
		t.Fatal("expected error for PDF with no extractable text")
	}
	if _, err := PdfToHwpx(pdfBytes); err == nil {
		t.Fatal("expected error for PDF with no extractable text")
	}
}

func TestPdfToDocxGarbageInput(t *testing.T) {
	garbage := []byte("this is not a pdf file at all")
	if _, err := PdfToDocx(garbage); err == nil {
		t.Fatal("expected error for garbage input")
	}
	if _, err := PdfToHwpx(garbage); err == nil {
		t.Fatal("expected error for garbage input")
	}
}

// TestEscapeXMLTextStripsIllegalControlChars locks in the fix stripping XML
// 1.0-illegal control characters (e.g. raw 0x01 from malformed PDF fonts)
// before escaping, while keeping legal whitespace and non-ASCII text intact.
func TestEscapeXMLTextStripsIllegalControlChars(t *testing.T) {
	input := "\x00A\x01B\x0Bok\ttab\nline 한글 & more"
	got := escapeXMLText(input)

	for _, illegal := range []string{"\x00", "\x01", "\x0B"} {
		if strings.Contains(got, illegal) {
			t.Errorf("escapeXMLText(%q) = %q; still contains illegal char %q", input, got, illegal)
		}
	}
	// Tab/newline are legal XML chars but xml.EscapeText renders them as
	// numeric entities rather than passing them through literally.
	for _, legal := range []string{"&#x9;", "&#xA;", "한글"} {
		if !strings.Contains(got, legal) {
			t.Errorf("escapeXMLText(%q) = %q; missing legal content %q", input, got, legal)
		}
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("escapeXMLText(%q) = %q; want %q escaped to \"&amp;\"", input, got, "&")
	}
}
