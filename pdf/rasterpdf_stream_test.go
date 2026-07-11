package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"testing"
)

type rasterTrackingReader struct {
	data       []byte
	offset     int
	maxRequest int
}

func (r *rasterTrackingReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRequest {
		r.maxRequest = len(p)
	}
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func rasterStartXRef(t *testing.T, data []byte) int {
	t.Helper()
	marker := []byte("startxref\n")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		t.Fatal("startxref not found")
	}
	start := index + len(marker)
	end := start + bytes.IndexByte(data[start:], '\n')
	value, err := strconv.Atoi(string(data[start:end]))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func rasterReplaceStartXRef(t *testing.T, data []byte, value int) []byte {
	t.Helper()
	marker := []byte("startxref\n")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		t.Fatal("startxref not found")
	}
	start := index + len(marker)
	endRelative := bytes.IndexByte(data[start:], '\n')
	if endRelative < 0 {
		t.Fatal("startxref line is incomplete")
	}
	end := start + endRelative
	out := append([]byte(nil), data[:start]...)
	out = strconv.AppendInt(out, int64(value), 10)
	out = append(out, data[end:]...)
	return out
}

func rasterHistoricalOnlyPDF(t *testing.T, canary string) []byte {
	t.Helper()
	b := &builder{}
	catalog := b.alloc()
	pages := b.alloc()
	page := b.alloc()
	content := b.alloc()
	font := b.alloc()
	b.objs[catalog.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pages}
	b.objs[pages.Num-1] = Dict{"Type": Name("Pages"), "Kids": Array{page}, "Count": 1}
	b.objs[page.Num-1] = Dict{
		"Type": Name("Page"), "Parent": pages, "MediaBox": Array{0, 0, 200, 100},
		"Resources": Dict{"Font": Dict{"F1": font}}, "Contents": content,
	}
	contentData := []byte("BT /F1 12 Tf 20 50 Td (" + canary + ") Tj ET")
	b.objs[content.Num-1] = &Stream{Dict: Dict{"Length": len(contentData)}, Data: contentData}
	b.objs[font.Num-1] = Dict{"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica")}
	base, err := b.bytes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	previousXRef := rasterStartXRef(t, base)
	var update bytes.Buffer
	pageOffset := len(base)
	update.WriteString("3 0 obj\n<< /MediaBox [0 0 200 100] /Parent 2 0 R /Type /Page >>\nendobj\n")
	xrefOffset := len(base) + update.Len()
	update.WriteString("xref\n3 1\n")
	fmt.Fprintf(&update, "%010d 00000 n \n", pageOffset)
	fmt.Fprintf(&update, "trailer\n<< /Prev %d /Root 1 0 R /Size 6 >>\nstartxref\n%d\n%%%%EOF\n", previousXRef, xrefOffset)
	return append(base, update.Bytes()...)
}

