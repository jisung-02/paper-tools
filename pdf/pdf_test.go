package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// classicPDF builds a PDF with a classic xref table: catalog, a Pages node
// with two kids (inheriting MediaBox), and two leaf pages, one of which sets
// its own /Rotate.
func classicPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, 5) // index 1..4
	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 /MediaBox [0 0 612 792] >>")
	writeObjRaw(3, "<< /Type /Page /Parent 2 0 R >>")
	writeObjRaw(4, "<< /Type /Page /Parent 2 0 R /Rotate 90 >>")

	xrefOff := buf.Len()
	buf.WriteString("xref\n0 5\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 5 >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func zlibCompress(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func TestInflateRejectsOversizedOutput(t *testing.T) {
	raw := bytes.Repeat([]byte{'A'}, maxInflateBytes+1)
	_, err := inflate(zlibCompress(raw))
	if err == nil {
		t.Fatal("expected oversized inflate output to fail")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too large error, got %v", err)
	}
}

// xrefStreamPDF builds a modern-layout PDF: catalog/Pages/page objects
// packed into an object stream, indexed by a compressed cross-reference
// stream that itself uses a PNG "Up" predictor.
func xrefStreamPDF() []byte {
	obj1 := "<< /Type /Catalog /Pages 2 0 R >>"
	obj2 := "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 100 200] >>"
	obj3 := "<< /Type /Page /Parent 2 0 R >>"
	sep := "\n"
	off1 := 0
	off2 := len(obj1) + len(sep)
	off3 := off2 + len(obj2) + len(sep)
	objData := obj1 + sep + obj2 + sep + obj3

	header := fmt.Sprintf("1 %d 2 %d 3 %d\n", off1, off2, off3)
	first := len(header)
	rawObjStm := header + objData
	compObjStm := zlibCompress([]byte(rawObjStm))
	objStmDict := fmt.Sprintf("<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode /Length %d >>", first, len(compObjStm))

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	off4 := buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n%s\nstream\n", objStmDict)
	buf.Write(compObjStm)
	buf.WriteString("\nendstream\nendobj\n")

	off5 := buf.Len()

	type row struct{ typ, f1, f2 int }
	rows := []row{
		{0, 0, 0},
		{2, 4, 0},
		{2, 4, 1},
		{2, 4, 2},
		{1, off4, 0},
		{1, off5, 0},
	}
	rawRows := make([][]byte, len(rows))
	for i, r := range rows {
		b := make([]byte, 7)
		b[0] = byte(r.typ)
		b[1] = byte(r.f1 >> 24)
		b[2] = byte(r.f1 >> 16)
		b[3] = byte(r.f1 >> 8)
		b[4] = byte(r.f1)
		b[5] = byte(r.f2 >> 8)
		b[6] = byte(r.f2)
		rawRows[i] = b
	}

	var filtered bytes.Buffer
	prev := make([]byte, 7)
	for _, rr := range rawRows {
		filtered.WriteByte(2) // PNG "Up" filter
		for i := 0; i < 7; i++ {
			filtered.WriteByte(rr[i] - prev[i])
		}
		prev = rr
	}
	compXref := zlibCompress(filtered.Bytes())
	xrefDict := fmt.Sprintf("<< /Type /XRef /W [1 4 2] /Size 6 /Root 1 0 R /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 7 >> /Length %d >>", len(compXref))
	fmt.Fprintf(&buf, "5 0 obj\n%s\nstream\n", xrefDict)
	buf.Write(compXref)
	buf.WriteString("\nendstream\nendobj\n")

	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off5)
	return buf.Bytes()
}

func negativeXrefWidthPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	off := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /XRef /W [1 -1 1] /Size 1 /Root 1 0 R /Length 1 >>\nstream\n")
	buf.WriteByte(1)
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off)
	return buf.Bytes()
}

func encryptedPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	off1 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	xrefOff := buf.Len()
	buf.WriteString("xref\n0 2\n")
	buf.WriteString("0000000000 65535 f \n")
	fmt.Fprintf(&buf, "%010d 00000 n \n", off1)
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 2 /Encrypt << /Filter /Standard >> >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case float64:
		return t
	}
	return -1
}

func TestMerge(t *testing.T) {
	out, err := Merge([][]byte{classicPDF(), xrefStreamPDF()})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse merged: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}

	pd0, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page0 is not a dict")
	}
	if _, ok := pd0["MediaBox"]; !ok {
		t.Fatalf("page0 missing inherited MediaBox in output")
	}

	pd2, ok := d.Get(pages[2].Num).(Dict)
	if !ok {
		t.Fatalf("page2 is not a dict")
	}
	mb, ok := d.R(pd2["MediaBox"]).(Array)
	if !ok || len(mb) != 4 {
		t.Fatalf("page2 MediaBox invalid: %v", pd2["MediaBox"])
	}
	want := []float64{0, 0, 100, 200}
	for i, w := range want {
		if got := toFloat(mb[i]); got != w {
			t.Errorf("MediaBox[%d] = %v want %v", i, got, w)
		}
	}
}

func TestSplit(t *testing.T) {
	out, err := Split(classicPDF(), "2")
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse split: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	pd, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page is not a dict")
	}
	rot, ok := pd["Rotate"].(int)
	if !ok || rot != 90 {
		t.Fatalf("Rotate = %v, want 90", pd["Rotate"])
	}
}

func TestReorderRequiresEveryPageOnce(t *testing.T) {
	for _, order := range []string{"2", "1,1"} {
		if _, err := Reorder(classicPDF(), order); err == nil {
			t.Fatalf("expected error for reorder %q", order)
		}
	}
	if _, err := Reorder(classicPDF(), "2,1"); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
}

func TestParseRanges(t *testing.T) {
	got, err := ParseRanges("1-3,5", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []int{1, 2, 3, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}

	got, err = ParseRanges("2-", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []int{2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}

	for _, bad := range []string{"0", "abc", "5-2", ""} {
		if _, err := ParseRanges(bad, 10); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestEncrypted(t *testing.T) {
	if _, err := Parse(encryptedPDF()); err == nil {
		t.Fatalf("expected error for encrypted PDF")
	}
}

func TestParseRejectsNegativeXrefStreamWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Parse panicked on malformed xref /W: %v", r)
		}
	}()
	if _, err := Parse(negativeXrefWidthPDF()); err == nil {
		t.Fatalf("expected error for negative xref /W entry")
	}
}

func TestUnpredictRejectsInvalidPredictorDimensions(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unpredict panicked on malformed predictor dimensions: %v", r)
		}
	}()
	if _, err := unpredict([]byte{0}, 12, -2, 1, 8); err == nil {
		t.Fatalf("expected error for invalid predictor dimensions")
	}
}

// FuzzParse exercises the PDF parser entry point with arbitrary bytes; the
// only failure mode under test is a panic (errors are expected and ignored).
func FuzzParse(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("%PDF-1.4"))
	f.Add(classicPDF())
	f.Add(xrefStreamPDF())
	f.Add(negativeXrefWidthPDF())
	f.Add(encryptedPDF())
	f.Fuzz(func(t *testing.T, data []byte) {
		Parse(data)
	})
}
