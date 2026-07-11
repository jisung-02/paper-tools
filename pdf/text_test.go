package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// textPDF builds a minimal single-page PDF with a classic xref table: a
// catalog, a Pages node, one Page with a content stream and a simple
// Type1/WinAnsiEncoding font resource, following the classicPDF() pattern in
// pdf_test.go. If tounicode is non-empty, object 5's font dict gets a
// /ToUnicode stream (object 6) built from it.
func textPDF(content string, tounicode string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	nObjs := 6
	offsets := make([]int, nObjs+1)

	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeStreamObj := func(num int, dictExtra string, data []byte) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, dictExtra, len(data))
		buf.Write(data)
		buf.WriteString("\nendstream\nendobj\n")
	}

	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] >>")
	fontExtra := ""
	if tounicode != "" {
		fontExtra = " /ToUnicode 6 0 R"
	}
	writeObjRaw(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	writeStreamObj(4, "", []byte(content))
	writeObjRaw(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding"+fontExtra+" >>")
	if tounicode != "" {
		writeStreamObj(6, "", []byte(tounicode))
	}

	xrefOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", nObjs+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= nObjs; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n", nObjs+1)
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func TestExtractText(t *testing.T) {
	content := "BT /F1 24 Tf 72 700 Td (Hello World) Tj ET"
	pdf := textPDF(content, "")

	got, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(got, "Hello World") {
		t.Fatalf("ExtractText output %q does not contain %q", got, "Hello World")
	}
}

func TestExtractTextToUnicodeCMap(t *testing.T) {
	content := "BT /F1 24 Tf 72 700 Td (\x41\x42) Tj ET"
	cmap := `/CIDInit /ProcSet findresource begin
1 begincodespacerange
<00> <FF>
endcodespacerange
1 beginbfchar
<41> <0391>
endbfchar
1 beginbfrange
<42> <42> <0392>
endbfrange
end`
	pdf := textPDF(content, cmap)

	got, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(got, "ΑΒ") {
		t.Fatalf("ExtractText output %q does not contain %q", got, "ΑΒ")
	}
}

func TestExtractTextEncrypted(t *testing.T) {
	if _, err := ExtractText(encryptedPDF()); err != ErrEncrypted {
		t.Fatalf("expected ErrEncrypted, got %v", err)
	}
}

func TestExtractTextRejectsOversizedToUnicodeRange(t *testing.T) {
	_, err := ExtractText(textPDF("BT /F1 12 Tf (A) Tj ET", `
1 beginbfrange
<FFFFFFFE> <FFFFFFFF> <0041>
endbfrange`))
	if !errors.Is(err, ErrToUnicodeCMapCode) {
		t.Fatalf("ExtractText error = %v, want ErrToUnicodeCMapCode", err)
	}
}

func TestExtractTextRejectsToUnicodeMappingBudget(t *testing.T) {
	var cmap strings.Builder
	cmap.WriteString("70000 beginbfchar\n")
	for i := 0; i < 70000; i++ {
		cmap.WriteString("<41> <0041>\n")
	}
	cmap.WriteString("endbfchar\n")

	_, err := ExtractText(textPDF("BT /F1 12 Tf (A) Tj ET", cmap.String()))
	if err == nil {
		t.Fatal("ExtractText accepted a ToUnicode CMap over the mapping budget")
	}
}

func TestParseToUnicodeCMapHonorsCodeWidth(t *testing.T) {
	_, err := parseToUnicodeCMap([]byte(`
1 beginbfrange
<0100> <0101> <0041>
endbfrange`), 1)
	if !errors.Is(err, ErrToUnicodeCMapCode) {
		t.Fatalf("parseToUnicodeCMap error = %v, want ErrToUnicodeCMapCode", err)
	}

	m, err := parseToUnicodeCMap([]byte(`
1 beginbfrange
<0100> <0101> <0041>
endbfrange`), 2)
	if err != nil {
		t.Fatalf("parseToUnicodeCMap two-byte code: %v", err)
	}
	if m[0x100] != "A" || m[0x101] != "B" {
		t.Fatalf("two-byte mappings = %#v, want 0x100=A and 0x101=B", m)
	}
}

func TestParseToUnicodeCMapAcceptsMaximumTwoByteRange(t *testing.T) {
	m, err := parseToUnicodeCMap([]byte(`
1 beginbfrange
<0000> <FFFF> <0041>
endbfrange`), 2)
	if err != nil {
		t.Fatalf("parseToUnicodeCMap: %v", err)
	}
	if len(m) != maxToUnicodeMappings {
		t.Fatalf("mapping count = %d, want %d", len(m), maxToUnicodeMappings)
	}
}
