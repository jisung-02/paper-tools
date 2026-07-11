package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// stampPNG returns a w x h PNG. If alpha, the bottom-right pixel is
// semi-transparent so embedPNG must emit an /SMask.
func stampPNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 20, B: 20, A: 255})
		}
	}
	if alpha {
		img.SetNRGBA(w-1, h-1, color.NRGBA{R: 200, G: 20, B: 20, A: 80})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func stampJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 10, G: 200, B: 10, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// lastContentData returns the Data of the last stream in pd's Contents
// array, and whether pd has a Contents entry at all.
func lastContentData(t *testing.T, d *Doc, pd Dict) ([]byte, bool) {
	t.Helper()
	v, has := pd["Contents"]
	if !has {
		return nil, false
	}
	contents, ok := d.R(v).(Array)
	if !ok || len(contents) == 0 {
		t.Fatalf("Contents = %v, want non-empty array", v)
	}
	st, ok := d.R(contents[len(contents)-1]).(*Stream)
	if !ok {
		t.Fatalf("last content entry is not a stream")
	}
	return st.Data, true
}

// rotatedRectPDF builds a single-page classic-xref PDF with the given
// MediaBox dimensions and /Rotate value, for exercising the rotation
// branches of StampImage's cm matrix.
func rotatedRectPDF(width, height float64, rotate int) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, 4) // index 1..3
	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObjRaw(3, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] /Rotate %d >>", width, height, rotate))

	xrefOff := buf.Len()
	buf.WriteString("xref\n0 4\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 4 >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

// TestStampImage_Rotate90TranslationStaysOnPage guards against a regression
// where the /Rotate 90 branch's cm-matrix x-translation ("e" term) used
// x0+y1-y instead of x1-y. On a non-square 400x600 page stamping
// bottom-right, the wrong formula produces e=576, far outside the page's
// [0,400] x-range; the correct formula keeps it on-page.
func TestStampImage_Rotate90TranslationStaysOnPage(t *testing.T) {
	img := stampPNG(t, 80, 40, false) // 2:1 aspect
	src := rotatedRectPDF(400, 600, 90)
	out, err := StampImage(src, img, StampOpts{
		Position:     PosBottomRight,
		WidthPercent: 20,
		MarginPt:     24,
		Pages:        "1",
	})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd1 := d.Get(pages[0].Num).(Dict)
	data, has := lastContentData(t, d, pd1)
	if !has {
		t.Fatalf("page1 missing Contents")
	}

	re := regexp.MustCompile(`([-\d.]+) ([-\d.]+) ([-\d.]+) ([-\d.]+) ([-\d.]+) ([-\d.]+) cm`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatalf("content = %q, could not find cm operator", data)
	}
	e, err := strconv.ParseFloat(string(m[5]), 64)
	if err != nil {
		t.Fatalf("parse cm e term %q: %v", m[5], err)
	}
	if e < 0 || e > 400 {
		t.Errorf("cm e term = %v, want within page x-range [0,400] (bug pushes it to 576)", e)
	}
}

func TestStampImage_BottomRightAnchor(t *testing.T) {
	img := stampPNG(t, 80, 40, false) // 2:1 aspect
	out, err := StampImage(classicPDF(), img, StampOpts{
		Position:     PosBottomRight,
		WidthPercent: 20,
		MarginPt:     24,
		Pages:        "1",
	})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}

	// page1 (612x792 MediaBox): drawW = 0.2*612 = 122.4, drawH = 122.4 * 40/80 = 61.2
	// x = 612 - 24 - 122.4 = 465.6, y = 0 + 24 = 24
	pd1 := d.Get(pages[0].Num).(Dict)
	data, has := lastContentData(t, d, pd1)
	if !has {
		t.Fatalf("page1 missing Contents")
	}
	want := fmt.Sprintf("%.2f 0 0 %.2f %.2f %.2f cm /StIm0 Do", 122.4, 61.2, 465.6, 24.0)
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("page1 content = %q, want substring %q", data, want)
	}
	if !bytes.Contains(data, []byte("/GSSt0 gs")) {
		t.Errorf("page1 content missing /GSSt0 gs: %s", data)
	}

	// page2 was not selected: no stamp, no Contents at all.
	pd2 := d.Get(pages[1].Num).(Dict)
	if _, has := lastContentData(t, d, pd2); has {
		t.Errorf("page2 should not have been stamped")
	}
}

func TestStampImage_TopLeftAnchor(t *testing.T) {
	img := stampPNG(t, 80, 40, false)
	out, err := StampImage(classicPDF(), img, StampOpts{
		Position:     PosTopLeft,
		WidthPercent: 20,
		MarginPt:     24,
		Pages:        "1",
	})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	// x = 0 + 24 = 24, y = 792 - 24 - 61.2 = 706.8
	pd1 := d.Get(pages[0].Num).(Dict)
	data, _ := lastContentData(t, d, pd1)
	want := fmt.Sprintf("%.2f 0 0 %.2f %.2f %.2f cm /StIm0 Do", 122.4, 61.2, 24.0, 706.8)
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("page1 content = %q, want substring %q", data, want)
	}
}

