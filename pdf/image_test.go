package pdf

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestImagesToPDF_PNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	img.SetNRGBA(1, 1, color.NRGBA{R: 40, G: 50, B: 60, A: 128}) // semi-transparent

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	out, err := ImagesToPDF([][]byte{buf.Bytes()}, ImagePageOpts{})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
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
	mb, ok := d.R(pd["MediaBox"]).(Array)
	if !ok || len(mb) != 4 {
		t.Fatalf("bad MediaBox: %v", pd["MediaBox"])
	}
	want := []float64{0, 0, 3, 2}
	for i, w := range want {
		if got := toFloat(d.R(mb[i])); got != w {
			t.Errorf("MediaBox[%d] = %v want %v", i, got, w)
		}
	}

	res, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatalf("page missing Resources")
	}
	xobjs, ok := d.R(res["XObject"]).(Dict)
	if !ok {
		t.Fatalf("Resources missing XObject dict")
	}
	imRef, ok := xobjs["Im0"]
	if !ok {
		t.Fatalf("XObject missing /Im0")
	}
	imgObj, ok := d.R(imRef).(*Stream)
	if !ok {
		t.Fatalf("/Im0 is not a stream")
	}
	if w := toFloat(d.R(imgObj.Dict["Width"])); w != 3 {
		t.Errorf("Width = %v want 3", w)
	}
	if h := toFloat(d.R(imgObj.Dict["Height"])); h != 2 {
		t.Errorf("Height = %v want 2", h)
	}
	if f, _ := d.R(imgObj.Dict["Filter"]).(Name); f != "FlateDecode" {
		t.Errorf("Filter = %v want FlateDecode", f)
	}

	smaskRef, ok := imgObj.Dict["SMask"]
	if !ok {
		t.Fatalf("expected SMask on translucent PNG")
	}
	smask, ok := d.R(smaskRef).(*Stream)
	if !ok {
		t.Fatalf("SMask target is not a stream")
	}
	if cs, _ := d.R(smask.Dict["ColorSpace"]).(Name); cs != "DeviceGray" {
		t.Errorf("SMask ColorSpace = %v want DeviceGray", cs)
	}

	decoded, err := d.decodeStream(imgObj)
	if err != nil {
		t.Fatalf("decodeStream: %v", err)
	}
	if len(decoded) != 18 {
		t.Errorf("decoded main stream length = %d want 18", len(decoded))
	}
}

func TestImagesToPDF_JPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	out, err := ImagesToPDF([][]byte{buf.Bytes()}, ImagePageOpts{A4: true})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
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
	mb, ok := d.R(pd["MediaBox"]).(Array)
	if !ok || len(mb) != 4 {
		t.Fatalf("bad MediaBox: %v", pd["MediaBox"])
	}
	want := []float64{0, 0, a4Width, a4Height}
	for i, w := range want {
		if got := toFloat(d.R(mb[i])); abs64(got-w) > 0.01 {
			t.Errorf("MediaBox[%d] = %v want %v", i, got, w)
		}
	}

	res, _ := d.R(pd["Resources"]).(Dict)
	xobjs, _ := d.R(res["XObject"]).(Dict)
	imgObj, ok := d.R(xobjs["Im0"]).(*Stream)
	if !ok {
		t.Fatalf("/Im0 is not a stream")
	}
	if f, _ := d.R(imgObj.Dict["Filter"]).(Name); f != "DCTDecode" {
		t.Errorf("Filter = %v want DCTDecode", f)
	}
	if w := toFloat(d.R(imgObj.Dict["Width"])); w != 4 {
		t.Errorf("Width = %v want 4", w)
	}
}

func TestImagesToPDF_Unsupported(t *testing.T) {
	_, err := ImagesToPDF([][]byte{[]byte("GIF89a...")}, ImagePageOpts{})
	if err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}

func TestJPEGGray(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x*16 + y*16)})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	out, err := ImagesToPDF([][]byte{buf.Bytes()}, ImagePageOpts{})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
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
	if cs, _ := d.R(imgObj.Dict["ColorSpace"]).(Name); cs != "DeviceGray" {
		t.Errorf("ColorSpace = %v want DeviceGray", cs)
	}
}

func abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
