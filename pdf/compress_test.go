package pdf

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestCompressDownsamples(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 400; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y * 2), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	in, err := ImagesToPDF([][]byte{buf.Bytes()}, ImagePageOpts{})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	out, err := Compress(in, CompressOpts{MaxWidth: 200, JPEGQuality: 60})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(out) >= len(in) {
		t.Errorf("Compress output (%d bytes) not smaller than input (%d bytes)", len(out), len(in))
	}

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse compressed: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd, _ := d.Get(pages[0].Num).(Dict)
	res, _ := d.R(pd["Resources"]).(Dict)
	xobjs, _ := d.R(res["XObject"]).(Dict)
	imgObj, ok := d.R(xobjs["Im0"]).(*Stream)
	if !ok {
		t.Fatalf("/Im0 is not a stream")
	}
	w := toFloat(d.R(imgObj.Dict["Width"]))
	h := toFloat(d.R(imgObj.Dict["Height"]))
	if w != 200 {
		t.Errorf("Width = %v want 200", w)
	}
	if h != 50 {
		t.Errorf("Height = %v want 50", h)
	}
}

// rawPDFWithBigStream builds a minimal one-page PDF whose content stream is
// 10KB of repeated, highly-compressible, unfiltered text.
func rawPDFWithBigStream() []byte {
	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()

	data := bytes.Repeat([]byte("Hello World, this is filler text. "), 300) // ~10.5KB
	contentRef := b.alloc()
	b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(data)}, Data: data}

	pageRef := b.alloc()
	b.objs[pageRef.Num-1] = Dict{
		"Type":     Name("Page"),
		"Parent":   pagesRef,
		"MediaBox": Array{0, 0, 612, 792},
		"Contents": contentRef,
	}
	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": Array{pageRef}, "Count": 1}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef)
}

func TestCompressReflates(t *testing.T) {
	in := rawPDFWithBigStream()

	out, err := Compress(in, CompressOpts{})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(out) >= len(in) {
		t.Errorf("Compress output (%d bytes) not smaller than input (%d bytes)", len(out), len(in))
	}

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse compressed: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
}

func TestCompressKeepsPages(t *testing.T) {
	out, err := Compress(classicPDF(), CompressOpts{})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse compressed: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
}
