package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func filteredTextPDF(content string, filter Name) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	offsets := make([]int, 6)
	writeObject := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	offsets[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Filter /%s /Length %d >>\nstream\n%s\nendstream\nendobj\n", filter, len(content), content)
	writeObject(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	xref := buf.Len()
	fmt.Fprint(&buf, "xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size 6 >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return buf.Bytes()
}

func filterFlate(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return out.Bytes()
}

func filterASCIIHex(data []byte) []byte {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, len(data)*2+1)
	for _, b := range data {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return append(out, '>')
}

func filterASCII85(data []byte) []byte {
	out := make([]byte, ascii85.MaxEncodedLen(len(data)))
	n := ascii85.Encode(out, data)
	return append(out[:n], '~', '>')
}

func filterRunLengthLiteral(data []byte) []byte {
	if len(data) == 0 || len(data) > 128 {
		panic("test helper accepts 1..128 bytes")
	}
	out := append([]byte{byte(len(data) - 1)}, data...)
	return append(out, 128)
}

func TestDecodeStreamPipeline(t *testing.T) {
	plain := []byte("BT /F1 12 Tf (pipeline) Tj ET")
	encoded := filterASCII85(filterFlate(t, filterRunLengthLiteral(plain)))
	stream := &Stream{Dict: Dict{
		"Filter":      Array{Name("ASCII85Decode"), Name("FlateDecode"), Name("RunLengthDecode")},
		"DecodeParms": Array{nil, nil, nil},
	}, Data: encoded}

	got, err := decodeStreamWith(func(v any) any { return v }, stream)
	if err != nil {
		t.Fatalf("decodeStreamWith: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decoded = %q, want %q", got, plain)
	}
}

func TestDecodeStreamASCIIHexThenFlate(t *testing.T) {
	plain := []byte("ASCIIHex then Flate")
	stream := &Stream{Dict: Dict{
		"Filter":      Array{Name("ASCIIHexDecode"), Name("FlateDecode")},
		"DecodeParms": Array{nil, Dict{"Predictor": 1}},
	}, Data: filterASCIIHex(filterFlate(t, plain))}

	got, err := decodeStreamWith(func(v any) any { return v }, stream)
	if err != nil {
		t.Fatalf("decodeStreamWith: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decoded = %q, want %q", got, plain)
	}
}

func TestDecodeStreamRejectsMisalignedDecodeParms(t *testing.T) {
	stream := &Stream{Dict: Dict{
		"Filter":      Array{Name("FlateDecode")},
		"DecodeParms": Array{nil, nil},
	}, Data: filterFlate(t, []byte("x"))}

	_, err := decodeStreamWith(func(v any) any { return v }, stream)
	if !errors.Is(err, ErrFilterDecodeParms) {
		t.Fatalf("decodeStreamWith error = %v, want ErrFilterDecodeParms", err)
	}
}

func TestDecodeStreamEnforcesCumulativeOutputBudget(t *testing.T) {
	stream := &Stream{Dict: Dict{
		"Filter": Array{Name("ASCIIHexDecode"), Name("RunLengthDecode")},
	}, Data: []byte("0241424380>")}

	_, err := decodeStreamWithLimit(func(v any) any { return v }, stream, 5)
	if !errors.Is(err, ErrFilterOutputTooLarge) {
		t.Fatalf("decodeStreamWithLimit error = %v, want ErrFilterOutputTooLarge", err)
	}
}

func TestExtractTextPropagatesUnsupportedContentFilter(t *testing.T) {
	pdf := filteredTextPDF("BT /F1 12 Tf (content) Tj ET", "DCTDecode")
	_, err := ExtractText(pdf)
	if !errors.Is(err, ErrUnsupportedFilter) {
		t.Fatalf("ExtractText error = %v, want ErrUnsupportedFilter", err)
	}
}

func TestNUpPropagatesUnsupportedContentFilter(t *testing.T) {
	pdf := filteredTextPDF("BT /F1 12 Tf (content) Tj ET", "DCTDecode")
	_, err := NUp(pdf, 2)
	if !errors.Is(err, ErrUnsupportedFilter) {
		t.Fatalf("NUp error = %v, want ErrUnsupportedFilter", err)
	}
}

func TestDecodeStreamRejectsUnsupportedFilter(t *testing.T) {
	stream := &Stream{Dict: Dict{"Filter": Name("DCTDecode")}, Data: []byte("raw")}
	_, err := decodeStreamWith(func(v any) any { return v }, stream)
	if !errors.Is(err, ErrUnsupportedFilter) || !strings.Contains(err.Error(), "DCTDecode") {
		t.Fatalf("decodeStreamWith error = %v, want named ErrUnsupportedFilter", err)
	}
}