func TestStampImage_CenterAnchorIgnoresMargin(t *testing.T) {
	img := stampPNG(t, 80, 40, false)
	out, err := StampImage(classicPDF(), img, StampOpts{
		Position:     PosCenter,
		WidthPercent: 20,
		MarginPt:     1000, // must be ignored for a centered anchor
		Pages:        "1",
	})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, _ := Parse(out)
	pages, _ := d.Pages()
	pd1 := d.Get(pages[0].Num).(Dict)
	data, _ := lastContentData(t, d, pd1)
	// cx=306, cy=396, drawW=122.4, drawH=61.2 -> x=306-61.2=244.8, y=396-30.6=365.4
	want := fmt.Sprintf("%.2f 0 0 %.2f %.2f %.2f cm /StIm0 Do", 122.4, 61.2, 244.8, 365.4)
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("page1 content = %q, want substring %q", data, want)
	}
}

func TestStampImage_AspectPreserved(t *testing.T) {
	img := stampPNG(t, 100, 25, false) // 4:1 aspect
	out, err := StampImage(classicPDF(), img, StampOpts{WidthPercent: 50, Pages: "1"})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, _ := Parse(out)
	pages, _ := d.Pages()
	pd1 := d.Get(pages[0].Num).(Dict)
	data, _ := lastContentData(t, d, pd1)
	// drawW = 0.5*612 = 306, drawH = 306 * 25/100 = 76.5
	want := fmt.Sprintf("%.2f 0 0 %.2f", 306.0, 76.5)
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("content = %q, want cm prefix %q (aspect not preserved)", data, want)
	}
}

func TestStampImage_PageRangeFirstLast(t *testing.T) {
	img := stampPNG(t, 80, 40, false)

	for _, tc := range []struct {
		pages        string
		wantFirst    bool
		wantSecond   bool
		wantSelected string
	}{
		{"first", true, false, "page1"},
		{"last", false, true, "page2"},
		{"2", false, true, "page2"},
	} {
		out, err := StampImage(classicPDF(), img, StampOpts{Pages: tc.pages})
		if err != nil {
			t.Fatalf("Pages=%q: StampImage: %v", tc.pages, err)
		}
		d, _ := Parse(out)
		pages, _ := d.Pages()
		pd1 := d.Get(pages[0].Num).(Dict)
		pd2 := d.Get(pages[1].Num).(Dict)
		_, has1 := lastContentData(t, d, pd1)
		_, has2 := lastContentData(t, d, pd2)
		if has1 != tc.wantFirst || has2 != tc.wantSecond {
			t.Errorf("Pages=%q: page1 stamped=%v (want %v), page2 stamped=%v (want %v)",
				tc.pages, has1, tc.wantFirst, has2, tc.wantSecond)
		}
	}
}

func TestStampImage_AllPagesByDefault(t *testing.T) {
	img := stampPNG(t, 80, 40, false)
	out, err := StampImage(classicPDF(), img, StampOpts{})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, _ := Parse(out)
	pages, _ := d.Pages()
	for i, pg := range pages {
		pd := d.Get(pg.Num).(Dict)
		if _, has := lastContentData(t, d, pd); !has {
			t.Errorf("page %d not stamped when Pages is empty", i+1)
		}
	}
}