func TestValidateRasterOnlyPDFStreamRejectsNonCanonicalLayout(t *testing.T) {
	valid, err := BuildRasterOnlyPDF([]RasterPage{{
		PNGData: testPNG(t, 3, 2), WidthPt: 120, HeightPt: 80,
	}}, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	xrefOffset := bytes.Index(valid, []byte("xref\n"))
	if xrefOffset < 0 {
		t.Fatal("xref not found")
	}
	unindexedObject := []byte("99 0 obj\n(HISTORICAL-UNINDEXED-CANARY)\nendobj\n")
	unindexed := append([]byte(nil), valid[:xrefOffset]...)
	unindexed = append(unindexed, unindexedObject...)
	unindexed = append(unindexed, valid[xrefOffset:]...)
	unindexed = rasterReplaceStartXRef(t, unindexed, xrefOffset+len(unindexedObject))

	xrefLine := bytes.Index(valid[xrefOffset:], []byte("00000 n \n"))
	if xrefLine < 10 {
		t.Fatal("xref entry not found")
	}
	xrefMutation := append([]byte(nil), valid...)
	entryStart := xrefOffset + xrefLine - 10
	copy(xrefMutation[entryStart:entryStart+10], "0000000000")

	tests := []struct {
		name string
		data []byte
	}{
		{name: "unindexed object", data: unindexed},
		{name: "wrong Size", data: bytes.Replace(valid, []byte("/Size 6"), []byte("/Size 7"), 1)},
		{name: "wrong xref offset", data: xrefMutation},
		{name: "nonzero generation", data: bytes.Replace(valid, []byte("3 0 obj"), []byte("3 1 obj"), 1)},
		{name: "wrong startxref", data: rasterReplaceStartXRef(t, valid, rasterStartXRef(t, valid)+1)},
		{name: "trailing bytes", data: append(append([]byte(nil), valid...), []byte("EXTRA")...)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRasterOnlyPDFStream(bytes.NewReader(tc.data), 1, RasterPDFValidationLimits{MaxInputBytes: uint64(len(tc.data))})
			if !errors.Is(err, ErrRasterPDFInvariant) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err := ValidateRasterOnlyPDF(unindexed, 1); !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("byte-slice validator accepted unindexed object: %v", err)
	}
}

func TestValidateRasterOnlyPDFStreamIsBoundedAndRejectsForbiddenBytes(t *testing.T) {
	const decodedCanary = "DECODED-CANARY"
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0x17, G: 0x2b, B: 0x41, A: 255})
		}
	}
	const decodedOffset = rasterValidationDecodedBuffer - 5
	for index := 0; index < len(decodedCanary); index++ {
		rgbIndex := decodedOffset + index
		pixel := rgbIndex / 3
		x, y := pixel%128, pixel/128
		value := img.NRGBAAt(x, y)
		switch rgbIndex % 3 {
		case 0:
			value.R = decodedCanary[index]
		case 1:
			value.G = decodedCanary[index]
		case 2:
			value.B = decodedCanary[index]
		}
		img.SetNRGBA(x, y, value)
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	out, err := BuildRasterOnlyPDF([]RasterPage{{PNGData: pngData.Bytes(), WidthPt: 128, HeightPt: 128}}, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(decodedCanary)) {
		t.Fatal("decoded canary unexpectedly appears in raw compressed output")
	}
	stats := &RasterPDFValidationStats{}
	reader := &rasterTrackingReader{data: out}
	err = ValidateRasterOnlyPDFStream(reader, 1, RasterPDFValidationLimits{
		MaxInputBytes:         uint64(len(out)),
		MaxDecodedStreamBytes: 128 * 128 * 3,
		Forbidden:             [][]byte{[]byte(decodedCanary)},
		Stats:                 stats,
	})
	if !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("decoded canary error = %v", err)
	}
	if reader.maxRequest > 64*1024 {
		t.Fatalf("validator requested %d input bytes", reader.maxRequest)
	}
	if stats.MaxInputReadBytes > 64*1024 || stats.MaxDecodedBufferBytes > 32*1024 {
		t.Fatalf("validator buffers = input %d decoded %d", stats.MaxInputReadBytes, stats.MaxDecodedBufferBytes)
	}

	err = ValidateRasterOnlyPDFStream(bytes.NewReader(out), 1, RasterPDFValidationLimits{
		MaxInputBytes: uint64(len(out)), Forbidden: [][]byte{[]byte("FlateDecode")},
	})
	if !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("raw forbidden error = %v", err)
	}
	if err := ValidateRasterOnlyPDFStream(bytes.NewReader(out), 1, RasterPDFValidationLimits{MaxInputBytes: uint64(len(out) - 1)}); !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("input limit error = %v", err)
	}
	if err := ValidateRasterOnlyPDFStream(bytes.NewReader(out), 1, RasterPDFValidationLimits{
		MaxInputBytes: uint64(len(out)), MaxTotalPixels: 1,
	}); !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("total pixel limit error = %v", err)
	}
}

func TestRasterOnlyOutputDropsIncrementalHistoricalCanary(t *testing.T) {
	const historicalCanary = "REDACT-HISTORICAL-ONLY-CANARY"
	source := rasterHistoricalOnlyPDF(t, historicalCanary)
	if !bytes.Contains(source, []byte(historicalCanary)) || !bytes.Contains(source, []byte("/Prev")) {
		t.Fatal("source is not an incremental historical canary fixture")
	}
	text, err := ExtractText(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, historicalCanary) {
		t.Fatalf("historical canary is still active: %q", text)
	}
	out, err := BuildRasterOnlyPDF([]RasterPage{{PNGData: testPNG(t, 2, 2), WidthPt: 200, HeightPt: 100}}, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := [][]byte{[]byte(historicalCanary)}
	for _, canary := range rasterSourceCanaries {
		forbidden = append(forbidden, []byte(canary))
	}
	if err := ValidateRasterOnlyPDFStream(bytes.NewReader(out), 1, RasterPDFValidationLimits{
		MaxInputBytes: uint64(len(out)), Forbidden: forbidden,
	}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(historicalCanary)) {
		t.Fatal("historical canary leaked into raw output")
	}
	doc, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	for number, entry := range doc.xref {
		if number == 0 || entry.typ == 0 {
			continue
		}
		stream, ok := doc.Get(number).(*Stream)
		if !ok {
			continue
		}
		decoded, err := doc.decodeStream(stream)
		if err != nil {
			t.Fatal(err)
		}
		for _, canary := range forbidden {
			if bytes.Contains(decoded, canary) {
				t.Fatalf("decoded stream %d retained %q", number, canary)
			}
		}
	}
}