func TestStampImage_AlphaPNGGetsSMask(t *testing.T) {
	img := stampPNG(t, 10, 10, true)
	out, err := StampImage(classicPDF(), img, StampOpts{Pages: "1", Opacity: 0.5})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, _ := d.Pages()
	pd1 := d.Get(pages[0].Num).(Dict)
	res, ok := d.R(pd1["Resources"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing Resources")
	}
	xobjs, ok := d.R(res["XObject"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing XObject resources")
	}
	imgObj, ok := d.R(xobjs["StIm0"]).(*Stream)
	if !ok {
		t.Fatalf("/StIm0 is not a stream")
	}
	smaskRef, ok := imgObj.Dict["SMask"]
	if !ok {
		t.Fatalf("expected SMask on translucent stamp PNG")
	}
	smask, ok := d.R(smaskRef).(*Stream)
	if !ok {
		t.Fatalf("SMask target is not a stream")
	}
	if cs, _ := d.R(smask.Dict["ColorSpace"]).(Name); cs != "DeviceGray" {
		t.Errorf("SMask ColorSpace = %v want DeviceGray", cs)
	}

	gs, ok := d.R(res["ExtGState"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing ExtGState resources")
	}
	gsDict, ok := d.R(gs["GSSt0"]).(Dict)
	if !ok {
		t.Fatalf("GSSt0 is not a dict")
	}
	if ca := toFloat(gsDict["ca"]); ca != 0.5 {
		t.Errorf("ExtGState ca = %v want 0.5", ca)
	}
}

func TestStampImage_JPEGInput(t *testing.T) {
	img := stampJPEG(t, 40, 40)
	out, err := StampImage(classicPDF(), img, StampOpts{Pages: "1"})
	if err != nil {
		t.Fatalf("StampImage: %v", err)
	}
	d, _ := Parse(out)
	pages, _ := d.Pages()
	pd1 := d.Get(pages[0].Num).(Dict)
	res, _ := d.R(pd1["Resources"]).(Dict)
	xobjs, _ := d.R(res["XObject"]).(Dict)
	imgObj, ok := d.R(xobjs["StIm0"]).(*Stream)
	if !ok {
		t.Fatalf("/StIm0 is not a stream")
	}
	if f, _ := d.R(imgObj.Dict["Filter"]).(Name); f != "DCTDecode" {
		t.Errorf("Filter = %v want DCTDecode", f)
	}
}

func TestStampImage_InvalidPosition(t *testing.T) {
	img := stampPNG(t, 10, 10, false)
	_, err := StampImage(classicPDF(), img, StampOpts{Position: "nowhere"})
	if err == nil {
		t.Fatalf("expected error for invalid position")
	}
	if !strings.Contains(err.Error(), "position") {
		t.Errorf("error should mention position, got: %v", err)
	}
}

func TestStampImage_UnsupportedImageFormat(t *testing.T) {
	_, err := StampImage(classicPDF(), []byte("GIF89a..."), StampOpts{})
	if err == nil {
		t.Fatalf("expected error for unsupported image format")
	}
}

func TestStampText_BottomRightAnchor(t *testing.T) {
	out, err := StampText(classicPDF(), StampTextOpts{
		Text:     "OK",
		Position: PosBottomRight,
		FontSize: 24,
		MarginPt: 24,
		Opacity:  0.75,
		Pages:    "1",
	})
	if err != nil {
		t.Fatalf("StampText: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}

	pd1 := d.Get(pages[0].Num).(Dict)
	data, has := lastContentData(t, d, pd1)
	if !has {
		t.Fatalf("page1 missing Contents")
	}
	// page1 (612x792 MediaBox): approximate text width = 0.5*24*2 = 24.
	// x = 612 - 24 - 24 = 564, y = 0 + 24 = 24.
	want := "1 0 0 1 564.00 24.00 Tm (OK) Tj"
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("page1 content = %q, want substring %q", data, want)
	}
	if !bytes.Contains(data, []byte("/GSStT0 gs BT /FStT0 24.00 Tf")) {
		t.Errorf("page1 content missing text resources: %s", data)
	}

	res, ok := d.R(pd1["Resources"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing Resources")
	}
	fonts, ok := d.R(res["Font"]).(Dict)
	if !ok || fonts["FStT0"] == nil {
		t.Fatalf("page1 missing /FStT0 font resource")
	}
	gs, ok := d.R(res["ExtGState"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing ExtGState resources")
	}
	gsDict, ok := d.R(gs["GSStT0"]).(Dict)
	if !ok {
		t.Fatalf("GSStT0 is not a dict")
	}
	if ca := toFloat(gsDict["ca"]); ca != 0.75 {
		t.Errorf("ExtGState ca = %v want 0.75", ca)
	}

	pd2 := d.Get(pages[1].Num).(Dict)
	if _, has := lastContentData(t, d, pd2); has {
		t.Errorf("page2 should not have been stamped")
	}
}

func TestStampText_EmbedsUnicodeFont(t *testing.T) {
	font := testFont(t)
	text := "승인"
	f, err := parseTTF(font)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	width := lineWidth(f, []rune(text), 18)

	out, err := StampText(classicPDF(), StampTextOpts{
		Text:     text,
		FontTTF:  font,
		Position: PosTopCenter,
		FontSize: 18,
		MarginPt: 30,
		Pages:    "first",
	})
	if err != nil {
		t.Fatalf("StampText: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd1 := d.Get(pages[0].Num).(Dict)
	data, has := lastContentData(t, d, pd1)
	if !has {
		t.Fatalf("page1 missing Contents")
	}
	want := fmt.Sprintf("1 0 0 1 %.2f 744.00 Tm <", 306-width/2)
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("page1 content = %q, want substring %q", data, want)
	}
	if bytes.Contains(data, []byte(text)) {
		t.Errorf("unicode text should be hex-encoded, got literal text in content: %s", data)
	}

	res, ok := d.R(pd1["Resources"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing Resources")
	}
	fonts, ok := d.R(res["Font"]).(Dict)
	if !ok {
		t.Fatalf("page1 missing Font resources")
	}
	fontObj, ok := d.R(fonts["FStT0"]).(Dict)
	if !ok {
		t.Fatalf("/FStT0 is not a font dict")
	}
	if subtype, _ := d.R(fontObj["Subtype"]).(Name); subtype != "Type0" {
		t.Errorf("/FStT0 subtype = %v want Type0", subtype)
	}
}
